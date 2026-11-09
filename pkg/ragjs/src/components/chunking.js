import { slug } from '../core/identity.ts';
import { clip, tokens } from '../core/util.js';
import { pickMeta } from './markdown.js';

// ────────────────────────────────────────────────────────────── chunking
function splitBlock(b, max) {
  if (tokens(b.text) <= max) return [b];
  const atomic = b.kind === 'fence' || b.kind === 'table';
  if (atomic && tokens(b.text) <= max * 3) return [b];
  const units = atomic || b.kind === 'list' ? b.text.split('\n') : b.text.split(/(?<=[.!?])\s+/);
  const out = [];
  let cur = [];
  for (const u of units) {
    if (cur.length && tokens(cur.join(' ') + u) > max) {
      out.push({ ...b, text: cur.join(atomic || b.kind === 'list' ? '\n' : ' ') });
      cur = [];
    }
    cur.push(u);
  }
  if (cur.length) out.push({ ...b, text: cur.join(atomic || b.kind === 'list' ? '\n' : ' ') });
  return out;
}

function blocksText(blocks) {
  return blocks
    .map((b) =>
      b.kind === 'fence'
        ? '```' + (b.lang || '') + '\n' + b.text + '\n```'
        : b.kind === 'callout'
          ? '[' + b.variant + '] ' + b.text
          : b.text,
    )
    .join('\n\n');
}

function makeChunk(doc, path, blocks, extra) {
  const text = typeof extra.text === 'string' ? extra.text : blocksText(blocks);
  return {
    id: '',
    docId: doc.id,
    title: doc.title,
    headingPath: path,
    kind: 'section',
    meta: pickMeta(doc.meta),
    callout: blocks
      .filter((b) => b.kind === 'callout')
      .map((b) => b.text)
      .join('\n'),
    hasCode: blocks.some((b) => b.kind === 'fence'),
    hasTable: blocks.some((b) => b.kind === 'table'),
    tokens: tokens(text),
    ...extra,
    text,
  };
}

// ids depend only on the chunk's own document (bases start with docId#, doc ids are
// unique per file) and ord is the position within the document (RAGWB-003 §6.2)
function assignIds(chunks) {
  const seen = new Map(),
    pos = new Map();
  chunks.forEach((c) => {
    const base =
      c.id ||
      `${c.docId}#${c.kind !== 'section' ? slug(c.kind) + '-' : ''}${slug(c.headingPath[c.headingPath.length - 1] || 'intro')}`;
    const n = seen.get(base) || 0;
    seen.set(base, n + 1);
    c.id = n ? `${base}-${n + 1}` : base;
    c.ord = pos.get(c.docId) || 0;
    pos.set(c.docId, c.ord + 1);
  });
  return chunks;
}

function chunkSections(doc, p) {
  const depth = p.depth ?? 3,
    max = p.maxTokens ?? 450,
    min = p.minTokens ?? 30;
  const groups = [];
  for (const s of doc.sections) {
    const blocks = s.blocks.filter(
      (b) => !(p.dropMermaid !== false && b.kind === 'fence' && b.lang === 'mermaid'),
    );
    if (s.level === 0 || s.level <= depth || !groups.length)
      groups.push({ path: s.path, blocks: [...blocks] });
    else groups[groups.length - 1].blocks.push({ kind: 'paragraph', text: s.title }, ...blocks);
  }
  const out = [];
  for (const g of groups) {
    if (!g.blocks.length) continue;
    const parts = [];
    let cur = [];
    for (const b of g.blocks.flatMap((b) => splitBlock(b, max))) {
      if (cur.length && tokens(cur) + tokens(b.text) > max) {
        parts.push(cur);
        cur = [];
      }
      cur.push(b);
    }
    if (cur.length) parts.push(cur);
    if (parts.length > 1 && tokens(parts[parts.length - 1]) < min) {
      const last = parts.pop();
      parts[parts.length - 1].push(...last);
    }
    parts.forEach((blocks, i) => {
      let text = blocksText(blocks);
      if (p.prefixHeading && g.path.length) text = g.path.join(' › ') + '\n' + text;
      if (p.overlap && i > 0)
        text = clip(blocksText(parts[i - 1]).slice(-p.overlap * 4), p.overlap * 4) + '\n' + text;
      out.push(makeChunk(doc, g.path, blocks, { text, part: i + 1, parts: parts.length }));
    });
  }
  return out;
}

export { assignIds, chunkSections, makeChunk };
