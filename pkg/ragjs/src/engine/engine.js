import { dataset, memoryDataset } from './dataset.ts';
import { ResourceRegistry, OwnershipScope } from './ownership.ts';
import { BuildJobs } from './jobs.ts';
import { REG } from '../components/registry.js';
import { RagError } from '../core/errors.ts';
import { slug } from '../core/identity.ts';
import { fnOf } from '../core/lambda.js';
import { arr, clip, hash, isBake, isLambda, isObj, now, tokens, uniq } from '../core/util.js';
import { compile, costsCalls } from '../graph/graph.js';
import { fromWireDecision, makeDecide, wireDecision } from './decision.js';
import { timed } from './environment.js';
import {
  asArray,
  FULL_DELTA,
  isView,
  Lazy,
  Outputs,
  persistOf,
  storeKey,
  unionChunks,
} from './records.js';
import { safeEmit, scoped } from './scope.ts';
import { Session } from './session.js';

class Engine {
  constructor(env = {}) {
    this.env = env;
    this.memo = new Map(); // build digest → { value, stats }
    this.resources = new ResourceRegistry();
    this.memoOwners = new Map();
    this.activeSnapshots = new Map();
    this.recent = new Map(); // recently run plans (noteIr)
    this.building = new BuildJobs();
    this.items = new Map(); // item key → value
    this.bakes = new Map();
    this.fileDigests = new Map();
    this.partial = new Map(); // digest → output built without Claude calls (llm items deferred)
    this.listeners = new Set();
  }
  on(fn) {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }
  emit(e) {
    for (const f of this.listeners)
      try {
        f(e);
      } catch (_) {
        /* listener errors stay local */
      }
  }
  files() {
    return (this.env.files ? this.env.files() : []) || [];
  }
  fileDigest(f) {
    const k = f.name + ':' + f.text.length;
    const c = this.fileDigests.get(k);
    if (c && c.text === f.text) return c.d;
    const d = hash(f.name + '\u0000' + f.text);
    this.fileDigests.set(k, { text: f.text, d });
    return d;
  }
  digests(ir) {
    const out = new Map();
    for (const n of ir.nodes) {
      if (n.phase !== 'build') continue;
      let extra = '';
      const def = REG.get(n.t);
      if (n.t === 'source.files')
        extra = this.files()
          .map((f) => this.fileDigest(f))
          .join(',');
      else if (def && typeof def.digest === 'function') extra = String(def.digest(n.p, this) || '');
      // a rag.define'd component's source is part of its identity: editing its body (same type,
      // same params) invalidates the node and, through the input digests, everything downstream.
      // Built-in components have no src, so their digests are unchanged.
      const src = def && def.src ? [def.src] : [];
      out.set(n.id, hash([n.t, n.p, n.in.map((i) => out.get(i) || i), extra, ...src]));
    }
    return out;
  }
  // lineage keys (RAGWB-003 §6.4): like digests, but a source contributes its
  // def.lineage(p) instead of its file stats, so "this node of this pipeline" keeps
  // its key across corpus edits. null: the node (or one upstream) has a digest
  // hook without a lineage hook and can't be continued incrementally.
  lineages(ir) {
    const out = new Map();
    for (const n of ir.nodes) {
      if (n.phase !== 'build') continue;
      const def = REG.get(n.t);
      let extra = '';
      if (n.t === 'source.files') extra = null;
      else if (def && typeof def.lineage === 'function') extra = String(def.lineage(n.p, this));
      else if (def && typeof def.digest === 'function') extra = null;
      const ins = n.in.map((i) => out.get(i));
      out.set(
        n.id,
        extra === null || ins.some((x) => x === null)
          ? null
          : hash(['lineage', n.t, n.p, ins, extra, ...(def && def.src ? [def.src] : [])]),
      );
    }
    return out;
  }
  dirty(ir) {
    const d = this.digests(ir);
    return [...d].filter(([, dg]) => !this.memo.has(dg)).map(([id]) => id);
  }
  // build nodes whose items still wait for Claude calls (deferred), with how many
  pending(ir) {
    const d = this.digests(ir),
      out = [];
    for (const n of ir.nodes) {
      if (n.phase !== 'build' || !costsCalls(n) || this.memo.has(d.get(n.id))) continue;
      const pt = this.partial.get(d.get(n.id));
      out.push({ node: n.id, items: pt ? pt.deferred : null });
    }
    return out;
  }
  sampler(counter, signal) {
    const base = this.env.sample;
    if (!base) {
      const no = () =>
        Promise.reject({
          code: 'sampling_unavailable',
          message: 'Claude is not available in this view, so LLM nodes cannot run',
        });
      no.json = no;
      return no;
    }
    const f = (input, opts = {}) => {
      counter.calls++;
      return base(input, { ...(signal ? { signal } : {}), ...opts });
    };
    f.json = (input, opts = {}) => {
      counter.calls++;
      return base.json(input, { ...(signal ? { signal } : {}), ...opts });
    };
    return f;
  }
  decider(counter, signal) {
    const base = this.env.decide;
    if (!base) return makeDecide(null);
    return makeDecide(async (req) => {
      const wire = wireDecision(req);
      counter.calls++;
      const t0 = now();
      const s = await base(JSON.stringify(wire), signal || null);
      return fromWireDecision(JSON.parse(s), +(now() - t0).toFixed(1));
    });
  }
  async build(ir, opts = {}) {
    ir = compile(ir);
    const t0 = now();
    const dg = this.digests(ir);
    let lineages = new Map();
    if (this.env.artifacts && this.env.artifacts.lineageRecord) {
      try {
        lineages = this.lineages(ir);
      } catch (e) {
        this.env.log && this.env.log(`lineage keys: ${e.message || e}`);
      }
    }
    const report = { ok: true, ms: 0, nodes: {}, errors: [] };
    const bemit = safeEmit(opts);
    const outputs = new Outputs();
    const deltas = new Map(); // node id → its change since the base the next node continues from (RAGWB-003 §6.5)
    const counter = { calls: 0 };
    for (const n of ir.nodes) {
      if (n.phase !== 'build') continue;
      const d = dg.get(n.id);
      deltas.set(n.id, FULL_DELTA);
      const st = {
        id: n.id,
        t: n.t,
        digest: d,
        reused: false,
        ms: 0,
        count: 0,
        computed: 0,
        cached: 0,
        calls: 0,
      };
      report.nodes[n.id] = st;
      const allowCalls = opts.calls ?? this.env.buildCalls !== 'explicit';
      const costly = costsCalls(n);
      const store = this.env.artifacts,
        codec = store ? persistOf(REG.get(n.t)) : null;
      if (codec && !this.memo.has(d) && !opts.force) {
        const sk = storeKey(d, REG.get(n.t)),
          hit = store.has(sk);
        if (hit) {
          const lz = new Lazy(
            () => {
              const value = codec.load(store.load(sk), n.p);
              this.ownMemo(d, value);
              return value;
            },
            (ms) => timed('load', n.id, ms, n.t),
          );
          this.memo.set(d, {
            get value() {
              return lz.force();
            },
            count: hit.count,
            lazy: lz,
          });
          st.stored = true;
        }
      }
      if (this.memo.has(d) && !opts.force) {
        const m = this.memo.get(d);
        outputs.set(n.id, m.lazy && !m.lazy.done ? m.lazy : m.value);
        Object.assign(st, { reused: true, count: m.count, ms: 0 });
        deltas.set(n.id, { none: true, from: d });
        if (bemit)
          bemit({
            type: 'build',
            node: n.id,
            component: n.t,
            ms: 0,
            reused: true,
            stored: !!st.stored,
            count: st.count,
          });
        continue;
      }
      if (costly && !allowCalls && this.partial.has(d)) {
        const m = this.partial.get(d);
        outputs.set(n.id, m.value);
        Object.assign(st, { reused: true, count: m.count, ms: 0, deferred: m.deferred });
        continue;
      }
      const t1 = now();
      const def = REG.get(n.t);
      const c0 = counter.calls;
      try {
        const p = await this.resolveBakes(n, n.p, outputs, ir);
        const inputArrays = () => n.in.map((i) => asArray(outputs.get(i))); // not before a delta build: it would read every input
        const ctx = this.buildCtx(n, ir, outputs, counter, opts.signal);
        let value,
          lazy = null;
        const inc =
          def.delta &&
          !def.map &&
          !(opts.full ?? this.env.fullBuild) &&
          !opts.force &&
          opts.persist !== false
            ? await this.tryDelta(n, def, d, lineages.get(n.id), deltas, outputs, ctx, p, opts)
            : null;
        if (inc) {
          if (inc.lazy) {
            lazy = new Lazy(
              () => {
                const value = inc.lazy();
                this.ownMemo(d, value);
                return value;
              },
              (ms) => timed('load', n.id, ms, n.t),
            );
            outputs.set(n.id, lazy);
          } else value = inc.value;
          deltas.set(n.id, { ...inc.delta, full: false, from: inc.base.digest });
          Object.assign(st, {
            incremental: true,
            base: inc.base.storeKey,
            delta: inc.summary || {},
          });
        } else if (def.map) {
          const items = inputArrays()[0] || [];
          const fnKey = hash([n.t, p]);
          const results = new Array(items.length);
          let next = 0,
            budget = (p.budget && p.budget.calls) ?? Infinity;
          const worker = async () => {
            while (next < items.length) {
              const i = next++;
              const item = items[i];
              const key = fnKey + ':' + hash(item);
              if (this.items.has(key)) {
                results[i] = this.items.get(key);
                st.cached++;
                continue;
              }
              if (def.async && this.env.cache) {
                const hit = await this.env.cache.get(key).catch(() => undefined);
                if (hit !== undefined && hit !== null) {
                  this.items.set(key, hit);
                  results[i] = hit;
                  st.cached++;
                  continue;
                }
              }
              if (def.async && counter.calls - c0 >= budget) {
                results[i] = def.out === 'chunks' ? item : null;
                st.skipped = (st.skipped || 0) + 1;
                continue;
              }
              if (costly && !allowCalls) {
                results[i] = def.out === 'chunks' ? item : null;
                st.deferred = (st.deferred || 0) + 1;
                continue;
              }
              if (opts.signal && opts.signal.aborted) throw new RagError('build', 'build stopped');
              const v = await def.map(item, { ...ctx, item }, p, n);
              results[i] = v;
              this.items.set(key, v);
              if (def.async && this.env.cache) this.env.cache.set(key, v).catch(() => {});
              st.computed++;
              if (opts.onProgress)
                opts.onProgress({ node: n.id, done: st.computed + st.cached, total: items.length });
              if (bemit)
                bemit({
                  type: 'progress',
                  node: n.id,
                  done: st.computed + st.cached,
                  total: items.length,
                  unit: 'items',
                });
            }
          };
          await Promise.all(
            Array.from({ length: def.async ? Math.max(1, p.concurrency ?? 3) : 1 }, worker),
          );
          value = results.flat().filter((x) => x != null);
          if (def.post) value = def.post(value);
        } else {
          value = await def.all(inputArrays(), ctx, p, n);
        }
        if (inc) st.count = inc.count ?? (lazy ? 0 : (value.length ?? 0));
        else {
          outputs.set(n.id, value);
          st.count = Array.isArray(value)
            ? value.length
            : value && value.N != null
              ? value.N
              : value
                ? Object.keys(value).length
                : 0;
        }
        if (inc && !lazy) outputs.set(n.id, value);
        st.calls = counter.calls - c0;
        if (st.deferred || st.skipped)
          this.partial.set(d, {
            value,
            count: st.count,
            deferred: (st.deferred || 0) + (st.skipped || 0),
          });
        else {
          if (lazy) {
            const lz = lazy,
              count = st.count;
            this.memo.set(d, {
              get value() {
                return lz.force();
              },
              count,
              lazy: lz,
            });
          } else {
            this.memo.set(d, { value, count: st.count });
            this.ownMemo(d, value);
          }
          this.partial.delete(d);
          const store = this.env.artifacts,
            codec = store ? persistOf(def) : null;
          if (codec && opts.persist !== false) {
            try {
              const sk = storeKey(d, def);
              store.save(sk, n.t, codec.save(lazy || value, n.p), st.count);
              st.saved = true;
              if (store.describe)
                store.describe(
                  sk,
                  lineages.get(n.id) || '',
                  n.in
                    .map((id) => {
                      const source = ir.nodes.find((x) => x.id === id);
                      return persistOf(REG.get(source.t))
                        ? storeKey(dg.get(id), REG.get(source.t))
                        : null;
                    })
                    .filter((key) => key && store.has(key)),
                );

              const lin = lineages.get(n.id);
              if (lin && store.lineageRecord)
                store.lineageRecord(
                  lin,
                  sk,
                  d,
                  n.in.map((i) => dg.get(i)),
                );
            } catch (e) {
              this.env.log && this.env.log(`[${n.id}] not persisted: ${e.message || e}`);
            }
          }
        }
      } catch (e) {
        report.ok = false;
        st.error = e.message || String(e.code || e);
        report.errors.push(`${n.id}: ${st.error}`);
        break;
      }
      st.ms = +(now() - t1).toFixed(1);
      timed('build', n.id, st.ms, n.t + ' · computed' + (st.saved ? ', saved' : ''));
      this.emit({ type: 'node-built', node: n.id, stats: st });
      if (bemit)
        bemit({
          type: 'build',
          node: n.id,
          component: n.t,
          ms: st.ms,
          count: st.count,
          incremental: !!st.incremental,
          ...(st.delta ? { delta: st.delta } : {}),
        });
    }
    // bake params of live nodes
    if (report.ok)
      for (const n of ir.nodes)
        if (n.phase === 'live') {
          try {
            await this.resolveBakes(n, n.p, outputs, ir);
          } catch (e) {
            report.ok = false;
            report.errors.push(`${n.id}: ${e.message}`);
          }
        }
    if (report.ok && this.env.artifacts?.publish && !this.pending(ir).length) {
      const roots = ir.nodes
        .filter((n) => n.phase === 'build' && persistOf(REG.get(n.t)))
        .map((n) => storeKey(dg.get(n.id), REG.get(n.t)))
        .filter((key) => this.env.artifacts.has(key));
      this.env.artifacts.publish(
        hash([
          'slot',
          ir.corpus,
          ir.nodes.filter((n) => n.phase === 'build').map((n) => [n.id, n.t, n.p, n.in]),
        ]),
        hash([...dg]),
        roots,
      );
    }
    report.ms = +(now() - t0).toFixed(1);
    report.calls = counter.calls;
    report.passes = passesOf(ir, report);
    if (bemit) bemit({ type: 'built', ok: report.ok, ms: report.ms, errors: report.errors });
    this.lastBuild = report;
    this.emit({ type: 'built', report });
    return report;
  }
  retainValue(value, scope) {
    if (!value || typeof value !== 'object') return;
    const handle = isView(value) ? value.__records : value.__ix;
    if (handle && typeof handle.close === 'function')
      scope.own(this.resources.retain(handle, () => handle.close()));
  }
  ownMemo(digest, value) {
    const owner = new OwnershipScope();
    this.retainValue(value, owner);
    this.memoOwners.get(digest)?.close();
    this.memoOwners.set(digest, owner);
  }
  acquire(art) {
    const owner = new OwnershipScope();
    try {
      for (const [node, value] of art.byId) {
        const resolved = art.byId.get(node);
        this.retainValue(resolved, owner);
      }
    } catch (error) {
      owner.close();
      throw error;
    }
    if (this.env.artifacts?.pin && art.storeKeys.length)
      owner.own(this.env.artifacts.pin(art.storeKeys));
    this.activeSnapshots.set(art, (this.activeSnapshots.get(art) || 0) + 1);
    let released = false;
    return () => {
      if (released) return;
      released = true;
      const remaining = this.activeSnapshots.get(art) - 1;
      if (remaining) this.activeSnapshots.set(art, remaining);
      else this.activeSnapshots.delete(art);
      owner.close();
    };
  }
  gc(irs) {
    const liveDigests = new Set();
    for (const ir of arr(irs))
      for (const d of this.digests(compile(ir)).values()) liveDigests.add(d);
    const before = this.resources.size;
    for (const [digest] of this.memo) {
      if (liveDigests.has(digest)) continue;
      this.memo.delete(digest);
      this.memoOwners.get(digest)?.close();
      this.memoOwners.delete(digest);
    }
    for (const digest of this.partial.keys())
      if (!liveDigests.has(digest)) this.partial.delete(digest);
    this._art = null;
    return {
      closed: before - this.resources.size,
      active: this.activeSnapshots.size,
      memo: this.memo.size,
    };
  }
  // Delta build of one node (RAGWB-003 §6.5): continue from the newest stored
  // version of this node's lineage that was computed from exactly the versions
  // the input deltas start from. null (no base, an input without a delta, the
  // component declined or threw) → the node is computed fully.
  async tryDelta(n, def, d, lin, deltas, outputs, ctx, p, opts) {
    const store = this.env.artifacts;
    if (!lin || !store || !store.lineageList) return null;
    const ins = n.in.map((i) => deltas.get(i) || FULL_DELTA);
    if (ins.some((x) => x.full && !def.delta.acceptsFull)) return null;
    const froms = ins.map((x) => x.from);
    const base = store
      .lineageList(lin)
      .find(
        (e) =>
          e.digest !== d &&
          e.inputs.length === froms.length &&
          e.inputs.every((x, i) => x === froms[i]),
      );
    if (!base) return null;
    const codec = persistOf(def);
    // the previous version is usually still in memory (serve): use it, don't reopen it
    const baseScope = new OwnershipScope();
    const loadBase = () => {
      // a computed docs/chunks array is not the record store a delta continues from: open that
      const m = this.memo.get(base.digest);
      if (m && !(m.lazy && !m.lazy.done) && (isView(m.value) || (m.value && m.value.__ix)))
        return m.value;
      const v = codec.load(store.load(base.storeKey), n.p);
      this.retainValue(v, baseScope);
      return v;
    };
    const b = {
      storeKey: base.storeKey,
      digest: base.digest,
      raw: () => store.load(base.storeKey),
      load: loadBase,
    };
    const inputs = n.in.map((id, i) => ({
      get value() {
        return outputs.get(id);
      },
      delta: ins[i],
    }));
    // inherit, don't spread: ctx.docs/ctx.chunks are getters that read every doc and chunk
    const dctx = Object.assign(Object.create(ctx), {
      store,
      deltaMaxFraction: opts.deltaMaxFraction ?? this.env.deltaMaxFraction ?? 0.2,
    });
    try {
      const r = await def.delta.apply(b, inputs, dctx, p);
      if (r?.value) this.ownMemo(d, r.value);
      return r ? { ...r, base: b } : null;
    } catch (e) {
      ctx.log(`delta build failed, building fully: ${e.message || e}`);
      return null;
    } finally {
      baseScope.close();
    }
  }
  buildCtx(n, ir, outputs, counter, signal) {
    const firstOf = (t) => {
      const id = ir.nodes.find((x) => x.phase === 'build' && x.out === t && outputs.has(x.id));
      return id ? asArray(outputs.get(id.id)) : [];
    };
    return {
      node: n,
      sample: this.sampler(counter, signal),
      decide: this.decider(counter, signal),
      tokens,
      slug,
      hash,
      clip,
      log: (...a) => this.env.log && this.env.log(`[${n.id}]`, ...a),
      files: () => this.files(),
      get docs() {
        return firstOf('docs');
      },
      get chunks() {
        return firstOf('chunks');
      },
    };
  }
  async resolveBakes(n, p, outputs, ir) {
    let found = false;
    const scan = (v) => {
      if (isBake(v)) found = true;
      else if (isObj(v)) Object.values(v).forEach(scan);
    };
    scan(p);
    if (!found) return p;
    const digests = this.digests(ir);
    const walk = async (v) => {
      if (isBake(v)) {
        if (!v.datasets) throw new RagError('bake', 'bake requires explicit dataset bindings');
        const bindings = {};
        for (const [name, id] of Object.entries(v.datasets)) {
          const node = ir.nodes.find((x) => x.id === id);
          if (!node || !['docs', 'chunks'].includes(node.out) || !outputs.has(id))
            throw new RagError('bake', `${n.id}: dataset '${id}' is unavailable`);
          const value = outputs.get(id);
          const kind = node.out === 'docs' ? 'documents' : 'chunks';
          bindings[name] = isView(value)
            ? dataset(digests.get(id), kind, value)
            : memoryDataset(digests.get(id), kind, value);
        }
        const key = hash([
          v.$bake,
          Object.entries(bindings).map(([name, value]) => [name, value.id]),
        ]);
        if (!this.bakes.has(key))
          this.bakes.set(
            key,
            await fnOf(
              { $fn: v.$bake },
              `${n.id} bake`,
            )({ datasets: Object.freeze(bindings), tokens, slug }),
          );
        return this.bakes.get(key);
      }
      if (isObj(v)) {
        const o = {};
        for (const [k, x] of Object.entries(v)) o[k] = await walk(x);
        return o;
      }
      return v;
    };
    const out = await walk(p);
    this.resolved = this.resolved || new Map();
    this.resolved.set(n.id + ':' + hash(p), out);
    return out;
  }
  baked(n) {
    if (!this.resolved) return n.p;
    return this.resolved.get(n.id + ':' + hash(n.p)) || n.p;
  }
  artifacts(ir) {
    const dg = this.digests(ir);
    const got = (d) => this.memo.get(d) || this.partial.get(d);
    const missing = [...dg].filter(([, d]) => !got(d)).map(([id]) => id);
    if (missing.length) return null;
    const key = [...dg.values()].map((d) => d + (this.memo.has(d) ? '' : '~')).join(',');
    if (this._art && this._art.key === key) return this._art;
    const tArt = now();
    const byId = new Outputs(); // stored outputs stay unloaded until read (sources usually never are)
    for (const [id, d] of dg) {
      const m = got(d);
      byId.set(id, m.lazy && !m.lazy.done ? m.lazy : m.value);
    }
    let chunks = new Map(),
      order = new Map();
    const indexes = new Map(),
      docNodes = [],
      views = [];
    let synonyms = null;
    for (const n of ir.nodes) {
      if (n.phase !== 'build') continue;
      // read only what artifacts expose: a stored source or docs output stays unloaded
      if (n.out === 'docs')
        docNodes.push(n.id); // docs load on first use (search rarely needs them)
      else if (n.out === 'chunks') {
        const v = byId.get(n.id);
        if (isView(v)) views.push(v);
        else for (const c of v) chunks.set(c.id, c);
      } else if (n.out === 'index')
        indexes.set(n.p.name || n.id.replace(/^ix-/, ''), byId.get(n.id));
      if (n.t === 'synonyms') synonyms = byId.get(n.id);
    }
    let docs = null,
      links = null;
    const docsMap = () => {
      if (!docs) {
        docs = new Map();
        const dup = [];
        for (const id of docNodes)
          for (const d of byId.get(id)) {
            if (docs.has(d.id)) dup.push(d.name || d.id);
            else docs.set(d.id, d);
          }
        // keep the first, say so: a silent overwrite hid notes (RAGWB-003 §4.3)
        if (dup.length && this.env.log)
          this.env.log(
            `[artifacts] ${dup.length} doc(s) share an id with an earlier doc and are not in art.docs: ${dup.slice(0, 5).join(', ')}`,
          );
      }
      return docs;
    };
    // reading order per doc from the first chunk stream
    const primary = ir.nodes.find(
      (n) => n.phase === 'build' && n.out === 'chunks' && n.t !== 'enrich' && n.t !== 'enrich.llm',
    );
    const primaryOut = primary ? byId.get(primary.id) : null;
    if (isView(primaryOut))
      order = {
        get: (docId) => primaryOut.order(docId),
        has: (docId) => primaryOut.order(docId).length > 0,
      };
    else if (primary)
      for (const c of primaryOut) {
        if (!order.has(c.docId)) order.set(c.docId, []);
        order.get(c.docId).push(c.id);
      }
    // stored chunk streams stay on disk: the single view is art.chunks; with several
    // streams, later ones win per id (as Map.set did) and the views answer the rest
    if (views.length)
      chunks = !chunks.size && views.length === 1 ? views[0] : unionChunks(chunks, views);
    // links: [[wiki]] targets and related notes resolved by title or alias (computed on first use)
    const linksMap = () => {
      if (links) return links;
      const norm = (s) =>
        String(s)
          .toLowerCase()
          .replace(/^(proj|project report|project)\s*-\s*/i, '')
          .replace(/[^a-z0-9]+/g, ' ')
          .trim();
      const names = new Map();
      for (const d of docsMap().values())
        for (const nm of [d.title, d.name.replace(/\.md$/, ''), ...arr(d.meta.aliases)])
          names.set(norm(nm), d.id);
      links = new Map();
      for (const d of docsMap().values()) {
        const targets = [
          ...d.links,
          ...arr(d.meta.related_vault_notes).map((x) => String(x).replace(/^\[\[|\]\]$/g, '')),
        ];
        links.set(
          d.id,
          uniq(targets.map((t) => names.get(norm(t))).filter((x) => x && x !== d.id)),
        );
      }
      return links;
    };
    this._art = {
      key,
      storeKeys: ir.nodes
        .filter((n) => n.phase === 'build' && persistOf(REG.get(n.t)))
        .map((n) => storeKey(dg.get(n.id), REG.get(n.t)))
        .filter((key) => this.env.artifacts?.has(key)),
      byId,
      chunks,
      order,
      indexes,
      synonyms,
      get docs() {
        return docsMap();
      },
      get links() {
        return linksMap();
      },
    };
    timed(
      'artifacts',
      ir.id,
      now() - tArt,
      `${chunks.size} chunks · ${indexes.size} indexes (incl. loads)`,
    );
    return this._art;
  }
  session(irOrGetter) {
    return new Session(this, typeof irOrGetter === 'function' ? irOrGetter : () => irOrGetter);
  }
  async run(ir, signals = {}, opts = {}) {
    // scoped() runs before the first await
    ir = compile(ir);
    this.noteIr(ir);
    return this.session(ir).run(signals, scoped(opts));
  }
  // plans this engine ran recently (serve: the app's plan plus those verbs ran),
  // so gc keeps their outputs (RAGWB-004 §6.8)
  noteIr(ir) {
    const key = ir.id + ':' + hash(ir.nodes.map((n) => [n.id, n.t, n.p]));
    this.recent.delete(key);
    this.recent.set(key, ir);
    while (this.recent.size > 8) this.recent.delete(this.recent.keys().next().value);
  }
  recentIrs() {
    return [...this.recent.values()];
  }
  // Request identity and event subscriptions do not change build semantics.
  buildShared(ir, opts = {}) {
    const { signal, onEvent, onProgress, scope, detail, ...semantics } = opts;
    const key = hash([ir.digest, [...this.digests(ir)], semantics]);
    return this.building.run(key, { signal, onEvent, onProgress }, (owned) =>
      this.build(ir, { ...semantics, ...owned }),
    );
  }
}

function passesOf(ir, report) {
  const by = (t) =>
    ir.nodes
      .filter((n) => n.phase === 'build' && t(n))
      .map((n) => report.nodes[n.id])
      .filter(Boolean);
  const sum = (xs, k) => xs.reduce((a, x) => a + (x[k] || 0), 0);
  const line = (name, xs, what) =>
    xs.length
      ? { pass: name, ms: +sum(xs, 'ms').toFixed(1), what, reused: xs.every((x) => x.reused) }
      : null;
  const src = by((n) => n.t.startsWith('source')),
    parse = by((n) => n.t.startsWith('parse')),
    ch = by((n) => n.out === 'chunks' && !n.t.startsWith('enrich')),
    en = by((n) => n.t.startsWith('enrich')),
    bk = by((n) => n.t === 'synonyms' || n.t === 'bake'),
    ix = by((n) => n.out === 'index');
  const lv = ir.nodes.filter((n) => n.phase === 'live');
  const lambdas = ir.nodes.reduce(
    (a, n) => a + Object.values(n.p).filter((v) => isLambda(v) || isBake(v)).length,
    0,
  );
  return [
    line('resolve(sources)', src, `${sum(src, 'count')} files`),
    line('parse(markdown)', parse, `${sum(parse, 'count')} docs`),
    line(
      'chunk',
      ch,
      ch.map((x) => `${x.id} ${x.count}`).join(' · ') +
        (ch.some((x) => x.reused) ? ' · reused' : ''),
    ),
    line(
      'enrich',
      en,
      en
        .map((x) => `${x.id}: ${x.computed} computed · ${x.cached} cached · ${x.calls} calls`)
        .join(' · '),
    ),
    line('bake', bk, `${bk.length} value${bk.length === 1 ? '' : 's'}`),
    line('index', ix, ix.map((x) => x.id.replace(/^ix-/, '')).join(' · ')),
    {
      pass: 'lower(plan)',
      ms: +(ir.compileMs || 0).toFixed(1),
      what: `${lv.length} live nodes · ${lambdas} λ`,
    },
    { pass: 'check(ports)', ms: 0, what: `${ir.edges.length} edges · 0 problems` },
  ].filter(Boolean);
}

export { Engine };
