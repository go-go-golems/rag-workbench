/* kit.js — shared pieces for pipeline scripts: a standard corpus, the golden cases, a standard search UI.
 * Everything here is ordinary rag/gui code; presets use it so they can focus on one idea. */
(function (root) {
  'use strict';
  const { rag, gui } = root;

  // [question, expected document, expected section]
  const CASES = [
    ['Why did ChatGPT show no actions after OAuth worked?', 'CoinVault', /no actions|stateless/i],
    ['What rule picked eight latent classes?', /Tier B|Broken Exports/, /latent class|model selection/i, /knee|ten percent|10%/i],
    ['How are arrays of objects flattened into metadata terms?', 'SQLite-Backed Forum', /metadata/i],
    ['Why is a content hash not enough for an operation id?', 'Workbench Identity', /separately intended|content hash/i],
    ['What happens to product flags when a product is deleted?', 'TTC', /cascade|what the data said|asked/i],
    ['How does the eval tool report line numbers in errors?', 'Agent runtime spec', /eval tool|execution/i],
    ['Why did the browser ECS service restart in a loop after the shared-image deploy?', 'CoinVault', /typed-nil/i],
    ['Why does the inbox cursor advance past events an agent is not eligible for?', 'SQLite-Backed Forum', /inbox|cursor/i],
    ['Why was the orders tab empty when the explorer loaded?', 'TTC', /failure|orders tab|architecture/i],
    ['How does protojson serialize int64 values?', 'Agentforum Web', /type traps|protobuf/i],
    ['What does the rebalance law say?', 'Workbench Core', /rebalance/i],
    ['How are generative UI lambdas stored so they survive a reload?', 'Agent runtime spec', /lambda|generative ui|concepts/i],
  ];

  const kit = {
    // files → markdown → heading sections → BM25 'text' + frontmatter terms 'meta'
    reports: () => rag.corpus('reports')
      .from(rag.files('*.md'))
      .parse(rag.markdown())
      .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
      .index('text', rag.bm25({ fields: { title: 2, headingPath: 3, callout: 2, text: 1 } }))
      .index('meta', rag.terms({ keys: ['type', 'tags', 'status', 'aliases'] })),

    // the 12 hand-written cases for the bundled reports; docOnly drops the section expectation
    goldens(o = {}) {
      let ev = rag.evals(o.docOnly ? 'goldens-doc' : 'goldens');
      for (const [q, doc, section, answerHas] of CASES) ev = ev.case(q, o.docOnly ? { doc } : { doc, section, ...(answerHas ? { answerHas } : {}) });
      return ev.metrics(['hit@5', 'mrr', 'ndcg@10']);
    },

    // search box, status, facets, results | answer, section viewer
    searchApp(plan, o = {}) {
      const facets = o.facets === false ? null : o.facets || ['type', 'tags'];
      return gui.app(o.id || 'search').title(o.title || 'Search').use(plan).state({ selected: null })
        .view(v => v.row(r => r
          .col(c => {
            c.searchBox('q', { placeholder: o.placeholder || 'Ask the reports…', ...(o.submitLabel ? { submitLabel: o.submitLabel } : {}) }).status();
            if (facets) c.facets('filters', { fields: facets });
            c.results(o.results || {});
          }, { grow: 3 })
          .col(c => { if (o.answer !== false) c.answer(); c.chunk(s => s.selected); }, { grow: 2 })));
    },
  };
  root.kit = kit;
})(typeof globalThis !== 'undefined' ? globalThis : this);
