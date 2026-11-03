/* agent.js — a chat agent whose only tool is eval(code).
 *
 * Code runs in the page as the body of an async function with the scope
 * objects rag, gui, wb, ws, ui, chat, ref, log, console. Results come back as
 * short YAML. Every step that can change state is snapshotted, so any step or
 * answer can be reverted. Run modes: Auto, Ask (approve each step), Read-only.
 */
(function (root) {
  'use strict';
  const { h, md, esc } = root.gui;
  const clip = (s, n) => (s && s.length > n ? s.slice(0, n - 1) + '…' : s || '');
  const now = () => Date.now();

  // ────────────────────────────────────────────────────────────── yaml
  function plain(v, depth = 6, seen = new WeakSet()) {
    if (v == null || typeof v === 'number' || typeof v === 'boolean') return v;
    if (typeof v === 'string') return v;
    if (typeof v === 'bigint') return String(v);
    if (typeof v === 'function') return `ƒ ${v.name || 'anonymous'}`;
    if (typeof v !== 'object') return String(v);
    if (typeof Node !== 'undefined' && v instanceof Node) return `<${v.nodeName.toLowerCase()}>`;
    if (v instanceof Error) return { error: `${v.name}: ${v.message}` };
    if (v instanceof RegExp) return String(v);
    if (seen.has(v)) return '[cycle]';
    seen.add(v);
    if (typeof v.toJSON === 'function' && !(v instanceof Date)) { try { return plain(v.toJSON(), depth, seen); } catch (_) { /* fall through */ } }
    if (v instanceof Date) return v.toISOString();
    if (depth <= 0) return Array.isArray(v) ? `[${v.length} items]` : '{…}';
    if (v instanceof Map) return plain(Object.fromEntries([...v].slice(0, 40)), depth, seen);
    if (v instanceof Set) return plain([...v].slice(0, 40), depth, seen);
    if (Array.isArray(v)) { const a = v.slice(0, 40).map(x => plain(x, depth - 1, seen)); if (v.length > 40) a.push(`… ${v.length - 40} more`); return a; }
    const o = {};
    const keys = Object.keys(v).filter(k => !k.startsWith('_'));
    for (const k of keys.slice(0, 40)) { const x = v[k]; if (x !== undefined) o[k] = plain(x, depth - 1, seen); }
    if (keys.length > 40) o['…'] = `${keys.length - 40} more keys`;
    return o;
  }
  const needsQuote = s => s === '' || /^[\s-?:,[\]{}#&*!|>'"%@`]|: |\s#|\s$|^(true|false|null|yes|no|~)$/i.test(s) || /^[-+]?\d/.test(s);
  function yaml(v, ind = '') {
    if (v == null) return 'null';
    if (typeof v === 'number' || typeof v === 'boolean') return String(v);
    if (typeof v === 'string') {
      if (v.includes('\n')) return '|-\n' + v.split('\n').map(l => ind + '  ' + l).join('\n');
      return needsQuote(v) ? JSON.stringify(v) : v;
    }
    if (Array.isArray(v)) {
      if (!v.length) return '[]';
      if (v.every(x => x == null || typeof x !== 'object') && v.join(', ').length < 80 && !v.some(x => typeof x === 'string' && x.includes('\n'))) return '[' + v.map(x => yaml(x)).join(', ') + ']';
      return v.map(x => {
        if (x && typeof x === 'object' && !Array.isArray(x) && Object.keys(x).length) {
          const body = yaml(x, ind + '  ');
          return '\n' + ind + '- ' + body.replace(/^\n/, '').replace(new RegExp('^' + ind + '  '), '');
        }
        return '\n' + ind + '- ' + yaml(x, ind + '  ');
      }).join('');
    }
    const ks = Object.keys(v);
    if (!ks.length) return '{}';
    return ks.map(k => {
      const x = v[k];
      const key = /^[\w.@$/-]+$/.test(k) ? k : JSON.stringify(k);
      if (x && typeof x === 'object' && (Array.isArray(x) ? x.length && yaml(x, ind + '  ').startsWith('\n') : Object.keys(x).length)) {
        const body = yaml(x, ind + '  ');
        return '\n' + ind + key + ':' + (body.startsWith('\n') ? body : '\n' + ind + '  ' + body);
      }
      return '\n' + ind + key + ': ' + yaml(x, ind + '  ');
    }).join('');
  }
  const toYAML = v => yaml(plain(v)).replace(/^\n/, '');

  // ────────────────────────────────────────────────────────────── API docs
  const DOCS = `# rag workbench API

Everything is reachable from code. The agent's eval code and the pipeline script use the same objects.

## Lambda phases
| form | runs | receives |
|---|---|---|
| build callback \`b => b.x()\` | once, while building | a builder |
| build slot, e.g. \`.chunk((doc, ctx) => …)\` | at build, per item, cached by digest | item, build ctx |
| \`rag.bake(ctx => value)\` | at build, once | \`{ docs, chunks }\` |
| live slot, e.g. \`.filter((s, ctx, hit) => …)\` | per query | signals \`s\`, ctx, input |

Lambdas are stored as source and run **closed**: a free identifier throws. Pass values through signals, \`rag.bake\`, or params:
a lambda component receives its own params as the last argument, e.g. \`.rewrite((s, ctx, queries, p) => …, { table: {…} })\`, \`.chunk('rows', (doc, ctx, p) => …, { minRows: 2 })\`.
Live ctx: \`ctx.sample\`, \`ctx.sample.json\`, \`ctx.index(name).search(q, k)\`, \`ctx.chunk(id)\`, \`ctx.chunks(f)\`, \`ctx.doc(id)\`, \`ctx.docs()\`, \`ctx.neighbors(id)\`, \`ctx.links(docId)\`, \`ctx.note(text)\`.

## Corpus (build phase)
\`\`\`js
rag.corpus(id).from(rag.files('*.md')).parse(rag.markdown())
  .chunk(rag.sections({ depth: 3, maxTokens: 450, prefixHeading: false }))
  .chunk('name', (doc, ctx) => [{ headingPath, text }])        // extra chunk stream
  .enrich('sem', (chunk, ctx) => value, { concurrency: 3, budget: { calls: 100 } })
  .enrich('sem', rag.enrich.llm(), { from: 'cards' })         // summary, questions, keywords; 1 quick call per chunk; from = a named stream
  .synonyms(rag.bake(ctx => ({ phrase: ['expansion'] })))
  .index('text', rag.bm25({ fields: { title: 2, headingPath: 3, text: 1, 'sem.questions': 3 } }), { from: 'chunks' })
  .index('meta', rag.terms({ keys: ['type', 'tags'] }))
\`\`\`
Build steps that spend Claude calls (enrich.llm, or any build lambda that calls ctx.sample) are deferred: items pass through un-enriched until the person presses Build (or \`wb.build()\`). \`ws.rebuild({ calls: true })\` runs them; \`rag.engine().pending(ws.ir)\` lists what waits.
Docs: \`{ id, title, meta, sections: [{ path, level, blocks: [{ kind: paragraph|list|fence|table|callout|quote, text, header, rows }] }], links }\`.
Chunks: \`{ id, docId, title, headingPath, kind, text, meta, tokens, ord }\`.

## Plan (live phase)
\`\`\`js
rag.plan(id).over(corpus).signals({ q: '', k: 8, filters: {} })
  .rewrite.llm({ n: 3 })              // submit-only (LLM)
  .rewrite((s, ctx, queries) => [...])
  .retrieve('text', { k: 40 })        // one per index; several in a row run in parallel
  .retrieve((s, ctx, queries) => hits)
  .fuse.rrf({ k: 60, weights: { text: 1 } })   // required after 2+ retrieves
  .filter.where()                      // s.filters ⊆ chunk.meta
  .filter((s, ctx, hit) => bool)
  .rerank.llm({ top: 20, keep: s => s.k })     // submit-only
  .rerank((s, ctx, hits) => hits)
  .limit(s => s.k)
  .expand.neighbors({ before: 0, after: 1 }) .expand.links({ decay: 0.5 })
  .pack({ budget: 40000, order: 'rank' })
  .answer({ tier: 'default' })         // submit-only, streams, cites [E1]
  .agentic({ maxReads: 8 })            // tool-using answer node (search/read/outline)
  .map((s, ctx, input) => output, { inType: 'hits', outType: 'hits' })
\`\`\`
Any param may be a live lambda \`s => value\`. Options on every step: \`{ id, trigger: 'change' | 'submit' }\`.
Triggers: typing runs only \`change\` nodes; Enter runs everything. Skipped transforms pass their input through.
Graph form: \`rag.graph(id, g => { g.over(c); const q = g.signal('q', ''); const a = g.node('a', rag.retrieve('text'), { from: q }); g.output('hits', a); })\`.
Variants: \`plan.variant('lex', [{ op: 'remove', node: 'rerank' }])\`. Fragments: \`rag.fragment(id, (spec, p) => spec…)\`, \`plan.use(fragment, p)\`.
Custom components: \`rag.define('boost.recent', { phase: 'live', in: 'hits', out: 'hits', params: {…}, run: (s, ctx, hits, p) => hits })\` then \`plan.boost.recent({…})\`.

## Compile, build, run
\`rag.compile(plan)\` → IR \`{ nodes: [{ id, t, phase, in, p, trigger }], edges, signals, outputs }\` with \`ir.summary()\`, \`ir.node(id)\`, \`ir.downstream(id)\`, \`ir.mermaid()\`.
\`await rag.run(planOrIr, { q }, { trigger: 'change' | 'submit' })\` → result \`{ hits, answer, context, outputs, trace, errors, ms, calls }\`, \`result.top(n)\`, \`result.explain(id)\`, \`result.chunk(id)\`.
\`await rag.search(target, q, { k, where })\` → top hits. \`rag.explain(result, id)\`.

## Workspace: edit the live pipeline with ops
\`ws\` is the workbench's workspace. Ops are data; a batch is atomic.
\`\`\`js
await ws.apply([
  { op: 'set', node: 'ix-text', path: 'fields.title', value: 4 },
  { op: 'insert', after: 'fuse', id: 'links', node: { t: 'expand.links', p: { decay: 0.5 } } },
  { op: 'remove', node: 'rerank' },
  { op: 'replace', node: 'filter', with: { t: 'filter', p: { fn: '(s, ctx, hit) => hit.tokens > 40' } } },
  { op: 'wire', node: 'fuse', in: ['text', 'meta'] },
  { op: 'signal', key: 'k', value: 6 },
  { op: 'trigger', node: 'rerank', value: 'change' },
], { expect: ws.revision })   // → { revision, live, dirty, changed }
\`\`\`
\`ws.dryRun(ops)\`, \`await ws.rebuild()\` (dirty build nodes only), \`ws.undo()\`, \`ws.redo()\`, \`ws.history\`, \`ws.ir\`, \`ws.revision\`,
\`const b = ws.branch('name')\` → isolated workspace; \`await ws.merge(b)\`. \`ws.diff(other)\`. \`await ws.run(signals, { trigger })\`.

## Evals and sweeps
\`\`\`js
const ev = rag.evals('id').case('question', { doc: 'title part' | /re/, section: /heading re/, answerHas: /re/ })
  .metrics(['hit@5', 'mrr', 'ndcg@10']).judge('grounded', async (s, ctx, c) => 1)
const report = await ev.run(wsOrPlan, { trigger: 'change' })   // summary, cases, misses, table()
await ev.compare([ws, branch])                                 // rows, delta, cases
await rag.evals.generate(ws, { n: 8 })                         // synthetic cases (Claude calls)
await rag.sweep(ws, { 'fuse.k': [20, 60], 'chunks.maxTokens': [300, 600] }).run(ev, { metric: 'mrr' })
\`\`\`

## gui: generative UI
\`\`\`js
const app = gui.app('id').title('Title').use(ws)      // .use binds signals q/k/filters and node outputs (s.out[nodeId])
  .state({ x: 1 }).derive('y', s => s.x * 2).watch('x', (s, ctx, v) => {})
  .view(v => v.row(r => r.col(c => c.searchBox('q').facets('filters').results()).col(c => c.answer().chunk(s => s.selected))))
\`\`\`
Containers: col row grid(cols, b) card(title, b) tabs(t => t.tab(title, b).tab(…)) when(cond, b) divider.
Content: h text(md) note(md, tone) badge stat(label, value, {delta, unit}) stats(items) kv table(rows, {columns, onRowClick}) list json link code.
Charts: line({ name: [[x, y]…] }, { xLabel, yLabel }) bar([[label, v]…]) scatter.
Inputs (bound to a key): slider(key, {min, max, step, label}) number select(key, options) toggle input button(label, (s, ctx) => …).
Drawing: canvas((s, ctx, g) => { g.clear(); g.rect(x, y, w, h, { fill, id }); g.circle(x, y, r, o); g.line(…); g.text(t, x, y, o) }, { width, height, onClick: (s, ctx, ev) => ev.hit, onHover }); g.theme has ink, muted, line, accent, ok, warn, alert.
Search: searchBox facets results({ from, onSelect }) answer({ from, title }) chunk(id) hits(list) trace status dag({ all }) (live plan only unless all: true).
Any prop may be a lambda \`(s, ctx) => value\`; reading \`s.x\` makes the component re-render when x changes. State keys that are plan signals re-run the plan.
Lambda ctx: \`ctx.set(patch)\`, \`ctx.get(k)\`, \`ctx.submit(patch)\`, \`ctx.run(trigger)\`, \`ctx.rag.chunk(id)\`, \`ctx.rag.chunks(f)\`, \`ctx.rag.docs()\`, \`ctx.rag.result()\`, \`ctx.toast(msg)\`, \`ctx.ws\`, \`ctx.evals\`, \`ctx.lib.rag\`, \`ctx.lib.gui\` (e.g. \`ctx.lib.rag.sweep(ctx.ws, grid).run(ctx.evals)\`).

## kit (pipeline scripts)
\`kit.reports()\` the standard corpus (sections + BM25 'text' + terms 'meta'); extend it, it is an immutable spec. \`kit.goldens({ docOnly })\` the 12 cases for the bundled reports. \`kit.searchApp(plan, { title, facets, answer, placeholder })\` the standard search layout.
Scripts run with \`rag, gui, log, kit\` and return \`{ plan, evals, app, variants }\`. Presets end with \`const ops = []; // end ops\` so "Keep in script" can write workspace ops back.

## Agent scope only
- \`wb.presets\`: \`list()\`, \`current()\`, \`await text(id)\` (the script), \`await load(id)\` (replaces the editor and builds). \`await wb.build()\` = the Build button (runs deferred Claude build steps).
- \`wb\`: \`wb.files()\`, \`wb.addFile(name, text)\`, \`wb.removeFile(name)\`, \`wb.script.get()\`, \`wb.script.set(text)\`, \`await wb.script.build()\`, \`wb.plan\`, \`wb.evals\`, \`wb.variants\`, \`wb.result\` (last search), \`wb.lastEval\`, \`await wb.runEvals({ trigger })\`, \`wb.chunk(id)\`, \`wb.docs()\`, \`wb.state()\`.
- \`ui\`: \`ui.query(q, { submit })\`, \`ui.filters(obj)\`, \`ui.tab('dag'|'ir'|'trace'|'evals'|'history'|'corpus')\`, \`ui.select(nodeId)\`, \`ui.chunk(id)\`, \`await ui.app(app)\` (mount in the App tab), \`ui.toast(msg)\`.
- \`chat\`: \`chat.table(rows, { title, columns })\`, \`chat.chart(kind, data, opts)\`, \`chat.ui(app)\`, \`chat.hits(resultOrHits, { title })\`, \`chat.dag({ highlight })\`, \`chat.evals(report)\`, \`chat.note(md, tone)\`, \`chat.code(text, lang)\`, \`chat.md(text)\`, \`chat.download(name, data)\`.
- \`ref\`: \`ref.query(q)\`, \`ref.chunk(id)\`, \`ref.node(id)\`, \`ref.tab(name)\` return \`wb:\` links for markdown: \`[label](\${ref.chunk(id)})\`.
`;

  // ────────────────────────────────────────────────────────────── agent
  const TIERS = { quick: 'Fast', default: 'Balanced', complex: 'Deep' };
  const MODES = { auto: 'Auto', ask: 'Ask', readonly: 'Read-only' };
  const HISTORY = 16;
  const FRIENDLY = {
    cancelled: 'Stopped.', not_granted: 'Allow Claude for this page when asked, then send again.',
    rate_limited: 'Too many requests or a usage limit was reached. Try again later.', prompt_too_large: 'The conversation is too long. Start a new one.',
    refused: 'Claude declined this request. Rephrase it.', tools_unavailable: 'This view cannot run page tools, so the agent can only talk, not act.',
    sampling_disabled: 'Claude is not available for this account.', session_expired: 'Sign in again, then retry.',
  };

  class Agent {
    constructor(host) {
      this.host = host;           // { scope(), summary(), snapshot(), restore(s), mountGui(el, ir), renderDag(el, ir, o), onRef(href), store, sample, limits }
      this.settings = { tier: 'default', mode: 'auto', ...(host.settings || {}) };
      this.conv = this.newConv();
      this.convs = [];
      this.busy = false;
      this.snaps = new WeakMap();
      this.pending = new Map();
      this.mountedGui = new Map();
    }
    newConv() { return { id: 'c' + now().toString(36) + Math.random().toString(36).slice(2, 6), title: 'New conversation', created: now(), updated: now(), messages: [] }; }

    // ── ui
    mount(el) {
      this.el = el;
      el.innerHTML = '';
      const tierSeg = h('div', { class: 'seg small', role: 'group', 'aria-label': 'Model' }, Object.entries(TIERS).map(([k, v]) =>
        h('button', { type: 'button', 'aria-pressed': String(this.settings.tier === k), onclick: () => { this.settings.tier = k; this.saveSettings(); this.renderSettings(); } }, v)));
      const modeSeg = h('div', { class: 'seg small', role: 'group', 'aria-label': 'Run mode' }, Object.entries(MODES).map(([k, v]) =>
        h('button', { type: 'button', 'aria-pressed': String(this.settings.mode === k), onclick: () => { this.settings.mode = k; this.saveSettings(); this.renderSettings(); } }, v)));
      this.titleEl = h('div', { class: 'ag-title', text: this.conv.title });
      this.histEl = h('div', { class: 'ag-hist', hidden: true });
      this.logEl = h('div', { class: 'chat-log', role: 'log', 'aria-live': 'polite' });
      this.input = h('textarea', { id: 'agent-input', rows: 2, placeholder: 'Ask the agent to change, test or explain the pipeline…',
        onkeydown: e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); this.send(); } } });
      this.sendBtn = h('button', { type: 'button', class: 'btn primary', onclick: () => this.send() }, 'Send');
      this.settingsEl = h('div', { class: 'ag-settings' }, h('span', { class: 'ctl-l', text: 'Model' }), tierSeg, h('span', { class: 'ctl-l', text: 'Mode' }), modeSeg);
      el.append(
        h('div', { class: 'panel-h' }, h('span', { text: 'Agent' }), this.titleEl,
          h('span', { class: 'ag-head-btns' },
            h('button', { type: 'button', class: 'btn tiny', onclick: () => this.toggleHistory() }, 'History'),
            h('button', { type: 'button', class: 'btn tiny', onclick: () => this.newChat() }, 'New chat'))),
        this.settingsEl, this.histEl, this.logEl,
        h('div', { class: 'chat-in' }, this.input, this.sendBtn));
      this.logEl.addEventListener('click', e => {
        const a = e.target.closest('a.ref');
        if (a) { e.preventDefault(); this.host.onRef(a.dataset.ref); }
        const s = e.target.closest('[data-sug]');
        if (s) { this.input.value = s.dataset.sug; this.send(); }
      });
      this.paint();
      return this;
    }
    renderSettings() {
      const segs = this.settingsEl.querySelectorAll('.seg');
      [...segs[0].children].forEach((b, i) => b.setAttribute('aria-pressed', String(Object.keys(TIERS)[i] === this.settings.tier)));
      [...segs[1].children].forEach((b, i) => b.setAttribute('aria-pressed', String(Object.keys(MODES)[i] === this.settings.mode)));
    }
    saveSettings() { if (this.host.store) this.host.store.saveSettings({ agent: this.settings }).catch(() => {}); }
    async toggleHistory() {
      if (!this.histEl.hidden) { this.histEl.hidden = true; return; }
      this.histEl.hidden = false;
      this.histEl.innerHTML = '<div class="empty">Loading…</div>';
      const list = this.host.store ? await this.host.store.listConversations().catch(() => []) : [];
      this.histEl.innerHTML = '';
      if (!list.length) { this.histEl.append(h('div', { class: 'empty', text: 'Conversations you have here are saved and listed here.' })); return; }
      for (const c of list) this.histEl.append(h('button', { type: 'button', class: 'ag-hrow' + (c.id === this.conv.id ? ' on' : ''), onclick: async () => {
        const full = await this.host.store.loadConversation(c.id).catch(() => null);
        if (full) { this.conv = full; this.histEl.hidden = true; this.paint(); }
      } }, h('b', { text: c.title }), h('small', { text: new Date(c.updated).toLocaleString() + ` · ${c.n || 0} messages` })));
    }
    newChat() { if (this.busy) return; this.conv = this.newConv(); this.mountedGui.clear(); this.paint(); this.input.focus(); }

    paint() {
      if (!this.logEl) return;
      const stick = this.logEl.scrollHeight - this.logEl.scrollTop - this.logEl.clientHeight < 40;
      this.titleEl.textContent = this.conv.title;
      this.sendBtn.textContent = this.busy ? 'Stop' : 'Send';
      this.logEl.innerHTML = '';
      if (!this.conv.messages.length) this.logEl.append(this.emptyState());
      this.conv.messages.forEach((m, mi) => this.logEl.append(this.renderMsg(m, mi)));
      if (stick) this.logEl.scrollTop = this.logEl.scrollHeight;
    }
    emptyState() {
      const sug = [
        'Which golden questions does the current plan miss, and why? Show the ranks.',
        'Fix the eval-tool miss on a branch, compare it with main, and keep it if it helps.',
        'Build me a small app with sliders for the fuse weights that reruns the goldens live.',
        'Explain how the top result for my current query got its rank.',
        'Which presets would help me tune ranking? Load the best one and walk me through it.',
      ];
      return h('div', { class: 'ag-empty' },
        h('p', { text: 'The agent works on this workbench with one tool: it writes JavaScript against rag, ws, ui and chat, and you see each step. It can change the pipeline, run evals, and build small apps.' }),
        h('div', { class: 'chat-sug' }, sug.map(s => h('button', { type: 'button', class: 'chip', 'data-sug': s, text: s }))));
    }
    renderMsg(m, mi) {
      if (m.role === 'user') return h('div', { class: 'msg user' }, h('span', { class: 'who', text: 'You' }), h('div', { class: 'g-md', html: md(m.text) }));
      const el = h('div', { class: 'msg agent' + (m.error ? ' has-error' : '') }, h('span', { class: 'who' }, 'Agent', m.tier ? h('small', { text: ` · ${TIERS[m.tier] || m.tier} · ${MODES[m.mode] || m.mode}` }) : null));
      const parts = [...(m.parts || [])].sort((a, b) => a.at - b.at);
      let pos = 0;
      const text = m.text || '';
      for (const [pi, p] of parts.entries()) {
        const seg = text.slice(pos, p.at);
        if (seg.trim()) el.append(h('div', { class: 'g-md', html: md(seg, { cite: false }) }));
        pos = Math.max(pos, p.at);
        el.append(p.type === 'tool' ? this.renderStep(m, p, mi) : this.renderOut(p, `${mi}:${pi}`));
      }
      const rest = text.slice(pos);
      if (rest.trim()) el.append(h('div', { class: 'g-md', html: md(rest) }));
      if (m.pending && !rest.trim() && !(m.parts || []).some(p => p.status === 'run' || p.status === 'ask')) el.append(h('div', { class: 'thinking', text: 'Thinking…' }));
      if (m.error) el.append(h('div', { class: 'msg-err', text: '✗ ' + m.error }));
      const steps = (m.parts || []).filter(p => p.type === 'tool');
      if (!m.pending && steps.some(p => this.snaps.has(p) && !p.reverted)) {
        el.append(h('div', { class: 'msg-foot' }, h('span', { class: 'meta', text: `${steps.length} step${steps.length > 1 ? 's' : ''}${m.usage ? ` · ${(m.usage.ms / 1000).toFixed(1)} s` : ''}` }),
          h('button', { type: 'button', class: 'link muted', onclick: () => this.revert(m, steps.find(p => this.snaps.has(p) && !p.reverted)) }, 'Undo this answer')));
      }
      return el;
    }
    renderStep(m, p) {
      const status = { run: 'running', ok: 'ok', err: 'error', ask: 'waiting for you', declined: 'declined' }[p.status] || p.status;
      if (p.status === 'ask') {
        return h('div', { class: 'step ask' },
          h('div', { class: 'step-h' }, h('b', { text: 'Run this step?' }), h('span', { class: 'meta', text: 'Ask mode' })),
          h('pre', { class: 'step-code', text: p.code }),
          h('div', { class: 'step-btns' },
            h('button', { type: 'button', class: 'btn primary tiny', onclick: () => this.decide(p, 'run') }, 'Run'),
            h('button', { type: 'button', class: 'btn tiny', onclick: () => this.decide(p, 'all') }, 'Run all in this answer'),
            h('button', { type: 'button', class: 'btn tiny bad', onclick: () => this.decide(p, 'deny') }, 'Decline')));
      }
      const d = h('details', { class: 'step ' + p.status + (p.reverted ? ' reverted' : '') },
        h('summary', {}, h('span', { class: 'step-k', text: 'eval' }), h('span', { class: 'step-s', text: status }),
          h('span', { class: 'step-first', text: clip((p.code || '').split('\n').find(l => l.trim() && !l.trim().startsWith('//')) || '', 64) }),
          h('span', { class: 'meta', text: p.ms != null ? `${p.ms} ms` : '' })),
        h('pre', { class: 'step-code', text: p.code }),
        p.result != null ? h('pre', { class: 'step-res' + (p.status === 'err' ? ' bad' : ''), text: clip(p.result, 6000) }) : null,
        p.logs ? h('pre', { class: 'step-logs', text: p.logs }) : null,
        this.snaps.has(p) && !p.reverted && p.status !== 'run' ? h('button', { type: 'button', class: 'link muted', onclick: () => this.revert(m, p) }, 'Revert to before this step') : null,
        p.reverted ? h('div', { class: 'meta', text: 'Reverted: this step and everything after it.' }) : null);
      if (p.open || p.status === 'err' || p.status === 'run') d.open = true;
      d.addEventListener('toggle', () => { p.open = d.open; });
      return d;
    }
    renderOut(p, key) {
      const d = p.data || {};
      const box = h('div', { class: 'out out-' + p.kind });
      const title = d.title ? h('div', { class: 'out-h', text: d.title }) : '';
      try {
        switch (p.kind) {
          case 'md': box.append(h('div', { class: 'g-md', html: md(d.text, { cite: false }) })); break;
          case 'note': box.className += ' g-note ' + ({ ok: 'ok', warn: 'warn', bad: 'bad' }[d.tone] || 'sig'); box.innerHTML = md(d.text); break;
          case 'code': box.append(title, h('pre', { class: 'g-code', text: d.text })); break;
          case 'json': box.append(title, h('pre', { class: 'g-code', text: JSON.stringify(d.value, null, 2) })); break;
          case 'table': case 'chart': case 'ui': case 'hits': case 'evals': {
            const holder = h('div', { class: 'out-gui' });
            box.append(title, holder);
            const k = key + ':' + (d.ir && d.ir.digest || p.kind);
            let rt = this.mountedGui.get(k);
            if (rt && rt.el) holder.append(rt.el);
            else { const host = h('div'); holder.append(host); rt = this.host.mountGui(host, d.ir); this.mountedGui.set(k, rt); }
            break;
          }
          case 'dag': {
            const holder = h('div', { class: 'out-dag' });
            box.append(title, holder);
            requestAnimationFrame(() => this.host.renderDag(holder, d.ir, { compact: true, highlight: d.highlight }));
            break;
          }
          case 'download': box.append(h('div', { class: 'out-dl' }, h('span', { text: `${d.name} · ${(d.size / 1024).toFixed(1)} KB` }),
            h('button', { type: 'button', class: 'btn tiny', onclick: () => this.host.download(d.name, d.data) }, 'Save'))); break;
          default: box.append(h('pre', { class: 'g-code', text: JSON.stringify(d) }));
        }
      } catch (e) { box.append(h('div', { class: 'g-err-inline', text: '✗ ' + e.message })); }
      return box;
    }

    // ── conversation
    async send() {
      if (this.busy) { if (this.ctl) this.ctl.abort(); return; }
      const text = this.input.value.trim();
      if (!text) return;
      this.input.value = '';
      await this.sendCore(text);
    }
    instructions() {
      const s = this.host.summary();
      return `You are the agent inside a RAG workbench: a page where a person builds and tunes search over a corpus of markdown reports. You act only through the eval tool, which runs JavaScript in the page.

${DOCS}

## How to work
- Prefer one eval that looks, acts, shows and returns a small answer. Return specific small values (a few rows, ids, numbers), never whole objects. Read errors: they name the right call.
- Change the running pipeline with ws.apply(ops) (it is undoable and shows in History). Edit the script (wb.script.set + build) only when the person asks for a code change.
- Try risky changes on a branch (ws.branch), measure with an eval set, show the comparison, then merge if it helps. Say what changed and the measured effect.
- Put evidence on screen: chat.table, chat.chart, chat.hits, chat.evals, chat.dag, ui.query, ui.tab. Build an app with gui when the person wants something interactive; mount it with chat.ui(app) or ui.app(app).
- In your text, link what you talk about with ref: [the section](wb:chunk?id=…), [node](wb:node?id=…), [query](wb:query?q=…). Keep prose short; the steps are visible.
- Cost: nodes marked submit (rewrite.llm, rerank.llm, answer, agentic, enrich.llm, judges) spend the person's Claude usage. Run evals with trigger 'change' (lexical, free) unless asked. Ask before building enrich.llm over the whole corpus (one call per chunk).
- Run mode is ${MODES[this.settings.mode]}.${this.settings.mode === 'readonly' ? ' Mutating calls (ws.apply, merge, rebuild, wb.script.set, wb.addFile) throw; explain what you would change instead. Branches you create are fine to edit and measure.' : this.settings.mode === 'ask' ? ' Each eval waits for the person to approve it, so batch work into fewer, clearer steps.' : ' Every step is snapshotted and can be reverted by the person.'}

## Current state
${s}`;
    }
    turnsFor(text, upto) {
      const win = this.conv.messages.slice(0, upto).slice(-HISTORY), hist = [];
      for (const m of win) {
        if (m.role === 'user') hist.push({ role: 'user', content: m.text });
        else {
          let c = m.text || '';
          for (const p of (m.parts || []).filter(x => x.type === 'tool')) c += `\n[eval]\n${clip(p.code, 1500)}\n→ ${p.status}:\n${clip(p.result || '', 900)}` + (p.reverted ? '\n[the person reverted this step and everything after it]' : '');
          for (const p of (m.parts || []).filter(x => x.type === 'out')) c += `\n[shown: ${p.kind}${p.data && p.data.title ? ' "' + p.data.title + '"' : ''}]`;
          if (c.trim()) hist.push({ role: 'assistant', content: clip(c, 12000) });
        }
      }
      while (hist.length && hist[0].role !== 'user') hist.shift();
      const turns = [{ role: 'user', content: this.instructions() }, ...hist, { role: 'user', content: text }];
      while (JSON.stringify(turns).length > 230000 && turns.length > 3) turns.splice(1, 1);
      return turns;
    }
    async sendCore(text) {
      const conv = this.conv;
      if (!conv.messages.length) conv.title = clip(text, 60);
      const upto = conv.messages.length;
      conv.messages.push({ role: 'user', text, at: now() });
      const msg = { role: 'assistant', text: '', parts: [], pending: true, at: now(), tier: this.settings.tier, mode: this.settings.mode };
      conv.messages.push(msg);
      this.busy = true;
      this.paint();
      const ctl = this.ctl = new AbortController();
      const sample = this.host.sample;
      try {
        if (!sample) throw { code: 'sampling_disabled' };
        const limits = this.host.limits || {};
        const turns = this.turnsFor(text, upto);
        const opts = { signal: ctl.signal, modelTier: this.settings.tier, onText: ({ text: t }) => { msg.text = t; this.paintSoon(); } };
        if (limits.tools) opts.tools = [this.evalTool(msg)]; else opts.cache = false;
        const r = await sample(turns, opts);
        msg.text = r.text;
        if (r.truncated) msg.error = 'The answer was cut short. Ask for less at a time.';
      } catch (e) {
        msg.error = FRIENDLY[e && e.code] || (e && e.message) || `Something went wrong (${e && e.code}). Try again.`;
        if (e && e.code === 'cancelled') msg.error = 'Stopped.';
        if (e && e.text != null && e.code !== 'refused') msg.text = e.text;
      } finally {
        msg.pending = false;
        msg.usage = { steps: msg.parts.filter(p => p.type === 'tool').length, ms: now() - msg.at };
        conv.updated = now();
        this.busy = false;
        this.paint();
        this.save();
      }
    }
    paintSoon() { if (this.raf) return; this.raf = requestAnimationFrame(() => { this.raf = null; this.paint(); }); }
    save() {
      if (!this.host.store) return;
      const conv = JSON.parse(JSON.stringify(this.conv, (k, v) => (k === 'result' && typeof v === 'string' ? clip(v, 2500) : k === 'logs' && typeof v === 'string' ? clip(v, 1000) : v)));
      this.host.store.saveConversation(conv).catch(e => this.host.toast && this.host.toast('Could not save the conversation: ' + (e.message || e.code)));
    }

    // ── the eval tool
    evalTool(msg) {
      return {
        name: 'eval',
        description: 'Run JavaScript in the workbench page as the body of an async function. Scope: rag, gui, wb, ws, ui, chat, ref, log, console. Use await; end with `return <small value>`. Returns "ok" plus a YAML summary of the returned value (strings verbatim) and captured logs, truncated near 9,000 characters; or "error" with the message and line.',
        inputSchema: { type: 'object', properties: { code: { type: 'string', description: 'JavaScript statements; use await; end with return <small value>' } }, required: ['code'] },
        execute: (input, ctx) => this.runEval(String((input && input.code) || ''), ctx && ctx.signal, msg),
      };
    }
    decide(part, choice) { const f = this.pending.get(part); if (f) { this.pending.delete(part); f(choice); } }
    askApproval(part, signal) {
      part.status = 'ask';
      this.paint();
      return new Promise(resolve => {
        this.pending.set(part, resolve);
        if (signal) signal.addEventListener('abort', () => this.decide(part, 'deny'), { once: true });
      });
    }
    async runEval(code, signal, msg) {
      const part = { type: 'tool', code, status: 'run', at: msg.text.length, t0: now() };
      msg.parts.push(part);
      this.paint();
      const mode = msg.mode || this.settings.mode;
      if (mode === 'ask' && !msg.approveAll) {
        const d = await this.askApproval(part, signal);
        if (d === 'deny') { Object.assign(part, { status: 'declined', result: 'declined: true', ms: 0 }); this.paint(); return 'The person declined this step. Ask what they would prefer, or explain without running it.'; }
        if (d === 'all') msg.approveAll = true;
        part.status = 'run';
        this.paint();
      }
      if (mode !== 'readonly') this.snaps.set(part, this.host.snapshot());
      const logs = [];
      const log = (...a) => logs.push(a.map(x => (typeof x === 'string' ? x : toYAML(x))).join(' '));
      const cons = { log, info: log, warn: (...a) => log('warn:', ...a), error: (...a) => log('error:', ...a) };
      const chat = this.makeChat(msg);
      const scope = this.host.scope({ readonly: mode === 'readonly', chat, log });
      const names = ['rag', 'gui', 'wb', 'ws', 'ui', 'chat', 'ref', 'log', 'console'];
      let ok = true, out;
      try {
        const fn = new Function(...names, `"use strict";return (async () => {\n${code}\n})();\n//# sourceURL=agent-eval.js`);
        const res = await fn(scope.rag, scope.gui, scope.wb, scope.ws, scope.ui, chat, scope.ref, log, cons);
        out = res === undefined ? '# returned undefined' : typeof res === 'string' ? res : toYAML(res);
      } catch (e) {
        ok = false;
        const line = (String(e && e.stack).match(/agent-eval\.js:(\d+)/) || [])[1];
        out = toYAML({ error: `${(e && e.name) || 'Error'}: ${(e && (e.message || e.code)) || e}`, ...(line ? { line: line - (this.host.lineOffset ?? 2) } : {}) });
      }
      if (out.length > 9000) out = out.slice(0, 9000) + `\n# … truncated: ${out.length - 9000} more characters`;
      Object.assign(part, { status: ok ? 'ok' : 'err', ms: now() - part.t0, result: out, logs: clip(logs.join('\n'), 6000) });
      if (this.host.afterStep) this.host.afterStep();
      this.paint();
      if (signal && signal.aborted) throw new Error('stopped');
      return `${ok ? 'ok' : 'error'}\n${out}${part.logs ? `\nlogs:\n${part.logs}` : ''}`.slice(0, 28000);
    }
    async revert(msg, part) {
      const steps = msg.parts.filter(p => p.type === 'tool');
      const k = steps.indexOf(part);
      if (k < 0 || !this.snaps.has(part)) return;
      await this.host.restore(this.snaps.get(part));
      for (const p of steps.slice(k)) p.reverted = true;
      this.host.toast && this.host.toast('Reverted to before that step');
      this.paint();
      this.save();
    }
    makeChat(msg) {
      const self = this;
      const push = (kind, data) => { msg.parts.push({ type: 'out', kind, data, at: msg.text.length }); self.paintSoon(); return `shown: ${kind}`; };
      const guiIr = app => (app && app.kind === 'gui' ? app : app.compile());
      return {
        md: text => push('md', { text: String(text) }),
        note: (text, tone = 'info') => push('note', { text: String(text), tone }),
        code: (text, lang) => push('code', { text: String(text), lang }),
        json: (value, o = {}) => push('json', { value: plain(value), title: o.title }),
        table: (rows, o = {}) => {
          rows = Array.isArray(rows) ? rows.map(r => plain(r, 2)) : Object.entries(rows || {}).map(([k, v]) => ({ key: k, value: plain(v, 2) }));
          const ir = root.gui.app('table').view(v => v.table(rows, { columns: o.columns, max: o.max || 100 })).compile();
          return push('table', { ir, title: o.title, n: rows.length });
        },
        chart: (kind, data, o = {}) => {
          const ir = root.gui.app('chart').view(v => v.chart(kind, plain(data, 4), { xLabel: o.xLabel, yLabel: o.yLabel, yMin: o.yMin, yMax: o.yMax })).compile();
          return push('chart', { ir, title: o.title });
        },
        ui: (app, o = {}) => { const ir = guiIr(app); return push('ui', { ir, title: o.title || ir.title }); },
        hits: (x, o = {}) => {
          const list = Array.isArray(x) ? x : x && x.hits ? x.hits : [];
          const ir = root.gui.app('hits').view(v => v.hits(list.slice(0, o.k || 8).map(hh => (typeof hh === 'string' ? { id: hh } : { id: hh.id, score: hh.score, reason: hh.reason })))).compile();
          return push('hits', { ir, title: o.title || (x && x.signals ? `Results for “${clip(x.signals.q, 60)}”` : 'Results') });
        },
        evals: (report, o = {}) => {
          const rows = report.table ? report.table() : report.cases || [];
          const s = report.summary || {};
          const ir = root.gui.app('evals').view(v => v
            .stats(Object.entries(s).filter(([k]) => !['n', 'calls'].includes(k)).map(([label, value]) => ({ label, value })))
            .table(rows, { max: 60 })).compile();
          return push('evals', { ir, title: o.title || `Evals · ${report.id || ''} on ${report.target || ''}` });
        },
        dag: (o = {}) => push('dag', { ir: JSON.parse(JSON.stringify((o.ir || self.host.currentIr()))), highlight: o.highlight || [], title: o.title }),
        download: (name, data) => { const s = typeof data === 'string' ? data : JSON.stringify(data, null, 2); return push('download', { name, data: s, size: s.length }); },
      };
    }
  }

  root.agentKit = { Agent, toYAML, plain, DOCS };
})(typeof globalThis !== 'undefined' ? globalThis : this);
