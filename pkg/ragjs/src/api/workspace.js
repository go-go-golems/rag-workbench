import { RagError } from '../core/errors.ts';
import { lam } from '../core/lambda.js';
import { arr, clone, stable, uniq } from '../core/util.js';
import { applyOps, compile, costsCalls, withMethods } from '../graph/graph.js';
import { defaultEngine } from './default-engine.js';

// ────────────────────────────────────────────────────────────── workspace
class Workspace {
  constructor(ir, opts = {}) {
    this.engine = opts.engine || defaultEngine();
    this.name = opts.name || 'main';
    this._ir = compile(ir);
    this.revision = opts.revision || 0;
    this.baseRevision = this.revision;
    this.history = [];
    this.future = [];
    this.branches = {};
    this.parent = opts.parent || null;
    this.listeners = new Set();
    this.lastBuild = null;
    this.readonly = false;
  }
  get ir() {
    return this._ir;
  }
  on(fn) {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }
  _emit(e) {
    for (const f of this.listeners)
      try {
        f(e);
      } catch (_) {
        /* keep emitting */
      }
  }
  _check(op) {
    if (this.readonly)
      throw new RagError(
        'readonly',
        `ws.${op} is blocked in Read-only mode. Describe the change instead, or ask the user to switch to Auto or Ask.`,
      );
  }
  classify(before, after) {
    const db = this.engine.digests(before),
      da = this.engine.digests(after);
    const dirty = [...da].filter(([id, d]) => !this.engine.memo.has(d)).map(([id]) => id);
    const changedBuild = [...da].filter(([id, d]) => db.get(id) !== d).map(([id]) => id);
    const nb = new Map(before.nodes.map((n) => [n.id, stable([n.t, n.in, n.p, n.trigger])]));
    const liveChanged = after.nodes
      .filter((n) => n.phase === 'live' && nb.get(n.id) !== stable([n.t, n.in, n.p, n.trigger]))
      .map((n) => n.id);
    const removed = before.nodes.filter((n) => !after.node(n.id)).map((n) => n.id);
    const sig = Object.keys({ ...before.signals, ...after.signals }).filter(
      (k) => stable(before.signals[k]) !== stable(after.signals[k]),
    );
    return { live: [...liveChanged, ...sig], dirty, changed: changedBuild, removed };
  }
  dryRun(ops) {
    try {
      const next = applyOps(this._ir, ops);
      const c = this.classify(this._ir, next);
      const llm = c.dirty.filter((id) => costsCalls(next.node(id))).length;
      const chunks = this.engine.artifacts(this._ir)
        ? this.engine.artifacts(this._ir).chunks.size
        : null;
      return { ok: true, ...c, estimate: { llmCalls: llm && chunks ? llm * chunks : 0 } };
    } catch (e) {
      return { ok: false, error: e.message };
    }
  }
  async apply(ops, opts = {}) {
    this._check('apply');
    if (opts.expect != null && opts.expect !== this.revision)
      throw new RagError(
        'conflict',
        `workspace '${this.name}' is at revision ${this.revision}; these ops were planned against ${opts.expect}. Re-read ws.ir and plan again.`,
      );
    ops = arr(ops).map((o) =>
      o && o.value && typeof o.value === 'function'
        ? { ...o, value: lam(o.value) }
        : o && typeof o.fn === 'function'
          ? { ...o, fn: lam(o.fn).$fn }
          : o,
    );
    const before = this._ir;
    const next = applyOps(before, ops);
    const c = this.classify(before, next);
    this.history.push({
      revision: this.revision + 1,
      ops: clone(ops),
      by: opts.by || 'user',
      note: opts.note,
      at: Date.now(),
      before: clone(before),
    });
    this.future = [];
    this._ir = next;
    this.revision++;
    const receipt = { revision: this.revision, ...c };
    this._emit({ type: 'apply', receipt, by: opts.by || 'user' });
    return receipt;
  }
  async rebuild(opts = {}) {
    this._check('rebuild');
    const rep = await this.engine.build(this._ir, opts);
    this.lastBuild = rep;
    this._emit({ type: 'build', report: rep });
    return {
      ok: rep.ok,
      ms: rep.ms,
      computed: Object.values(rep.nodes)
        .filter((x) => !x.reused)
        .map((x) => x.id),
      reused: Object.values(rep.nodes).filter((x) => x.reused).length,
      calls: rep.calls,
      errors: rep.errors,
    };
  }
  undo() {
    this._check('undo');
    const h = this.history.pop();
    if (!h) return null;
    this.future.push({ ...h, after: clone(this._ir) });
    this._ir = withMethods(h.before);
    this.revision++;
    this._emit({ type: 'undo', revision: this.revision, undone: h.revision });
    return { revision: this.revision, undone: h.revision, ops: h.ops };
  }
  redo() {
    this._check('redo');
    const f = this.future.pop();
    if (!f) return null;
    this.history.push({ ...f, before: clone(this._ir) });
    this._ir = withMethods(f.after);
    this.revision++;
    this._emit({ type: 'redo', revision: this.revision });
    return { revision: this.revision };
  }
  reset(ir, note = 'reset') {
    const before = this._ir;
    this.history.push({
      revision: this.revision + 1,
      ops: [{ op: 'reset', note }],
      by: 'user',
      note,
      at: Date.now(),
      before: clone(before),
    });
    this._ir = compile(ir);
    this.revision++;
    this._emit({ type: 'reset', revision: this.revision });
    return { revision: this.revision };
  }
  branch(name) {
    const b = new Workspace(this._ir, {
      engine: this.engine,
      name,
      revision: this.revision,
      parent: this,
    });
    b.baseRevision = this.revision;
    this.branches[name] = b;
    return b;
  }
  async merge(b) {
    this._check('merge');
    if (typeof b === 'string') b = this.branches[b];
    if (!b) throw new RagError('op', 'no such branch');
    if (b.baseRevision !== this.revision)
      throw new RagError(
        'conflict',
        `'${this.name}' moved from revision ${b.baseRevision} to ${this.revision} since branch '${b.name}' was made. Re-apply the branch ops on a fresh branch.`,
      );
    const ops = b.history.flatMap((h) => h.ops);
    const receipt = await this.apply(ops, { by: 'merge', note: `merge ${b.name}` });
    delete this.branches[b.name];
    return { ...receipt, merged: b.name, ops: ops.length };
  }
  run(signals, opts) {
    return this.engine.session(() => this._ir).run(signals, opts);
  }
  session() {
    return this.engine.session(() => this._ir);
  }
  snapshot() {
    return {
      ir: clone(this._ir),
      revision: this.revision,
      history: this.history.length,
      future: [...this.future],
    };
  }
  restore(snap) {
    this._ir = withMethods(clone(snap.ir));
    this.history = this.history.slice(0, snap.history);
    this.future = snap.future;
    this.revision++;
    this._emit({ type: 'restore', revision: this.revision });
  }
  diff(other) {
    return diffIr(this._ir, other instanceof Workspace ? other.ir : compile(other));
  }
  toJSON() {
    return {
      name: this.name,
      revision: this.revision,
      nodes: this._ir.summary(),
      dirty: this.engine.dirty(this._ir),
      branches: Object.keys(this.branches),
      history: this.history.map((h) => ({
        revision: h.revision,
        by: h.by,
        ops: h.ops
          .map((o) => o.op + (o.node ? ' ' + o.node : '') + (o.path ? '.' + o.path : ''))
          .join(', '),
      })),
    };
  }
}

function diffIr(a, b) {
  const out = [];
  const am = new Map(a.nodes.map((n) => [n.id, n])),
    bm = new Map(b.nodes.map((n) => [n.id, n]));
  for (const [id, n] of bm) {
    const o = am.get(id);
    if (!o) {
      out.push({ change: 'added', node: id, t: n.t });
      continue;
    }
    if (o.t !== n.t) out.push({ change: 'type', node: id, from: o.t, to: n.t });
    if (stable(o.in) !== stable(n.in))
      out.push({ change: 'wiring', node: id, from: o.in, to: n.in });
    const keys = uniq([...Object.keys(o.p), ...Object.keys(n.p)]);
    for (const k of keys)
      if (stable(o.p[k]) !== stable(n.p[k]))
        out.push({ change: 'param', node: id, path: k, from: o.p[k], to: n.p[k] });
    if ((o.trigger || '') !== (n.trigger || ''))
      out.push({ change: 'trigger', node: id, from: o.trigger, to: n.trigger });
  }
  for (const id of am.keys()) if (!bm.has(id)) out.push({ change: 'removed', node: id });
  return out;
}

export { diffIr, Workspace };
