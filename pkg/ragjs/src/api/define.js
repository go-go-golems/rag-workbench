import { assignIds, makeChunk } from '../components/chunking.js';
import { optArgs, REG } from '../components/registry.js';
import { RagError } from '../core/errors.ts';
import { arr } from '../core/util.js';

// ────────────────────────────────────────────────────────────── define, export
function registerComponent(type, def) {
  if (!/^[a-z][\w]*(\.[a-z][\w]*)?$/i.test(type))
    throw new RagError(
      'compile',
      `component type '${type}' should look like 'family' or 'family.variant'`,
    );
  if (!def || !['build', 'live'].includes(def.phase))
    throw new RagError('compile', "define needs phase: 'build' or 'live'");
  const d = { ...def, type };
  d.src = String(def.map || def.all || def.run || '');
  d.in = arr(def.in || (def.phase === 'live' ? 'hits' : 'docs'));
  d.out = def.out || (def.phase === 'live' ? 'hits' : 'chunks');
  d.args = def.args || optArgs;
  d.id = def.id || (() => type.split('.').pop());
  if (def.phase === 'live') {
    const run = def.run;
    if (typeof run !== 'function')
      throw new RagError('compile', 'a live component needs run(s, ctx, input, p)');
    d.run = (s, ctx, input, p) => run(s, ctx, input, p);
  } else {
    if (def.map) {
      const map = def.map;
      d.map = (item, ctx, p) => map(item, ctx, p);
      if (d.out === 'chunks') d.post = assignIds;
      d.async = true;
    } else if (def.all) {
      const all = def.all;
      d.all = (inputs, ctx, p) => all(inputs, ctx, p);
    } else
      throw new RagError(
        'compile',
        'a build component needs map(item, ctx, p) or all(inputs, ctx, p)',
      );
    if (d.out === 'chunks' && def.map) {
      const map = def.map;
      d.map = async (doc, ctx, p) =>
        arr(await map(doc, ctx, p))
          .filter(Boolean)
          .map((c) =>
            makeChunk(doc, arr(c.headingPath || []), arr(c.blocks), {
              kind: type.split('.').pop(),
              ...c,
            }),
          );
    }
  }
  REG.set(type, d);
  return type;
}

export { registerComponent };
