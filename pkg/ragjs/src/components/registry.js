import { lam } from '../core/lambda.js';
import { isLambda } from '../core/util.js';

// ────────────────────────────────────────────────────────────── registry
// A component type declares its phase, its ports and how it runs.
// Ports: files docs chunks index value | queries hits context answer.
const REG = new Map();

const live = (type, def) => REG.set(type, { type, phase: 'live', ...def });

const build = (type, def) => REG.set(type, { type, phase: 'build', ...def });

const fnArgs = (a, b) =>
  typeof a === 'function' || isLambda(a) || typeof a === 'string'
    ? { fn: lam(a), ...(b || {}) }
    : { ...(a || {}) };

const optArgs = (a) => ({ ...(a || {}) });

build('source.files', {
  in: [],
  out: 'files',
  args: (g) => (typeof g === 'string' || g == null ? { glob: g || '*' } : { ...g }),
  describe: (p) => p.glob,
  all: (inputs, ctx, p) => {
    const re = new RegExp(
      '^' +
        String(p.glob || '*')
          .split('*')
          .map((s) => s.replace(/[.+?^${}()|[\]\\]/g, '\\$&'))
          .join('.*') +
        '$',
      'i',
    );
    return ctx
      .files()
      .filter((f) => re.test(f.name))
      .map((f) => ({ name: f.name, text: f.text }));
  },
});

function portsOf(node) {
  const def = REG.get(node.t);
  if (!def) return { in: [], out: null };
  if (def.ports) return def.ports(node.p || {});
  return { in: def.in, out: def.out };
}

const accepts = (port, type) =>
  port.replace('*', '') === type || (port.startsWith('queries') && type === 'signal');

export { accepts, build, fnArgs, live, optArgs, portsOf, REG };
