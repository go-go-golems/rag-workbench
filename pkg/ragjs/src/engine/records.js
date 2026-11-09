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

const JSON_PERSIST = { save: (v) => v, load: (x) => x };

const FULL_DELTA = Object.freeze({ full: true });

const persistOf = (def) =>
  !def || def.persist === false
    ? null
    : def.persist || (['docs', 'chunks'].includes(def.out) ? JSON_PERSIST : null);

const storeKey = (d, def) => hash([d, def.src || '']);

export { FULL_DELTA, isView, JSON_PERSIST, Lazy, Outputs, persistOf, storeKey };
