import { RE_WORD } from './util.js';

// ────────────────────────────────────────────────────────────── text
const STOP = new Set(
  (
    'a an and are as at be but by can could did do does for from had has have how i if in into is it its ' +
    'me more most my no not of on or our out so such than that the their them then there these they this those through to ' +
    'too up us was we were what when where which while who why will with would you your about after again all also any ' +
    'because been before being between both each few further here just only other own same should some very via over under'
  ).split(' '),
);

function stem(w: string): string {
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

function tokenize(text: unknown, opts: { stem?: boolean } = {}): string[] {
  const out = [];
  const raw = String(text || '').match(RE_WORD()) || [];
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

export { stem, tokenize };
