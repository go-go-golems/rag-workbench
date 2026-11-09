import { tokenize } from '../core/text.ts';
import { arr, getPath, RE_NONWORD } from '../core/util.js';

// ────────────────────────────────────────────────────────────── indexes
class Bm25Index {
  constructor(chunks, p = {}) {
    this.kind = 'bm25';
    this.fields = p.fields || { title: 2, headingPath: 3, text: 1 };
    this.k1 = p.k1 ?? 1.2;
    this.b = p.b ?? 0.75;
    this.stem = p.stem !== false;
    this.ids = [];
    this.docOf = [];
    this.len = [];
    this.post = new Map();
    for (const c of chunks) {
      const i = this.ids.length;
      this.ids.push(c.id);
      this.docOf.push(c.docId);
      const tf = new Map();
      let len = 0;
      for (const [f, w] of Object.entries(this.fields)) {
        let v = getPath(c, f);
        if (Array.isArray(v)) v = v.join(' ');
        if (v == null || v === '') continue;
        const ts = tokenize(typeof v === 'string' ? v : JSON.stringify(v), { stem: this.stem });
        len += ts.length * w;
        for (const t of ts) tf.set(t, (tf.get(t) || 0) + w);
      }
      this.len.push(len);
      for (const [t, n] of tf) {
        if (!this.post.has(t)) this.post.set(t, []);
        this.post.get(t).push(i, n);
      }
    }
    this.N = this.ids.length;
    this.avg = this.len.reduce((a, b) => a + b, 0) / Math.max(1, this.N);
  }
  terms(q, synonyms) {
    const w = new Map();
    for (const t of tokenize(q, { stem: this.stem })) w.set(t, Math.max(w.get(t) || 0, 1));
    if (synonyms) {
      const lq = ' ' + String(q).toLowerCase().replace(RE_NONWORD(), ' ') + ' ';
      for (const [k, vs] of Object.entries(synonyms)) {
        const kk = ' ' + k.toLowerCase().replace(RE_NONWORD(), ' ').trim() + ' ';
        if (kk.trim() && lq.includes(kk))
          for (const v of arr(vs))
            for (const t of tokenize(v, { stem: this.stem })) if (!w.has(t)) w.set(t, 0.5);
      }
    }
    return w;
  }
  search(q, k = 20, opts = {}) {
    const qt = this.terms(q, opts.synonyms);
    const score = new Map(),
      why = new Map();
    for (const [t, qw] of qt) {
      const pl = this.post.get(t);
      if (!pl) continue;
      const df = pl.length / 2;
      const idf = Math.log(1 + (this.N - df + 0.5) / (df + 0.5));
      for (let j = 0; j < pl.length; j += 2) {
        const i = pl[j],
          tf = pl[j + 1];
        if (opts.only && !opts.only.has(i)) continue;
        const s =
          (qw * idf * (tf * (this.k1 + 1))) /
          (tf + this.k1 * (1 - this.b + (this.b * this.len[i]) / this.avg));
        score.set(i, (score.get(i) || 0) + s);
        if (!why.has(i)) why.set(i, {});
        why.get(i)[t] = +s.toFixed(2);
      }
    }
    return [...score]
      .sort((a, b) => b[1] - a[1])
      .slice(0, k)
      .map(([i, s]) => ({
        id: this.ids[i],
        score: +s.toFixed(4),
        why: Object.fromEntries(
          Object.entries(why.get(i))
            .sort((a, b) => b[1] - a[1])
            .slice(0, 5),
        ),
      }));
  }
  searchIn(docId, q, k = 3, opts = {}) {
    const only = new Set();
    this.docOf.forEach((d, i) => {
      if (d === docId) only.add(i);
    });
    return this.search(q, k, { ...opts, only });
  }
  toJSON() {
    return {
      kind: 'bm25',
      chunks: this.N,
      terms: this.post.size,
      avgLen: +this.avg.toFixed(1),
      fields: this.fields,
    };
  }
}

class TermsIndex {
  constructor(chunks, p = {}) {
    this.kind = 'terms';
    this.keys = p.keys || ['type', 'tags', 'status'];
    this.map = new Map(); // "value" → Map(key → [chunkIds])
    this.byDoc = new Map();
    this.N = chunks.length;
    for (const c of chunks) {
      if (!this.byDoc.has(c.docId)) this.byDoc.set(c.docId, []);
      this.byDoc.get(c.docId).push(c);
      for (const key of this.keys)
        for (const v of arr(getPath(c.meta, key))) {
          const val = String(v).toLowerCase();
          if (!this.map.has(val)) this.map.set(val, new Map());
          const m = this.map.get(val);
          if (!m.has(key)) m.set(key, []);
          m.get(key).push(c.id);
        }
    }
  }
  search(q, k = 20) {
    const lq = ' ' + String(q).toLowerCase().replace(RE_NONWORD(), ' ') + ' ';
    const score = new Map(),
      why = new Map();
    for (const [val, byKey] of this.map) {
      const words = ' ' + val.replace(RE_NONWORD(), ' ').trim() + ' ';
      if (words.trim().length < 2 || !lq.includes(words)) continue;
      for (const [key, ids] of byKey) {
        const w = 1 / Math.log(2 + ids.length / 10);
        for (const id of ids) {
          score.set(id, (score.get(id) || 0) + w);
          why.set(id, { ...(why.get(id) || {}), [key]: val });
        }
      }
    }
    return [...score]
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .slice(0, k)
      .map(([id, s]) => ({ id, score: +s.toFixed(4), why: why.get(id) }));
  }
  values(key) {
    const out = {};
    for (const [val, byKey] of this.map) if (byKey.has(key)) out[val] = byKey.get(key).length;
    return out;
  }
  toJSON() {
    return { kind: 'terms', keys: this.keys, values: this.map.size };
  }
}

export { Bm25Index, TermsIndex };
