import { arr, clip } from '../core/util.js';

// hits as events: ids, scores and labels; the chunk itself only when asked (detail.chunks)
function hitsEvent(node, hits, art, detail = {}, partial = false) {
  const top = detail.top ?? 20,
    list = arr(hits);
  return {
    type: 'hits',
    node,
    ...(partial ? { partial: true } : {}),
    total: list.length,
    hits: list.slice(0, top).map((h, i) => {
      const c = (art && art.chunks.get(h.id)) || {};
      const o = {
        id: h.id,
        score: h.score,
        rank: i + 1,
        doc: c.title,
        heading: (c.headingPath || []).join(' › '),
      };
      if (h.via) o.via = h.via;
      if (h.reason) o.reason = h.reason;
      if (detail.chunks && c.id) o.chunk = c;
      return o;
    }),
  };
}

const summarize = (v) =>
  Array.isArray(v)
    ? `${v.length} result${v.length === 1 ? '' : 's'}`
    : typeof v === 'string'
      ? clip(v, 120)
      : v && typeof v === 'object'
        ? clip(JSON.stringify(v), 120)
        : String(v);

export { hitsEvent, summarize };
