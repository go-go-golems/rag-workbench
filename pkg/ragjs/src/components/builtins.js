import { RagError } from '../core/errors.ts';
import { fnOf, lam } from '../core/lambda.js';
import { arr, clip, getPath, isBake, isLambda, isObj, uniq } from '../core/util.js';
import { answerLine } from '../engine/decision.js';
import { rag_bake } from '../graph/graph.js';
import { assignIds, chunkSections, makeChunk } from './chunking.js';
import { normHits, viewHit } from './hits.js';
import { Bm25Index, TermsIndex } from './indexes.js';
import { parseMarkdown } from './markdown.js';
import { build, fnArgs, live, optArgs } from './registry.js';

export function installBuiltins() {
  build('source.inline', {
    in: [],
    out: 'files',
    args: (files) => ({ files }),
    all: (i, c, p) => p.files,
  });

  build('parse.markdown', {
    in: ['files'],
    out: 'docs',
    args: optArgs,
    describe: () => 'frontmatter · headings · fences · tables',
    map: (file, ctx, p) => parseMarkdown(file, p),
  });

  build('chunk.sections', {
    in: ['docs'],
    out: 'chunks',
    args: optArgs,
    describe: (p) => `depth ${p.depth ?? 3} · ≤${p.maxTokens ?? 450} tok`,
    map: (doc, ctx, p) => chunkSections(doc, p),
    post: assignIds,
  });

  build('chunk', {
    in: ['docs'],
    out: 'chunks',
    args: fnArgs,
    lambdas: ['fn'],
    describe: () => 'build λ per doc',
    map: async (doc, ctx, p, node) => {
      const out = await fnOf(p.fn, `chunk lambda '${node.id}'`)(doc, ctx, p);
      return arr(out)
        .filter(Boolean)
        .map((c) =>
          makeChunk(doc, arr(c.headingPath || []), arr(c.blocks), { kind: node.id, ...c }),
        );
    },
    post: assignIds,
  });

  build('enrich', {
    in: ['chunks'],
    out: 'chunks',
    args: fnArgs,
    lambdas: ['fn'],
    describe: (p) => `${p.name} · build λ per chunk`,
    map: async (chunk, ctx, p, node) => ({
      ...chunk,
      [p.name]: await fnOf(p.fn, `enrich lambda '${node.id}'`)(chunk, ctx, p),
    }),
    async: true,
  });

  build('enrich.llm', {
    in: ['chunks'],
    out: 'chunks',
    args: optArgs,
    llm: true,
    describe: (p) => `${p.name} · 1 quick call per chunk`,
    map: async (chunk, ctx, p) => {
      const r = await ctx.sample.json(
        (p.prompt ||
          'For this section of an engineering report, reply with only JSON: {"summary": string (one sentence), "questions": [3-5 questions this section answers], "keywords": [5-8 search keywords, including synonyms not in the text]}.') +
          '\n\nDocument: ' +
          chunk.title +
          '\nSection: ' +
          chunk.headingPath.join(' › ') +
          '\n\n' +
          clip(chunk.text, 6000),
        { modelTier: p.tier || 'quick', cache: { gcTime: 86400000 } },
      );
      return { ...chunk, [p.name]: r };
    },
    async: true,
  });

  // RAGWB-007 §3.5: ask the questions about every chunk (the chunk is the state);
  // compact answers go to chunk[name] and chunk.meta[name], so index.terms and
  // filter.where can use them. Costs one decision call per chunk: deferred like enrich.llm.
  build('enrich.decide', {
    in: ['chunks'],
    out: 'chunks',
    args: optArgs,
    llm: true,
    describe: (p) =>
      `${p.name} · ${Object.keys(p.questions || {}).join(' · ')} · 1 decision per chunk`,
    map: async (chunk, ctx, p) => {
      if (!isObj(p.questions) || !Object.keys(p.questions).length)
        throw new RagError('build', `enrich.decide '${p.name}': questions are required`);
      const state = {
        title: chunk.title,
        section: (chunk.headingPath || []).join(' › '),
        text: clip(chunk.text || '', p.clip ?? 4000),
        ...(p.corpus ? { corpus: p.corpus } : {}),
      };
      const r = await ctx.decide({ state, questions: p.questions });
      const v = {};
      for (const [id, a] of Object.entries(r.answers)) {
        if (a.type === 'choice') {
          v[id] = a.choice;
          v[id + '_p'] = +(+(a.probabilities[a.choice] ?? a.confidence)).toFixed(4);
        } else if (a.type === 'noul') v[id] = +(+a.probability).toFixed(4);
        else v[id] = +(+a.score).toFixed(3);
      }
      return { ...chunk, [p.name]: v, meta: { ...(chunk.meta || {}), [p.name]: v } };
    },
    async: true,
  });

  build('index.bm25', {
    in: ['chunks'],
    out: 'index',
    args: optArgs,
    describe: (p) =>
      Object.entries(p.fields || { title: 2, headingPath: 3, text: 1 })
        .map(([k, v]) => `${k}×${v}`)
        .join(' '),
    all: ([chunks], ctx, p) => new Bm25Index(chunks, p),
  });

  build('index.terms', {
    in: ['chunks'],
    out: 'index',
    args: optArgs,
    describe: (p) => (p.keys || ['type', 'tags', 'status']).join(' · '),
    all: ([chunks], ctx, p) => new TermsIndex(chunks, p),
    // artifact store: the value → key → ids map (byDoc, which nothing reads, comes back empty)
    persist: {
      save: (ix) => ({ keys: ix.keys, N: ix.N, map: [...ix.map].map(([v, m]) => [v, [...m]]) }),
      load: (d) =>
        Object.assign(Object.create(TermsIndex.prototype), {
          kind: 'terms',
          keys: d.keys,
          map: new Map(d.map.map(([v, m]) => [v, new Map(m)])),
          byDoc: new Map(),
          N: d.N,
        }),
    },
  });

  build('synonyms', {
    in: ['docs'],
    out: 'value',
    args: (t) => ({ table: isBake(t) ? t : t || {} }),
    describe: () => 'query expansion table',
    all: (inputs, ctx, p) => p.table || {},
  });

  build('bake', {
    in: ['docs'],
    out: 'value',
    args: (f) => ({ value: rag_bake(f) }),
    describe: () => 'baked value',
    all: (inputs, ctx, p) => p.value,
  });

  live('retrieve', {
    in: ['queries'],
    out: 'hits',
    source: true,
    lambdas: ['fn'],
    args: (a, b) =>
      typeof a === 'function' || isLambda(a)
        ? { fn: lam(a), ...(b || {}) }
        : { index: a, ...(b || {}) },
    id: (p) => (p.index ? String(p.index) : 'retrieve'),
    uses: (p) => (p.index ? ['ix-' + p.index] : []),
    describe: (p) => (p.fn ? 'custom λ' : `${p.index} · k ${p.k ?? 20}`),
    run: async (s, ctx, queries, p) => {
      if (p.fn)
        return normHits(
          await fnOf(p.fn, `retrieve lambda '${ctx.node.id}'`)(s, ctx, queries, p),
          ctx.node.id,
        );
      const ix = ctx.index(p.index);
      if (!ix) throw new RagError('run', `retrieve: no index named '${p.index}'`);
      const qs = p.perQuery === false ? [queries.join(' ')] : queries;
      const best = new Map();
      for (const q of qs)
        for (const h of ix.search(q, p.k ?? 20, { synonyms: ctx.synonyms })) {
          const prev = best.get(h.id);
          if (!prev || h.score > prev.score) best.set(h.id, { ...h, q });
        }
      return [...best.values()]
        .sort((a, b) => b.score - a.score)
        .slice(0, p.k ?? 20)
        .map((h) => ({ ...h, via: ctx.node.id }));
    },
  });

  live('rewrite', {
    in: ['queries'],
    out: 'queries',
    args: fnArgs,
    lambdas: ['fn'],
    skip: 'pass',
    describe: () => 'live λ → queries',
    run: async (s, ctx, queries, p) =>
      uniq(
        arr(await fnOf(p.fn, `rewrite lambda '${ctx.node.id}'`)(s, ctx, queries, p))
          .map(String)
          .filter(Boolean),
      ),
  });

  live('rewrite.llm', {
    in: ['queries'],
    out: 'queries',
    args: optArgs,
    skip: 'pass',
    trigger: 'submit',
    reads: () => ['q'],
    describe: (p) => `${p.n ?? 3} keyword queries · ${p.tier || 'quick'}`,
    run: async (s, ctx, queries, p) => {
      const r = await ctx.sample.json(
        `Rewrite the question as ${p.n ?? 3} short keyword queries (2-6 words each) for a BM25 index over engineering project reports. Use the vocabulary such reports would use, including likely technical terms. Reply with only JSON: {"queries": [string]}.\n\nQuestion: ${s.q}`,
        { modelTier: p.tier || 'quick' },
      );
      return uniq([...queries, ...arr(r && r.queries).map(String)]).slice(
        0,
        (p.n ?? 3) + queries.length,
      );
    },
  });

  live('fuse.rrf', {
    in: ['hits*'],
    out: 'hits',
    args: optArgs,
    id: () => 'fuse',
    describe: (p) => `rrf k ${p.k ?? 60}`,
    run: (s, ctx, lists, p) => {
      const k = p.k ?? 60,
        w = p.weights || {},
        acc = new Map();
      lists.forEach((list, li) => {
        const src = ctx.node.in[li],
          wt = w[src] ?? 1;
        arr(list).forEach((h, r) => {
          const a = acc.get(h.id) || { id: h.id, score: 0, parts: {} };
          a.score += wt / (k + r + 1);
          a.parts[src] = r + 1;
          acc.set(h.id, a);
        });
      });
      return [...acc.values()]
        .sort((a, b) => b.score - a.score)
        .slice(0, p.limit ?? 60)
        .map((h) => ({ ...h, score: +h.score.toFixed(5), via: ctx.node.id }));
    },
  });

  live('fuse.max', {
    in: ['hits*'],
    out: 'hits',
    args: optArgs,
    id: () => 'fuse',
    run: (s, ctx, lists) => {
      const acc = new Map();
      for (const list of lists)
        for (const h of arr(list))
          if (!acc.has(h.id) || acc.get(h.id).score < h.score) acc.set(h.id, h);
      return [...acc.values()].sort((a, b) => b.score - a.score);
    },
  });

  live('filter', {
    in: ['hits'],
    out: 'hits',
    args: fnArgs,
    lambdas: ['fn'],
    describe: () => 'live λ per hit',
    run: async (s, ctx, hits, p) => {
      const f = fnOf(p.fn, `filter lambda '${ctx.node.id}'`),
        out = [];
      for (const h of hits) if (await f(s, ctx, viewHit(ctx, h), p)) out.push(h);
      return out;
    },
  });

  live('filter.where', {
    in: ['hits'],
    out: 'hits',
    args: optArgs,
    id: () => 'filter',
    reads: (p) => [p.key || 'filters'],
    describe: (p) => `meta ⊇ s.${p.key || 'filters'}`,
    run: (s, ctx, hits, p) => {
      const f = s[p.key || 'filters'] || {};
      const entries = Object.entries(f).filter(
        ([, v]) => v != null && v !== '' && !(Array.isArray(v) && !v.length),
      );
      if (!entries.length) return hits;
      return hits.filter((h) => {
        const c = ctx.chunk(h.id);
        if (!c) return false;
        return entries.every(([k, v]) => {
          const have =
            k === 'doc' ? [c.docId] : arr(getPath(c.meta, k)).map((x) => String(x).toLowerCase());
          return arr(v).some((x) => have.includes(String(x).toLowerCase()));
        });
      });
    },
  });

  live('rerank', {
    in: ['hits'],
    out: 'hits',
    args: fnArgs,
    lambdas: ['fn'],
    skip: 'pass',
    describe: () => 'live λ over hits',
    run: async (s, ctx, hits, p) =>
      normHits(
        await fnOf(p.fn, `rerank lambda '${ctx.node.id}'`)(
          s,
          ctx,
          hits.map((h) => viewHit(ctx, h)),
          p,
        ),
        ctx.node.id,
      ).map((h) => ({ id: h.id, score: h.score, via: h.via, reason: h.reason })),
  });

  live('rerank.llm', {
    in: ['hits'],
    out: 'hits',
    args: optArgs,
    id: () => 'rerank',
    skip: 'pass',
    trigger: 'submit',
    reads: () => ['q', 'k'],
    describe: (p) =>
      `top ${p.top ?? 20} → keep ${typeof p.keep === 'number' ? p.keep : 's.k'} · ${p.tier || 'quick'}`,
    run: async (s, ctx, hits, p) => {
      const top = hits.slice(0, p.top ?? 20);
      if (!top.length) return [];
      const keep = Number(p.keep ?? s.k ?? 8);
      const passages = top
        .map((h, i) => {
          const c = ctx.chunk(h.id) || {};
          return `[p${i + 1}] ${c.title} › ${(c.headingPath || []).join(' › ')}\n${clip((c.text || '').replace(/\s+/g, ' '), 700)}`;
        })
        .join('\n\n');
      const r = await ctx.sample.json(
        `Rank the passages by how directly they answer the question. Reply with only JSON: {"ranking": [{"p": "p3", "score": 0.0-1.0, "reason": "under 12 words"}]}, best first, including only passages that help answer it.\n\nQuestion: ${s.q}\n\nPassages:\n${passages}`,
        { modelTier: p.tier || 'quick' },
      );
      const seen = new Set(),
        out = [];
      for (const x of arr(r && r.ranking)) {
        const i = parseInt(String(x.p || x.id || '').replace(/\D/g, ''), 10) - 1;
        if (!(i >= 0 && i < top.length) || seen.has(i)) continue;
        seen.add(i);
        out.push({
          id: top[i].id,
          score: +Number(x.score ?? 0).toFixed(3),
          reason: x.reason,
          via: ctx.node.id,
        });
      }
      return (out.length ? out : top).slice(0, keep);
    },
  });

  // RAGWB-007 §3.4: grade hits with noul relevance questions — one shared state
  // (the query), batches of ≤ 32 questions (djev's max_questions), ≤ p.concurrency in flight.
  const RELEVANCE = {
    question: 'Does this passage help answer the query?',
    true: 'The passage contains facts needed to answer the query',
    false: 'The passage is off-topic or only loosely related',
  };

  async function gradeHits(s, ctx, hits, p) {
    const top = hits.slice(0, p.top ?? 20);
    if (!top.length) return [];
    const size = Math.min(32, Math.max(1, p.batch ?? 16));
    const state = { query: s.q, ...(p.corpus ? { corpus: p.corpus } : {}) };
    const batches = [];
    for (let i = 0; i < top.length; i += size)
      batches.push(top.slice(i, i + size).map((h, j) => [i + j, h]));
    const ps = new Array(top.length);
    let next = 0,
      done = 0;
    const worker = async () => {
      while (next < batches.length) {
        const b = batches[next++];
        const questions = {};
        for (const [i, h] of b) {
          const c = ctx.chunk(h.id) || {};
          questions['h' + i] = {
            type: 'noul',
            instructions: {
              passage: `${c.title || ''} › ${(c.headingPath || []).join(' › ')}\n${clip((c.text || '').replace(/\s+/g, ' '), p.clip ?? 1500)}`,
              question: p.instructions || RELEVANCE.question,
            },
            true: p.true || RELEVANCE.true,
            false: p.false || RELEVANCE.false,
          };
        }
        const r = await ctx.decide({ state, questions });
        for (const [i] of b) ps[i] = r.answers['h' + i].probability;
        done += b.length;
        ctx.progress(done, top.length, 'passages');
      }
    };
    await Promise.all(
      Array.from({ length: Math.min(batches.length, Math.max(1, p.concurrency ?? 2)) }, worker),
    );
    return top.map((h, i) => ({ ...h, p: +(+ps[i]).toFixed(4) }));
  }

  live('filter.decide', {
    in: ['hits'],
    out: 'hits',
    args: optArgs,
    id: () => 'filter',
    skip: 'pass',
    trigger: 'submit',
    reads: () => ['q'],
    describe: (p) =>
      `top ${p.top ?? 20} · keep p ≥ ${p.threshold ?? 0.5}${p.min ? ` · min ${p.min}` : ''} · decision`,
    run: async (s, ctx, hits, p) => {
      const graded = await gradeHits(s, ctx, hits, p);
      const th = p.threshold ?? 0.5;
      let kept = graded.filter((h) => h.p >= th);
      if (p.min && kept.length < p.min) {
        const best = new Set(
          [...graded]
            .sort((a, b) => b.p - a.p)
            .slice(0, p.min)
            .map((h) => h.id),
        );
        kept = graded.filter((h) => best.has(h.id));
      }
      ctx.note(
        `graded ${graded.length} · kept ${kept.length} with p ≥ ${th}${kept.length && kept.some((h) => h.p < th) ? ` (min ${p.min})` : ''}`,
      );
      return kept.map((h) => ({ ...h, reason: `p=${h.p.toFixed(2)}` }));
    },
  });

  live('rerank.decide', {
    in: ['hits'],
    out: 'hits',
    args: optArgs,
    id: () => 'rerank',
    skip: 'pass',
    trigger: 'submit',
    reads: () => ['q', 'k'],
    describe: (p) =>
      `top ${p.top ?? 20} → keep ${typeof p.keep === 'number' ? p.keep : 's.k'} by p(relevant) · decision`,
    run: async (s, ctx, hits, p) => {
      const graded = await gradeHits(s, ctx, hits, p);
      const keep = Number(p.keep ?? s.k ?? 8);
      const out = graded
        .map((h, i) => [h, i])
        .sort((a, b) => b[0].p - a[0].p || a[1] - b[1])
        .slice(0, keep)
        .map(([h]) => ({ ...h, score: h.p, reason: `p=${h.p.toFixed(2)}` }));
      ctx.note(`graded ${graded.length} · kept top ${out.length}`);
      return out;
    },
  });

  live('limit', {
    in: ['hits'],
    out: 'hits',
    args: (a) =>
      typeof a === 'number' || isLambda(a) || typeof a === 'function'
        ? { k: typeof a === 'number' ? a : lam(a) }
        : { ...(a || {}) },
    describe: (p) => `k ${typeof p.k === 'number' ? p.k : 's.k'}`,
    run: (s, ctx, hits, p) => hits.slice(0, Number(p.k ?? s.k ?? 8)),
  });

  live('expand.neighbors', {
    in: ['hits'],
    out: 'hits',
    args: optArgs,
    id: () => 'neighbors',
    describe: (p) => `before ${p.before ?? 0} · after ${p.after ?? 1}`,
    run: (s, ctx, hits, p) => {
      const have = new Set(hits.map((h) => h.id)),
        out = [];
      let extra = 0;
      hits.forEach((h, i) => {
        out.push(h);
        if (i >= (p.top ?? 5)) return;
        for (const n of ctx.neighbors(h.id, { before: p.before ?? 0, after: p.after ?? 1 })) {
          if (have.has(n.id) || extra >= (p.maxChars ?? 6000)) continue;
          have.add(n.id);
          extra += n.text.length;
          out.push({
            id: n.id,
            score: +(h.score * (p.decay ?? 0.5)).toFixed(5),
            via: ctx.node.id,
            of: h.id,
          });
        }
      });
      return out;
    },
  });

  live('expand.links', {
    in: ['hits'],
    out: 'hits',
    args: optArgs,
    id: () => 'links',
    reads: () => ['q'],
    uses: (p) => (p.index ? ['ix-' + p.index] : []),
    describe: (p) => `[[links]] · decay ${p.decay ?? 0.5}`,
    run: (s, ctx, hits, p) => {
      const ix = ctx.index(p.index) || ctx.firstIndex('bm25');
      const seenDocs = new Set(hits.map((h) => (ctx.chunk(h.id) || {}).docId)),
        out = [...hits];
      let added = 0;
      for (const h of hits.slice(0, p.top ?? 5)) {
        const c = ctx.chunk(h.id);
        if (!c) continue;
        for (const d of ctx.links(c.docId)) {
          if (seenDocs.has(d) || added >= (p.max ?? 4) || !ix) continue;
          seenDocs.add(d);
          const best = ix.searchIn(d, s.q, 1)[0];
          if (best) {
            out.push({
              id: best.id,
              score: +(h.score * (p.decay ?? 0.5)).toFixed(5),
              via: ctx.node.id,
              of: h.id,
            });
            added++;
          }
        }
      }
      return out;
    },
  });

  live('pack', {
    in: ['hits'],
    out: 'context',
    args: (a) => (typeof a === 'number' ? { budget: a } : { ...(a || {}) }),
    describe: (p) => `${Math.round((p.budget ?? 40000) / 1000)} KB · ${p.order || 'rank'} order`,
    run: (s, ctx, hits, p) => {
      const budget = p.budget ?? 40000,
        perDoc = new Map(),
        evidence = [];
      let bytes = 0;
      for (const h of hits) {
        const c = ctx.chunk(h.id);
        if (!c) continue;
        const n = perDoc.get(c.docId) || 0;
        if (n >= (p.maxPerDoc ?? 6)) continue;
        const piece = c.text.length;
        if (bytes + piece > budget && evidence.length) break;
        perDoc.set(c.docId, n + 1);
        bytes += piece;
        evidence.push({
          label: 'E' + (evidence.length + 1),
          id: c.id,
          docId: c.docId,
          title: c.title,
          headingPath: c.headingPath,
        });
      }
      const order =
        p.order === 'doc'
          ? [...evidence].sort((a, b) =>
              (a.docId + ctx.chunk(a.id).ord).localeCompare(b.docId + ctx.chunk(b.id).ord),
            )
          : evidence;
      const text = order
        .map(
          (e) => `[${e.label}] ${e.title} › ${e.headingPath.join(' › ')}\n${ctx.chunk(e.id).text}`,
        )
        .join('\n\n---\n\n');
      return { text, evidence, bytes };
    },
  });

  live('answer', {
    in: ['context'],
    out: 'answer',
    args: optArgs,
    skip: 'null',
    trigger: 'submit',
    reads: () => ['q'],
    describe: (p) => `${p.tier || 'default'} · cites [E#]`,
    run: async (s, ctx, context, p) => {
      if (!context || !context.evidence.length) {
        if (!p.direct)
          return { text: 'No evidence was retrieved for this question.', evidence: [], cited: [] };
        // RAGWB-007: retrieval was skipped (e.g. a when-condition) — answer without documents, and say so
        ctx.note('answered without retrieval');
        const r = await ctx.sample(
          (p.directInstructions ||
            'Answer the question directly and briefly. No documents were consulted, so do not cite sources; if the question needs facts from a specific document collection, say that you would need to look them up.') +
            `\n\nQuestion: ${s.q}`,
          { modelTier: p.tier || 'default', onText: ({ text }) => ctx.emit(text) },
        );
        return { text: r.text, evidence: [], cited: [], direct: true, truncated: r.truncated };
      }
      const prompt =
        (p.instructions ||
          'Answer the question using only the evidence below, drawn from a set of engineering project reports. Cite evidence inline as [E1], [E2]. If the evidence does not answer the question, say what is missing. Be specific and concise: under 200 words unless the question needs more. Use plain markdown.') +
        `\n\n<evidence>\n${context.text}\n</evidence>\n\nQuestion: ${s.q}`;
      const r = await ctx.sample(prompt, {
        modelTier: p.tier || 'default',
        onText: ({ text }) => ctx.emit(text),
      });
      const labels = uniq(r.text.match(/\bE\d+\b/g) || []);
      return { text: r.text, evidence: context.evidence, cited: labels, truncated: r.truncated };
    },
  });

  // RAGWB-007 §3.3: ask a question set about the query; the answers are this
  // node's 'decision' output, read by when-conditions and ctx.decision(id).
  live('classify', {
    in: [],
    out: 'decision',
    args: optArgs,
    trigger: 'submit',
    skip: 'null',
    reads: () => ['q'],
    id: () => 'route',
    describe: (p) => `${Object.keys(p.questions || {}).join(' · ') || 'no questions'} · decision`,
    run: async (s, ctx, _in, p) => {
      if (!isObj(p.questions) || !Object.keys(p.questions).length)
        throw new RagError('run', `classify '${ctx.node.id}': questions are required`);
      const state = p.state ?? { query: s.q, ...(p.corpus ? { corpus: p.corpus } : {}) };
      const r = await ctx.decide({ state, questions: p.questions });
      for (const [id, a] of Object.entries(r.answers)) ctx.note(`${id} → ${answerLine(a)}`);
      return Object.defineProperties(
        { ...r.answers },
        {
          $decision: { value: true, enumerable: false },
          $meta: { value: { model: r.model, ms: r.ms }, enumerable: false },
        },
      );
    },
  });

  live('agentic', {
    in: ['queries'],
    out: 'answer',
    args: optArgs,
    skip: 'null',
    trigger: 'submit',
    reads: () => ['q'],
    uses: (p) => arr(p.indexes).map((i) => 'ix-' + i),
    describe: (p) => `tools · ≤${p.maxReads ?? 8} reads · ${p.tier || 'default'}`,
    run: async (s, ctx, queries, p) => {
      const names = arr(p.indexes).length
        ? arr(p.indexes)
        : ctx.indexNames().filter((n) => (ctx.index(n) || {}).kind === 'bm25');
      const evidence = [],
        rounds = [];
      const label = (id) => {
        let e = evidence.find((x) => x.id === id);
        if (!e) {
          const c = ctx.chunk(id);
          e = {
            label: 'E' + (evidence.length + 1),
            id,
            docId: c.docId,
            title: c.title,
            headingPath: c.headingPath,
          };
          evidence.push(e);
        }
        return e.label;
      };
      const maxReads = p.maxReads ?? 8;
      const tools = [
        {
          name: 'search',
          description: `Search the report corpus with a short keyword query. Returns up to 8 sections as {id, doc, heading, snippet}. Indexes: ${names.join(', ')}. Search several phrasings.`,
          inputSchema: {
            type: 'object',
            properties: { query: { type: 'string' }, index: { type: 'string', enum: names } },
            required: ['query'],
          },
          execute: ({ query, index }) => {
            const ix = ctx.index(String(index || names[0]));
            if (!ix) throw new Error('unknown index ' + index);
            const res = ix.search(String(query || ''), 8, { synonyms: ctx.synonyms });
            rounds.push({
              tool: 'search',
              query: String(query),
              index: String(index || names[0]),
              found: res.length,
            });
            ctx.note(`search("${clip(String(query), 40)}") → ${res.length}`);
            return res.map((h) => {
              const c = ctx.chunk(h.id);
              return {
                id: h.id,
                doc: c.title,
                heading: c.headingPath.join(' › '),
                snippet: clip(c.text.replace(/\s+/g, ' '), 220),
              };
            });
          },
        },
        {
          name: 'read',
          description:
            'Read one section by id. Returns its full text and the evidence label to cite, like [E2].',
          inputSchema: { type: 'object', properties: { id: { type: 'string' } }, required: ['id'] },
          execute: ({ id }) => {
            const c = ctx.chunk(String(id));
            if (!c) throw new Error('no section ' + id + '; use an id returned by search');
            if (evidence.length >= maxReads && !evidence.find((e) => e.id === c.id))
              throw new Error(
                `read budget of ${maxReads} sections used; answer now from what you read`,
              );
            const l = label(c.id);
            rounds.push({ tool: 'read', id: c.id });
            ctx.note(`read(${c.id}) → [${l}]`);
            return {
              label: l,
              doc: c.title,
              heading: c.headingPath.join(' › '),
              text: clip(c.text, 6000),
            };
          },
        },
        {
          name: 'outline',
          description:
            "List a document's sections as {id, heading}. Pass a section id or a document id.",
          inputSchema: { type: 'object', properties: { id: { type: 'string' } }, required: ['id'] },
          execute: ({ id }) => {
            const c = ctx.chunk(String(id)),
              docId = c ? c.docId : String(id);
            rounds.push({ tool: 'outline', id: docId });
            return ctx
              .docChunks(docId)
              .map((x) => ({ id: x.id, heading: x.headingPath.join(' › ') }));
          },
        },
        {
          name: 'documents',
          description: 'List all documents as {id, title, type, tags}.',
          inputSchema: { type: 'object', properties: {} },
          execute: () => {
            rounds.push({ tool: 'documents' });
            return ctx
              .docs()
              .map((d) => ({ id: d.id, title: d.title, type: d.meta.type, tags: d.meta.tags }));
          },
        },
      ];
      const prompt =
        (p.instructions ||
          'You answer questions about a corpus of engineering project reports using the tools. Search with several phrasings, read the sections you rely on, then answer. Cite read sections inline as [E1], [E2] using the labels read() returns. Stop searching as soon as you can answer. Final answer: plain markdown, under 250 words.') +
        `\n\nQuestion: ${s.q}`;
      const r = await ctx.sample(prompt, {
        tools,
        modelTier: p.tier || 'default',
        onText: ({ text }) => ctx.emit(text),
      });
      return { text: r.text, evidence, cited: uniq(r.text.match(/\bE\d+\b/g) || []), rounds };
    },
  });

  live('map', {
    in: ['hits'],
    out: 'hits',
    args: fnArgs,
    lambdas: ['fn'],
    describe: (p) => `live λ ${p.inType || 'hits'} → ${p.outType || 'hits'}`,
    ports: (p) => ({ in: [p.inType || 'hits'], out: p.outType || 'hits' }),
    run: async (s, ctx, input, p) => fnOf(p.fn, `map lambda '${ctx.node.id}'`)(s, ctx, input, p),
  });
}
