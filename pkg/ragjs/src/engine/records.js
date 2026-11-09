import { hash, now } from '../core/util.js';

// ────────────────────────────────────────────────────────────── engine
// Persisted build outputs (env.artifacts, the Go artifact store): a node whose
// digest is stored becomes a Lazy memo entry, loaded only when something reads
// it (a node that must recompute, or artifacts()). What a node persists comes
// from def.persist ({ save(v) → data, load(data, p) → v } or false); docs
// and chunks default to JSON (sources are cheaper to re-read than to load).
// The store key adds the component's source
// text, so editing a rag.define'd component never reuses its old outputs.
class Lazy {
  constructor(load, onLoad) {
    this.load = load;
    this.onLoad = onLoad;
    this.done = false;
    this.v = undefined;
  }
  force() {
    if (!this.done) {
      const t0 = now();
      this.v = this.load();
      this.done = true;
      this.load = null;
      if (this.onLoad) this.onLoad(now() - t0);
    }
    return this.v;
  }
}

function unionChunks(map, views) {
  const get = (id) => map.get(id) ?? views.reduce((hit, v) => hit ?? v.get(id), undefined);
  return {
    get,
    has: (id) => map.has(id) || views.some((v) => v.has(id)),
    get size() {
      return new Set([...map.keys(), ...views.flatMap((v) => [...v.keys()])]).size;
    },
    *values() {
      const seen = new Set(map.keys());
      for (const v of views)
        for (const c of v.values())
          if (!seen.has(c.id)) {
            seen.add(c.id);
            yield get(c.id);
          }
      yield* map.values();
    },
    *keys() {
      for (const c of this.values()) yield c.id;
    },
    *entries() {
      for (const c of this.values()) yield [c.id, c];
    },
    forEach(f) {
      for (const c of this.values()) f(c, c.id, this);
    },
    [Symbol.iterator]() {
      return this.entries();
    },
  };
}

class Outputs extends Map {
  get(k) {
    const v = super.get(k);
    return v instanceof Lazy ? v.force() : v;
  }
}

// A record view stands in for a stored chunks output in Go runtimes (RAGWB-002,
// natives/persist.go): it iterates like the array, get/has/size like art.chunks,
// order(docId) like art.order. Components that need the array get toArray().
const isView = (v) => v != null && typeof v === 'object' && v.__records !== undefined;

const asArray = (v) => (isView(v) ? v.toArray() : v);

const JSON_PERSIST = { save: (v) => v, load: (x) => x };

const FULL_DELTA = Object.freeze({ full: true });

const persistOf = (def) =>
  !def || def.persist === false
    ? null
    : def.persist || (['docs', 'chunks'].includes(def.out) ? JSON_PERSIST : null);

const storeKey = (d, def) => hash([d, def.src || '']);

export {
  asArray,
  FULL_DELTA,
  isView,
  JSON_PERSIST,
  Lazy,
  Outputs,
  persistOf,
  storeKey,
  unionChunks,
};
