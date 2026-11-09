import { docIdFor, slug } from '../core/identity.ts';
import { uniq } from '../core/util.js';

// ────────────────────────────────────────────────────────────── markdown
function parseScalar(v) {
  v = v.trim();
  if (v === '') return '';
  if (/^\[.*\]$/.test(v))
    return v
      .slice(1, -1)
      .split(',')
      .map((x) => parseScalar(x))
      .filter((x) => x !== '');
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
    if (item && key) {
      if (!Array.isArray(meta[key])) meta[key] = [];
      meta[key].push(parseScalar(item[1]));
      continue;
    }
    const kv = /^([A-Za-z0-9_-]+):\s*(.*)$/.exec(line);
    if (kv) {
      key = kv[1];
      meta[key] = parseScalar(kv[2]);
      if (meta[key] === '') meta[key] = [];
    }
  }
  for (const k of Object.keys(meta)) if (Array.isArray(meta[k]) && !meta[k].length) delete meta[k];
  return { meta, body: text.slice(text.indexOf('\n', end + 1) + 1) };
}

const plain = (s) =>
  String(s)
    .replace(/`([^`]*)`/g, '$1')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\[\[([^\]|]+)(\|[^\]]*)?\]\]/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/#+\s*$/, '')
    .trim();

function parseMarkdown(file, p = {}) {
  const { meta, body } =
    p.frontmatter === false ? { meta: {}, body: file.text } : parseFrontmatter(file.text);
  const lines = body.split(/\r?\n/);
  const sections = [];
  let sec = { level: 0, title: '', path: [], blocks: [] };
  sections.push(sec);
  const stack = [];
  let para = [];
  let h1 = null;
  const flush = () => {
    if (para.length) {
      sec.blocks.push({ kind: 'paragraph', text: para.join('\n') });
      para = [];
    }
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const fence = /^\s*(`{3,}|~{3,})\s*([\w+-]*)/.exec(line);
    if (fence) {
      flush();
      const marker = fence[1],
        body2 = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith(marker)) body2.push(lines[i++]);
      sec.blocks.push({ kind: 'fence', lang: fence[2] || '', text: body2.join('\n') });
      continue;
    }
    const h = /^(#{1,6})\s+(.*?)\s*$/.exec(line);
    if (h) {
      flush();
      const level = h[1].length,
        title = plain(h[2]);
      if (level === 1 && h1 === null) {
        h1 = title;
        continue;
      }
      while (stack.length && stack[stack.length - 1].level >= level) stack.pop();
      sec = { level, title, path: [...stack.map((x) => x.title), title], blocks: [] };
      stack.push(sec);
      sections.push(sec);
      continue;
    }
    if (/^\s*\|/.test(line)) {
      flush();
      const rows = [];
      while (i < lines.length && /^\s*\|/.test(lines[i])) rows.push(lines[i++]);
      i--;
      const cells = (r) =>
        r
          .trim()
          .replace(/^\||\|$/g, '')
          .split('|')
          .map((c) => plain(c.trim()));
      const data = rows.filter((r) => !/^\s*\|[\s:|-]+\|\s*$/.test(r)).map(cells);
      sec.blocks.push({
        kind: 'table',
        header: data[0] || [],
        rows: data.slice(1),
        text: rows.join('\n'),
      });
      continue;
    }
    if (/^\s*>/.test(line)) {
      flush();
      const q = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) q.push(lines[i++].replace(/^\s*>\s?/, ''));
      i--;
      const c = /^\[!(\w+)\]\s*(.*)$/.exec(q[0] || '');
      sec.blocks.push(
        c
          ? {
              kind: 'callout',
              variant: c[1].toLowerCase(),
              text: [c[2], ...q.slice(1)].filter(Boolean).join('\n'),
            }
          : { kind: 'quote', text: q.join('\n') },
      );
      continue;
    }
    if (/^\s*([-*+]|\d+\.)\s+/.test(line)) {
      flush();
      const items = [];
      while (
        i < lines.length &&
        (/^\s*([-*+]|\d+\.)\s+/.test(lines[i]) || (/^\s{2,}\S/.test(lines[i]) && items.length))
      )
        items.push(lines[i++]);
      i--;
      sec.blocks.push({ kind: 'list', text: items.join('\n') });
      continue;
    }
    if (!line.trim()) {
      flush();
      continue;
    }
    para.push(line);
  }
  flush();
  const name = file.name;
  const id = docIdFor(name);
  const title = String(meta.title || h1 || name.replace(/\.md$/i, '')).replace(/^"|"$/g, '');
  const links = [];
  const linkRe = /\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|[^\]]*)?\]\]/g;
  let m;
  while ((m = linkRe.exec(file.text))) links.push(m[1].trim());
  for (const s of sections) {
    s.id = slug(s.path.length ? s.path[s.path.length - 1] : 'intro');
    s.text = s.blocks.map((b) => b.text).join('\n\n');
  }
  return {
    id,
    name,
    title,
    meta,
    h1,
    sections: sections.filter((s) => s.text.trim() || s.level > 0),
    links: uniq(links),
  };
}

const META_SKIP = new Set([
  'title',
  'created',
  'updated',
  'source_ticket_path',
  'source_design_doc',
  'related_vault_notes',
  'derived-from',
]);

function pickMeta(meta) {
  const out = {};
  for (const [k, v] of Object.entries(meta || {})) {
    if (META_SKIP.has(k)) continue;
    if (typeof v === 'string' && v.length > 120) continue;
    if (Array.isArray(v) && v.some((x) => typeof x === 'string' && x.length > 120)) continue;
    out[k] = v;
  }
  return out;
}

export { parseFrontmatter, parseMarkdown, pickMeta };
