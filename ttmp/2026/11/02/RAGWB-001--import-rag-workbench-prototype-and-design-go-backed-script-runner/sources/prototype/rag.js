/* rag.js — a grammar for building search experiences.
 *
 * Fluent builders (corpus, plan, graph) compile to a JSON IR: a DAG of build
 * nodes (files → docs → chunks → indexes) and live nodes (signals → queries →
 * hits → context → answer). Lambdas are stored as source and run closed.
 * A workspace edits the IR with ops (set, insert, remove, …) for agents.
 *
 * Browser: window.rag. Node: module.exports.
 */
(function (root, factory) {
  const rag = factory();
  if (typeof module === 'object' && module.exports) module.exports = rag;
  else root.rag = rag;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  // ────────────────────────────────────────────────────────────── utilities
  const now = () => (typeof performance !== 'undefined' ? performance.now() : Date.now());

  function cyrb53(str, seed = 0) {
    let h1 = 0xdeadbeef ^ seed, h2 = 0x41c6ce57 ^ seed;
    for (let i = 0, ch; i < str.length; i++) {
      ch = str.charCodeAt(i);
      h1 = Math.imul(h1 ^ ch, 2654435761);
      h2 = Math.imul(h2 ^ ch, 1597334677);
    }
    h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909);
    h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909);
    return (h2 >>> 0).toString(16).padStart(8, '0') + (h1 >>> 0).toString(16).padStart(8, '0').slice(0, 6);
  }

  function stable(v) {
    if (v === undefined) return 'null';
    if (v === null || typeof v !== 'object') {
      if (typeof v === 'function') return JSON.stringify('$fn:' + v.toString());
      return JSON.stringify(v);
    }
    if (v instanceof RegExp) return JSON.stringify('/' + v.source + '/' + v.flags);
    if (Array.isArray(v)) return '[' + v.map(stable).join(',') + ']';
    return '{' + Object.keys(v).sort().filter(k => v[k] !== undefined).map(k => JSON.stringify(k) + ':' + stable(v[k])).join(',') + '}';
  }
  const hash = v => cyrb53(typeof v === 'string' ? v : stable(v));

  const clone = v => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));
  const isObj = v => v !== null && typeof v === 'object' && !Array.isArray(v);
  const isLambda = v => isObj(v) && typeof v.$fn === 'string';
  const isBake = v => isObj(v) && typeof v.$bake === 'string';
  const arr = v => (v == null ? [] : Array.isArray(v) ? v : [v]);
  const uniq = xs => [...new Set(xs)];
  const slug = s => String(s || '').toLowerCase().replace(/[`'"’]/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64) || 'x';
  const tokens = t => Math.ceil(String(Array.isArray(t) ? t.map(b => (b && b.text) || b).join('\n') : (t && t.text) || t || '').length / 4);
  const clip = (s, n) => (s && s.length > n ? s.slice(0, n - 1) + '…' : s || '');

  function getPath(obj, path) {
    if (!path) return obj;
    let cur = obj;
    for (const k of String(path).split('.')) { if (cur == null) return undefined; cur = cur[k]; }
    return cur;
  }
  function setPath(obj, path, value) {
    const ks = String(path).split('.');
    let cur = obj;
    for (let i = 0; i < ks.length - 1; i++) {
      if (!isObj(cur[ks[i]])) cur[ks[i]] = {};
      cur = cur[ks[i]];
    }
    if (value === undefined) delete cur[ks[ks.length - 1]];
    else cur[ks[ks.length - 1]] = value;
  }

  class RagError extends Error {
    constructor(code, message, detail) {
      super(message);
      this.name = 'Rag' + code[0].toUpperCase() + code.slice(1) + 'Error';
      this.code = code;
      if (detail) this.detail = detail;
    }
  }

  // ────────────────────────────────────────────────────────────── lambdas
  // Lambdas are stored as source text ({$fn}) and compiled without their
  // closure. A guard turns any free identifier into an error that says so.
  const ALLOWED = new Set(('Math JSON Array Object String Number Boolean Date RegExp Map Set WeakMap WeakSet Promise Symbol Error ' +
    'TypeError RangeError Intl parseInt parseFloat isNaN isFinite Infinity NaN undefined encodeURIComponent decodeURIComponent ' +
    'console structuredClone BigInt Uint8Array TextEncoder TextDecoder').split(' '));
  const compiled = new Map();

  function lam(f) {
    if (isLambda(f)) return f;
    if (isBake(f)) return f;
    if (typeof f === 'string') return { $fn: f.trim() };
    if (typeof f !== 'function') throw new RagError('lambda', 'expected a function, got ' + typeof f);
    const src = f.toString();
    if (/\[native code\]/.test(src)) throw new RagError('lambda', 'native or bound functions cannot be stored as lambdas; write an arrow function');
    return { $fn: src };
  }
  function guard(label) {
    return new Proxy({}, {
      has: (t, k) => typeof k === 'string' && !ALLOWED.has(k),
      get: (t, k) => {
        if (k === Symbol.unscopables) return undefined;
        throw new RagError('lambda', `${label} references free identifier \`${String(k)}\`. Lambdas are stored as source and run without their closure: use their arguments, a signal, or rag.bake.`);
      },
    });
  }
  function compileSource(src, label) {
    const make = s => new Function('__g', 'with (__g) { return (' + s + '\n); }')(guard(label));
    try { return make(src); } catch (e) {
      if (!(e instanceof SyntaxError)) throw e;
      // method shorthand: `name(a) { … }` / `async name(a) { … }`
      const m = /^(async\s+)?([A-Za-z_$][\w$]*)\s*\(/.exec(src);
      if (m && m[2] !== 'function') { try { return make((m[1] || '') + 'function ' + m[2] + src.slice(m[0].length - 1)); } catch (_) { /* fall through */ } }
      throw new RagError('lambda', `${label}: ${e.message}`);
    }
  }
  function fnOf(spec, label = 'lambda') {
    const src = isLambda(spec) ? spec.$fn : isBake(spec) ? spec.$bake : spec;
    const key = label + '\u0000' + src;
    if (!compiled.has(key)) compiled.set(key, compileSource(src, label));
    return compiled.get(key);
  }
  function checkLambda(spec, label) {
    const src = isLambda(spec) ? spec.$fn : spec.$bake;
    try { new Function('return (' + src + '\n);'); return null; } catch (e) {
      try { fnOf(spec, label); return null; } catch (e2) { return `${label}: ${e2.message}`; }
    }
  }
  // signal names a lambda reads: s.name, s?.name, s['name'], ({ name }) destructuring of s
  function signalReads(src) {
    const out = new Set();
    const m = /^\s*(?:async\s*)?(?:function[^(]*)?\(?\s*([A-Za-z_$][\w$]*|\{[^}]*\})/.exec(src);
    const first = m && m[1];
    if (first && first.startsWith('{')) {
      for (const part of first.slice(1, -1).split(',')) { const k = part.split(/[:=]/)[0].trim(); if (/^[A-Za-z_$][\w$]*$/.test(k)) out.add(k); }
      return [...out];
    }
    const name = first || 's';
    const re = new RegExp('\\b' + name.replace(/\$/g, '\\$') + "\\s*(?:\\?\\.|\\.)\\s*([A-Za-z_$][\\w$]*)|\\b" + name.replace(/\$/g, '\\$') + "\\s*\\[\\s*['\"]([^'\"]+)['\"]\\s*\\]", 'g');
    let r;
    while ((r = re.exec(src))) out.add(r[1] || r[2]);
    return [...out];
  }

  // ────────────────────────────────────────────────────────────── text
  const STOP = new Set(('a an and are as at be but by can could did do does for from had has have how i if in into is it its ' +
    'me more most my no not of on or our out so such than that the their them then there these they this those through to ' +
    'too up us was we were what when where which while who why will with would you your about after again all also any ' +
    'because been before being between both each few further here just only other own same should some very via over under').split(' '));

  function stem(w) {
    if (w.length <= 3 || /\d/.test(w)) return w;
    if (w.endsWith("'s")) w = w.slice(0, -2);
    if (w.endsWith('ies') && w.length > 4) w = w.slice(0, -3) + 'y';
    else if (w.endsWith('sses')) w = w.slice(0, -2);
    else if (/(xes|ches|shes|zes)$/.test(w)) w = w.slice(0, -2);
    else if (w.endsWith('s') && !/(ss|us|is|ys)$/.test(w) && w.length > 4) w = w.slice(0, -1);
    if (w.endsWith('ing') && w.length > 6) w = w.slice(0, -3);
    else if (w.endsWith('ed') && w.length > 5) w = w.slice(0, -2);
    else if (w.endsWith('ly') && w.length > 5) w = w.slice(0, -2);
    if (/([^aeiouls])\1$/.test(w)) w = w.slice(0, -1);
    if (w.endsWith('e') && w.length > 4) w = w.slice(0, -1);
    return w;
  }
  function tokenize(text, opts = {}) {
    const out = [];
    const raw = String(text || '').match(/[\p{L}\p{N}_]+/gu) || [];
    for (const r of raw) {
      const parts = r.split(/_+|(?<=[a-z0-9])(?=[A-Z])/).filter(Boolean);
      const words = parts.length > 1 ? [...parts, parts.join('')] : parts;
      for (let w of words) {
        w = w.toLowerCase();
        if (w.length < 2 && !/\d/.test(w)) continue;
        if (STOP.has(w)) continue;
        out.push(opts.stem === false ? w : stem(w));
      }
    }
    return out;
  }

  // ────────────────────────────────────────────────────────────── markdown
  function parseScalar(v) {
    v = v.trim();
    if (v === '') return '';
    if (/^\[.*\]$/.test(v)) return v.slice(1, -1).split(',').map(x => parseScalar(x)).filter(x => x !== '');
    if (/^".*"$|^'.*'$/.test(v)) return v.slice(1, -1);
    if (v === 'true') return true;
    if (v === 'false') return false;
    if (/^-?\d+(\.\d+)?$/.test(v) && v.length < 12) return Number(v);
    return v;
  }
  function parseFrontmatter(text) {
    if (!/^---\r?\n/.test(text)) return { meta: {}, body: text };
    const end = text.indexOf('\n---', 4);
    if (end < 0) return { meta: {}, body: text };
    const meta = {};
    let key = null;
    for (const line of text.slice(4, end).split(/\r?\n/)) {
      const item = /^\s+-\s+(.*)$/.exec(line);
      if (item && key) { if (!Array.isArray(meta[key])) meta[key] = []; meta[key].push(parseScalar(item[1])); continue; }
      const kv = /^([A-Za-z0-9_-]+):\s*(.*)$/.exec(line);
      if (kv) { key = kv[1]; meta[key] = parseScalar(kv[2]); if (meta[key] === '') meta[key] = []; }
    }
    for (const k of Object.keys(meta)) if (Array.isArray(meta[k]) && !meta[k].length) delete meta[k];
    return { meta, body: text.slice(text.indexOf('\n', end + 1) + 1) };
  }
  const plain = s => String(s).replace(/`([^`]*)`/g, '$1').replace(/\*\*([^*]+)\*\*/g, '$1').replace(/\[\[([^\]|]+)(\|[^\]]*)?\]\]/g, '$1').replace(/\[([^\]]+)\]\([^)]*\)/g, '$1').replace(/#+\s*$/, '').trim();

  function parseMarkdown(file, p = {}) {
    const { meta, body } = p.frontmatter === false ? { meta: {}, body: file.text } : parseFrontmatter(file.text);
    const lines = body.split(/\r?\n/);
    const sections = [];
    let sec = { level: 0, title: '', path: [], blocks: [] };
    sections.push(sec);
    const stack = [];
    let para = [];
    let h1 = null;
    const flush = () => { if (para.length) { sec.blocks.push({ kind: 'paragraph', text: para.join('\n') }); para = []; } };
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      const fence = /^\s*(`{3,}|~{3,})\s*([\w+-]*)/.exec(line);
      if (fence) {
        flush();
        const marker = fence[1], body2 = [];
        i++;
        while (i < lines.length && !lines[i].trim().startsWith(marker)) body2.push(lines[i++]);
        sec.blocks.push({ kind: 'fence', lang: fence[2] || '', text: body2.join('\n') });
        continue;
      }
      const h = /^(#{1,6})\s+(.*?)\s*$/.exec(line);
      if (h) {
        flush();
        const level = h[1].length, title = plain(h[2]);
        if (level === 1 && h1 === null) { h1 = title; continue; }
        while (stack.length && stack[stack.length - 1].level >= level) stack.pop();
        sec = { level, title, path: [...stack.map(x => x.title), title], blocks: [] };
        stack.push(sec);
        sections.push(sec);
        continue;
      }
      if (/^\s*\|/.test(line)) {
        flush();
        const rows = [];
        while (i < lines.length && /^\s*\|/.test(lines[i])) rows.push(lines[i++]);
        i--;
        const cells = r => r.trim().replace(/^\||\|$/g, '').split('|').map(c => plain(c.trim()));
        const data = rows.filter(r => !/^\s*\|[\s:|-]+\|\s*$/.test(r)).map(cells);
        sec.blocks.push({ kind: 'table', header: data[0] || [], rows: data.slice(1), text: rows.join('\n') });
        continue;
      }
      if (/^\s*>/.test(line)) {
        flush();
        const q = [];
        while (i < lines.length && /^\s*>/.test(lines[i])) q.push(lines[i++].replace(/^\s*>\s?/, ''));
        i--;
        const c = /^\[!(\w+)\]\s*(.*)$/.exec(q[0] || '');
        sec.blocks.push(c ? { kind: 'callout', variant: c[1].toLowerCase(), text: [c[2], ...q.slice(1)].filter(Boolean).join('\n') } : { kind: 'quote', text: q.join('\n') });
        continue;
      }
      if (/^\s*([-*+]|\d+\.)\s+/.test(line)) {
        flush();
        const items = [];
        while (i < lines.length && (/^\s*([-*+]|\d+\.)\s+/.test(lines[i]) || (/^\s{2,}\S/.test(lines[i]) && items.length))) items.push(lines[i++]);
        i--;
        sec.blocks.push({ kind: 'list', text: items.join('\n') });
        continue;
      }
      if (!line.trim()) { flush(); continue; }
      para.push(line);
    }
    flush();
    const name = file.name;
    const id = slug(name.replace(/\.md$/i, ''));
    const title = String(meta.title || h1 || name.replace(/\.md$/i, '')).replace(/^"|"$/g, '');
    const links = [];
    const linkRe = /\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|[^\]]*)?\]\]/g;
    let m;
    while ((m = linkRe.exec(file.text))) links.push(m[1].trim());
    for (const s of sections) { s.id = slug(s.path.length ? s.path[s.path.length - 1] : 'intro'); s.text = s.blocks.map(b => b.text).join('\n\n'); }
    return { id, name, title, meta, h1, sections: sections.filter(s => s.text.trim() || s.level > 0), links: uniq(links) };
  }

  const META_SKIP = new Set(['title', 'created', 'updated', 'source_ticket_path', 'source_design_doc', 'related_vault_notes', 'derived-from']);
  function pickMeta(meta) {
    const out = {};
    for (const [k, v] of Object.entries(meta || {})) {
      if (META_SKIP.has(k)) continue;
      if (typeof v === 'string' && v.length > 120) continue;
      if (Array.isArray(v) && v.some(x => typeof x === 'string' && x.length > 120)) continue;
      out[k] = v;
    }
    return out;
  }

  // ────────────────────────────────────────────────────────────── chunking
  function splitBlock(b, max) {
    if (tokens(b.text) <= max) return [b];
    const atomic = b.kind === 'fence' || b.kind === 'table';
    if (atomic && tokens(b.text) <= max * 3) return [b];
    const units = atomic || b.kind === 'list' ? b.text.split('\n') : b.text.split(/(?<=[.!?])\s+/);
    const out = [];
    let cur = [];
    for (const u of units) {
      if (cur.length && tokens(cur.join(' ') + u) > max) { out.push({ ...b, text: cur.join(atomic || b.kind === 'list' ? '\n' : ' ') }); cur = []; }
      cur.push(u);
    }
    if (cur.length) out.push({ ...b, text: cur.join(atomic || b.kind === 'list' ? '\n' : ' ') });
    return out;
  }
  function blocksText(blocks) {
    return blocks.map(b => (b.kind === 'fence' ? '```' + (b.lang || '') + '\n' + b.text + '\n```' : b.kind === 'callout' ? '[' + b.variant + '] ' + b.text : b.text)).join('\n\n');
  }
  function makeChunk(doc, path, blocks, extra) {
    const text = typeof extra.text === 'string' ? extra.text : blocksText(blocks);
    return {
      id: '', docId: doc.id, title: doc.title, headingPath: path, kind: 'section', text, meta: pickMeta(doc.meta),
      callout: blocks.filter(b => b.kind === 'callout').map(b => b.text).join('\n'),
      hasCode: blocks.some(b => b.kind === 'fence'), hasTable: blocks.some(b => b.kind === 'table'), tokens: tokens(text), ...extra, text,
    };
  }
  function assignIds(chunks) {
    const seen = new Map();
    chunks.forEach((c, i) => {
      const base = c.id || `${c.docId}#${c.kind !== 'section' ? slug(c.kind) + '-' : ''}${slug(c.headingPath[c.headingPath.length - 1] || 'intro')}`;
      const n = seen.get(base) || 0;
      seen.set(base, n + 1);
      c.id = n ? `${base}-${n + 1}` : base;
      c.ord = i;
    });
    return chunks;
  }
  function chunkSections(doc, p) {
    const depth = p.depth ?? 3, max = p.maxTokens ?? 450, min = p.minTokens ?? 30;
    const groups = [];
    for (const s of doc.sections) {
      const blocks = s.blocks.filter(b => !(p.dropMermaid !== false && b.kind === 'fence' && b.lang === 'mermaid'));
      if (s.level === 0 || s.level <= depth || !groups.length) groups.push({ path: s.path, blocks: [...blocks] });
      else groups[groups.length - 1].blocks.push({ kind: 'paragraph', text: s.title }, ...blocks);
    }
    const out = [];
    for (const g of groups) {
      if (!g.blocks.length) continue;
      const parts = [];
      let cur = [];
      for (const b of g.blocks.flatMap(b => splitBlock(b, max))) {
        if (cur.length && tokens(cur) + tokens(b.text) > max) { parts.push(cur); cur = []; }
        cur.push(b);
      }
      if (cur.length) parts.push(cur);
      if (parts.length > 1 && tokens(parts[parts.length - 1]) < min) { const last = parts.pop(); parts[parts.length - 1].push(...last); }
      parts.forEach((blocks, i) => {
        let text = blocksText(blocks);
        if (p.prefixHeading && g.path.length) text = g.path.join(' › ') + '\n' + text;
        if (p.overlap && i > 0) text = clip(blocksText(parts[i - 1]).slice(-p.overlap * 4), p.overlap * 4) + '\n' + text;
        out.push(makeChunk(doc, g.path, blocks, { text, part: i + 1, parts: parts.length }));
      });
    }
    return out;
  }

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
        for (const [t, n] of tf) { if (!this.post.has(t)) this.post.set(t, []); this.post.get(t).push(i, n); }
      }
      this.N = this.ids.length;
      this.avg = this.len.reduce((a, b) => a + b, 0) / Math.max(1, this.N);
    }
    terms(q, synonyms) {
      const w = new Map();
      for (const t of tokenize(q, { stem: this.stem })) w.set(t, Math.max(w.get(t) || 0, 1));
      if (synonyms) {
        const lq = ' ' + String(q).toLowerCase().replace(/[^\p{L}\p{N}]+/gu, ' ') + ' ';
        for (const [k, vs] of Object.entries(synonyms)) {
          const kk = ' ' + k.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, ' ').trim() + ' ';
          if (kk.trim() && lq.includes(kk)) for (const v of arr(vs)) for (const t of tokenize(v, { stem: this.stem })) if (!w.has(t)) w.set(t, 0.5);
        }
      }
      return w;
    }
    search(q, k = 20, opts = {}) {
      const qt = this.terms(q, opts.synonyms);
      const score = new Map(), why = new Map();
      for (const [t, qw] of qt) {
        const pl = this.post.get(t);
        if (!pl) continue;
        const df = pl.length / 2;
        const idf = Math.log(1 + (this.N - df + 0.5) / (df + 0.5));
        for (let j = 0; j < pl.length; j += 2) {
          const i = pl[j], tf = pl[j + 1];
          if (opts.only && !opts.only.has(i)) continue;
          const s = qw * idf * (tf * (this.k1 + 1)) / (tf + this.k1 * (1 - this.b + this.b * this.len[i] / this.avg));
          score.set(i, (score.get(i) || 0) + s);
          if (!why.has(i)) why.set(i, {});
          why.get(i)[t] = +s.toFixed(2);
        }
      }
      return [...score].sort((a, b) => b[1] - a[1]).slice(0, k).map(([i, s]) => ({
        id: this.ids[i], score: +s.toFixed(4),
        why: Object.fromEntries(Object.entries(why.get(i)).sort((a, b) => b[1] - a[1]).slice(0, 5)),
      }));
    }
    searchIn(docId, q, k = 3, opts = {}) {
      const only = new Set();
      this.docOf.forEach((d, i) => { if (d === docId) only.add(i); });
      return this.search(q, k, { ...opts, only });
    }
    toJSON() { return { kind: 'bm25', chunks: this.N, terms: this.post.size, avgLen: +this.avg.toFixed(1), fields: this.fields }; }
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
        for (const key of this.keys) for (const v of arr(getPath(c.meta, key))) {
          const val = String(v).toLowerCase();
          if (!this.map.has(val)) this.map.set(val, new Map());
          const m = this.map.get(val);
          if (!m.has(key)) m.set(key, []);
          m.get(key).push(c.id);
        }
      }
    }
    search(q, k = 20) {
      const lq = ' ' + String(q).toLowerCase().replace(/[^\p{L}\p{N}]+/gu, ' ') + ' ';
      const score = new Map(), why = new Map();
      for (const [val, byKey] of this.map) {
        const words = ' ' + val.replace(/[^\p{L}\p{N}]+/gu, ' ').trim() + ' ';
        if (words.trim().length < 2 || !lq.includes(words)) continue;
        for (const [key, ids] of byKey) {
          const w = 1 / Math.log(2 + ids.length / 10);
          for (const id of ids) { score.set(id, (score.get(id) || 0) + w); why.set(id, { ...(why.get(id) || {}), [key]: val }); }
        }
      }
      return [...score].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, k).map(([id, s]) => ({ id, score: +s.toFixed(4), why: why.get(id) }));
    }
    values(key) {
      const out = {};
      for (const [val, byKey] of this.map) if (byKey.has(key)) out[val] = byKey.get(key).length;
      return out;
    }
    toJSON() { return { kind: 'terms', keys: this.keys, values: this.map.size }; }
  }

  // ────────────────────────────────────────────────────────────── registry
  // A component type declares its phase, its ports and how it runs.
  // Ports: files docs chunks index value | queries hits context answer.
  const REG = new Map();
  const live = (type, def) => REG.set(type, { type, phase: 'live', ...def });
  const build = (type, def) => REG.set(type, { type, phase: 'build', ...def });
  const fnArgs = (a, b) => (typeof a === 'function' || isLambda(a) || typeof a === 'string' ? { fn: lam(a), ...(b || {}) } : { ...(a || {}) });
  const optArgs = a => ({ ...(a || {}) });

  build('source.files', {
    in: [], out: 'files', args: g => (typeof g === 'string' || g == null ? { glob: g || '*' } : { ...g }),
    describe: p => p.glob,
    all: (inputs, ctx, p) => {
      const re = new RegExp('^' + String(p.glob || '*').split('*').map(s => s.replace(/[.+?^${}()|[\]\\]/g, '\\$&')).join('.*') + '$', 'i');
      return ctx.files().filter(f => re.test(f.name)).map(f => ({ name: f.name, text: f.text }));
    },
  });
  build('source.inline', { in: [], out: 'files', args: files => ({ files }), all: (i, c, p) => p.files });
  build('parse.markdown', {
    in: ['files'], out: 'docs', args: optArgs, describe: () => 'frontmatter · headings · fences · tables',
    map: (file, ctx, p) => parseMarkdown(file, p),
  });
  build('chunk.sections', {
    in: ['docs'], out: 'chunks', args: optArgs, describe: p => `depth ${p.depth ?? 3} · ≤${p.maxTokens ?? 450} tok`,
    map: (doc, ctx, p) => chunkSections(doc, p), post: assignIds,
  });
  build('chunk', {
    in: ['docs'], out: 'chunks', args: fnArgs, lambdas: ['fn'], describe: () => 'build λ per doc',
    map: async (doc, ctx, p, node) => {
      const out = await fnOf(p.fn, `chunk lambda '${node.id}'`)(doc, ctx, p);
      return arr(out).filter(Boolean).map(c => makeChunk(doc, arr(c.headingPath || []), arr(c.blocks), { kind: node.id, ...c }));
    },
    post: assignIds,
  });
  build('enrich', {
    in: ['chunks'], out: 'chunks', args: fnArgs, lambdas: ['fn'], describe: p => `${p.name} · build λ per chunk`,
    map: async (chunk, ctx, p, node) => ({ ...chunk, [p.name]: await fnOf(p.fn, `enrich lambda '${node.id}'`)(chunk, ctx, p) }),
    async: true,
  });
  build('enrich.llm', {
    in: ['chunks'], out: 'chunks', args: optArgs, llm: true, describe: p => `${p.name} · 1 quick call per chunk`,
    map: async (chunk, ctx, p) => {
      const r = await ctx.sample.json(
        (p.prompt || 'For this section of an engineering report, reply with only JSON: {"summary": string (one sentence), "questions": [3-5 questions this section answers], "keywords": [5-8 search keywords, including synonyms not in the text]}.') +
        '\n\nDocument: ' + chunk.title + '\nSection: ' + chunk.headingPath.join(' › ') + '\n\n' + clip(chunk.text, 6000),
        { modelTier: p.tier || 'quick', cache: { gcTime: 86400000 } });
      return { ...chunk, [p.name]: r };
    },
    async: true,
  });
  build('index.bm25', {
    in: ['chunks'], out: 'index', args: optArgs, describe: p => Object.entries(p.fields || { title: 2, headingPath: 3, text: 1 }).map(([k, v]) => `${k}×${v}`).join(' '),
    all: ([chunks], ctx, p) => new Bm25Index(chunks, p),
  });
  build('index.terms', {
    in: ['chunks'], out: 'index', args: optArgs, describe: p => (p.keys || ['type', 'tags', 'status']).join(' · '),
    all: ([chunks], ctx, p) => new TermsIndex(chunks, p),
  });
  build('synonyms', {
    in: ['docs'], out: 'value', args: t => ({ table: isBake(t) ? t : t || {} }), describe: () => 'query expansion table',
    all: (inputs, ctx, p) => p.table || {},
  });
  build('bake', {
    in: ['docs'], out: 'value', args: f => ({ value: rag_bake(f) }), describe: () => 'baked value',
    all: (inputs, ctx, p) => p.value,
  });

  function viewHit(ctx, h) {
    const c = ctx.chunk(h.id) || {};
    return { ...h, docId: c.docId, title: c.title, headingPath: c.headingPath || [], text: c.text || '', meta: c.meta || {}, kind: c.kind, tokens: c.tokens || 0 };
  }
  const normHits = (xs, via) => arr(xs).filter(Boolean).map((h, i) => (typeof h === 'string' ? { id: h, score: 1 / (i + 1), via } : { ...h, via: h.via || via }));

  live('retrieve', {
    in: ['queries'], out: 'hits', source: true, lambdas: ['fn'],
    args: (a, b) => (typeof a === 'function' || isLambda(a) ? { fn: lam(a), ...(b || {}) } : { index: a, ...(b || {}) }),
    id: p => (p.index ? String(p.index) : 'retrieve'),
    uses: p => (p.index ? ['ix-' + p.index] : []),
    describe: p => (p.fn ? 'custom λ' : `${p.index} · k ${p.k ?? 20}`),
    run: async (s, ctx, queries, p) => {
      if (p.fn) return normHits(await fnOf(p.fn, `retrieve lambda '${ctx.node.id}'`)(s, ctx, queries, p), ctx.node.id);
      const ix = ctx.index(p.index);
      if (!ix) throw new RagError('run', `retrieve: no index named '${p.index}'`);
      const qs = p.perQuery === false ? [queries.join(' ')] : queries;
      const best = new Map();
      for (const q of qs) for (const h of ix.search(q, p.k ?? 20, { synonyms: ctx.synonyms })) {
        const prev = best.get(h.id);
        if (!prev || h.score > prev.score) best.set(h.id, { ...h, q });
      }
      return [...best.values()].sort((a, b) => b.score - a.score).slice(0, p.k ?? 20).map(h => ({ ...h, via: ctx.node.id }));
    },
  });
  live('rewrite', {
    in: ['queries'], out: 'queries', args: fnArgs, lambdas: ['fn'], skip: 'pass', describe: () => 'live λ → queries',
    run: async (s, ctx, queries, p) => uniq(arr(await fnOf(p.fn, `rewrite lambda '${ctx.node.id}'`)(s, ctx, queries, p)).map(String).filter(Boolean)),
  });
  live('rewrite.llm', {
    in: ['queries'], out: 'queries', args: optArgs, skip: 'pass', trigger: 'submit', reads: () => ['q'],
    describe: p => `${p.n ?? 3} keyword queries · ${p.tier || 'quick'}`,
    run: async (s, ctx, queries, p) => {
      const r = await ctx.sample.json(
        `Rewrite the question as ${p.n ?? 3} short keyword queries (2-6 words each) for a BM25 index over engineering project reports. Use the vocabulary such reports would use, including likely technical terms. Reply with only JSON: {"queries": [string]}.\n\nQuestion: ${s.q}`,
        { modelTier: p.tier || 'quick' });
      return uniq([...queries, ...arr(r && r.queries).map(String)]).slice(0, (p.n ?? 3) + queries.length);
    },
  });
  live('fuse.rrf', {
    in: ['hits*'], out: 'hits', args: optArgs, id: () => 'fuse', describe: p => `rrf k ${p.k ?? 60}`,
    run: (s, ctx, lists, p) => {
      const k = p.k ?? 60, w = p.weights || {}, acc = new Map();
      lists.forEach((list, li) => {
        const src = ctx.node.in[li], wt = w[src] ?? 1;
        arr(list).forEach((h, r) => {
          const a = acc.get(h.id) || { id: h.id, score: 0, parts: {} };
          a.score += wt / (k + r + 1);
          a.parts[src] = r + 1;
          acc.set(h.id, a);
        });
      });
      return [...acc.values()].sort((a, b) => b.score - a.score).slice(0, p.limit ?? 60).map(h => ({ ...h, score: +h.score.toFixed(5), via: ctx.node.id }));
    },
  });
  live('fuse.max', {
    in: ['hits*'], out: 'hits', args: optArgs, id: () => 'fuse',
    run: (s, ctx, lists) => {
      const acc = new Map();
      for (const list of lists) for (const h of arr(list)) if (!acc.has(h.id) || acc.get(h.id).score < h.score) acc.set(h.id, h);
      return [...acc.values()].sort((a, b) => b.score - a.score);
    },
  });
  live('filter', {
    in: ['hits'], out: 'hits', args: fnArgs, lambdas: ['fn'], describe: () => 'live λ per hit',
    run: async (s, ctx, hits, p) => {
      const f = fnOf(p.fn, `filter lambda '${ctx.node.id}'`), out = [];
      for (const h of hits) if (await f(s, ctx, viewHit(ctx, h), p)) out.push(h);
      return out;
    },
  });
  live('filter.where', {
    in: ['hits'], out: 'hits', args: optArgs, id: () => 'filter', reads: p => [p.key || 'filters'],
    describe: p => `meta ⊇ s.${p.key || 'filters'}`,
    run: (s, ctx, hits, p) => {
      const f = s[p.key || 'filters'] || {};
      const entries = Object.entries(f).filter(([, v]) => v != null && v !== '' && !(Array.isArray(v) && !v.length));
      if (!entries.length) return hits;
      return hits.filter(h => {
        const c = ctx.chunk(h.id);
        if (!c) return false;
        return entries.every(([k, v]) => {
          const have = k === 'doc' ? [c.docId] : arr(getPath(c.meta, k)).map(x => String(x).toLowerCase());
          return arr(v).some(x => have.includes(String(x).toLowerCase()));
        });
      });
    },
  });
  live('rerank', {
    in: ['hits'], out: 'hits', args: fnArgs, lambdas: ['fn'], skip: 'pass', describe: () => 'live λ over hits',
    run: async (s, ctx, hits, p) => normHits(await fnOf(p.fn, `rerank lambda '${ctx.node.id}'`)(s, ctx, hits.map(h => viewHit(ctx, h)), p), ctx.node.id).map(h => ({ id: h.id, score: h.score, via: h.via, reason: h.reason })),
  });
  live('rerank.llm', {
    in: ['hits'], out: 'hits', args: optArgs, id: () => 'rerank', skip: 'pass', trigger: 'submit', reads: () => ['q', 'k'],
    describe: p => `top ${p.top ?? 20} → keep ${typeof p.keep === 'number' ? p.keep : 's.k'} · ${p.tier || 'quick'}`,
    run: async (s, ctx, hits, p) => {
      const top = hits.slice(0, p.top ?? 20);
      if (!top.length) return [];
      const keep = Number(p.keep ?? s.k ?? 8);
      const passages = top.map((h, i) => { const c = ctx.chunk(h.id) || {}; return `[p${i + 1}] ${c.title} › ${(c.headingPath || []).join(' › ')}\n${clip((c.text || '').replace(/\s+/g, ' '), 700)}`; }).join('\n\n');
      const r = await ctx.sample.json(
        `Rank the passages by how directly they answer the question. Reply with only JSON: {"ranking": [{"p": "p3", "score": 0.0-1.0, "reason": "under 12 words"}]}, best first, including only passages that help answer it.\n\nQuestion: ${s.q}\n\nPassages:\n${passages}`,
        { modelTier: p.tier || 'quick' });
      const seen = new Set(), out = [];
      for (const x of arr(r && r.ranking)) {
        const i = parseInt(String(x.p || x.id || '').replace(/\D/g, ''), 10) - 1;
        if (!(i >= 0 && i < top.length) || seen.has(i)) continue;
        seen.add(i);
        out.push({ id: top[i].id, score: +Number(x.score ?? 0).toFixed(3), reason: x.reason, via: ctx.node.id });
      }
      return (out.length ? out : top).slice(0, keep);
    },
  });
  live('limit', {
    in: ['hits'], out: 'hits', args: a => (typeof a === 'number' || isLambda(a) || typeof a === 'function' ? { k: typeof a === 'number' ? a : lam(a) } : { ...(a || {}) }),
    describe: p => `k ${typeof p.k === 'number' ? p.k : 's.k'}`,
    run: (s, ctx, hits, p) => hits.slice(0, Number(p.k ?? s.k ?? 8)),
  });
  live('expand.neighbors', {
    in: ['hits'], out: 'hits', args: optArgs, id: () => 'neighbors', describe: p => `before ${p.before ?? 0} · after ${p.after ?? 1}`,
    run: (s, ctx, hits, p) => {
      const have = new Set(hits.map(h => h.id)), out = [];
      let extra = 0;
      hits.forEach((h, i) => {
        out.push(h);
        if (i >= (p.top ?? 5)) return;
        for (const n of ctx.neighbors(h.id, { before: p.before ?? 0, after: p.after ?? 1 })) {
          if (have.has(n.id) || extra >= (p.maxChars ?? 6000)) continue;
          have.add(n.id);
          extra += n.text.length;
          out.push({ id: n.id, score: +(h.score * (p.decay ?? 0.5)).toFixed(5), via: ctx.node.id, of: h.id });
        }
      });
      return out;
    },
  });
  live('expand.links', {
    in: ['hits'], out: 'hits', args: optArgs, id: () => 'links', reads: () => ['q'],
    uses: p => (p.index ? ['ix-' + p.index] : []), describe: p => `[[links]] · decay ${p.decay ?? 0.5}`,
    run: (s, ctx, hits, p) => {
      const ix = ctx.index(p.index) || ctx.firstIndex('bm25');
      const seenDocs = new Set(hits.map(h => (ctx.chunk(h.id) || {}).docId)), out = [...hits];
      let added = 0;
      for (const h of hits.slice(0, p.top ?? 5)) {
        const c = ctx.chunk(h.id);
        if (!c) continue;
        for (const d of ctx.links(c.docId)) {
          if (seenDocs.has(d) || added >= (p.max ?? 4) || !ix) continue;
          seenDocs.add(d);
          const best = ix.searchIn(d, s.q, 1)[0];
          if (best) { out.push({ id: best.id, score: +(h.score * (p.decay ?? 0.5)).toFixed(5), via: ctx.node.id, of: h.id }); added++; }
        }
      }
      return out;
    },
  });
  live('pack', {
    in: ['hits'], out: 'context', args: a => (typeof a === 'number' ? { budget: a } : { ...(a || {}) }),
    describe: p => `${Math.round((p.budget ?? 40000) / 1000)} KB · ${p.order || 'rank'} order`,
    run: (s, ctx, hits, p) => {
      const budget = p.budget ?? 40000, perDoc = new Map(), evidence = [];
      let bytes = 0;
      for (const h of hits) {
        const c = ctx.chunk(h.id);
        if (!c) continue;
        const n = perDoc.get(c.docId) || 0;
        if (n >= (p.maxPerDoc ?? 6)) continue;
        const piece = c.text.length;
        if (bytes + piece > budget && evidence.length) break;
        perDoc.set(c.docId, n + 1);
        bytes += piece;
        evidence.push({ label: 'E' + (evidence.length + 1), id: c.id, docId: c.docId, title: c.title, headingPath: c.headingPath });
      }
      const order = p.order === 'doc' ? [...evidence].sort((a, b) => (a.docId + ctx.chunk(a.id).ord).localeCompare(b.docId + ctx.chunk(b.id).ord)) : evidence;
      const text = order.map(e => `[${e.label}] ${e.title} › ${e.headingPath.join(' › ')}\n${ctx.chunk(e.id).text}`).join('\n\n---\n\n');
      return { text, evidence, bytes };
    },
  });
  live('answer', {
    in: ['context'], out: 'answer', args: optArgs, skip: 'null', trigger: 'submit', reads: () => ['q'],
    describe: p => `${p.tier || 'default'} · cites [E#]`,
    run: async (s, ctx, context, p) => {
      if (!context || !context.evidence.length) return { text: 'No evidence was retrieved for this question.', evidence: [], cited: [] };
      const prompt = (p.instructions || 'Answer the question using only the evidence below, drawn from a set of engineering project reports. Cite evidence inline as [E1], [E2]. If the evidence does not answer the question, say what is missing. Be specific and concise: under 200 words unless the question needs more. Use plain markdown.') +
        `\n\n<evidence>\n${context.text}\n</evidence>\n\nQuestion: ${s.q}`;
      const r = await ctx.sample(prompt, { modelTier: p.tier || 'default', onText: ({ text }) => ctx.emit(text) });
      const labels = uniq((r.text.match(/\bE\d+\b/g) || []));
      return { text: r.text, evidence: context.evidence, cited: labels, truncated: r.truncated };
    },
  });
  live('agentic', {
    in: ['queries'], out: 'answer', args: optArgs, skip: 'null', trigger: 'submit', reads: () => ['q'],
    uses: p => arr(p.indexes).map(i => 'ix-' + i), describe: p => `tools · ≤${p.maxReads ?? 8} reads · ${p.tier || 'default'}`,
    run: async (s, ctx, queries, p) => {
      const names = arr(p.indexes).length ? arr(p.indexes) : ctx.indexNames().filter(n => (ctx.index(n) || {}).kind === 'bm25');
      const evidence = [], rounds = [];
      const label = id => { let e = evidence.find(x => x.id === id); if (!e) { const c = ctx.chunk(id); e = { label: 'E' + (evidence.length + 1), id, docId: c.docId, title: c.title, headingPath: c.headingPath }; evidence.push(e); } return e.label; };
      const maxReads = p.maxReads ?? 8;
      const tools = [
        {
          name: 'search', description: `Search the report corpus with a short keyword query. Returns up to 8 sections as {id, doc, heading, snippet}. Indexes: ${names.join(', ')}. Search several phrasings.`,
          inputSchema: { type: 'object', properties: { query: { type: 'string' }, index: { type: 'string', enum: names } }, required: ['query'] },
          execute: ({ query, index }) => {
            const ix = ctx.index(String(index || names[0]));
            if (!ix) throw new Error('unknown index ' + index);
            const res = ix.search(String(query || ''), 8, { synonyms: ctx.synonyms });
            rounds.push({ tool: 'search', query: String(query), index: String(index || names[0]), found: res.length });
            ctx.note(`search("${clip(String(query), 40)}") → ${res.length}`);
            return res.map(h => { const c = ctx.chunk(h.id); return { id: h.id, doc: c.title, heading: c.headingPath.join(' › '), snippet: clip(c.text.replace(/\s+/g, ' '), 220) }; });
          },
        },
        {
          name: 'read', description: 'Read one section by id. Returns its full text and the evidence label to cite, like [E2].',
          inputSchema: { type: 'object', properties: { id: { type: 'string' } }, required: ['id'] },
          execute: ({ id }) => {
            const c = ctx.chunk(String(id));
            if (!c) throw new Error('no section ' + id + '; use an id returned by search');
            if (evidence.length >= maxReads && !evidence.find(e => e.id === c.id)) throw new Error(`read budget of ${maxReads} sections used; answer now from what you read`);
            const l = label(c.id);
            rounds.push({ tool: 'read', id: c.id });
            ctx.note(`read(${c.id}) → [${l}]`);
            return { label: l, doc: c.title, heading: c.headingPath.join(' › '), text: clip(c.text, 6000) };
          },
        },
        {
          name: 'outline', description: 'List a document\'s sections as {id, heading}. Pass a section id or a document id.',
          inputSchema: { type: 'object', properties: { id: { type: 'string' } }, required: ['id'] },
          execute: ({ id }) => {
            const c = ctx.chunk(String(id)), docId = c ? c.docId : String(id);
            rounds.push({ tool: 'outline', id: docId });
            return ctx.chunks(x => x.docId === docId).map(x => ({ id: x.id, heading: x.headingPath.join(' › ') }));
          },
        },
        {
          name: 'documents', description: 'List all documents as {id, title, type, tags}.',
          inputSchema: { type: 'object', properties: {} },
          execute: () => { rounds.push({ tool: 'documents' }); return ctx.docs().map(d => ({ id: d.id, title: d.title, type: d.meta.type, tags: d.meta.tags })); },
        },
      ];
      const prompt = (p.instructions || 'You answer questions about a corpus of engineering project reports using the tools. Search with several phrasings, read the sections you rely on, then answer. Cite read sections inline as [E1], [E2] using the labels read() returns. Stop searching as soon as you can answer. Final answer: plain markdown, under 250 words.') + `\n\nQuestion: ${s.q}`;
      const r = await ctx.sample(prompt, { tools, modelTier: p.tier || 'default', onText: ({ text }) => ctx.emit(text) });
      return { text: r.text, evidence, cited: uniq(r.text.match(/\bE\d+\b/g) || []), rounds };
    },
  });
  live('map', {
    in: ['hits'], out: 'hits', args: fnArgs, lambdas: ['fn'], describe: p => `live λ ${p.inType || 'hits'} → ${p.outType || 'hits'}`,
    ports: p => ({ in: [p.inType || 'hits'], out: p.outType || 'hits' }),
    run: async (s, ctx, input, p) => fnOf(p.fn, `map lambda '${ctx.node.id}'`)(s, ctx, input, p),
  });

  function rag_bake(f) { return { $bake: lam(f).$fn }; }

  function portsOf(node) {
    const def = REG.get(node.t);
    if (!def) return { in: [], out: null };
    if (def.ports) return def.ports(node.p || {});
    return { in: def.in, out: def.out };
  }
  const accepts = (port, type) => port.replace('*', '') === type || (port.startsWith('queries') && type === 'signal');

  // ────────────────────────────────────────────────────────────── specs
  // Specs are immutable: every builder call returns a new spec.
  function splitOpts(p) {
    const { id, trigger, from, in: inn, ...rest } = p || {};
    return [rest, { id, trigger, from: from ?? inn }];
  }
  function normalize(type, args) {
    const def = REG.get(type);
    if (!def) throw new RagError('compile', `unknown component '${type}'. Known: ${[...REG.keys()].join(', ')}`);
    const raw = args.length === 1 && isObj(args[0]) && args[0].$c ? args[0] : { $c: type, p: (def.args || optArgs)(...args) };
    const [p, opts] = splitOpts(raw.p);
    const d2 = REG.get(raw.$c) || def;
    return { t: raw.$c, p: normParams({ ...(d2.params || {}), ...p }), opts: { ...opts, ...(raw.opts || {}) } };
  }
  // functions in params become stored lambdas; regexps become {$re}
  function normParams(v) {
    if (typeof v === 'function') return lam(v);
    if (v instanceof RegExp) return { $re: v.source, flags: v.flags };
    if (Array.isArray(v)) return v.map(normParams);
    if (isObj(v) && !isLambda(v) && !isBake(v)) { const o = {}; for (const [k, x] of Object.entries(v)) o[k] = normParams(x); return o; }
    return v;
  }
  function freshId(nodes, base) {
    base = slug(base).replace(/-/g, '_').replace(/^(\d)/, 'n$1') || 'node';
    const ids = new Set(nodes.map(n => n.id));
    if (!ids.has(base)) return base;
    let i = 2;
    while (ids.has(base + '_' + i)) i++;
    return base + '_' + i;
  }

  class CorpusSpec {
    constructor(st) { this.st = st; Object.freeze(this); }
    get id() { return this.st.id; }
    _with(patch) { return new CorpusSpec({ ...this.st, ...patch }); }
    _add(node, cursorKey) {
      const n = { phase: 'build', trigger: undefined, ...node };
      return this._with({ nodes: [...this.st.nodes, n], cursor: { ...this.st.cursor, [cursorKey]: n.id } });
    }
    as(id) { return this._with({ id }); }
    from(src) {
      const c = typeof src === 'string' ? normalize('source.files', [src]) : Array.isArray(src) ? normalize('source.inline', [src]) : normalize(src.$c || 'source.files', [src]);
      return this._add({ id: c.opts.id || 'files', t: c.t, in: [], p: c.p }, 'files');
    }
    parse(parser) {
      const c = parser ? normalize(parser.$c, [parser]) : normalize('parse.markdown', [{}]);
      return this._add({ id: c.opts.id || 'parse', t: c.t, in: [this.st.cursor.files], p: c.p }, 'docs');
    }
    chunk(a, b, o) {
      const named = typeof a === 'string' && !/=>|function|\(/.test(a);
      const name = named ? a : null, spec = named ? b : a, opts = named ? o : b;
      const c = spec && spec.$c ? normalize(spec.$c, [spec]) : normalize('chunk', [spec, opts]);
      const id = c.opts.id || (name ? slug(name).replace(/-/g, '_') : (this.st.cursor.chunks ? freshId(this.st.nodes, 'chunks') : 'chunks'));
      const from = c.opts.from || this.st.cursor.docs;
      const next = this._add({ id, t: c.t, in: [from], p: c.p }, name && this.st.cursor.chunks ? '_' + id : 'chunks');
      return next;
    }
    enrich(name, spec, opts = {}) {
      const c = spec && spec.$c ? normalize(spec.$c, [{ ...spec.p, name }]) : normalize('enrich', [spec, { ...opts, name }]);
      c.p.name = name;
      const src = opts.from || c.opts.from || this.st.cursor.chunks;
      const id = opts.id || c.opts.id || 'enrich_' + slug(name).replace(/-/g, '_');
      return this._add({ id, t: c.t, in: [src], p: c.p }, src === this.st.cursor.chunks ? 'chunks' : '_' + id);
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
    use(fragment, params) { return fragment.apply(this, params); }
    then(comp, opts = {}) {
      const c = normalize(comp.$c, [comp]);
      const def = REG.get(c.t);
      const from = c.opts.from || opts.from || this.st.cursor[def.in[0]];
      return this._add({ id: c.opts.id || opts.id || freshId(this.st.nodes, c.t.split('.').pop()), t: c.t, in: arr(from), p: c.p }, def.out);
    }
    toJSON() { return { kind: 'corpus', id: this.st.id, nodes: this.st.nodes }; }
  }

  class PlanSpec {
    constructor(st) { this.st = st; }
    get id() { return this.st.id; }
    _with(patch) { return makePlan({ ...this.st, ...patch }); }
    as(id) { return this._with({ id }); }
    over(corpus) { return this._with({ corpus }); }
    signals(obj) {
      const signals = { ...this.st.signals, ...obj };
      const cur = { ...this.st.cursor };
      if ('q' in obj && !cur.queries) cur.queries = 'q';
      return this._with({ signals, cursor: cur });
    }
    patch(ops) { return this._with({ ops: [...(this.st.ops || []), ...arr(ops)] }); }
    variant(id, f) { return typeof f === 'function' ? f(this.as(id)) : this.as(id).patch(f); }
    use(fragment, params) { return fragment.apply(this, params); }
    then(comp, opts = {}) { const c = normalize(comp.$c, [comp]); return this._addNode(c.t, c.p, { ...opts, ...c.opts }); }
    _addNode(t, p, opts = {}) {
      const def = REG.get(t);
      const ports = def.ports ? def.ports(p) : def;
      const nodes = this.st.nodes;
      const cur = { ...this.st.cursor, hits: [...(this.st.cursor.hits || [])] };
      let inn;
      if (opts.from != null) inn = arr(opts.from).map(x => (typeof x === 'object' ? x.id : x));
      else {
        inn = [];
        for (const port of ports.in) {
          if (port === 'hits*') { inn.push(...cur.hits); continue; }
          if (port === 'hits') {
            if (cur.hits.length > 1) throw new RagError('compile', `${t}: ${cur.hits.length} hit streams (${cur.hits.join(', ')}) need a fuse step first, e.g. .fuse.rrf()`);
            if (cur.hits[0]) inn.push(cur.hits[0]);
            continue;
          }
          if (cur[port]) inn.push(cur[port]);
        }
      }
      const id = opts.id || freshId([...nodes, ...Object.keys(this.st.signals).map(k => ({ id: k })), ...(this.st.corpus ? this.st.corpus.st.nodes : [])], def.id ? def.id(p) : t.split('.')[0]);
      const node = { id, t, phase: 'live', in: inn, p };
      if (opts.trigger) node.trigger = opts.trigger;
      if (ports.out === 'hits') cur.hits = def.source && opts.from == null ? [...cur.hits, id] : [id];
      else if (ports.out) cur[ports.out] = id;
      return this._with({ nodes: [...nodes, node], cursor: cur });
    }
    toJSON() { return { kind: 'plan', id: this.st.id, signals: this.st.signals, nodes: this.st.nodes, ops: this.st.ops }; }
  }
  // family sugar: plan.retrieve(...), plan.fuse.rrf(...), plan.expand.links(...)
  // Installed as prototype getters, so components defined later appear on every plan.
  function familyAccessor(spec, fam) {
    const base = REG.get(fam);
    const add = type => (...args) => { const c = normalize(type, args); return spec._addNode(c.t, c.p, c.opts); };
    const f = base && base.phase === 'live' ? add(fam) : {};
    for (const def of REG.values()) if (def.phase === 'live' && def.type.startsWith(fam + '.')) f[def.type.slice(fam.length + 1)] = add(def.type);
    return f;
  }
  function installFamilies() {
    for (const d of REG.values()) {
      if (d.phase !== 'live') continue;
      const fam = d.type.split('.')[0];
      if (Object.prototype.hasOwnProperty.call(PlanSpec.prototype, fam)) continue;
      Object.defineProperty(PlanSpec.prototype, fam, { get() { return familyAccessor(this, fam); }, configurable: true });
    }
  }
  function makePlan(st) { return Object.freeze(new PlanSpec(st)); }

  // graph form: explicit wiring, same nodes
  function graph(id, fn) {
    let plan = makePlan({ id, corpus: null, signals: {}, nodes: [], cursor: {}, outputs: {} });
    const outputs = {};
    const g = {
      over(corpus) { plan = plan.over(corpus); return g; },
      signal(name, value) { plan = plan.signals({ [name]: value }); return { id: name, kind: 'signal' }; },
      node(nid, comp, opts = {}) {
        const c = normalize(comp.$c, [comp]);
        plan = plan._addNode(c.t, c.p, { ...c.opts, ...opts, id: nid, from: arr(opts.from ?? c.opts.from).map(x => (typeof x === 'object' ? x.id : x)) });
        return { id: nid, kind: 'node' };
      },
      output(name, h) { outputs[name] = h.id; return g; },
    };
    fn(g);
    return plan._with({ outputs });
  }

  class Fragment {
    constructor(id, fn) { this.id = id; this.fn = fn; }
    apply(spec, params) { return this.fn(spec, params || {}); }
  }

  // ────────────────────────────────────────────────────────────── IR
  const ID_RE = /^[A-Za-z][\w-]*$/;
  function finalize(raw) {
    const problems = [];
    const nodes = raw.nodes.map(n => ({ ...n, in: [...(n.in || [])], p: n.p || {} }));
    const signals = { ...(raw.signals || {}) };
    for (const k of Object.keys(signals)) if (!nodes.find(n => n.id === k)) nodes.push({ id: k, t: 'signal', phase: 'signal', in: [], p: {} });
    const byId = new Map();
    for (const n of nodes) {
      if (!ID_RE.test(n.id)) problems.push(`node id '${n.id}' must start with a letter and use letters, digits, _ or - (no dots)`);
      if (byId.has(n.id)) problems.push(`duplicate node id '${n.id}'`);
      byId.set(n.id, n);
    }
    const edges = [];
    for (const n of nodes) {
      if (n.t === 'signal') { n.out = 'signal'; continue; }
      const def = REG.get(n.t);
      if (!def) { problems.push(`${n.id}: unknown component '${n.t}'`); continue; }
      n.phase = def.phase;
      const ports = portsOf(n);
      n.out = ports.out;
      const variadic = ports.in[0] === 'hits*';
      if (!variadic && n.in.length !== ports.in.length) problems.push(`${n.id} (${n.t}) takes ${ports.in.length ? ports.in.join(', ') : 'no inputs'} but is wired to [${n.in.join(', ')}]`);
      n.in.forEach((src, i) => {
        const from = byId.get(src);
        if (!from) { problems.push(`${n.id}: input '${src}' does not exist`); return; }
        const port = variadic ? 'hits' : ports.in[i];
        const type = from.t === 'signal' ? 'signal' : portsOf(from).out;
        if (port && !accepts(port, type)) problems.push(`${n.id}: input '${src}' gives ${type} but this port takes ${port}`);
        if (def.phase === 'build' && from.phase === 'live') problems.push(`${n.id}: a build node cannot read live node '${src}'`);
        edges.push([src, n.id, from.t === 'signal' ? 'signal' : 'data']);
      });
      n.uses = uniq([...(def.uses ? def.uses(n.p) : [])]);
      if (n.t === 'retrieve' && byId.has('synonyms')) n.uses.push('synonyms');
      for (const u of n.uses) {
        if (!byId.has(u)) problems.push(`${n.id}: uses '${u}', which does not exist${u.startsWith('ix-') ? ` (indexes: ${nodes.filter(x => x.id.startsWith('ix-')).map(x => x.id.slice(3)).join(', ') || 'none'})` : ''}`);
        else edges.push([u, n.id, 'artifact']);
      }
      const reads = new Set(def.reads ? def.reads(n.p) : []);
      const walk = (v, key) => {
        if (isLambda(v)) {
          const err = checkLambda(v, `${n.id}.${key}`);
          if (err) problems.push(err);
          if (def.phase === 'live') signalReads(v.$fn).forEach(r => reads.add(r));
        } else if (isBake(v)) {
          const err = checkLambda(v, `${n.id}.${key} (bake)`);
          if (err) problems.push(err);
        } else if (isObj(v)) for (const [k, x] of Object.entries(v)) walk(x, key + '.' + k);
      };
      for (const [k, v] of Object.entries(n.p)) walk(v, k);
      n.reads = [...reads];
      for (const r of n.reads) if (byId.has(r) && byId.get(r).t === 'signal' && !n.in.includes(r)) edges.push([r, n.id, 'signal']);
    }
    // topological order
    const indeg = new Map(nodes.map(n => [n.id, 0])), out = new Map(nodes.map(n => [n.id, []]));
    for (const [a, b] of edges) { if (!indeg.has(a) || !indeg.has(b)) continue; indeg.set(b, indeg.get(b) + 1); out.get(a).push(b); }
    const order = [], q = nodes.filter(n => !indeg.get(n.id)).map(n => n.id);
    while (q.length) { const id = q.shift(); order.push(id); for (const b of out.get(id)) { indeg.set(b, indeg.get(b) - 1); if (!indeg.get(b)) q.push(b); } }
    if (order.length < nodes.length) problems.push('the graph has a cycle through: ' + nodes.filter(n => !order.includes(n.id)).map(n => n.id).join(', '));
    // outputs
    const liveOrder = order.filter(id => byId.get(id).phase === 'live');
    const lastOf = type => [...liveOrder].reverse().find(id => byId.get(id).out === type);
    const pinned = uniq(raw.pinned || Object.keys(raw.outputs || {}).filter(k => (raw.outputs || {})[k]));
    const outputs = {};
    for (const k of pinned) outputs[k] = raw.outputs[k];
    if (!outputs.answer) outputs.answer = lastOf('answer');
    if (!outputs.context) outputs.context = lastOf('context');
    if (!outputs.hits) {
      const packer = outputs.context && byId.get(outputs.context);
      const beforePack = packer && packer.in[0];
      const rerank = [...liveOrder].reverse().find(id => /^rerank/.test(byId.get(id).t));
      outputs.hits = rerank || beforePack || lastOf('hits');
    }
    for (const [k, v] of Object.entries(outputs)) if (v && !byId.has(v)) problems.push(`output ${k} points at missing node '${v}'`);
    if (problems.length) throw new RagError('compile', problems.length + ' problem' + (problems.length > 1 ? 's' : '') + ':\n  ' + problems.join('\n  '), { problems });
    const ir = {
      kind: 'rag', version: 1, id: raw.id, corpus: raw.corpus || null, signals,
      nodes: order.map(id => byId.get(id)), edges, outputs, pinned,
    };
    ir.digest = hash({ n: ir.nodes.map(n => [n.id, n.t, n.in, n.p, n.trigger]), s: signals, o: outputs });
    return withMethods(ir);
  }

  function withMethods(ir) {
    const def = (k, v) => Object.defineProperty(ir, k, { value: v, enumerable: false, configurable: true, writable: true });
    def('node', id => ir.nodes.find(n => n.id === id));
    def('downstream', id => {
      const seen = new Set(), stack = [id];
      while (stack.length) { const x = stack.pop(); for (const [a, b] of ir.edges) if (a === x && !seen.has(b)) { seen.add(b); stack.push(b); } }
      return ir.nodes.map(n => n.id).filter(n => seen.has(n));
    });
    def('upstream', id => {
      const seen = new Set(), stack = [id];
      while (stack.length) { const x = stack.pop(); for (const [a, b] of ir.edges) if (b === x && !seen.has(a)) { seen.add(a); stack.push(a); } }
      return ir.nodes.map(n => n.id).filter(n => seen.has(n));
    });
    def('summary', () => ir.nodes.map(n => `${n.id}:${n.t}${n.phase === 'live' && (n.trigger || (REG.get(n.t) || {}).trigger) === 'submit' ? '!' : ''}`).join(' → '));
    def('mermaid', () => {
      const lines = ['flowchart LR'];
      for (const n of ir.nodes) {
        const label = `${n.id}<br/><small>${n.t}</small>`;
        lines.push(n.phase === 'signal' ? `  ${n.id}((${n.id}))` : n.phase === 'build' ? `  ${n.id}[/"${label}"/]` : `  ${n.id}["${label}"]`);
      }
      for (const [a, b, k] of ir.edges) lines.push(`  ${a} ${k === 'data' ? '-->' : k === 'artifact' ? '-.->' : '-.-'} ${b}`);
      return lines.join('\n');
    });
    def('clone', () => withMethods(clone(ir)));
    def('describe', id => describeNode(ir.node(id)));
    return ir;
  }
  function describeNode(n) {
    if (!n) return '';
    if (n.t === 'signal') return 'signal';
    const def = REG.get(n.t);
    try { return def && def.describe ? def.describe(n.p || {}) : ''; } catch (e) { return ''; }
  }
  function triggerOf(n) { return n.trigger || (REG.get(n.t) || {}).trigger || 'change'; }
  // a build node spends Claude calls when its component says so or its lambda calls ctx.sample
  function costsCalls(n) {
    const def = REG.get(n.t) || {};
    if (def.phase !== 'build') return false;
    return !!def.llm || Object.values(n.p || {}).some(v => isLambda(v) && /\bsample\b/.test(v.$fn));
  }

  function rawOf(target) {
    if (target instanceof PlanSpec) {
      const corpus = target.st.corpus;
      return { id: target.st.id, corpus: corpus ? corpus.st.id : null, signals: target.st.signals, nodes: [...(corpus ? corpus.st.nodes : []), ...target.st.nodes], outputs: target.st.outputs || {}, pinned: Object.keys(target.st.outputs || {}), ops: target.st.ops };
    }
    if (target instanceof CorpusSpec) return { id: target.st.id, corpus: target.st.id, signals: {}, nodes: target.st.nodes };
    throw new RagError('compile', 'compile() takes a plan, a corpus or an IR');
  }
  function compile(target) {
    if (target && target.kind === 'rag') return withMethods(clone(target));
    if (target && target.ir && target.ir.kind === 'rag') return target.ir;
    const t0 = now();
    const raw = rawOf(target);
    let ir = finalize(raw);
    if (raw.ops && raw.ops.length) ir = applyOps(ir, raw.ops);
    Object.defineProperty(ir, 'compileMs', { value: now() - t0, enumerable: false, configurable: true });
    return ir;
  }

  // ────────────────────────────────────────────────────────────── ops
  // Ops are data. applyOps returns a new, re-checked IR or throws.
  function applyOps(ir, ops) {
    const pinned = arr(ir.pinned);
    let raw = { id: ir.id, corpus: ir.corpus, signals: clone(ir.signals), nodes: clone(ir.nodes.filter(n => n.t !== 'signal')), outputs: Object.fromEntries(pinned.map(k => [k, ir.outputs[k]])), pinned };
    arr(ops).forEach((op, i) => {
      try { raw = applyOp(raw, op); } catch (e) {
        throw new RagError('op', `op ${i + 1} (${op && op.op}): ${e.message}`, { index: i, op });
      }
    });
    raw.pinned = raw.pinned.filter(k => raw.outputs[k]);
    try { return finalize(raw); } catch (e) { throw new RagError('op', 'after ops: ' + e.message, e.detail); }
  }
  function nodeOf(raw, id) {
    const n = raw.nodes.find(x => x.id === id);
    if (!n) throw new Error(`no node '${id}'. Nodes: ${raw.nodes.map(x => x.id).join(', ')}`);
    return n;
  }
  function normValue(v) { return clone(normParams(v)); }
  function specOf(nodeSpec) {
    if (!nodeSpec) throw new Error('missing node: {t, p}');
    if (nodeSpec.$c) { const c = normalize(nodeSpec.$c, [nodeSpec]); return { t: c.t, p: c.p, trigger: c.opts.trigger, id: c.opts.id }; }
    if (!REG.get(nodeSpec.t)) throw new Error(`unknown component '${nodeSpec.t}'`);
    const p = {};
    for (const [k, v] of Object.entries(nodeSpec.p || {})) p[k] = normValue(v);
    return { t: nodeSpec.t, p, trigger: nodeSpec.trigger, id: nodeSpec.id };
  }
  function applyOp(raw, op) {
    raw = { ...raw, nodes: raw.nodes.map(n => ({ ...n, in: [...n.in], p: clone(n.p) })), signals: { ...raw.signals }, outputs: { ...raw.outputs }, pinned: [...raw.pinned] };
    const consumers = id => raw.nodes.filter(n => n.in.includes(id));
    switch (op.op) {
      case 'set': {
        const n = nodeOf(raw, op.node);
        if (!op.path) throw new Error('set needs a path into the node params, e.g. "k" or "fields.title"');
        setPath(n.p, op.path, normValue(op.value));
        return raw;
      }
      case 'unset': { setPath(nodeOf(raw, op.node).p, op.path, undefined); return raw; }
      case 'trigger': {
        const n = nodeOf(raw, op.node);
        if (!['change', 'submit'].includes(op.value)) throw new Error("trigger is 'change' or 'submit'");
        n.trigger = op.value;
        return raw;
      }
      case 'signal': { raw.signals[op.key] = clone(op.value); return raw; }
      case 'replace': {
        const n = nodeOf(raw, op.node);
        if (op.fn != null) {
          const key = op.param || 'fn';
          if (!isLambda(n.p[key]) && key === 'fn' && !(REG.get(n.t).lambdas || []).includes('fn')) throw new Error(`${n.id} (${n.t}) has no lambda; use {op:'replace', node, with:{t, p}}`);
          n.p[key] = lam(op.fn);
          return raw;
        }
        const s = specOf(op.with);
        n.t = s.t; n.p = s.p;
        if (s.trigger) n.trigger = s.trigger; else delete n.trigger;
        return raw;
      }
      case 'insert': {
        const s = specOf(op.node);
        const def = REG.get(s.t);
        const id = op.id || s.id || freshId([...raw.nodes, ...Object.keys(raw.signals).map(k => ({ id: k }))], def.id ? def.id(s.p) : s.t.split('.')[0]);
        if (raw.nodes.find(n => n.id === id)) throw new Error(`node '${id}' already exists`);
        const node = { id, t: s.t, phase: def.phase, in: [], p: s.p };
        if (s.trigger || op.trigger) node.trigger = s.trigger || op.trigger;
        if (op.after) {
          nodeOf(raw, op.after);
          node.in = [op.after];
          for (const c of consumers(op.after)) if (c.id !== id && (!op.only || arr(op.only).includes(c.id))) c.in = c.in.map(x => (x === op.after ? id : x));
          for (const [k, v] of Object.entries(raw.outputs)) if (v === op.after && portsOf(node).out === portsOf(nodeOf(raw, op.after)).out) raw.outputs[k] = id;
        } else if (op.before) {
          const b = nodeOf(raw, op.before);
          if (b.in.length !== 1) throw new Error(`'${op.before}' has ${b.in.length} inputs; insert with {in:[…]} and wire it explicitly`);
          node.in = [...b.in];
          b.in = [id];
        } else if (op.in) {
          node.in = arr(op.in);
          for (const f of arr(op.feeds)) { const c = nodeOf(raw, f); c.in.push(id); }
        } else throw new Error('insert needs after, before, or in');
        const at = op.after ? raw.nodes.findIndex(n => n.id === op.after) + 1 : raw.nodes.length;
        raw.nodes.splice(at, 0, node);
        return raw;
      }
      case 'remove': {
        const n = nodeOf(raw, op.node);
        const users = raw.nodes.filter(x => (REG.get(x.t)?.uses?.(x.p) || []).includes(n.id));
        if (users.length) throw new Error(`'${n.id}' is used by ${users.map(u => u.id).join(', ')}; remove those first`);
        for (const c of consumers(n.id)) {
          const variadic = (portsOf(c).in[0] || '') === 'hits*';
          if (variadic) c.in = c.in.flatMap(x => (x === n.id ? n.in.filter(y => !c.in.includes(y)) : [x]));
          else {
            const data = n.in.filter(x => (raw.nodes.find(y => y.id === x) || { t: 'signal' }).t !== 'signal' || portsOf(c).in[0] === 'queries');
            if (data.length !== 1) throw new Error(`cannot rewire '${c.id}' around '${n.id}' (it has ${n.in.length} inputs); use wire`);
            c.in = c.in.map(x => (x === n.id ? data[0] : x));
          }
        }
        for (const [k, v] of Object.entries(raw.outputs)) if (v === n.id) delete raw.outputs[k];
        raw.nodes = raw.nodes.filter(x => x.id !== n.id);
        return raw;
      }
      case 'wire': { nodeOf(raw, op.node).in = arr(op.in); return raw; }
      case 'output': { nodeOf(raw, op.node); raw.outputs[op.name] = op.node; if (!raw.pinned.includes(op.name)) raw.pinned.push(op.name); return raw; }
      case 'rename': {
        const n = nodeOf(raw, op.node);
        if (!ID_RE.test(op.to)) throw new Error('invalid id ' + op.to);
        for (const x of raw.nodes) x.in = x.in.map(y => (y === n.id ? op.to : y));
        for (const [k, v] of Object.entries(raw.outputs)) if (v === n.id) raw.outputs[k] = op.to;
        n.id = op.to;
        return raw;
      }
      default: throw new Error(`unknown op '${op.op}'. Ops: set unset insert remove replace wire signal trigger output rename`);
    }
  }

  // ────────────────────────────────────────────────────────────── engine
  class Engine {
    constructor(env = {}) {
      this.env = env;
      this.memo = new Map();   // build digest → { value, stats }
      this.items = new Map();  // item key → value
      this.bakes = new Map();
      this.fileDigests = new Map();
      this.partial = new Map(); // digest → output built without Claude calls (llm items deferred)
      this.listeners = new Set();
    }
    on(fn) { this.listeners.add(fn); return () => this.listeners.delete(fn); }
    emit(e) { for (const f of this.listeners) try { f(e); } catch (_) { /* listener errors stay local */ } }
    files() { return (this.env.files ? this.env.files() : []) || []; }
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
        if (n.t === 'source.files') extra = this.files().map(f => this.fileDigest(f)).join(',');
        out.set(n.id, hash([n.t, n.p, n.in.map(i => out.get(i) || i), extra]));
      }
      return out;
    }
    dirty(ir) {
      const d = this.digests(ir);
      return [...d].filter(([, dg]) => !this.memo.has(dg)).map(([id]) => id);
    }
    // build nodes whose items still wait for Claude calls (deferred), with how many
    pending(ir) {
      const d = this.digests(ir), out = [];
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
        const no = () => Promise.reject({ code: 'sampling_unavailable', message: 'Claude is not available in this view, so LLM nodes cannot run' });
        no.json = no;
        return no;
      }
      const f = (input, opts = {}) => { counter.calls++; return base(input, { ...(signal ? { signal } : {}), ...opts }); };
      f.json = (input, opts = {}) => { counter.calls++; return base.json(input, { ...(signal ? { signal } : {}), ...opts }); };
      return f;
    }
    async build(ir, opts = {}) {
      ir = compile(ir);
      const t0 = now();
      const dg = this.digests(ir);
      const report = { ok: true, ms: 0, nodes: {}, errors: [] };
      const outputs = new Map();
      const counter = { calls: 0 };
      for (const n of ir.nodes) {
        if (n.phase !== 'build') continue;
        const d = dg.get(n.id);
        const st = { id: n.id, t: n.t, digest: d, reused: false, ms: 0, count: 0, computed: 0, cached: 0, calls: 0 };
        report.nodes[n.id] = st;
        const allowCalls = opts.calls ?? (this.env.buildCalls !== 'explicit');
        const costly = costsCalls(n);
        if (this.memo.has(d) && !opts.force) {
          const m = this.memo.get(d);
          outputs.set(n.id, m.value);
          Object.assign(st, { reused: true, count: m.count, ms: 0 });
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
          const inputs = n.in.map(i => outputs.get(i));
          const ctx = this.buildCtx(n, ir, outputs, counter, opts.signal);
          let value;
          if (def.map) {
            const items = inputs[0] || [];
            const fnKey = hash([n.t, p]);
            const results = new Array(items.length);
            let next = 0, budget = (p.budget && p.budget.calls) ?? Infinity;
            const worker = async () => {
              while (next < items.length) {
                const i = next++;
                const item = items[i];
                const key = fnKey + ':' + hash(item);
                if (this.items.has(key)) { results[i] = this.items.get(key); st.cached++; continue; }
                if (def.async && this.env.cache) {
                  const hit = await this.env.cache.get(key).catch(() => undefined);
                  if (hit !== undefined && hit !== null) { this.items.set(key, hit); results[i] = hit; st.cached++; continue; }
                }
                if (def.async && counter.calls - c0 >= budget) { results[i] = def.out === 'chunks' ? item : null; st.skipped = (st.skipped || 0) + 1; continue; }
                if (costly && !allowCalls) { results[i] = def.out === 'chunks' ? item : null; st.deferred = (st.deferred || 0) + 1; continue; }
                if (opts.signal && opts.signal.aborted) throw new RagError('build', 'build stopped');
                const v = await def.map(item, { ...ctx, item }, p, n);
                results[i] = v;
                this.items.set(key, v);
                if (def.async && this.env.cache) this.env.cache.set(key, v).catch(() => {});
                st.computed++;
                if (opts.onProgress) opts.onProgress({ node: n.id, done: st.computed + st.cached, total: items.length });
              }
            };
            await Promise.all(Array.from({ length: def.async ? Math.max(1, p.concurrency ?? 3) : 1 }, worker));
            value = results.flat().filter(x => x != null);
            if (def.post) value = def.post(value);
          } else {
            value = await def.all(inputs, ctx, p, n);
          }
          outputs.set(n.id, value);
          st.count = Array.isArray(value) ? value.length : value && value.N != null ? value.N : value ? Object.keys(value).length : 0;
          st.calls = counter.calls - c0;
          if (st.deferred || st.skipped) this.partial.set(d, { value, count: st.count, deferred: (st.deferred || 0) + (st.skipped || 0) });
          else { this.memo.set(d, { value, count: st.count }); this.partial.delete(d); }
        } catch (e) {
          report.ok = false;
          st.error = e.message || String(e.code || e);
          report.errors.push(`${n.id}: ${st.error}`);
          break;
        }
        st.ms = +(now() - t1).toFixed(1);
        this.emit({ type: 'node-built', node: n.id, stats: st });
      }
      // bake params of live nodes
      if (report.ok) for (const n of ir.nodes) if (n.phase === 'live') {
        try { await this.resolveBakes(n, n.p, outputs, ir); } catch (e) { report.ok = false; report.errors.push(`${n.id}: ${e.message}`); }
      }
      report.ms = +(now() - t0).toFixed(1);
      report.calls = counter.calls;
      report.passes = passesOf(ir, report);
      this.lastBuild = report;
      this.emit({ type: 'built', report });
      return report;
    }
    buildCtx(n, ir, outputs, counter, signal) {
      const firstOf = t => { const id = ir.nodes.find(x => x.phase === 'build' && x.out === t && outputs.has(x.id)); return id ? outputs.get(id.id) : []; };
      return {
        node: n, sample: this.sampler(counter, signal), tokens, slug, hash, clip,
        log: (...a) => this.env.log && this.env.log(`[${n.id}]`, ...a),
        files: () => this.files(),
        get docs() { return firstOf('docs'); },
        get chunks() { return firstOf('chunks'); },
      };
    }
    async resolveBakes(n, p, outputs, ir) {
      let found = false;
      const scan = v => { if (isBake(v)) found = true; else if (isObj(v)) Object.values(v).forEach(scan); };
      scan(p);
      if (!found) return p;
      const docsNode = ir.nodes.find(x => x.out === 'docs' && outputs.has(x.id));
      const chunksNode = ir.nodes.find(x => x.out === 'chunks' && outputs.has(x.id));
      const docs = docsNode ? outputs.get(docsNode.id) : [], chunks = chunksNode ? outputs.get(chunksNode.id) : [];
      const key0 = hash([docs.map(d => d.id + d.sections.length), chunks.length]);
      const walk = async v => {
        if (isBake(v)) {
          const key = key0 + ':' + v.$bake;
          if (!this.bakes.has(key)) this.bakes.set(key, await fnOf({ $fn: v.$bake }, `${n.id} bake`)({ docs, chunks, tokens, slug }));
          return this.bakes.get(key);
        }
        if (isObj(v)) { const o = {}; for (const [k, x] of Object.entries(v)) o[k] = await walk(x); return o; }
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
      const got = d => this.memo.get(d) || this.partial.get(d);
      const missing = [...dg].filter(([, d]) => !got(d)).map(([id]) => id);
      if (missing.length) return null;
      const key = [...dg.values()].map(d => d + (this.memo.has(d) ? '' : '~')).join(',');
      if (this._art && this._art.key === key) return this._art;
      const byId = new Map();
      for (const [id, d] of dg) byId.set(id, got(d).value);
      const chunks = new Map(), docs = new Map(), order = new Map(), indexes = new Map();
      let synonyms = null;
      for (const n of ir.nodes) {
        if (n.phase !== 'build') continue;
        const v = byId.get(n.id);
        if (n.out === 'docs') for (const d of v) docs.set(d.id, d);
        if (n.out === 'chunks') for (const c of v) chunks.set(c.id, c);
        if (n.out === 'index') indexes.set(n.p.name || n.id.replace(/^ix-/, ''), v);
        if (n.t === 'synonyms') synonyms = v;
      }
      // reading order per doc from the first chunk stream
      const primary = ir.nodes.find(n => n.phase === 'build' && n.out === 'chunks' && n.t !== 'enrich' && n.t !== 'enrich.llm');
      if (primary) for (const c of byId.get(primary.id)) { if (!order.has(c.docId)) order.set(c.docId, []); order.get(c.docId).push(c.id); }
      // links: [[wiki]] targets and related notes resolved by title or alias
      const norm = s => String(s).toLowerCase().replace(/^(proj|project report|project)\s*-\s*/i, '').replace(/[^a-z0-9]+/g, ' ').trim();
      const names = new Map();
      for (const d of docs.values()) for (const nm of [d.title, d.name.replace(/\.md$/, ''), ...arr(d.meta.aliases)]) names.set(norm(nm), d.id);
      const links = new Map();
      for (const d of docs.values()) {
        const targets = [...d.links, ...arr(d.meta.related_vault_notes).map(x => String(x).replace(/^\[\[|\]\]$/g, ''))];
        links.set(d.id, uniq(targets.map(t => names.get(norm(t))).filter(x => x && x !== d.id)));
      }
      this._art = { key, byId, chunks, docs, order, indexes, synonyms, links };
      return this._art;
    }
    session(irOrGetter) { return new Session(this, typeof irOrGetter === 'function' ? irOrGetter : () => irOrGetter); }
    async run(ir, signals = {}, opts = {}) { return this.session(compile(ir)).run(signals, opts); }
  }

  function passesOf(ir, report) {
    const by = t => ir.nodes.filter(n => n.phase === 'build' && t(n)).map(n => report.nodes[n.id]).filter(Boolean);
    const sum = (xs, k) => xs.reduce((a, x) => a + (x[k] || 0), 0);
    const line = (name, xs, what) => xs.length ? { pass: name, ms: +sum(xs, 'ms').toFixed(1), what, reused: xs.every(x => x.reused) } : null;
    const src = by(n => n.t.startsWith('source')), parse = by(n => n.t.startsWith('parse')), ch = by(n => n.out === 'chunks' && !n.t.startsWith('enrich')),
      en = by(n => n.t.startsWith('enrich')), bk = by(n => n.t === 'synonyms' || n.t === 'bake'), ix = by(n => n.out === 'index');
    const lv = ir.nodes.filter(n => n.phase === 'live');
    const lambdas = ir.nodes.reduce((a, n) => a + Object.values(n.p).filter(v => isLambda(v) || isBake(v)).length, 0);
    return [
      line('resolve(sources)', src, `${sum(src, 'count')} files`),
      line('parse(markdown)', parse, `${sum(parse, 'count')} docs`),
      line('chunk', ch, ch.map(x => `${x.id} ${x.count}`).join(' · ') + (ch.some(x => x.reused) ? ' · reused' : '')),
      line('enrich', en, en.map(x => `${x.id}: ${x.computed} computed · ${x.cached} cached · ${x.calls} calls`).join(' · ')),
      line('bake', bk, `${bk.length} value${bk.length === 1 ? '' : 's'}`),
      line('index', ix, ix.map(x => x.id.replace(/^ix-/, '')).join(' · ')),
      { pass: 'lower(plan)', ms: +(ir.compileMs || 0).toFixed(1), what: `${lv.length} live nodes · ${lambdas} λ` },
      { pass: 'check(ports)', ms: 0, what: `${ir.edges.length} edges · 0 problems` },
    ].filter(Boolean);
  }

  // ────────────────────────────────────────────────────────────── live run
  const countOf = v => (v == null ? 0 : Array.isArray(v) ? v.length : v.evidence ? v.evidence.length : 1);
  class Session {
    constructor(engine, getIr) { this.engine = engine; this.getIr = getIr; this.memo = new Map(); this.coerced = new Map(); this.last = null; this.runs = 0; }
    queries(v) {
      const list = arr(v).map(String).filter(Boolean), key = stable(list);
      if (!this.coerced.has(key)) { if (this.coerced.size > 200) this.coerced.clear(); this.coerced.set(key, list); }
      return this.coerced.get(key);
    }
    get ir() { return this.getIr(); }
    async run(signals = {}, opts = {}) {
      const ir = this.getIr();
      const engine = this.engine;
      let art = engine.artifacts(ir);
      if (!art) { const rep = await engine.build(ir, { signal: opts.signal, onProgress: opts.onProgress }); if (!rep.ok) throw new RagError('build', rep.errors.join('\n')); art = engine.artifacts(ir); }
      const t0 = now();
      const trigger = opts.trigger || 'submit';
      const s = { ...ir.signals, ...signals };
      const outputs = {}, trace = [], errors = [];
      const counter = { calls: 0 };
      const sample = engine.sampler(counter, opts.signal);
      const runId = ++this.runs;
      for (const n of ir.nodes) {
        if (n.phase === 'build') continue;
        if (n.phase === 'signal') { outputs[n.id] = s[n.id]; continue; }
        const def = REG.get(n.t);
        const inputs = n.in.map(id => {
          const v = outputs[id];
          const src = ir.node(id);
          return src.t === 'signal' && (portsOf(n).in[0] || '').startsWith('queries') ? this.queries(v) : v;
        });
        const input = (portsOf(n).in[0] || '') === 'hits*' ? inputs : inputs[0];
        const nd = hash([n.t, n.p, n.trigger]);
        const wants = trigger === 'submit' || triggerOf(n) === 'change';
        const m = this.memo.get(n.id);
        const sameInputs = m && m.inputs.length === inputs.length && m.inputs.every((v, i) => v === inputs[i]);
        const sameReads = m && Object.entries(m.reads).every(([k, v]) => stable(s[k]) === v);
        const entry = { node: n.id, t: n.t, ms: 0, in: countOf(input), out: 0, status: 'ran' };
        if (m && m.digest === nd && sameInputs && sameReads && (m.full || !wants)) {
          outputs[n.id] = m.out;
          entry.status = m.full ? 'cached' : 'skipped';
          entry.out = countOf(m.out);
          if (m.notes) entry.notes = m.notes;
          trace.push(entry);
          continue;
        }
        if (!wants) {
          outputs[n.id] = def.skip === 'null' ? null : input;
          entry.status = 'skipped';
          entry.out = countOf(outputs[n.id]);
          trace.push(entry);
          this.memo.set(n.id, { digest: nd, inputs, reads: {}, out: outputs[n.id], full: false });
          continue;
        }
        const reads = {};
        const sp = new Proxy(s, { get: (t, k) => { if (typeof k === 'string') reads[k] = stable(t[k]); return t[k]; } });
        const notes = [];
        const c0 = counter.calls;
        const ctx = this.ctx(n, ir, art, sample, s, notes, opts);
        const t1 = now();
        try {
          const p = await this.params(n, sp, ctx, art, ir);
          let out = await def.run(sp, ctx, input, p);
          if (n.out === 'hits') out = normHits(out, n.id);
          outputs[n.id] = out;
          entry.out = countOf(out);
          if (n.out === 'hits') entry.top = out.slice(0, 50).map(h => ({ id: h.id, score: h.score, why: h.why, parts: h.parts, reason: h.reason }));
          this.memo.set(n.id, { digest: nd, inputs, reads, out, full: true, notes });
        } catch (e) {
          const msg = e && (e.message || e.code) ? `${e.code && !e.message ? e.code : e.message}` : String(e);
          entry.status = 'error';
          entry.error = msg;
          errors.push(`${n.id}: ${msg}`);
          outputs[n.id] = def.skip === 'null' || n.out === 'answer' ? null : n.out === 'hits' || n.out === 'queries' ? input || [] : null;
          if (e && e.code === 'cancelled') { trace.push(entry); break; }
        }
        entry.ms = +(now() - t1).toFixed(1);
        entry.calls = counter.calls - c0;
        if (notes.length) entry.notes = notes;
        trace.push(entry);
        if (opts.onNode) opts.onNode(entry);
      }
      const o = ir.outputs;
      const result = {
        runId, trigger, signals: s, hits: o.hits ? outputs[o.hits] || [] : [], context: o.context ? outputs[o.context] : null,
        answer: o.answer ? outputs[o.answer] : null, outputs, trace, errors, ms: +(now() - t0).toFixed(1), calls: counter.calls,
      };
      decorateResult(result, art);
      this.last = result;
      return result;
    }
    async params(n, sp, ctx, art, ir) {
      const p = await this.engine.resolveBakes(n, n.p, art.byId, ir);
      const keep = new Set(REG.get(n.t).lambdas || []);
      let out = p;
      for (const [k, v] of Object.entries(p)) {
        if (isLambda(v) && !keep.has(k)) { if (out === p) out = { ...p }; out[k] = await fnOf(v, `${n.id}.${k}`)(sp, ctx); }
      }
      return out;
    }
    ctx(n, ir, art, sample, s, notes, opts) {
      const chunkList = [...art.chunks.values()];
      return {
        node: n, sample, signal: opts.signal, signals: Object.freeze({ ...s }), synonyms: art.synonyms, tokens, clip,
        index: name => art.indexes.get(String(name)) || null,
        indexNames: () => [...art.indexes.keys()],
        firstIndex: kind => [...art.indexes.values()].find(x => x.kind === kind) || null,
        chunk: id => art.chunks.get(id) || null,
        chunks: f => (f ? chunkList.filter(f) : chunkList),
        doc: id => art.docs.get(id) || null,
        docs: () => [...art.docs.values()],
        links: docId => art.links.get(docId) || [],
        neighbors: (id, o = {}) => {
          const c = art.chunks.get(id);
          if (!c) return [];
          const list = art.order.get(c.docId) || [], i = list.indexOf(id);
          if (i < 0) return [];
          return [...list.slice(Math.max(0, i - (o.before ?? 1)), i), ...list.slice(i + 1, i + 1 + (o.after ?? 1))].map(x => art.chunks.get(x));
        },
        log: (...a) => this.engine.env.log && this.engine.env.log(`[${n.id}]`, ...a),
        note: t => notes.push(String(t)),
        emit: text => opts.onText && opts.onText(n.id, text),
      };
    }
  }
  function decorateResult(result, art) {
    const def = (k, v) => Object.defineProperty(result, k, { value: v, enumerable: false });
    def('chunk', id => art.chunks.get(id) || null);
    def('top', (n = 5, from) => {
      const list = from ? result.outputs[from] || [] : result.hits;
      return arr(list).slice(0, n).map(h => { const c = art.chunks.get(h.id) || {}; return { id: h.id, score: h.score, doc: c.title, heading: (c.headingPath || []).join(' › '), ...(h.reason ? { reason: h.reason } : {}) }; });
    });
    def('explain', id => explain(result, typeof id === 'object' ? id.id : id));
    def('toJSON', () => ({
      trigger: result.trigger, q: result.signals.q, ms: result.ms, calls: result.calls,
      top: result.top(8), answer: result.answer ? clip(result.answer.text, 1200) : null, errors: result.errors,
      trace: result.trace.map(t => `${t.node} ${t.status} ${t.ms}ms ${t.in}→${t.out}${t.error ? ' ✗ ' + t.error : ''}`),
    }));
  }
  function explain(result, id) {
    const out = { chunk: id, path: [] };
    for (const t of result.trace) {
      if (!t.top) continue;
      const i = t.top.findIndex(h => h.id === id);
      if (i < 0) { if (t.in) out.path.push({ node: t.node, rank: null }); continue; }
      const h = t.top[i];
      out.path.push({ node: t.node, rank: i + 1, score: h.score, ...(h.why ? { terms: h.why } : {}), ...(h.parts ? { parts: h.parts } : {}), ...(h.reason ? { reason: h.reason } : {}) });
    }
    const c = result.chunk(id);
    if (c) out.where = `${c.title} › ${c.headingPath.join(' › ')}`;
    return out;
  }

  // ────────────────────────────────────────────────────────────── evals
  function testOne(pat, strings) {
    if (pat == null) return true;
    if (pat instanceof RegExp) return strings.some(s => pat.test(s));
    if (isObj(pat) && pat.$re) return testOne(new RegExp(pat.$re, pat.flags || 'i'), strings);
    if (Array.isArray(pat)) return pat.some(p => testOne(p, strings));
    const l = String(pat).toLowerCase();
    return strings.some(s => String(s).toLowerCase().includes(l));
  }
  function relevant(expect, chunk, doc) {
    if (!chunk) return false;
    const d = doc || {};
    const docOk = testOne(expect.doc, [chunk.title, d.name || '', chunk.docId, ...arr((d.meta || {}).aliases)]);
    const secOk = expect.any || testOne(expect.section, [chunk.headingPath.join(' › ')]);
    return docOk && secOk;
  }
  const showPat = p => (p instanceof RegExp ? '/' + p.source + '/' + p.flags : isObj(p) && p.$re ? '/' + p.$re + '/' : p);
  function resolveTarget(target, engine) {
    if (target instanceof Workspace) return { ir: target.ir, engine: target.engine, name: target.name };
    if (target && target.ir && target.engine) return { ir: compile(target.ir), engine: target.engine, name: target.name || target.ir.id };
    const ir = compile(target);
    return { ir, engine: engine || defaultEngine(), name: ir.id };
  }
  function metricK(m) { const r = /@(\d+)$/.exec(m); return r ? +r[1] : null; }

  class Evals {
    constructor(st) { this.st = st; Object.freeze(this); }
    get id() { return this.st.id; }
    get cases() { return this.st.cases; }
    _with(p) { return new Evals({ ...this.st, ...p }); }
    as(id) { return this._with({ id }); }
    case(q, expect = {}, opts = {}) { return this._with({ cases: [...this.st.cases, { q, expect, ...opts }] }); }
    concat(other) { return this._with({ cases: [...this.st.cases, ...other.cases] }); }
    metrics(ms) { return this._with({ metrics: arr(ms) }); }
    judge(name, fn) { return this._with({ judges: { ...this.st.judges, [name]: lam(fn) } }); }
    filter(f) { return this._with({ cases: this.st.cases.filter(f) }); }
    toJSON() { return { id: this.st.id, cases: this.st.cases.map(c => ({ ...c, expect: Object.fromEntries(Object.entries(c.expect).map(([k, v]) => [k, showPat(v)])) })), metrics: this.st.metrics }; }
    async run(target, opts = {}) {
      const { ir, engine, name } = resolveTarget(target, opts.engine);
      const metrics = this.st.metrics;
      const k = Math.max(10, ...metrics.map(metricK).filter(Boolean));
      const trigger = opts.trigger || 'change';
      const session = engine.session(ir);
      const t0 = now();
      let art = engine.artifacts(ir);
      if (!art) { const r = await engine.build(ir); if (!r.ok) throw new RagError('build', r.errors.join('\n')); art = engine.artifacts(ir); }
      const cases = [];
      let calls = 0;
      for (const [i, c] of this.st.cases.entries()) {
        if (opts.signal && opts.signal.aborted) throw new RagError('run', 'evals stopped');
        const res = await session.run({ ...(opts.signals || {}), ...(c.signals || {}), q: c.q, k: opts.k ?? Math.max(k, ir.signals.k || 0) }, { trigger, signal: opts.signal });
        calls += res.calls;
        const hits = res.hits && res.hits.length ? res.hits : ((res.answer && res.answer.evidence) || []).map(e => ({ id: e.id }));
        const rel = hits.map(h => relevant(c.expect, art.chunks.get(h.id), art.docs.get((art.chunks.get(h.id) || {}).docId)));
        const rank = rel.indexOf(true) + 1 || null;
        const R = [...art.chunks.values()].filter(ch => relevant(c.expect, ch, art.docs.get(ch.docId))).length;
        const row = {
          i: i + 1, q: c.q, rank, synthetic: !!c.synthetic, R,
          expect: Object.fromEntries(Object.entries(c.expect).map(([kk, v]) => [kk, showPat(v)])),
          top: res.top(3), ms: res.ms, m: {},
        };
        for (const m of metrics) {
          const mk = metricK(m), name2 = m.replace(/@\d+$/, '');
          if (name2 === 'hit') row.m[m] = rank && rank <= mk ? 1 : 0;
          else if (name2 === 'mrr') row.m[m] = rank ? 1 / rank : 0;
          else if (name2 === 'recall') row.m[m] = R ? rel.slice(0, mk).filter(Boolean).length / R : 0;
          else if (name2 === 'ndcg') {
            const dcg = rel.slice(0, mk).reduce((a, r, j) => a + (r ? 1 / Math.log2(j + 2) : 0), 0);
            const idcg = Array.from({ length: Math.min(R, mk) }, (_, j) => 1 / Math.log2(j + 2)).reduce((a, b) => a + b, 0);
            row.m[m] = idcg ? dcg / idcg : 0;
          }
        }
        if (res.answer) {
          row.answer = clip(res.answer.text, 600);
          if (c.expect.answerHas) row.m.answerHas = testOne(c.expect.answerHas, [res.answer.text]) ? 1 : 0;
          if (opts.judge) for (const [jn, jf] of Object.entries(this.st.judges || {})) {
            try { row.m[jn] = Number(await fnOf(jf, `judge '${jn}'`)(res.signals, { sample: engine.sampler({ calls: 0 }, opts.signal) }, { q: c.q, answer: res.answer.text, evidence: (res.answer.evidence || []).map(e => (art.chunks.get(e.id) || {}).text || '') })); calls++; } catch (e) { row.m[jn] = null; row.judgeError = e.message; }
          }
        }
        if (res.errors.length) row.errors = res.errors;
        cases.push(row);
        if (opts.onCase) opts.onCase(row, i, this.st.cases.length);
      }
      const keys = uniq(cases.flatMap(c => Object.keys(c.m)));
      const summary = { n: cases.length };
      for (const key of keys) { const vs = cases.map(c => c.m[key]).filter(v => v != null); summary[key] = vs.length ? +(vs.reduce((a, b) => a + b, 0) / vs.length).toFixed(3) : null; }
      const msList = cases.map(c => c.ms).sort((a, b) => a - b);
      summary.p50ms = msList.length ? +msList[Math.floor(msList.length / 2)].toFixed(1) : 0;
      summary.calls = calls;
      const first = metrics[0];
      const report = { id: this.st.id, target: name, irDigest: ir.digest, trigger, at: Date.now(), ms: +(now() - t0).toFixed(1), summary, cases, misses: cases.filter(c => !c.m[first]).map(c => ({ q: c.q, rank: c.rank, got: c.top[0] ? `${c.top[0].doc} › ${c.top[0].heading}` : null, want: c.expect })) };
      Object.defineProperty(report, 'table', { value: () => cases.map(c => ({ '#': c.i, question: clip(c.q, 70), rank: c.rank ?? '—', ...Object.fromEntries(Object.entries(c.m).map(([kk, v]) => [kk, v == null ? '—' : +(+v).toFixed(2)])) })), enumerable: false });
      return report;
    }
    async compare(targets, opts = {}) {
      const reports = [];
      for (const t of arr(targets)) reports.push(await this.run(t, opts));
      const metrics = Object.keys(reports[0].summary).filter(k => k !== 'n');
      const rows = reports.map(r => ({ variant: r.target, ...Object.fromEntries(metrics.map(m => [m, r.summary[m]])) }));
      const delta = reports.length > 1 ? Object.fromEntries(metrics.filter(m => typeof reports[0].summary[m] === 'number').map(m => [m, +(reports[reports.length - 1].summary[m] - reports[0].summary[m]).toFixed(3)])) : {};
      const cases = reports[0].cases.map((c, i) => ({ q: clip(c.q, 60), ...Object.fromEntries(reports.map(r => [r.target, r.cases[i].rank ?? '—'])) }));
      return { rows, delta, cases, reports };
    }
  }
  function evals(id) { return new Evals({ id: id || 'evals', cases: [], metrics: ['hit@5', 'mrr', 'ndcg@10'], judges: {} }); }
  evals.generate = async function (target, opts = {}) {
    const { ir, engine } = resolveTarget(target, opts.engine);
    let art = engine.artifacts(ir);
    if (!art) { await engine.build(ir); art = engine.artifacts(ir); }
    const n = opts.n ?? 12;
    let seed = opts.seed ?? 7;
    const rand = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
    const pool = [...art.chunks.values()].filter(c => c.kind === 'section' && c.tokens > 120 && c.headingPath.length);
    const picked = [];
    while (picked.length < Math.min(n, pool.length)) { const c = pool[Math.floor(rand() * pool.length)]; if (!picked.includes(c)) picked.push(c); }
    const sample = engine.sampler({ calls: 0 }, opts.signal);
    let out = evals(opts.id || 'synthetic');
    for (let i = 0; i < picked.length; i += 8) {
      const batch = picked.slice(i, i + 8);
      const r = await sample.json(
        'For each numbered section of an engineering report, write one question a colleague might ask that this section answers well. Phrase it naturally, without copying the heading. Reply with only JSON: {"questions": [{"n": 1, "q": "…"}]}.\n\n' +
        batch.map((c, j) => `(${j + 1}) ${c.title} › ${c.headingPath.join(' › ')}\n${clip(c.text, 1500)}`).join('\n\n'),
        { modelTier: opts.tier || 'quick' });
      for (const x of arr(r && r.questions)) {
        const c = batch[(x.n | 0) - 1];
        if (c && x.q) out = out.case(String(x.q), { doc: c.title, section: c.headingPath.join(' › ') }, { synthetic: true });
      }
    }
    return out;
  };

  // ────────────────────────────────────────────────────────────── sweep
  function cartesian(grid) {
    const keys = Object.keys(grid);
    return keys.reduce((acc, k) => acc.flatMap(c => arr(grid[k]).map(v => ({ ...c, [k]: v }))), [{}]);
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
          const ops = Object.entries(cfg).map(([key, value]) => { const dot = key.indexOf('.'); return { op: 'set', node: key.slice(0, dot), path: key.slice(dot + 1), value }; });
          const ir = applyOps(base.ir, ops);
          const rep = await base.engine.build(ir);
          const computed = Object.values(rep.nodes).filter(x => !x.reused).map(x => x.id);
          const r = await ev.run({ ir, engine: base.engine, name: 'cfg' + (i + 1) }, opts);
          rows.push({ config: cfg, ...r.summary, rebuilt: computed });
          if (opts.onConfig) opts.onConfig(rows[rows.length - 1], i, configs.length);
        }
        rows.sort((a, b) => (b[metric] ?? 0) - (a[metric] ?? 0));
        return { metric, rows, best: rows[0], top: (n = 3) => rows.slice(0, n) };
      },
    };
  }

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
    get ir() { return this._ir; }
    on(fn) { this.listeners.add(fn); return () => this.listeners.delete(fn); }
    _emit(e) { for (const f of this.listeners) try { f(e); } catch (_) { /* keep emitting */ } }
    _check(op) { if (this.readonly) throw new RagError('readonly', `ws.${op} is blocked in Read-only mode. Describe the change instead, or ask the user to switch to Auto or Ask.`); }
    classify(before, after) {
      const db = this.engine.digests(before), da = this.engine.digests(after);
      const dirty = [...da].filter(([id, d]) => !this.engine.memo.has(d)).map(([id]) => id);
      const changedBuild = [...da].filter(([id, d]) => db.get(id) !== d).map(([id]) => id);
      const nb = new Map(before.nodes.map(n => [n.id, stable([n.t, n.in, n.p, n.trigger])]));
      const liveChanged = after.nodes.filter(n => n.phase === 'live' && nb.get(n.id) !== stable([n.t, n.in, n.p, n.trigger])).map(n => n.id);
      const removed = before.nodes.filter(n => !after.node(n.id)).map(n => n.id);
      const sig = Object.keys({ ...before.signals, ...after.signals }).filter(k => stable(before.signals[k]) !== stable(after.signals[k]));
      return { live: [...liveChanged, ...sig], dirty, changed: changedBuild, removed };
    }
    dryRun(ops) {
      try {
        const next = applyOps(this._ir, ops);
        const c = this.classify(this._ir, next);
        const llm = c.dirty.filter(id => costsCalls(next.node(id))).length;
        const chunks = this.engine.artifacts(this._ir) ? this.engine.artifacts(this._ir).chunks.size : null;
        return { ok: true, ...c, estimate: { llmCalls: llm && chunks ? llm * chunks : 0 } };
      } catch (e) { return { ok: false, error: e.message }; }
    }
    async apply(ops, opts = {}) {
      this._check('apply');
      if (opts.expect != null && opts.expect !== this.revision) throw new RagError('conflict', `workspace '${this.name}' is at revision ${this.revision}; these ops were planned against ${opts.expect}. Re-read ws.ir and plan again.`);
      ops = arr(ops).map(o => (o && o.value && typeof o.value === 'function' ? { ...o, value: lam(o.value) } : o && typeof o.fn === 'function' ? { ...o, fn: lam(o.fn).$fn } : o));
      const before = this._ir;
      const next = applyOps(before, ops);
      const c = this.classify(before, next);
      this.history.push({ revision: this.revision + 1, ops: clone(ops), by: opts.by || 'user', note: opts.note, at: Date.now(), before: clone(before) });
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
      return { ok: rep.ok, ms: rep.ms, computed: Object.values(rep.nodes).filter(x => !x.reused).map(x => x.id), reused: Object.values(rep.nodes).filter(x => x.reused).length, calls: rep.calls, errors: rep.errors };
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
      this.history.push({ revision: this.revision + 1, ops: [{ op: 'reset', note }], by: 'user', note, at: Date.now(), before: clone(before) });
      this._ir = compile(ir);
      this.revision++;
      this._emit({ type: 'reset', revision: this.revision });
      return { revision: this.revision };
    }
    branch(name) {
      const b = new Workspace(this._ir, { engine: this.engine, name, revision: this.revision, parent: this });
      b.baseRevision = this.revision;
      this.branches[name] = b;
      return b;
    }
    async merge(b) {
      this._check('merge');
      if (typeof b === 'string') b = this.branches[b];
      if (!b) throw new RagError('op', 'no such branch');
      if (b.baseRevision !== this.revision) throw new RagError('conflict', `'${this.name}' moved from revision ${b.baseRevision} to ${this.revision} since branch '${b.name}' was made. Re-apply the branch ops on a fresh branch.`);
      const ops = b.history.flatMap(h => h.ops);
      const receipt = await this.apply(ops, { by: 'merge', note: `merge ${b.name}` });
      delete this.branches[b.name];
      return { ...receipt, merged: b.name, ops: ops.length };
    }
    run(signals, opts) { return this.engine.session(() => this._ir).run(signals, opts); }
    session() { return this.engine.session(() => this._ir); }
    snapshot() { return { ir: clone(this._ir), revision: this.revision, history: this.history.length, future: [...this.future] }; }
    restore(snap) {
      this._ir = withMethods(clone(snap.ir));
      this.history = this.history.slice(0, snap.history);
      this.future = snap.future;
      this.revision++;
      this._emit({ type: 'restore', revision: this.revision });
    }
    diff(other) { return diffIr(this._ir, other instanceof Workspace ? other.ir : compile(other)); }
    toJSON() { return { name: this.name, revision: this.revision, nodes: this._ir.summary(), dirty: this.engine.dirty(this._ir), branches: Object.keys(this.branches), history: this.history.map(h => ({ revision: h.revision, by: h.by, ops: h.ops.map(o => o.op + (o.node ? ' ' + o.node : '') + (o.path ? '.' + o.path : '')).join(', ') })) }; }
  }
  function diffIr(a, b) {
    const out = [];
    const am = new Map(a.nodes.map(n => [n.id, n])), bm = new Map(b.nodes.map(n => [n.id, n]));
    for (const [id, n] of bm) {
      const o = am.get(id);
      if (!o) { out.push({ change: 'added', node: id, t: n.t }); continue; }
      if (o.t !== n.t) out.push({ change: 'type', node: id, from: o.t, to: n.t });
      if (stable(o.in) !== stable(n.in)) out.push({ change: 'wiring', node: id, from: o.in, to: n.in });
      const keys = uniq([...Object.keys(o.p), ...Object.keys(n.p)]);
      for (const k of keys) if (stable(o.p[k]) !== stable(n.p[k])) out.push({ change: 'param', node: id, path: k, from: o.p[k], to: n.p[k] });
      if ((o.trigger || '') !== (n.trigger || '')) out.push({ change: 'trigger', node: id, from: o.trigger, to: n.trigger });
    }
    for (const id of am.keys()) if (!bm.has(id)) out.push({ change: 'removed', node: id });
    return out;
  }

  // ────────────────────────────────────────────────────────────── define, export
  function define(type, def) {
    if (!/^[a-z][\w]*(\.[a-z][\w]*)?$/i.test(type)) throw new RagError('compile', `component type '${type}' should look like 'family' or 'family.variant'`);
    if (!def || !['build', 'live'].includes(def.phase)) throw new RagError('compile', "define needs phase: 'build' or 'live'");
    const d = { ...def, type };
    d.in = arr(def.in || (def.phase === 'live' ? 'hits' : 'docs'));
    d.out = def.out || (def.phase === 'live' ? 'hits' : 'chunks');
    d.args = def.args || optArgs;
    d.id = def.id || (() => type.split('.').pop());
    if (def.phase === 'live') {
      const run = def.run;
      if (typeof run !== 'function') throw new RagError('compile', 'a live component needs run(s, ctx, input, p)');
      d.run = (s, ctx, input, p) => run(s, ctx, input, p);
    } else {
      if (def.map) { const map = def.map; d.map = (item, ctx, p) => map(item, ctx, p); if (d.out === 'chunks') d.post = assignIds; d.async = true; }
      else if (def.all) { const all = def.all; d.all = (inputs, ctx, p) => all(inputs, ctx, p); }
      else throw new RagError('compile', 'a build component needs map(item, ctx, p) or all(inputs, ctx, p)');
      if (d.out === 'chunks' && def.map) {
        const map = def.map;
        d.map = async (doc, ctx, p) => arr(await map(doc, ctx, p)).filter(Boolean).map(c => makeChunk(doc, arr(c.headingPath || []), arr(c.blocks), { kind: type.split('.').pop(), ...c }));
      }
    }
    REG.set(type, d);
    refreshFactories();
    installFamilies();
    return type;
  }

  async function exportHtml(target, opts = {}) {
    const { ir, engine } = resolveTarget(target);
    const lexical = applyOps(ir, ir.nodes.filter(n => n.phase === 'live' && triggerOf(n) === 'submit' && n.out !== 'answer').map(n => ({ op: 'remove', node: n.id })));
    const pruned = applyOps(lexical, lexical.nodes.filter(n => n.out === 'answer' || n.out === 'context').reverse().map(n => ({ op: 'remove', node: n.id })));
    const src = engine.env.librarySource ? await engine.env.librarySource() : '';
    const files = engine.files().map(f => ({ name: f.name, text: f.text }));
    const title = opts.title || ir.id;
    const esc = s => String(s).replace(/[&<>]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[c]);
    const json = s => JSON.stringify(s).replace(/</g, '\\u003c');
    return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${esc(title)}</title>
<style>:root{--paper:#FAFAF8;--ink:#111417;--muted:#5D646A;--hair:#E3E6E8;--sig:#1F4FD1}@media(prefers-color-scheme:dark){:root{--paper:#121518;--ink:#E3E6E9;--muted:#98A0A7;--hair:#2A3035;--sig:#86A2F7}}
body{margin:0;background:var(--paper);color:var(--ink);font:14px/1.5 Arial,Helvetica,sans-serif}main{max-width:760px;margin:0 auto;padding:24px max(16px,env(safe-area-inset-left))}
h1{font-size:20px;margin:0 0 12px}input{width:100%;box-sizing:border-box;font:15px Arial,sans-serif;padding:10px;border:1px solid var(--ink);background:transparent;color:var(--ink)}
.r{border-bottom:1px solid var(--hair);padding:10px 0}.r b{display:block}.r small{color:var(--muted);font:11px ui-monospace,monospace}.r p{margin:4px 0 0;color:var(--muted)}mark{background:none;color:var(--sig);font-weight:700}</style></head>
<body><main><h1>${esc(title)}</h1><input id="q" placeholder="Search…" autofocus><div id="out"></div></main>
<script>${src.replace(/<\/script/gi, '<\\/script')}<\/script>
<script>
const files=${json(files)}, ir=rag.ir(${json(pruned)});
rag.configure({ files: () => files });
const ws = rag.workspace(ir), out = document.getElementById('out'), q = document.getElementById('q');
const esc = s => String(s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'})[c]);
let t; q.addEventListener('input', () => { clearTimeout(t); t = setTimeout(go, 150); });
async function go(){ const r = await ws.run({ q: q.value }, { trigger: 'change' });
  const words = q.value.toLowerCase().split(/\\W+/).filter(w => w.length > 2);
  out.innerHTML = r.hits.slice(0, 20).map(h => { const c = r.chunk(h.id); let s = esc(c.text.replace(/\\s+/g,' ').slice(0, 300)); for (const w of words) s = s.replace(new RegExp('(' + w.replace(/[^a-z0-9]/g,'') + ')', 'gi'), '<mark>$1</mark>');
    return '<div class="r"><b>' + esc(c.headingPath.join(' › ') || c.title) + '</b><small>' + esc(c.title) + '</small><p>' + s + '…</p></div>'; }).join(''); }
q.value = ${json(ir.signals.q || '')}; go();
<\/script></body></html>`;
  }

  // ────────────────────────────────────────────────────────────── namespace
  let engine0 = null;
  let env0 = {};
  function defaultEngine() { if (!engine0) engine0 = new Engine(env0); return engine0; }

  const rag = {
    version: '0.1.0',
    // builders
    corpus: id => new CorpusSpec({ id: id || 'corpus', nodes: [], cursor: {} }),
    plan: id => makePlan({ id: id || 'plan', corpus: null, signals: {}, nodes: [], cursor: {}, outputs: {} }),
    graph, evals, sweep,
    fragment: (id, fn) => new Fragment(id, fn),
    bake: rag_bake,
    live: f => lam(f),
    c: (type, p = {}) => ({ $c: type, p }),
    // compile and run
    compile, applyOps,
    ir: json => withMethods(clone(json)),
    build: (target, opts) => defaultEngine().build(compile(target), opts),
    run: (target, signals, opts) => (target instanceof Workspace ? target.run(signals, opts) : defaultEngine().run(compile(target), signals, opts)),
    search: async (target, q, opts = {}) => {
      const r = await rag.run(target, { ...(opts.signals || {}), q, ...(opts.k ? { k: opts.k } : {}), ...(opts.where ? { filters: opts.where } : {}) }, { trigger: opts.trigger || 'change' });
      return r.top(opts.k || 10);
    },
    explain,
    workspace: (target, opts) => new Workspace(target, opts),
    engine: () => defaultEngine(),
    configure(env) { env0 = Object.assign(env0, env); if (engine0) engine0.env = env0; return env0; },
    define,
    components: () => [...REG.values()].map(d => ({ type: d.type, phase: d.phase, in: d.in, out: d.out, trigger: d.trigger || 'change' })),
    export: exportHtml,
    // pieces, for custom components and tests
    util: { hash, stable, tokenize, stem, parseMarkdown, parseFrontmatter, chunkSections, Bm25Index, TermsIndex, slug, clip, signalReads, fnOf, lam, diffIr, triggerOf, describeNode, costsCalls },
    Engine, Workspace, Session, Evals, RagError,
  };

  function refreshFactories() {
    const fams = new Map();
    for (const d of REG.values()) {
      const [fam, variant] = d.type.split('.');
      if (!fams.has(fam)) fams.set(fam, {});
      if (variant) fams.get(fam)[variant] = d.type;
      else fams.get(fam)[''] = d.type;
    }
    for (const [fam, vs] of fams) {
      if (['source', 'parse', 'index', 'bake'].includes(fam) && !vs['']) { /* namespaces only */ }
      const make = type => (...args) => { const c = normalize(type, args); return { $c: c.t, p: { ...c.p, ...(c.opts.id ? { id: c.opts.id } : {}), ...(c.opts.trigger ? { trigger: c.opts.trigger } : {}) } }; };
      const f = vs[''] ? make(vs['']) : {};
      for (const [v, type] of Object.entries(vs)) if (v) f[v] = make(type);
      if (fam === 'bake') continue;
      rag[fam] = f;
    }
    rag.files = rag.source.files;
    rag.markdown = rag.parse.markdown;
    rag.sections = rag.chunk.sections;
    rag.bm25 = rag.index.bm25;
    rag.terms = rag.index.terms;
    rag.bake = rag_bake;
  }
  refreshFactories();
  installFamilies();
  return rag;
});
