import { normHits } from '../components/hits.js';
import { portsOf, REG } from '../components/registry.js';
import { RagError } from '../core/errors.ts';
import { fnOf } from '../core/lambda.js';
import { arr, clip, hash, isLambda, now, stable, tokens } from '../core/util.js';
import { triggerOf, whenHolds, whenSkipOutput } from '../graph/graph.js';
import { answerLine, eventDecide, eventSample, makeDecide } from './decision.js';
import { timed } from './environment.js';
import { hitsEvent } from './events.js';
import { safeEmit } from './scope.ts';

// ────────────────────────────────────────────────────────────── live run
const countOf = (v) =>
  v == null ? 0 : Array.isArray(v) ? v.length : v.evidence ? v.evidence.length : 1;

class Session {
  constructor(engine, getIr) {
    this.engine = engine;
    this.getIr = getIr;
    this.memo = new Map();
    this.coerced = new Map();
    this.last = null;
    this.runs = 0;
  }
  queries(v) {
    const list = arr(v).map(String).filter(Boolean),
      key = stable(list);
    if (!this.coerced.has(key)) {
      if (this.coerced.size > 200) this.coerced.clear();
      this.coerced.set(key, list);
    }
    return this.coerced.get(key);
  }
  get ir() {
    return this.getIr();
  }
  async run(signals = {}, opts = {}) {
    const ir = this.getIr();
    const engine = this.engine;
    const emit = safeEmit(opts) || (() => {});
    let art = engine.artifacts(ir);
    if (!art) {
      const rep = await engine.buildShared(ir, {
        signal: opts.signal,
        onProgress: opts.onProgress,
        onEvent: opts.onEvent,
        scope: null,
      });
      if (!rep.ok) throw new RagError('build', rep.errors.join('\n'));
      art = engine.artifacts(ir);
    }
    // live outputs memoized against other artifacts (a rebuilt corpus) are stale
    if (this.artKey !== art.key) {
      this.memo.clear();
      this.artKey = art.key;
    }
    const t0 = now();
    const trigger = opts.trigger || 'submit';
    const s = { ...ir.signals, ...signals };
    const outputs = {},
      trace = [],
      errors = [];
    const counter = { calls: 0 };
    const sample = engine.sampler(counter, opts.signal);
    const decider = engine.decider(counter, opts.signal);
    const runId = ++this.runs;
    const detail = opts.detail || {};
    emit({ type: 'run', plan: ir.id, trigger, signals: s });
    // a step's output in the trace entry (top hits) and the event stream (hits, context)
    const describe = (n, out, entry) => {
      if (n.out === 'hits' && Array.isArray(out)) {
        entry.top = out
          .slice(0, 50)
          .map((h) => ({ id: h.id, score: h.score, why: h.why, parts: h.parts, reason: h.reason }));
        if (opts.onEvent) emit(hitsEvent(n.id, out, art, detail));
      }
      if (n.out === 'decision' && out)
        entry.decision = Object.fromEntries(
          Object.entries(out).map(([id, a]) => [id, answerLine(a)]),
        );
      if (n.out === 'context' && out && opts.onEvent)
        emit({
          type: 'context',
          node: n.id,
          evidence: arr(out.evidence).map((e) => ({ label: e.label, id: e.id })),
          bytes: out.bytes,
        });
    };
    for (const n of ir.nodes) {
      if (n.phase === 'build') continue;
      if (n.phase === 'signal') {
        outputs[n.id] = s[n.id];
        continue;
      }
      const def = REG.get(n.t);
      const inputs = n.in.map((id) => {
        const v = outputs[id];
        const src = ir.node(id);
        return src.t === 'signal' && (portsOf(n).in[0] || '').startsWith('queries')
          ? this.queries(v)
          : v;
      });
      const input = (portsOf(n).in[0] || '') === 'hits*' ? inputs : inputs[0];
      if (n.when) {
        const decision = (id) => {
          const v = outputs[id];
          return v && v.$decision ? v : null;
        };
        let w;
        try {
          w = isLambda(n.when)
            ? {
                on: !!(await fnOf(n.when, `${n.id}.when`)(s, { decision, signals: s })),
                why: 'when λ',
              }
            : whenHolds(n.when, decision);
        } catch (e) {
          w = { on: true, why: `when failed (${e.message || e}) → run` };
        }
        if (!w.on) {
          outputs[n.id] = whenSkipOutput(n, input);
          const entry = {
            node: n.id,
            t: n.t,
            ms: 0,
            in: countOf(input),
            out: countOf(outputs[n.id]),
            status: 'skipped',
            skippedBy: 'when',
            notes: [`when: ${w.why}`],
          };
          trace.push(entry);
          emit({
            type: 'node',
            node: n.id,
            component: n.t,
            phase: 'end',
            status: 'skipped',
            skippedBy: 'when',
            ms: 0,
            in: entry.in,
            out: entry.out,
            notes: entry.notes,
          });
          continue;
        }
      }
      const nd = hash([n.t, n.p, n.trigger, def.src || '']); // a redefined live component reruns
      const wants = trigger === 'submit' || triggerOf(n) === 'change';
      const m = this.memo.get(n.id);
      const sameInputs =
        m && m.inputs.length === inputs.length && m.inputs.every((v, i) => v === inputs[i]);
      const sameReads = m && Object.entries(m.reads).every(([k, v]) => stable(s[k]) === v);
      const entry = { node: n.id, t: n.t, ms: 0, in: countOf(input), out: 0, status: 'ran' };
      if (m && m.digest === nd && sameInputs && sameReads && (m.full || !wants)) {
        outputs[n.id] = m.out;
        entry.status = m.full ? 'cached' : 'skipped';
        entry.out = countOf(m.out);
        if (m.notes) entry.notes = m.notes;
        if (m.full) describe(n, m.out, entry); // a cached step reports its output like one that ran
        trace.push(entry);
        emit({
          type: 'node',
          node: n.id,
          component: n.t,
          phase: 'end',
          status: entry.status,
          ms: 0,
          in: entry.in,
          out: entry.out,
        });
        continue;
      }
      if (!wants) {
        outputs[n.id] = def.skip === 'null' ? null : input;
        entry.status = 'skipped';
        entry.out = countOf(outputs[n.id]);
        trace.push(entry);
        emit({
          type: 'node',
          node: n.id,
          component: n.t,
          phase: 'end',
          status: 'skipped',
          ms: 0,
          in: entry.in,
          out: entry.out,
        });
        this.memo.set(n.id, { digest: nd, inputs, reads: {}, out: outputs[n.id], full: false });
        continue;
      }
      const reads = {};
      const sp = new Proxy(s, {
        get: (t, k) => {
          if (typeof k === 'string') reads[k] = stable(t[k]);
          return t[k];
        },
      });
      const notes = [];
      const c0 = counter.calls;
      const ctx = this.ctx(
        n,
        ir,
        art,
        sample,
        s,
        notes,
        opts,
        opts.onEvent ? emit : null,
        decider,
        outputs,
      );
      const t1 = now();
      emit({ type: 'node', node: n.id, component: n.t, phase: 'start' });
      try {
        const p = await this.params(n, sp, ctx, art, ir);
        let out = await def.run(sp, ctx, input, p);
        if (n.out === 'hits') out = normHits(out, n.id);
        outputs[n.id] = out;
        entry.out = countOf(out);
        describe(n, out, entry);
        this.memo.set(n.id, { digest: nd, inputs, reads, out, full: true, notes });
      } catch (e) {
        const msg =
          e && (e.message || e.code) ? `${e.code && !e.message ? e.code : e.message}` : String(e);
        entry.status = 'error';
        entry.error = msg;
        errors.push(`${n.id}: ${msg}`);
        outputs[n.id] =
          def.skip === 'null' || n.out === 'answer'
            ? null
            : n.out === 'hits' || n.out === 'queries'
              ? input || []
              : null;
        if (e && e.code === 'cancelled') {
          trace.push(entry);
          break;
        }
      }
      entry.ms = +(now() - t1).toFixed(1);
      timed('run', n.id, entry.ms, `${n.t} · ${entry.status} · ${entry.in}→${entry.out}`);
      entry.calls = counter.calls - c0;
      if (notes.length) entry.notes = notes;
      trace.push(entry);
      if (opts.onNode) opts.onNode(entry);
      emit({
        type: 'node',
        node: n.id,
        component: n.t,
        phase: 'end',
        status: entry.status,
        ms: entry.ms,
        in: entry.in,
        out: entry.out,
        calls: entry.calls,
        ...(entry.notes ? { notes: entry.notes } : {}),
        ...(entry.error ? { error: entry.error } : {}),
      });
    }
    const o = ir.outputs;
    const result = {
      runId,
      trigger,
      signals: s,
      hits: o.hits ? outputs[o.hits] || [] : [],
      context: o.context ? outputs[o.context] : null,
      answer: o.answer ? outputs[o.answer] : null,
      outputs,
      trace,
      errors,
      ms: +(now() - t0).toFixed(1),
      calls: counter.calls,
    };
    decorateResult(result, art);
    emit({ type: 'runEnd', ms: result.ms, calls: result.calls, errors });
    this.last = result;
    return result;
  }
  async params(n, sp, ctx, art, ir) {
    const p = await this.engine.resolveBakes(n, n.p, art.byId, ir);
    const keep = new Set(REG.get(n.t).lambdas || []);
    let out = p;
    for (const [k, v] of Object.entries(p)) {
      if (isLambda(v) && !keep.has(k)) {
        if (out === p) out = { ...p };
        out[k] = await fnOf(v, `${n.id}.${k}`)(sp, ctx);
      }
    }
    return out;
  }
  ctx(n, ir, art, sample, s, notes, opts, emit, decider, outputs) {
    let chunkList = null; // all chunks, only for components that ask (ctx.chunks)
    const allChunks = () => chunkList || (chunkList = [...art.chunks.values()]);
    let lastText = ''; // ctx.emit takes the text so far; events carry what is new
    return {
      node: n,
      sample: emit ? eventSample(sample, n.id, emit) : sample,
      decide: decider ? (emit ? eventDecide(decider, n.id, emit) : decider) : makeDecide(null),
      // a classify node's answers in this run (null if it did not run): RAGWB-007 §3.3
      decision: (id) => {
        const v = outputs ? outputs[String(id)] : null;
        return v && v.$decision ? v : null;
      },
      signal: opts.signal,
      signals: Object.freeze({ ...s }),
      synonyms: art.synonyms,
      tokens,
      clip,
      index: (name) => art.indexes.get(String(name)) || null,
      indexNames: () => [...art.indexes.keys()],
      firstIndex: (kind) => [...art.indexes.values()].find((x) => x.kind === kind) || null,
      chunk: (id) => art.chunks.get(id) || null,
      chunks: (f) => (f ? allChunks().filter(f) : allChunks()),
      docChunks: (docId) =>
        (art.order.get(docId) || []).map((id) => art.chunks.get(id)).filter(Boolean),
      doc: (id) => art.docs.get(id) || null,
      docs: () => [...art.docs.values()],
      links: (docId) => art.links.get(docId) || [],
      neighbors: (id, o = {}) => {
        const c = art.chunks.get(id);
        if (!c) return [];
        const list = art.order.get(c.docId) || [],
          i = list.indexOf(id);
        if (i < 0) return [];
        return [
          ...list.slice(Math.max(0, i - (o.before ?? 1)), i),
          ...list.slice(i + 1, i + 1 + (o.after ?? 1)),
        ].map((x) => art.chunks.get(x));
      },
      log: (...a) => this.engine.env.log && this.engine.env.log(`[${n.id}]`, ...a),
      note: (t) => {
        notes.push(String(t));
        if (emit) emit({ type: 'note', node: n.id, text: String(t) });
      },
      emit: (text) => {
        if (opts.onText) opts.onText(n.id, text);
        if (!emit) return;
        text = String(text);
        const delta = text.startsWith(lastText) ? text.slice(lastText.length) : text;
        if (!text.startsWith(lastText)) emit({ type: 'text', node: n.id, reset: true, delta: '' });
        lastText = text;
        if (delta) emit({ type: 'text', node: n.id, delta });
      },
      partial: (hits) => {
        if (emit) emit(hitsEvent(n.id, normHits(hits, n.id), art, opts.detail || {}, true));
      },
      progress: (done, total, unit) => {
        if (emit)
          emit({
            type: 'progress',
            node: n.id,
            done: Number(done),
            total: Number(total),
            ...(unit ? { unit: String(unit) } : {}),
          });
      },
    };
  }
}

function decorateResult(result, art) {
  const def = (k, v) => Object.defineProperty(result, k, { value: v, enumerable: false });
  def('chunk', (id) => art.chunks.get(id) || null);
  def('top', (n = 5, from) => {
    const list = from ? result.outputs[from] || [] : result.hits;
    return arr(list)
      .slice(0, n)
      .map((h) => {
        const c = art.chunks.get(h.id) || {};
        return {
          id: h.id,
          score: h.score,
          doc: c.title,
          heading: (c.headingPath || []).join(' › '),
          ...(h.reason ? { reason: h.reason } : {}),
        };
      });
  });
  def('explain', (id) => explain(result, typeof id === 'object' ? id.id : id));
  def('toJSON', () => ({
    trigger: result.trigger,
    q: result.signals.q,
    ms: result.ms,
    calls: result.calls,
    top: result.top(8),
    answer: result.answer ? clip(result.answer.text, 1200) : null,
    errors: result.errors,
    trace: result.trace.map(
      (t) => `${t.node} ${t.status} ${t.ms}ms ${t.in}→${t.out}${t.error ? ' ✗ ' + t.error : ''}`,
    ),
  }));
}

function explain(result, id) {
  const out = { chunk: id, path: [] };
  for (const t of result.trace) {
    if (!t.top) continue;
    const i = t.top.findIndex((h) => h.id === id);
    if (i < 0) {
      if (t.in) out.path.push({ node: t.node, rank: null });
      continue;
    }
    const h = t.top[i];
    out.path.push({
      node: t.node,
      rank: i + 1,
      score: h.score,
      ...(h.why ? { terms: h.why } : {}),
      ...(h.parts ? { parts: h.parts } : {}),
      ...(h.reason ? { reason: h.reason } : {}),
    });
  }
  const c = result.chunk(id);
  if (c) out.where = `${c.title} › ${c.headingPath.join(' › ')}`;
  return out;
}

export { explain, Session };
