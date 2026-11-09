import { RagError } from '../core/errors.ts';
import { arr, isObj, now } from '../core/util.js';
import { summarize } from './events.js';

// tool calls of an LLM node as events: wrap each tool's execute
// ── decisions (RAGWB-007): env.decide(requestJSON, signal) → response JSON.
// Requests are normalised here so choice options keep their order on the
// wire ([{name, description}]); responses come back camelCase.
const DECISION_TYPES = ['noul', 'choice', 'score'];

function wireOptions(o) {
  if (o == null) return undefined;
  if (Array.isArray(o))
    return o.map((x) =>
      typeof x === 'string'
        ? { name: x }
        : {
            name: String(x && x.name),
            ...(x && x.description != null ? { description: String(x.description) } : {}),
          },
    );
  if (isObj(o))
    return Object.entries(o).map(([name, d]) => ({
      name,
      ...(d != null && d !== '' ? { description: String(d) } : {}),
    }));
  throw new RagError('decide', 'options must be an object {name: description} or an array');
}

function wireDecision(req) {
  if (!isObj(req) || !isObj(req.questions))
    throw new RagError('decide', 'decide({state, questions}) needs questions keyed by id');
  const questions = {};
  for (const [id, q] of Object.entries(req.questions)) {
    if (!isObj(q) || !DECISION_TYPES.includes(q.type))
      throw new RagError(
        'decide',
        `question '${id}': type must be one of ${DECISION_TYPES.join(', ')}`,
      );
    const w = { type: q.type, instructions: q.instructions };
    if (q.options != null) w.options = wireOptions(q.options);
    if (q.levels != null) w.levels = arr(q.levels).map(String);
    if (q.true != null) w.true = String(q.true);
    if (q.false != null) w.false = String(q.false);
    questions[id] = w;
  }
  return {
    state: req.state,
    questions,
    ...(req.images ? { images: arr(req.images) } : {}),
    ...(req.model ? { model: String(req.model) } : {}),
  };
}

function fromWireDecision(r, ms) {
  const answers = {};
  for (const [id, a] of Object.entries(r.answers || {})) {
    answers[id] =
      a.type === 'noul'
        ? { type: 'noul', probability: a.probability }
        : a.type === 'choice'
          ? {
              type: 'choice',
              choice: a.choice,
              confidence: a.confidence,
              probabilities: a.probabilities || {},
            }
          : {
              type: 'score',
              score: a.score,
              confidence: a.confidence,
              levelProbabilities: a.level_probabilities || [],
            };
  }
  return {
    provider: r.provider,
    model: r.model,
    answers,
    ms: r.duration_ms ?? ms,
    ...(r.usage
      ? {
          usage: {
            inputTokens: r.usage.input_tokens || 0,
            outputTokens: r.usage.output_tokens || 0,
          },
        }
      : {}),
    ...(r.request_id ? { requestId: r.request_id } : {}),
  };
}

// the one-line form of an answer: "semantic 0.92", "0.97", "level 1.9"
function answerValue(a) {
  if (!a) return null;
  if (a.type === 'noul') return a.probability;
  if (a.type === 'choice') return a.choice;
  return a.score;
}

function answerLine(a) {
  if (!a) return 'none';
  if (a.type === 'noul') return (+a.probability).toFixed(2);
  if (a.type === 'choice')
    return `${a.choice} ${(+(a.probabilities[a.choice] ?? a.confidence)).toFixed(2)}`;
  return `level ${(+a.score).toFixed(1)}`;
}

// makeDecide wraps a call(request) → Promise<response> with the one-question helpers.
function makeDecide(call) {
  const f =
    call ||
    (() =>
      Promise.reject({
        code: 'decide_unavailable',
        message: 'no decision model is configured (start ragwb with --decide pi|lunaroute|fake)',
      }));
  const one = (type, crit) => async (state, instructions, x) => {
    const q = { type, instructions, ...crit(x) };
    const r = await f({ state, questions: { q } });
    return type === 'noul' ? r.answers.q.probability : r.answers.q;
  };
  f.noul = one('noul', (o) => ({
    ...(o && o.true ? { true: o.true } : {}),
    ...(o && o.false ? { false: o.false } : {}),
  }));
  f.choice = one('choice', (options) => ({ options }));
  f.score = one('score', (levels) => ({ levels }));
  return f;
}

// eventDecide emits one 'decision' event per call (node, model, ms, compact answers).
function eventDecide(decide, node, emit) {
  let calls = 0;
  return makeDecide(async (req) => {
    const call = ++calls;
    const r = await decide(req);
    const answers = {};
    for (const [id, a] of Object.entries(r.answers))
      answers[id] = {
        type: a.type,
        value: answerValue(a),
        ...(a.type === 'choice'
          ? { p: a.probabilities[a.choice] }
          : a.type === 'score'
            ? { p: a.confidence }
            : {}),
      };
    emit({ type: 'decision', node, call, model: r.model, ms: r.ms, answers });
    return r;
  });
}

function eventSample(sample, node, emit) {
  let calls = 0;
  const wrap = (o) =>
    !o || !Array.isArray(o.tools) || !o.tools.length
      ? o
      : {
          ...o,
          tools: o.tools.map((t) => ({
            ...t,
            execute: async (input, eo) => {
              const call = ++calls,
                t0 = now();
              emit({ type: 'tool', node, call, name: String(t.name), phase: 'start', input });
              try {
                const v = await t.execute(input, eo);
                emit({
                  type: 'tool',
                  node,
                  call,
                  name: String(t.name),
                  phase: 'end',
                  ms: +(now() - t0).toFixed(1),
                  summary: summarize(v),
                });
                return v;
              } catch (e) {
                emit({
                  type: 'tool',
                  node,
                  call,
                  name: String(t.name),
                  phase: 'end',
                  ms: +(now() - t0).toFixed(1),
                  isError: true,
                  summary: String((e && e.message) || e),
                });
                throw e;
              }
            },
          })),
        };
  const f = (input, o) => sample(input, wrap(o));
  f.json = (input, o) => sample.json(input, wrap(o));
  if (sample.limits) f.limits = sample.limits;
  return f;
}

export { answerLine, eventDecide, eventSample, fromWireDecision, makeDecide, wireDecision };
