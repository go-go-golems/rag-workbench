import { resolveTarget } from '../api/target.js';
import { RagError } from '../core/errors.ts';
import { fnOf, lam } from '../core/lambda.js';
import { arr, clip, isObj, now, uniq } from '../core/util.js';
import { applyOps } from '../graph/graph.js';

// ────────────────────────────────────────────────────────────── evals
function testOne(pat, strings) {
  if (pat == null) return true;
  if (pat instanceof RegExp) return strings.some((s) => pat.test(s));
  if (isObj(pat) && pat.$re) return testOne(new RegExp(pat.$re, pat.flags || 'i'), strings);
  if (Array.isArray(pat)) return pat.some((p) => testOne(p, strings));
  const l = String(pat).toLowerCase();
  return strings.some((s) => String(s).toLowerCase().includes(l));
}

function relevant(expect, chunk, doc) {
  if (!chunk) return false;
  const d = doc || {};
  const docOk = testOne(expect.doc, [
    chunk.title,
    d.name || '',
    chunk.docId,
    ...arr((d.meta || {}).aliases),
  ]);
  const secOk = expect.any || testOne(expect.section, [chunk.headingPath.join(' › ')]);
  return docOk && secOk;
}

const showPat = (p) =>
  p instanceof RegExp ? '/' + p.source + '/' + p.flags : isObj(p) && p.$re ? '/' + p.$re + '/' : p;

function metricK(m) {
  const r = /@(\d+)$/.exec(m);
  return r ? +r[1] : null;
}

class Evals {
  constructor(st) {
    this.st = st;
    Object.freeze(this);
  }
  get id() {
    return this.st.id;
  }
  get cases() {
    return this.st.cases;
  }
  _with(p) {
    return new Evals({ ...this.st, ...p });
  }
  as(id) {
    return this._with({ id });
  }
  case(q, expect = {}, opts = {}) {
    return this._with({ cases: [...this.st.cases, { q, expect, ...opts }] });
  }
  concat(other) {
    return this._with({ cases: [...this.st.cases, ...other.cases] });
  }
  metrics(ms) {
    return this._with({ metrics: arr(ms) });
  }
  judge(name, fn) {
    return this._with({ judges: { ...this.st.judges, [name]: lam(fn) } });
  }
  filter(f) {
    return this._with({ cases: this.st.cases.filter(f) });
  }
  toJSON() {
    return {
      id: this.st.id,
      cases: this.st.cases.map((c) => ({
        ...c,
        expect: Object.fromEntries(Object.entries(c.expect).map(([k, v]) => [k, showPat(v)])),
      })),
      metrics: this.st.metrics,
    };
  }
  async run(target, opts = {}) {
    const { ir, engine, name } = resolveTarget(target, opts.engine);
    const metrics = this.st.metrics;
    const k = Math.max(10, ...metrics.map(metricK).filter(Boolean));
    const trigger = opts.trigger || 'change';
    const session = engine.session(ir);
    const t0 = now();
    let art = engine.artifacts(ir);
    if (!art) {
      const r = await engine.build(ir);
      if (!r.ok) throw new RagError('build', r.errors.join('\n'));
      art = engine.artifacts(ir);
    }
    const cases = [];
    let calls = 0;
    let all = null; // every chunk, read once per evals run (not per case)
    const allChunks = () => all || (all = [...art.chunks.values()]);
    for (const [i, c] of this.st.cases.entries()) {
      if (opts.signal && opts.signal.aborted) throw new RagError('run', 'evals stopped');
      const res = await session.run(
        {
          ...(opts.signals || {}),
          ...(c.signals || {}),
          q: c.q,
          k: opts.k ?? Math.max(k, ir.signals.k || 0),
        },
        { trigger, signal: opts.signal },
      );
      calls += res.calls;
      const hits =
        res.hits && res.hits.length
          ? res.hits
          : ((res.answer && res.answer.evidence) || []).map((e) => ({ id: e.id }));
      const rel = hits.map((h) =>
        relevant(c.expect, art.chunks.get(h.id), art.docs.get((art.chunks.get(h.id) || {}).docId)),
      );
      const rank = rel.indexOf(true) + 1 || null;
      const R = allChunks().filter((ch) => relevant(c.expect, ch, art.docs.get(ch.docId))).length;
      const row = {
        i: i + 1,
        q: c.q,
        rank,
        synthetic: !!c.synthetic,
        R,
        expect: Object.fromEntries(Object.entries(c.expect).map(([kk, v]) => [kk, showPat(v)])),
        top: res.top(3),
        ms: res.ms,
        m: {},
      };
      for (const m of metrics) {
        const mk = metricK(m),
          name2 = m.replace(/@\d+$/, '');
        if (name2 === 'hit') row.m[m] = rank && rank <= mk ? 1 : 0;
        else if (name2 === 'mrr') row.m[m] = rank ? 1 / rank : 0;
        else if (name2 === 'recall') row.m[m] = R ? rel.slice(0, mk).filter(Boolean).length / R : 0;
        else if (name2 === 'ndcg') {
          const dcg = rel.slice(0, mk).reduce((a, r, j) => a + (r ? 1 / Math.log2(j + 2) : 0), 0);
          const idcg = Array.from(
            { length: Math.min(R, mk) },
            (_, j) => 1 / Math.log2(j + 2),
          ).reduce((a, b) => a + b, 0);
          row.m[m] = idcg ? dcg / idcg : 0;
        }
      }
      if (res.answer) {
        row.answer = clip(res.answer.text, 600);
        if (c.expect.answerHas)
          row.m.answerHas = testOne(c.expect.answerHas, [res.answer.text]) ? 1 : 0;
        if (opts.judge)
          for (const [jn, jf] of Object.entries(this.st.judges || {})) {
            try {
              row.m[jn] = Number(
                await fnOf(jf, `judge '${jn}'`)(
                  res.signals,
                  { sample: engine.sampler({ calls: 0 }, opts.signal) },
                  {
                    q: c.q,
                    answer: res.answer.text,
                    evidence: (res.answer.evidence || []).map(
                      (e) => (art.chunks.get(e.id) || {}).text || '',
                    ),
                  },
                ),
              );
              calls++;
            } catch (e) {
              row.m[jn] = null;
              row.judgeError = e.message;
            }
          }
      }
      if (res.errors.length) row.errors = res.errors;
      cases.push(row);
      if (opts.onCase) opts.onCase(row, i, this.st.cases.length);
    }
    const keys = uniq(cases.flatMap((c) => Object.keys(c.m)));
    const summary = { n: cases.length };
    for (const key of keys) {
      const vs = cases.map((c) => c.m[key]).filter((v) => v != null);
      summary[key] = vs.length ? +(vs.reduce((a, b) => a + b, 0) / vs.length).toFixed(3) : null;
    }
    const msList = cases.map((c) => c.ms).sort((a, b) => a - b);
    summary.p50ms = msList.length ? +msList[Math.floor(msList.length / 2)].toFixed(1) : 0;
    summary.calls = calls;
    const first = metrics[0];
    const report = {
      id: this.st.id,
      target: name,
      irDigest: ir.digest,
      trigger,
      at: Date.now(),
      ms: +(now() - t0).toFixed(1),
      summary,
      cases,
      misses: cases
        .filter((c) => !c.m[first])
        .map((c) => ({
          q: c.q,
          rank: c.rank,
          got: c.top[0] ? `${c.top[0].doc} › ${c.top[0].heading}` : null,
          want: c.expect,
        })),
    };
    Object.defineProperty(report, 'table', {
      value: () =>
        cases.map((c) => ({
          '#': c.i,
          question: clip(c.q, 70),
          rank: c.rank ?? '—',
          ...Object.fromEntries(
            Object.entries(c.m).map(([kk, v]) => [kk, v == null ? '—' : +(+v).toFixed(2)]),
          ),
        })),
      enumerable: false,
    });
    return report;
  }
  async compare(targets, opts = {}) {
    const reports = [];
    for (const t of arr(targets)) reports.push(await this.run(t, opts));
    const metrics = Object.keys(reports[0].summary).filter((k) => k !== 'n');
    const rows = reports.map((r) => ({
      variant: r.target,
      ...Object.fromEntries(metrics.map((m) => [m, r.summary[m]])),
    }));
    const delta =
      reports.length > 1
        ? Object.fromEntries(
            metrics
              .filter((m) => typeof reports[0].summary[m] === 'number')
              .map((m) => [
                m,
                +(reports[reports.length - 1].summary[m] - reports[0].summary[m]).toFixed(3),
              ]),
          )
        : {};
    const cases = reports[0].cases.map((c, i) => ({
      q: clip(c.q, 60),
      ...Object.fromEntries(reports.map((r) => [r.target, r.cases[i].rank ?? '—'])),
    }));
    return { rows, delta, cases, reports };
  }
}

function evals(id) {
  return new Evals({
    id: id || 'evals',
    cases: [],
    metrics: ['hit@5', 'mrr', 'ndcg@10'],
    judges: {},
  });
}

evals.generate = async function (target, opts = {}) {
  const { ir, engine } = resolveTarget(target, opts.engine);
  let art = engine.artifacts(ir);
  if (!art) {
    await engine.build(ir);
    art = engine.artifacts(ir);
  }
  const n = opts.n ?? 12;
  let seed = opts.seed ?? 7;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };
  const pool = [...art.chunks.values()].filter(
    (c) => c.kind === 'section' && c.tokens > 120 && c.headingPath.length,
  );
  const picked = [];
  while (picked.length < Math.min(n, pool.length)) {
    const c = pool[Math.floor(rand() * pool.length)];
    if (!picked.includes(c)) picked.push(c);
  }
  const sample = engine.sampler({ calls: 0 }, opts.signal);
  let out = evals(opts.id || 'synthetic');
  for (let i = 0; i < picked.length; i += 8) {
    const batch = picked.slice(i, i + 8);
    const r = await sample.json(
      'For each numbered section of an engineering report, write one question a colleague might ask that this section answers well. Phrase it naturally, without copying the heading. Reply with only JSON: {"questions": [{"n": 1, "q": "…"}]}.\n\n' +
        batch
          .map(
            (c, j) => `(${j + 1}) ${c.title} › ${c.headingPath.join(' › ')}\n${clip(c.text, 1500)}`,
          )
          .join('\n\n'),
      { modelTier: opts.tier || 'quick' },
    );
    for (const x of arr(r && r.questions)) {
      const c = batch[(x.n | 0) - 1];
      if (c && x.q)
        out = out.case(
          String(x.q),
          { doc: c.title, section: c.headingPath.join(' › ') },
          { synthetic: true },
        );
    }
  }
  return out;
};

// ────────────────────────────────────────────────────────────── sweep
function cartesian(grid) {
  const keys = Object.keys(grid);
  return keys.reduce(
    (acc, k) => acc.flatMap((c) => arr(grid[k]).map((v) => ({ ...c, [k]: v }))),
    [{}],
  );
}

function sweep(target, grid) {
  const base = resolveTarget(target);
  const configs = cartesian(grid);
  return {
    configs,
    async run(ev, opts = {}) {
      const metric = opts.metric || ev.st.metrics[0];
      const rows = [];
      for (const [i, cfg] of configs.entries()) {
        const ops = Object.entries(cfg).map(([key, value]) => {
          const dot = key.indexOf('.');
          return { op: 'set', node: key.slice(0, dot), path: key.slice(dot + 1), value };
        });
        const ir = applyOps(base.ir, ops);
        const rep = await base.engine.build(ir);
        const computed = Object.values(rep.nodes)
          .filter((x) => !x.reused)
          .map((x) => x.id);
        const r = await ev.run({ ir, engine: base.engine, name: 'cfg' + (i + 1) }, opts);
        rows.push({ config: cfg, ...r.summary, rebuilt: computed });
        if (opts.onConfig) opts.onConfig(rows[rows.length - 1], i, configs.length);
      }
      rows.sort((a, b) => (b[metric] ?? 0) - (a[metric] ?? 0));
      return { metric, rows, best: rows[0], top: (n = 3) => rows.slice(0, n) };
    },
  };
}

export { Evals, evals, sweep };
