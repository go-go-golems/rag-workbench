import { cyrb53 } from './identity.ts';

// ────────────────────────────────────────────────────────────── utilities
// Letter/number class body. Browsers support \p{L}\p{N}; goja (as of 2025-11) silently
// matches nothing for Unicode property escapes, so fall back to explicit ranges there.
const LN = (() => {
  try {
    if (new RegExp('^[\\p{L}\\p{N}]+$', 'u').test('é9')) return '\\p{L}\\p{N}';
  } catch (_) {
    /* no \p support */
  }
  return 'A-Za-z0-9\\u00AA\\u00B5\\u00BA\\u00C0-\\u00D6\\u00D8-\\u00F6\\u00F8-\\u024F\\u0370-\\u03FF\\u0400-\\u04FF\\u0590-\\u05FF\\u0600-\\u06FF\\u3040-\\u30FF\\u3400-\\u4DBF\\u4E00-\\u9FFF\\uAC00-\\uD7AF';
})();

// Compiled once: String#match and #replace reset lastIndex on global regexes, so sharing is safe.
// (Compiling per call cost ~10x in goja's index.bm25 build: RAGWB-001 diary step 7.)
const RE_WORD_G = new RegExp('[' + LN + '_]+', 'gu');

const RE_NONWORD_G = new RegExp('[^' + LN + ']+', 'gu');

const RE_WORD = () => RE_WORD_G;

const RE_NONWORD = () => RE_NONWORD_G;

const now = () => (typeof performance !== 'undefined' ? performance.now() : Date.now());

function stable(v) {
  if (v === undefined) return 'null';
  if (v === null || typeof v !== 'object') {
    if (typeof v === 'function') return JSON.stringify('$fn:' + v.toString());
    return JSON.stringify(v);
  }
  if (v instanceof RegExp) return JSON.stringify('/' + v.source + '/' + v.flags);
  if (Array.isArray(v)) return '[' + v.map(stable).join(',') + ']';
  return (
    '{' +
    Object.keys(v)
      .sort()
      .filter((k) => v[k] !== undefined)
      .map((k) => JSON.stringify(k) + ':' + stable(v[k]))
      .join(',') +
    '}'
  );
}

const hash = (v) => cyrb53(typeof v === 'string' ? v : stable(v));

const clone = (v) => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));

const isObj = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);

const isLambda = (v) => isObj(v) && typeof v.$fn === 'string';

const isBake = (v) => isObj(v) && typeof v.$bake === 'string';

const arr = (v) => (v == null ? [] : Array.isArray(v) ? v : [v]);

const uniq = (xs) => [...new Set(xs)];

const tokens = (t) =>
  Math.ceil(
    String(
      Array.isArray(t) ? t.map((b) => (b && b.text) || b).join('\n') : (t && t.text) || t || '',
    ).length / 4,
  );

const clip = (s, n) => (s && s.length > n ? s.slice(0, n - 1) + '…' : s || '');

function getPath(obj, path) {
  if (!path) return obj;
  let cur = obj;
  for (const k of String(path).split('.')) {
    if (cur == null) return undefined;
    cur = cur[k];
  }
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

export {
  arr,
  clip,
  clone,
  getPath,
  hash,
  isBake,
  isLambda,
  isObj,
  LN,
  now,
  RE_NONWORD,
  RE_WORD,
  setPath,
  stable,
  tokens,
  uniq,
};
