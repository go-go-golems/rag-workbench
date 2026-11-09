import { RagError } from './errors.ts';
import { isBake, isLambda } from './util.js';

// ────────────────────────────────────────────────────────────── lambdas
// Lambdas are stored as source text ({$fn}) and compiled without their
// closure. A guard turns any free identifier into an error that says so.
const ALLOWED = new Set(
  (
    'Math JSON Array Object String Number Boolean Date RegExp Map Set WeakMap WeakSet Promise Symbol Error ' +
    'TypeError RangeError Intl parseInt parseFloat isNaN isFinite Infinity NaN undefined encodeURIComponent decodeURIComponent ' +
    'console structuredClone BigInt Uint8Array TextEncoder TextDecoder'
  ).split(' '),
);

const compiled = new Map();

function lam(f) {
  if (isLambda(f)) return f;
  if (isBake(f)) return f;
  if (typeof f === 'string') return { $fn: f.trim() };
  if (typeof f !== 'function') throw new RagError('lambda', 'expected a function, got ' + typeof f);
  const src = f.toString();
  if (/\[native code\]/.test(src))
    throw new RagError(
      'lambda',
      'native or bound functions cannot be stored as lambdas; write an arrow function',
    );
  return { $fn: src };
}

function guard(label) {
  return new Proxy(
    {},
    {
      has: (t, k) => typeof k === 'string' && !ALLOWED.has(k),
      get: (t, k) => {
        if (k === Symbol.unscopables) return undefined;
        throw new RagError(
          'lambda',
          `${label} references free identifier \`${String(k)}\`. Lambdas are stored as source and run without their closure: use their arguments, a signal, or rag.bake.`,
        );
      },
    },
  );
}

function compileSource(src, label) {
  const make = (s) => new Function('__g', 'with (__g) { return (' + s + '\n); }')(guard(label));
  try {
    return make(src);
  } catch (e) {
    if (!(e instanceof SyntaxError)) throw e;
    // method shorthand: `name(a) { … }` / `async name(a) { … }`
    const m = /^(async\s+)?([A-Za-z_$][\w$]*)\s*\(/.exec(src);
    if (m && m[2] !== 'function') {
      try {
        return make((m[1] || '') + 'function ' + m[2] + src.slice(m[0].length - 1));
      } catch (_) {
        /* fall through */
      }
    }
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
  try {
    new Function('return (' + src + '\n);');
    return null;
  } catch (e) {
    try {
      fnOf(spec, label);
      return null;
    } catch (e2) {
      return `${label}: ${e2.message}`;
    }
  }
}

// signal names a lambda reads: s.name, s?.name, s['name'], ({ name }) destructuring of s
function signalReads(src) {
  const out = new Set();
  const m = /^\s*(?:async\s*)?(?:function[^(]*)?\(?\s*([A-Za-z_$][\w$]*|\{[^}]*\})/.exec(src);
  const first = m && m[1];
  if (first && first.startsWith('{')) {
    for (const part of first.slice(1, -1).split(',')) {
      const k = part.split(/[:=]/)[0].trim();
      if (/^[A-Za-z_$][\w$]*$/.test(k)) out.add(k);
    }
    return [...out];
  }
  const name = first || 's';
  const re = new RegExp(
    '\\b' +
      name.replace(/\$/g, '\\$') +
      '\\s*(?:\\?\\.|\\.)\\s*([A-Za-z_$][\\w$]*)|\\b' +
      name.replace(/\$/g, '\\$') +
      '\\s*\\[\\s*[\'"]([^\'"]+)[\'"]\\s*\\]',
    'g',
  );
  let r;
  while ((r = re.exec(src))) out.add(r[1] || r[2]);
  return [...out];
}

export { checkLambda, fnOf, lam, signalReads };
