import { references, rewriteReferences, outputReferences } from './references.ts';
import { accepts, optArgs, portsOf, REG } from '../components/registry.js';
import { RagError } from '../core/errors.ts';
import { slug } from '../core/identity.ts';
import { checkLambda, lam, signalReads } from '../core/lambda.js';
import { arr, clone, hash, isBake, isLambda, isObj, now, setPath, uniq } from '../core/util.js';
import { answerLine } from '../engine/decision.js';
import { timed } from '../engine/environment.js';

function rag_bake(f) {
  return { $bake: lam(f).$fn };
}

// ────────────────────────────────────────────────────────────── specs
// Specs are immutable: every builder call returns a new spec.
function splitOpts(p) {
  const { id, trigger, from, in: inn, when, ...rest } = p || {};
  return [rest, { id, trigger, from: from ?? inn, ...(when != null ? { when } : {}) }];
}

function normalize(type, args) {
  const def = REG.get(type);
  if (!def)
    throw new RagError(
      'compile',
      `unknown component '${type}'. Known: ${[...REG.keys()].join(', ')}`,
    );
  const raw =
    args.length === 1 && isObj(args[0]) && args[0].$c
      ? args[0]
      : { $c: type, p: (def.args || optArgs)(...args) };
  const [p, opts] = splitOpts(raw.p);
  const d2 = REG.get(raw.$c) || def;
  return {
    t: raw.$c,
    p: normParams({ ...(d2.params || {}), ...p }),
    opts: { ...opts, ...(raw.opts || {}) },
  };
}

// functions in params become stored lambdas; regexps become {$re}
function normParams(v) {
  if (typeof v === 'function') return lam(v);
  if (v instanceof RegExp) return { $re: v.source, flags: v.flags };
  if (Array.isArray(v)) return v.map(normParams);
  if (isObj(v) && !isLambda(v) && !isBake(v)) {
    const o = {};
    for (const [k, x] of Object.entries(v)) o[k] = normParams(x);
    return o;
  }
  return v;
}

function freshId(nodes, base) {
  base = slug(base).replace(/-/g, '_').replace(/^(\d)/, 'n$1') || 'node';
  const ids = new Set(nodes.map((n) => n.id));
  if (!ids.has(base)) return base;
  let i = 2;
  while (ids.has(base + '_' + i)) i++;
  return base + '_' + i;
}

class CorpusSpec {
  constructor(st) {
    this.st = st;
    Object.freeze(this);
  }
  get id() {
    return this.st.id;
  }
  _with(patch) {
    return new CorpusSpec({ ...this.st, ...patch });
  }
  _add(node, cursorKey) {
    const n = { phase: 'build', trigger: undefined, ...node };
    return this._with({
      nodes: [...this.st.nodes, n],
      cursor: { ...this.st.cursor, [cursorKey]: n.id },
    });
  }
  as(id) {
    return this._with({ id });
  }
  from(src) {
    const c =
      typeof src === 'string'
        ? normalize('source.files', [src])
        : Array.isArray(src)
          ? normalize('source.inline', [src])
          : normalize(src.$c || 'source.files', [src]);
    return this._add({ id: c.opts.id || 'files', t: c.t, in: [], p: c.p }, 'files');
  }
  parse(parser) {
    const c = parser ? normalize(parser.$c, [parser]) : normalize('parse.markdown', [{}]);
    return this._add(
      { id: c.opts.id || 'parse', t: c.t, in: [this.st.cursor.files], p: c.p },
      'docs',
    );
  }
  chunk(a, b, o) {
    const named = typeof a === 'string' && !/=>|function|\(/.test(a);
    const name = named ? a : null,
      spec = named ? b : a,
      opts = named ? o : b;
    const c = spec && spec.$c ? normalize(spec.$c, [spec]) : normalize('chunk', [spec, opts]);
    const id =
      c.opts.id ||
      (name
        ? slug(name).replace(/-/g, '_')
        : this.st.cursor.chunks
          ? freshId(this.st.nodes, 'chunks')
          : 'chunks');
    const from = c.opts.from || this.st.cursor.docs;
    const next = this._add(
      { id, t: c.t, in: [from], p: c.p },
      name && this.st.cursor.chunks ? '_' + id : 'chunks',
    );
    return next;
  }
  enrich(name, spec, opts = {}) {
    const c =
      spec && spec.$c
        ? normalize(spec.$c, [{ ...spec.p, name }])
        : normalize('enrich', [spec, { ...opts, name }]);
    c.p.name = name;
    const src = opts.from || c.opts.from || this.st.cursor.chunks;
    const id = opts.id || c.opts.id || 'enrich_' + slug(name).replace(/-/g, '_');
    return this._add(
      { id, t: c.t, in: [src], p: c.p },
      src === this.st.cursor.chunks ? 'chunks' : '_' + id,
    );
  }
  index(name, spec, opts = {}) {
    const c = normalize(spec.$c, [spec]);
    const from = opts.from || c.opts.from || this.st.cursor.chunks;
    return this._add({ id: 'ix-' + name, t: c.t, in: [from], p: { ...c.p, name } }, '_ix');
  }
  synonyms(table) {
    const c = normalize('synonyms', [table]);
    return this._add({ id: 'synonyms', t: 'synonyms', in: [this.st.cursor.docs], p: c.p }, '_syn');
  }
  use(fragment, params) {
    return fragment.apply(this, params);
  }
  then(comp, opts = {}) {
    const c = normalize(comp.$c, [comp]);
    const def = REG.get(c.t);
    const from = c.opts.from || opts.from || this.st.cursor[def.in[0]];
    return this._add(
      {
        id: c.opts.id || opts.id || freshId(this.st.nodes, c.t.split('.').pop()),
        t: c.t,
        in: arr(from),
        p: c.p,
      },
      def.out,
    );
  }
  toJSON() {
    return { kind: 'corpus', id: this.st.id, nodes: this.st.nodes };
  }
}

class PlanSpec {
  constructor(st) {
    this.st = st;
  }
  get id() {
    return this.st.id;
  }
  _with(patch) {
    return makePlan({ ...this.st, ...patch });
  }
  as(id) {
    return this._with({ id });
  }
  over(corpus) {
    return this._with({ corpus });
  }
  signals(obj) {
    const signals = { ...this.st.signals, ...obj };
    const cur = { ...this.st.cursor };
    if ('q' in obj && !cur.queries) cur.queries = 'q';
    return this._with({ signals, cursor: cur });
  }
  patch(ops) {
    return this._with({ ops: [...(this.st.ops || []), ...arr(ops)] });
  }
  variant(id, f) {
    return typeof f === 'function' ? f(this.as(id)) : this.as(id).patch(f);
  }
  use(fragment, params) {
    return fragment.apply(this, params);
  }
  then(comp, opts = {}) {
    const c = normalize(comp.$c, [comp]);
    return this._addNode(c.t, c.p, { ...opts, ...c.opts });
  }
  _addNode(t, p, opts = {}) {
    const def = REG.get(t);
    const ports = def.ports ? def.ports(p) : def;
    const nodes = this.st.nodes;
    const cur = { ...this.st.cursor, hits: [...(this.st.cursor.hits || [])] };
    let inn;
    if (opts.from != null) inn = arr(opts.from).map((x) => (typeof x === 'object' ? x.id : x));
    else {
      inn = [];
      for (const port of ports.in) {
        if (port === 'hits*') {
          inn.push(...cur.hits);
          continue;
        }
        if (port === 'hits') {
          if (cur.hits.length > 1)
            throw new RagError(
              'compile',
              `${t}: ${cur.hits.length} hit streams (${cur.hits.join(', ')}) need a fuse step first, e.g. .fuse.rrf()`,
            );
          if (cur.hits[0]) inn.push(cur.hits[0]);
          continue;
        }
        if (cur[port]) inn.push(cur[port]);
      }
    }
    const id =
      opts.id ||
      freshId(
        [
          ...nodes,
          ...Object.keys(this.st.signals).map((k) => ({ id: k })),
          ...(this.st.corpus ? this.st.corpus.st.nodes : []),
        ],
        def.id ? def.id(p) : t.split('.')[0],
      );
    const node = { id, t, phase: 'live', in: inn, p };
    if (opts.trigger) node.trigger = opts.trigger;
    if (opts.when != null) node.when = normWhen(opts.when, id);
    if (ports.out === 'hits') cur.hits = def.source && opts.from == null ? [...cur.hits, id] : [id];
    else if (ports.out) cur[ports.out] = id;
    return this._with({ nodes: [...nodes, node], cursor: cur });
  }
  toJSON() {
    return {
      kind: 'plan',
      id: this.st.id,
      signals: this.st.signals,
      nodes: this.st.nodes,
      ops: this.st.ops,
    };
  }
}

// family sugar: plan.retrieve(...), plan.fuse.rrf(...), plan.expand.links(...)
// Installed as prototype getters, so components defined later appear on every plan.
function familyAccessor(spec, fam) {
  const base = REG.get(fam);
  const add =
    (type) =>
    (...args) => {
      const c = normalize(type, args);
      return spec._addNode(c.t, c.p, c.opts);
    };
  const f = base && base.phase === 'live' ? add(fam) : {};
  for (const def of REG.values())
    if (def.phase === 'live' && def.type.startsWith(fam + '.'))
      f[def.type.slice(fam.length + 1)] = add(def.type);
  return f;
}

function installFamilies() {
  for (const d of REG.values()) {
    if (d.phase !== 'live') continue;
    const fam = d.type.split('.')[0];
    if (Object.prototype.hasOwnProperty.call(PlanSpec.prototype, fam)) continue;
    Object.defineProperty(PlanSpec.prototype, fam, {
      get() {
        return familyAccessor(this, fam);
      },
      configurable: true,
    });
  }
}

function makePlan(st) {
  return Object.freeze(new PlanSpec(st));
}

// graph form: explicit wiring, same nodes
function graph(id, fn) {
  let plan = makePlan({ id, corpus: null, signals: {}, nodes: [], cursor: {}, outputs: {} });
  const outputs = {};
  const g = {
    over(corpus) {
      plan = plan.over(corpus);
      return g;
    },
    signal(name, value) {
      plan = plan.signals({ [name]: value });
      return { id: name, kind: 'signal' };
    },
    node(nid, comp, opts = {}) {
      const c = normalize(comp.$c, [comp]);
      plan = plan._addNode(c.t, c.p, {
        ...c.opts,
        ...opts,
        id: nid,
        from: arr(opts.from ?? c.opts.from).map((x) => (typeof x === 'object' ? x.id : x)),
      });
      return { id: nid, kind: 'node' };
    },
    output(name, h) {
      outputs[name] = h.id;
      return g;
    },
  };
  fn(g);
  return plan._with({ outputs });
}

class Fragment {
  constructor(id, fn) {
    this.id = id;
    this.fn = fn;
  }
  apply(spec, params) {
    return this.fn(spec, params || {});
  }
}

// ────────────────────────────────────────────────────────────── IR
// ── when (RAGWB-007 §3.3): a live node runs only if its condition holds.
// Forms: {decision: 'node.question', in, not, min, max} or a lambda (s, ctx) => bool.
const WHEN_KEYS = ['decision', 'in', 'not', 'min', 'max'];

function normWhen(w, id) {
  if (typeof w === 'function' || typeof w === 'string')
    throw new RagError(
      'compile',
      `${id}: callback conditions require {$fn, bindings: {name: decisionNode}}`,
    );
  if (isLambda(w)) {
    if (!isObj(w.bindings))
      throw new RagError('compile', `${id}: callback conditions require named decision bindings`);
    return { ...w, bindings: { ...w.bindings } };
  }
  if (!isObj(w) || typeof w.decision !== 'string' || !/^[A-Za-z][\w-]*\.[\w.-]+$/.test(w.decision))
    throw new RagError(
      'compile',
      `${id}: when must be (s, ctx) => boolean or {decision: 'node.question', in?, not?, min?, max?}`,
    );
  const bad = Object.keys(w).filter((k) => !WHEN_KEYS.includes(k));
  if (bad.length)
    throw new RagError(
      'compile',
      `${id}: when has unknown key${bad.length > 1 ? 's' : ''} ${bad.join(', ')} (expected ${WHEN_KEYS.join(', ')})`,
    );
  const out = { decision: w.decision };
  if (w.in != null) out.in = arr(w.in).map(String);
  if (w.not != null) out.not = arr(w.not).map(String);
  if (w.min != null) out.min = Number(w.min);
  if (w.max != null) out.max = Number(w.max);
  return out;
}

// does a declarative condition hold for these answers? Fails open (runs) when the decision is missing.
function whenHolds(w, decision) {
  const [nodeId, ...rest] = w.decision.split('.');
  const qid = rest.join('.');
  const d = decision(nodeId);
  const a = d && d[qid];
  if (!a) return { on: true, why: `${w.decision} unavailable → run` };
  let on = true;
  if (a.type === 'choice') {
    const names = Object.keys(a.probabilities || {});
    const chosen = w.in ? w.in : w.not ? names.filter((x) => !w.not.includes(x)) : [a.choice];
    if (w.min != null || w.max != null) {
      const mass = chosen.reduce((t, x) => t + (a.probabilities[x] || 0), 0);
      if (w.min != null) on = on && mass >= w.min;
      if (w.max != null) on = on && mass <= w.max;
    } else {
      if (w.in) on = on && w.in.includes(a.choice);
      if (w.not) on = on && !w.not.includes(a.choice);
    }
  } else {
    const v = a.type === 'noul' ? a.probability : a.score;
    if (w.min != null) on = on && v >= w.min;
    if (w.max != null) on = on && v <= w.max;
    if (w.in) on = on && w.in.includes(String(v));
  }
  return { on, why: `${w.decision} = ${answerLine(a)}` };
}

// what a node skipped by when outputs: its input for same-type ports, else empty
function whenSkipOutput(n, input) {
  const ports = portsOf(n);
  const inT = ports.in[0] || '';
  if (inT === ports.out) return input;
  if (ports.out === 'hits') return [];
  if (ports.out === 'queries') return arr(input);
  return null;
}

const ID_RE = /^[A-Za-z][\w-]*$/;

function finalize(raw) {
  const problems = [];
  const nodes = raw.nodes.map((n) => ({
    ...n,
    in: [...(n.in || [])],
    p: n.p || {},
    ...(n.when ? { when: normWhen(n.when, n.id) } : {}),
  }));
  const signals = { ...(raw.signals || {}) };
  for (const k of Object.keys(signals))
    if (!nodes.find((n) => n.id === k))
      nodes.push({ id: k, t: 'signal', phase: 'signal', in: [], p: {} });
  const byId = new Map();
  for (const n of nodes) {
    if (!ID_RE.test(n.id))
      problems.push(
        `node id '${n.id}' must start with a letter and use letters, digits, _ or - (no dots)`,
      );
    if (byId.has(n.id)) problems.push(`duplicate node id '${n.id}'`);
    byId.set(n.id, n);
  }
  const edges = [];
  for (const n of nodes) {
    if (n.t === 'signal') {
      n.out = 'signal';
      continue;
    }
    const def = REG.get(n.t);
    if (!def) {
      problems.push(`${n.id}: unknown component '${n.t}'`);
      continue;
    }
    n.phase = def.phase;
    const ports = portsOf(n);
    n.out = ports.out;
    const variadic = ports.in[0] === 'hits*';
    if (!variadic && n.in.length !== ports.in.length)
      problems.push(
        `${n.id} (${n.t}) takes ${ports.in.length ? ports.in.join(', ') : 'no inputs'} but is wired to [${n.in.join(', ')}]`,
      );
    n.in.forEach((src, i) => {
      const from = byId.get(src);
      if (!from) {
        problems.push(`${n.id}: input '${src}' does not exist`);
        return;
      }
      const port = variadic ? 'hits' : ports.in[i];
      const type = from.t === 'signal' ? 'signal' : portsOf(from).out;
      if (port && !accepts(port, type))
        problems.push(`${n.id}: input '${src}' gives ${type} but this port takes ${port}`);
      if (def.phase === 'build' && from.phase === 'live')
        problems.push(`${n.id}: a build node cannot read live node '${src}'`);
      edges.push([src, n.id, from.t === 'signal' ? 'signal' : 'data']);
    });
    const refs = references(n, def);
    n.uses = uniq(refs.filter((r) => r.kind === 'artifact').map((r) => r.target));
    if (n.t === 'retrieve' && byId.has('synonyms')) n.uses.push('synonyms');
    for (const u of n.uses) {
      if (!byId.has(u))
        problems.push(
          `${n.id}: uses '${u}', which does not exist${
            u.startsWith('ix-')
              ? ` (indexes: ${
                  nodes
                    .filter((x) => x.id.startsWith('ix-'))
                    .map((x) => x.id.slice(3))
                    .join(', ') || 'none'
                })`
              : ''
          }`,
        );
      else edges.push([u, n.id, 'artifact']);
    }
    if (n.when) {
      if (def.phase !== 'live') problems.push(`${n.id}: when applies only to live nodes`);
      if (isLambda(n.when)) {
        const err = checkLambda(n.when, `${n.id}.when`);
        if (err) problems.push(err);
      }
      for (const r of refs.filter((r) => r.kind === 'decision').map((r) => r.target)) {
        const from = byId.get(r);
        if (!from) problems.push(`${n.id}: when reads decision '${r}', which does not exist`);
        else if (from.t === 'signal' || portsOf(from).out !== 'decision')
          problems.push(`${n.id}: when reads '${r}', which is not a decision node (classify)`);
        else edges.push([r, n.id, 'decision']);
      }
    }
    const reads = new Set(def.reads ? def.reads(n.p) : []);
    const walk = (v, key) => {
      if (isLambda(v)) {
        const err = checkLambda(v, `${n.id}.${key}`);
        if (err) problems.push(err);
        if (def.phase === 'live') signalReads(v.$fn).forEach((r) => reads.add(r));
      } else if (isBake(v)) {
        const err = checkLambda(v, `${n.id}.${key} (bake)`);
        if (err) problems.push(err);
      } else if (isObj(v)) for (const [k, x] of Object.entries(v)) walk(x, key + '.' + k);
    };
    for (const [k, v] of Object.entries(n.p)) walk(v, k);
    n.reads = [...reads];
    for (const r of n.reads)
      if (byId.has(r) && byId.get(r).t === 'signal' && !n.in.includes(r))
        edges.push([r, n.id, 'signal']);
  }
  // topological order
  const indeg = new Map(nodes.map((n) => [n.id, 0])),
    out = new Map(nodes.map((n) => [n.id, []]));
  for (const [a, b] of edges) {
    if (!indeg.has(a) || !indeg.has(b)) continue;
    indeg.set(b, indeg.get(b) + 1);
    out.get(a).push(b);
  }
  const order = [],
    q = nodes.filter((n) => !indeg.get(n.id)).map((n) => n.id);
  while (q.length) {
    const id = q.shift();
    order.push(id);
    for (const b of out.get(id)) {
      indeg.set(b, indeg.get(b) - 1);
      if (!indeg.get(b)) q.push(b);
    }
  }
  if (order.length < nodes.length)
    problems.push(
      'the graph has a cycle through: ' +
        nodes
          .filter((n) => !order.includes(n.id))
          .map((n) => n.id)
          .join(', '),
    );
  // outputs
  const liveOrder = order.filter((id) => byId.get(id).phase === 'live');
  const lastOf = (type) => [...liveOrder].reverse().find((id) => byId.get(id).out === type);
  const pinned = uniq(
    raw.pinned || Object.keys(raw.outputs || {}).filter((k) => (raw.outputs || {})[k]),
  );
  const outputs = {};
  for (const k of pinned) outputs[k] = raw.outputs[k];
  if (!outputs.answer) outputs.answer = lastOf('answer');
  if (!outputs.context) outputs.context = lastOf('context');
  if (!outputs.hits) {
    const packer = outputs.context && byId.get(outputs.context);
    const beforePack = packer && packer.in[0];
    const rerank = [...liveOrder].reverse().find((id) => /^rerank/.test(byId.get(id).t));
    outputs.hits = rerank || beforePack || lastOf('hits');
  }
  for (const [k, v] of Object.entries(outputs))
    if (v && !byId.has(v)) problems.push(`output ${k} points at missing node '${v}'`);
  if (problems.length)
    throw new RagError(
      'compile',
      problems.length +
        ' problem' +
        (problems.length > 1 ? 's' : '') +
        ':\n  ' +
        problems.join('\n  '),
      { problems },
    );
  const ir = {
    kind: 'rag',
    version: 2,
    id: raw.id,
    corpus: raw.corpus || null,
    signals,
    nodes: order.map((id) => byId.get(id)),
    edges,
    outputs,
    pinned,
  };
  ir.digest = hash({
    n: ir.nodes.map((n) =>
      n.when ? [n.id, n.t, n.in, n.p, n.trigger, n.when] : [n.id, n.t, n.in, n.p, n.trigger],
    ),
    s: signals,
    o: outputs,
  });
  return withMethods(ir);
}

function withMethods(ir) {
  const def = (k, v) =>
    Object.defineProperty(ir, k, {
      value: v,
      enumerable: false,
      configurable: true,
      writable: true,
    });
  def('node', (id) => ir.nodes.find((n) => n.id === id));
  def('downstream', (id) => {
    const seen = new Set(),
      stack = [id];
    while (stack.length) {
      const x = stack.pop();
      for (const [a, b] of ir.edges)
        if (a === x && !seen.has(b)) {
          seen.add(b);
          stack.push(b);
        }
    }
    return ir.nodes.map((n) => n.id).filter((n) => seen.has(n));
  });
  def('upstream', (id) => {
    const seen = new Set(),
      stack = [id];
    while (stack.length) {
      const x = stack.pop();
      for (const [a, b] of ir.edges)
        if (b === x && !seen.has(a)) {
          seen.add(a);
          stack.push(a);
        }
    }
    return ir.nodes.map((n) => n.id).filter((n) => seen.has(n));
  });
  def('summary', () =>
    ir.nodes
      .map(
        (n) =>
          `${n.id}:${n.t}${n.phase === 'live' && (n.trigger || (REG.get(n.t) || {}).trigger) === 'submit' ? '!' : ''}`,
      )
      .join(' → '),
  );
  def('mermaid', () => {
    const lines = ['flowchart LR'];
    for (const n of ir.nodes) {
      const label = `${n.id}<br/><small>${n.t}</small>`;
      lines.push(
        n.phase === 'signal'
          ? `  ${n.id}((${n.id}))`
          : n.phase === 'build'
            ? `  ${n.id}[/"${label}"/]`
            : `  ${n.id}["${label}"]`,
      );
    }
    for (const [a, b, k] of ir.edges)
      lines.push(`  ${a} ${k === 'data' ? '-->' : k === 'artifact' ? '-.->' : '-.-'} ${b}`);
    return lines.join('\n');
  });
  def('clone', () => withMethods(clone(ir)));
  def('describe', (id) => describeNode(ir.node(id)));
  return ir;
}

function describeNode(n) {
  if (!n) return '';
  if (n.t === 'signal') return 'signal';
  const def = REG.get(n.t);
  try {
    return def && def.describe ? def.describe(n.p || {}) : '';
  } catch (e) {
    return '';
  }
}

function triggerOf(n) {
  return n.trigger || (REG.get(n.t) || {}).trigger || 'change';
}

// a build node spends Claude calls when its component says so or its lambda calls ctx.sample
function costsCalls(n) {
  const def = REG.get(n.t) || {};
  if (def.phase !== 'build') return false;
  return !!def.llm || Object.values(n.p || {}).some((v) => isLambda(v) && /\bsample\b/.test(v.$fn));
}

function rawOf(target) {
  if (target instanceof PlanSpec) {
    const corpus = target.st.corpus;
    return {
      id: target.st.id,
      corpus: corpus ? corpus.st.id : null,
      signals: target.st.signals,
      nodes: [...(corpus ? corpus.st.nodes : []), ...target.st.nodes],
      outputs: target.st.outputs || {},
      pinned: Object.keys(target.st.outputs || {}),
      ops: target.st.ops,
    };
  }
  if (target instanceof CorpusSpec)
    return { id: target.st.id, corpus: target.st.id, signals: {}, nodes: target.st.nodes };
  throw new RagError('compile', 'compile() takes a plan, a corpus or an IR');
}

function compile(target) {
  if (target && target.kind === 'rag') {
    if (target.version !== 2)
      throw new RagError('compile', 'unsupported IR version; rebuild the plan');
    return finalize(clone(target));
  }
  if (target && target.ir && target.ir.kind === 'rag') return target.ir;
  const t0 = now();
  const raw = rawOf(target);
  let ir = finalize(raw);
  if (raw.ops && raw.ops.length) ir = applyOps(ir, raw.ops);
  Object.defineProperty(ir, 'compileMs', {
    value: now() - t0,
    enumerable: false,
    configurable: true,
  });
  timed('compile', ir.id, ir.compileMs, ir.nodes.length + ' nodes');
  return ir;
}

// ────────────────────────────────────────────────────────────── ops
// Ops are data. applyOps returns a new, re-checked IR or throws.
function applyOps(ir, ops) {
  const pinned = arr(ir.pinned);
  let raw = {
    id: ir.id,
    corpus: ir.corpus,
    signals: clone(ir.signals),
    nodes: clone(ir.nodes.filter((n) => n.t !== 'signal')),
    outputs: Object.fromEntries(pinned.map((k) => [k, ir.outputs[k]])),
    pinned,
  };
  arr(ops).forEach((op, i) => {
    try {
      raw = applyOp(raw, op);
    } catch (e) {
      throw new RagError('op', `op ${i + 1} (${op && op.op}): ${e.message}`, { index: i, op });
    }
  });
  raw.pinned = raw.pinned.filter((k) => raw.outputs[k]);
  try {
    return finalize(raw);
  } catch (e) {
    throw new RagError('op', 'after ops: ' + e.message, e.detail);
  }
}

function nodeOf(raw, id) {
  const n = raw.nodes.find((x) => x.id === id);
  if (!n) throw new Error(`no node '${id}'. Nodes: ${raw.nodes.map((x) => x.id).join(', ')}`);
  return n;
}

function normValue(v) {
  return clone(normParams(v));
}

function specOf(nodeSpec) {
  if (!nodeSpec) throw new Error('missing node: {t, p}');
  if (nodeSpec.$c) {
    const c = normalize(nodeSpec.$c, [nodeSpec]);
    return { t: c.t, p: c.p, trigger: c.opts.trigger, id: c.opts.id, when: c.opts.when };
  }
  if (!REG.get(nodeSpec.t)) throw new Error(`unknown component '${nodeSpec.t}'`);
  const p = {};
  for (const [k, v] of Object.entries(nodeSpec.p || {})) p[k] = normValue(v);
  return { t: nodeSpec.t, p, trigger: nodeSpec.trigger, id: nodeSpec.id, when: nodeSpec.when };
}

function applyOp(raw, op) {
  raw = {
    ...raw,
    nodes: clone(raw.nodes),
    signals: { ...raw.signals },
    outputs: { ...raw.outputs },
    pinned: [...raw.pinned],
  };
  const consumers = (id) => raw.nodes.filter((n) => n.in.includes(id));
  switch (op.op) {
    case 'set': {
      const n = nodeOf(raw, op.node);
      if (!op.path)
        throw new Error('set needs a path into the node params, e.g. "k" or "fields.title"');
      setPath(n.p, op.path, normValue(op.value));
      return raw;
    }
    case 'unset': {
      setPath(nodeOf(raw, op.node).p, op.path, undefined);
      return raw;
    }
    case 'trigger': {
      const n = nodeOf(raw, op.node);
      if (!['change', 'submit'].includes(op.value))
        throw new Error("trigger is 'change' or 'submit'");
      n.trigger = op.value;
      return raw;
    }
    case 'signal': {
      raw.signals[op.key] = clone(op.value);
      return raw;
    }
    case 'replace': {
      const n = nodeOf(raw, op.node);
      if (op.fn != null) {
        const key = op.param || 'fn';
        if (!isLambda(n.p[key]) && key === 'fn' && !(REG.get(n.t).lambdas || []).includes('fn'))
          throw new Error(`${n.id} (${n.t}) has no lambda; use {op:'replace', node, with:{t, p}}`);
        n.p[key] = lam(op.fn);
        return raw;
      }
      const s = specOf(op.with);
      n.t = s.t;
      n.p = s.p;
      if (s.trigger) n.trigger = s.trigger;
      else delete n.trigger;
      if (s.when != null) n.when = normWhen(s.when, n.id);
      return raw;
    }
    case 'insert': {
      const s = specOf(op.node);
      const def = REG.get(s.t);
      const id =
        op.id ||
        s.id ||
        freshId(
          [...raw.nodes, ...Object.keys(raw.signals).map((k) => ({ id: k }))],
          def.id ? def.id(s.p) : s.t.split('.')[0],
        );
      if (raw.nodes.find((n) => n.id === id)) throw new Error(`node '${id}' already exists`);
      const node = { id, t: s.t, phase: def.phase, in: [], p: s.p };
      if (s.trigger || op.trigger) node.trigger = s.trigger || op.trigger;
      if (s.when != null || op.when != null)
        node.when = normWhen(s.when != null ? s.when : op.when, id);
      if (op.after) {
        nodeOf(raw, op.after);
        node.in = [op.after];
        for (const c of consumers(op.after))
          if (c.id !== id && (!op.only || arr(op.only).includes(c.id)))
            c.in = c.in.map((x) => (x === op.after ? id : x));
        for (const [k, v] of Object.entries(raw.outputs))
          if (v === op.after && portsOf(node).out === portsOf(nodeOf(raw, op.after)).out)
            raw.outputs[k] = id;
      } else if (op.before) {
        const b = nodeOf(raw, op.before);
        if (b.in.length !== 1)
          throw new Error(
            `'${op.before}' has ${b.in.length} inputs; insert with {in:[…]} and wire it explicitly`,
          );
        node.in = [...b.in];
        b.in = [id];
      } else if (op.in) {
        node.in = arr(op.in);
        for (const f of arr(op.feeds)) {
          const c = nodeOf(raw, f);
          c.in.push(id);
        }
      } else throw new Error('insert needs after, before, or in');
      const at = op.after ? raw.nodes.findIndex((n) => n.id === op.after) + 1 : raw.nodes.length;
      raw.nodes.splice(at, 0, node);
      return raw;
    }
    case 'remove': {
      const n = nodeOf(raw, op.node);
      const users = raw.nodes.filter((x) =>
        references(x, REG.get(x.t)).some((r) => r.target === n.id && r.kind !== 'data'),
      );
      if (users.length)
        throw new Error(
          `'${n.id}' is used by ${users.map((u) => u.id).join(', ')}; remove those first`,
        );
      for (const c of consumers(n.id)) {
        const variadic = (portsOf(c).in[0] || '') === 'hits*';
        if (variadic)
          c.in = c.in.flatMap((x) => (x === n.id ? n.in.filter((y) => !c.in.includes(y)) : [x]));
        else {
          const data = n.in.filter(
            (x) =>
              (raw.nodes.find((y) => y.id === x) || { t: 'signal' }).t !== 'signal' ||
              portsOf(c).in[0] === 'queries',
          );
          if (data.length !== 1)
            throw new Error(
              `cannot rewire '${c.id}' around '${n.id}' (it has ${n.in.length} inputs); use wire`,
            );
          c.in = c.in.map((x) => (x === n.id ? data[0] : x));
        }
      }
      for (const [k, v] of Object.entries(raw.outputs)) if (v === n.id) delete raw.outputs[k];
      raw.nodes = raw.nodes.filter((x) => x.id !== n.id);
      return raw;
    }
    case 'wire': {
      nodeOf(raw, op.node).in = arr(op.in);
      return raw;
    }
    case 'output': {
      nodeOf(raw, op.node);
      raw.outputs[op.name] = op.node;
      if (!raw.pinned.includes(op.name)) raw.pinned.push(op.name);
      return raw;
    }
    case 'rename': {
      const n = nodeOf(raw, op.node);
      if (!ID_RE.test(op.to)) throw new Error('invalid id ' + op.to);
      if (
        raw.nodes.some((x) => x.id === op.to) ||
        Object.prototype.hasOwnProperty.call(raw.signals, op.to)
      )
        throw new Error(`node '${op.to}' already exists`);
      for (const x of raw.nodes) rewriteReferences(references(x, REG.get(x.t)), n.id, op.to);
      rewriteReferences(outputReferences(raw.outputs), n.id, op.to);
      n.id = op.to;
      return raw;
    }
    default:
      throw new Error(
        `unknown op '${op.op}'. Ops: set unset insert remove replace wire signal trigger output rename`,
      );
  }
}

// Lexical browser projection is one transaction: remove external-model work,
// bypass compatible stages, and remove gates before validating the result.
function projectLexical(ir) {
  const raw = clone(ir);
  const removed = new Set(
    raw.nodes
      .filter(
        (n) =>
          (REG.get(n.t)?.capabilities || []).includes('model') ||
          n.out === 'answer' ||
          n.out === 'context',
      )
      .map((n) => n.id),
  );
  const byId = new Map(raw.nodes.map((n) => [n.id, n]));
  const bypass = (id, seen = new Set()) => {
    if (!removed.has(id)) return [id];
    if (seen.has(id)) throw new RagError('export', 'cyclic projection');
    const n = byId.get(id);
    const next = new Set([...seen, id]);
    return n.in.flatMap((input) => bypass(input, next));
  };
  raw.nodes = raw.nodes.filter((n) => !removed.has(n.id));
  for (const n of raw.nodes) {
    n.in = uniq(n.in.flatMap((id) => bypass(id)));
    if (references(n, REG.get(n.t)).some((r) => r.kind === 'decision' && removed.has(r.target)))
      delete n.when;
    const dangling = references(n, REG.get(n.t)).find((r) => removed.has(r.target));
    if (dangling)
      throw new RagError('export', `${n.id}: unsupported dependency ${dangling.target}`);
  }
  raw.outputs = Object.fromEntries(
    Object.entries(raw.outputs).filter(([, id]) => !removed.has(id)),
  );
  raw.pinned = raw.pinned.filter((name) => raw.outputs[name]);
  return finalize(raw);
}

export {
  projectLexical,
  applyOps,
  CorpusSpec,
  compile,
  costsCalls,
  describeNode,
  Fragment,
  graph,
  installFamilies,
  makePlan,
  normalize,
  rag_bake,
  triggerOf,
  whenHolds,
  whenSkipOutput,
  withMethods,
};
