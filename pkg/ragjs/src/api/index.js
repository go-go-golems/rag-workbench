import { installBuiltins } from '../components/builtins.js';
import { chunkSections } from '../components/chunking.js';
import { Bm25Index, TermsIndex } from '../components/indexes.js';
import { parseFrontmatter, parseMarkdown } from '../components/markdown.js';
import { REG } from '../components/registry.js';
import { RagError } from '../core/errors.ts';
import { slug } from '../core/identity.ts';
import { fnOf, lam, signalReads } from '../core/lambda.js';
import { stem, tokenize } from '../core/text.ts';
import { clip, clone, hash, LN, stable } from '../core/util.js';
import { makeDecide } from '../engine/decision.js';
import { Engine } from '../engine/engine.js';
import { currentScope, newScope, scoped, withScope } from '../engine/scope.ts';
import { explain, Session } from '../engine/session.js';
import { Evals, evals, sweep } from '../evaluation/evals.js';
import { exportHtml } from '../export/html.js';
import {
  applyOps,
  CorpusSpec,
  compile,
  costsCalls,
  describeNode,
  Fragment,
  graph,
  installFamilies,
  makePlan,
  normalize,
  rag_bake,
  triggerOf,
  withMethods,
} from '../graph/graph.js';
import { configure, defaultEngine } from './default-engine.js';
import { registerComponent } from './define.js';
import { diffIr, Workspace } from './workspace.js';

installBuiltins();

function define(type, def) {
  const result = registerComponent(type, def);
  refreshFactories();
  installFamilies();
  return result;
}

const rag = {
  version: '0.1.0',
  // builders
  corpus: (id) => new CorpusSpec({ id: id || 'corpus', nodes: [], cursor: {} }),
  plan: (id) =>
    makePlan({ id: id || 'plan', corpus: null, signals: {}, nodes: [], cursor: {}, outputs: {} }),
  graph,
  evals,
  sweep,
  fragment: (id, fn) => new Fragment(id, fn),
  bake: rag_bake,
  live: (f) => lam(f),
  c: (type, p = {}) => ({ $c: type, p }),
  // compile and run
  compile,
  applyOps,
  ir: (json) => withMethods(clone(json)),
  build: (target, opts) => defaultEngine().build(compile(target), scoped(opts)),
  scope: () => currentScope,
  withScope,
  newScope,
  run: (target, signals, opts) =>
    target instanceof Workspace
      ? target.run(signals, opts)
      : defaultEngine().run(compile(target), signals, opts),
  search: async (target, q, opts = {}) => {
    const r = await rag.run(
      target,
      {
        ...(opts.signals || {}),
        q,
        ...(opts.k ? { k: opts.k } : {}),
        ...(opts.where ? { filters: opts.where } : {}),
      },
      { trigger: opts.trigger || 'change' },
    );
    return r.top(opts.k || 10);
  },
  explain,
  workspace: (target, opts) => new Workspace(target, opts),
  engine: () => defaultEngine(),
  // ask a decision model outside a plan run (RAGWB-007)
  decide: makeDecide((req) => defaultEngine().decider({ calls: 0 })(req)),
  configure,
  define,
  components: () =>
    [...REG.values()].map((d) => ({
      type: d.type,
      phase: d.phase,
      in: d.in,
      out: d.out,
      trigger: d.trigger || 'change',
    })),
  export: exportHtml,
  // pieces, for custom components and tests
  util: {
    LN,
    hash,
    stable,
    tokenize,
    stem,
    parseMarkdown,
    parseFrontmatter,
    chunkSections,
    Bm25Index,
    TermsIndex,
    slug,
    clip,
    signalReads,
    fnOf,
    lam,
    diffIr,
    triggerOf,
    describeNode,
    costsCalls,
  },
  Engine,
  Workspace,
  Session,
  Evals,
  RagError,
};

function refreshFactories() {
  const fams = new Map();
  for (const d of REG.values()) {
    const [fam, variant] = d.type.split('.');
    if (!fams.has(fam)) fams.set(fam, {});
    if (variant) fams.get(fam)[variant] = d.type;
    else fams.get(fam)[''] = d.type;
  }
  for (const [fam, vs] of fams) {
    if (['source', 'parse', 'index', 'bake'].includes(fam) && !vs['']) {
      /* namespaces only */
    }
    const make =
      (type) =>
      (...args) => {
        const c = normalize(type, args);
        return {
          $c: c.t,
          p: {
            ...c.p,
            ...(c.opts.id ? { id: c.opts.id } : {}),
            ...(c.opts.trigger ? { trigger: c.opts.trigger } : {}),
            ...(c.opts.when != null ? { when: c.opts.when } : {}),
          },
        };
      };
    const f = vs[''] ? make(vs['']) : {};
    for (const [v, type] of Object.entries(vs)) if (v) f[v] = make(type);
    if (fam === 'bake') continue;
    rag[fam] = f;
  }
  rag.files = rag.source.files;
  rag.markdown = rag.parse.markdown;
  rag.sections = rag.chunk.sections;
  rag.bm25 = rag.index.bm25;
  rag.terms = rag.index.terms;
  rag.bake = rag_bake;
}

refreshFactories();

installFamilies();

export { rag };
