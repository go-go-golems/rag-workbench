function cyrb53(str: string, seed = 0) {
  let h1 = 0xdeadbeef ^ seed,
    h2 = 0x41c6ce57 ^ seed;
  for (let i = 0, ch; i < str.length; i++) {
    ch = str.charCodeAt(i);
    h1 = Math.imul(h1 ^ ch, 2654435761);
    h2 = Math.imul(h2 ^ ch, 1597334677);
  }
  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909);
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909);
  return (
    (h2 >>> 0).toString(16).padStart(8, '0') + (h1 >>> 0).toString(16).padStart(8, '0').slice(0, 6)
  );
}

const slugFull = (s: unknown) =>
  String(s || '')
    .toLowerCase()
    .replace(/[`'"’]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');

const slug = (s: unknown) => slugFull(s).slice(0, 64) || 'x';

// a document's id from its own path only (RAGWB-003 §6.2): the uncapped slug, or past
// 160 chars a prefix + 8 hex of cyrb53(name); 64-char cuts made distinct notes collide
const DOC_ID_MAX = 160;

const docIdFor = (name: string) => {
  const full = slugFull(String(name).replace(/\.md$/i, ''));
  if (!full) return 'x';
  return full.length <= DOC_ID_MAX
    ? full
    : full.slice(0, DOC_ID_MAX - 9).replace(/-+$/, '') + '-' + cyrb53(String(name)).slice(0, 8);
};

export { cyrb53, docIdFor, slug };
