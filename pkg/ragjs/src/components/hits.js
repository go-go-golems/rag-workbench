import { arr } from '../core/util.js';

export function viewHit(ctx, h) {
  const c = ctx.chunk(h.id) || {};
  return {
    ...h,
    docId: c.docId,
    title: c.title,
    headingPath: c.headingPath || [],
    text: c.text || '',
    meta: c.meta || {},
    kind: c.kind,
    tokens: c.tokens || 0,
  };
}

export const normHits = (xs, via) =>
  arr(xs)
    .filter(Boolean)
    .map((h, i) =>
      typeof h === 'string' ? { id: h, score: 1 / (i + 1), via } : { ...h, via: h.via || via },
    );
