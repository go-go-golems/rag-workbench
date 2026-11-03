/* app.js — the workbench shell: editor, search surface, inspector, agent host. */
(function () {
  'use strict';
  const { h, esc, md } = gui;
  const $ = s => document.querySelector(s);
  const clip = rag.util.clip;
  const W = {
    bundled: [], uploaded: [], disabled: new Set(), built: null, ws: null, engine: null, searchRt: null, appRt: null, appIr: null,
    lastResult: null, lastEval: null, lastCompare: null, selectedNode: null, traceNode: null, explainId: null, corpusDoc: null,
    inspTab: 'dag', ideTab: 'code', surface: 'search', caps: {}, buildSeq: 0, scriptBuiltText: null, lastArt: null, lastBuild: null,
    lineOffset: 2, evalProgress: null, presets: [], preset: null, presetText: null, stash: null,
  };
  window.__wb = W;

  // ────────────────────────────────────────────────────────────── small ui
  const time = () => new Date().toTimeString().slice(0, 8);
  function clog(kind, ...args) {
    const box = $('#console');
    if (!box) return;
    const text = args.map(a => (typeof a === 'string' ? a : agentKit.toYAML(a))).join(' ');
    box.append(h('div', { class: 'cl ' + (kind || '') }, h('span', { class: 'ct', text: time() }), text));
    while (box.children.length > 300) box.firstChild.remove();
    box.scrollTop = box.scrollHeight;
  }
  let toastT;
  function toast(msg, kind = '') {
    document.querySelectorAll('.toast').forEach(t => t.remove());
    const t = h('div', { class: 'toast ' + kind, role: 'status', text: msg });
    $('#app').append(t);
    clearTimeout(toastT);
    toastT = setTimeout(() => t.remove(), 2600);
  }
  function setStatus(kind, text) { const s = $('#status'); s.className = 'status ' + kind; s.textContent = text; s.title = text; }
  async function copy(text) {
    try { await navigator.clipboard.writeText(text); toast('Copied', 'ok'); } catch (_) {
      const ta = h('textarea', {}, text); document.body.append(ta); ta.select(); try { document.execCommand('copy'); toast('Copied', 'ok'); } catch (e) { toast('Copy is blocked here; select the text instead', 'bad'); } ta.remove();
    }
  }
  async function download(name, data) {
    const dl = W.caps.downloads;
    if (!dl) { toast('Saving files is not available in this view', 'bad'); return; }
    try { await dl.save({ filename: name, data: typeof data === 'string' ? data : JSON.stringify(data, null, 2) }); toast('Saved ' + name, 'ok'); } catch (e) { if (e.code !== 'declined') toast('Could not save: ' + (e.message || e.code), 'bad'); }
  }
  const art = () => (W.engine && W.ws && W.engine.artifacts(W.ws.ir)) || W.lastArt;
  const activeFiles = () => [...W.bundled, ...W.uploaded].filter(f => !W.disabled.has(f.name));

  // ────────────────────────────────────────────────────────────── store (db)
  const store = {
    ready: false, root: null, state: {}, chains: new Map(),
    init() {
      if (!W.caps.db || !W.caps.uid) return false;
      try { this.root = W.caps.db.doc(`data/users/${W.caps.uid}/ragwb`); this.ready = true; } catch (e) { clog('error', 'storage: ' + e.message); }
      return this.ready;
    },
    write(ref, fn) {
      const k = ref.path;
      const next = (this.chains.get(k) || Promise.resolve()).then(fn, fn);
      this.chains.set(k, next.catch(() => {}));
      return next;
    },
    async load() { const s = await this.root.get(); this.state = s.exists ? JSON.parse(JSON.stringify(s.data())) : {}; return this.state; },
    saveState(patch) { if (!this.ready) return Promise.resolve(); Object.assign(this.state, patch); return this.write(this.root, () => this.root.set({ ...this.state, updated: Date.now() })); },
    saveSettings(p) { return this.saveState(p); },
    async listConversations() { return (this.state.convIndex || []).slice().sort((a, b) => b.updated - a.updated); },
    async loadConversation(id) { const s = await this.root.collection('transcripts').doc(id).get(); return s.exists ? JSON.parse(JSON.stringify(s.data())) : null; },
    async saveConversation(conv) {
      if (!this.ready) return;
      conv = fitConversation(conv);
      const ref = this.root.collection('transcripts').doc(conv.id);
      await this.write(ref, () => ref.set(conv));
      const idx = (this.state.convIndex || []).filter(c => c.id !== conv.id);
      idx.unshift({ id: conv.id, title: conv.title, updated: conv.updated, n: conv.messages.length });
      await this.saveState({ convIndex: idx.slice(0, 60) });
    },
    async files() { const q = await this.root.collection('files').get(); return q.docs.map(d => JSON.parse(JSON.stringify(d.data()))); },
    fileRef(name) { return this.root.collection('files').doc(rag.util.slug(name).slice(0, 60) + '-' + rag.util.hash(name).slice(0, 6)); },
    addFile(f) { if (!this.ready) return Promise.resolve(); const ref = this.fileRef(f.name); return this.write(ref, () => ref.set({ name: f.name, text: f.text, added: Date.now() })); },
    removeFile(name) { if (!this.ready) return Promise.resolve(); const ref = this.fileRef(name); return this.write(ref, () => ref.delete()); },
    cache: {
      get: async key => { if (!store.ready) return undefined; const s = await store.root.collection('cache').doc(key.replace(/[^\w:.-]/g, '_')).get(); return s.exists ? JSON.parse(JSON.stringify(s.data().v)) : undefined; },
      set: async (key, v) => { if (!store.ready) return; const ref = store.root.collection('cache').doc(key.replace(/[^\w:.-]/g, '_')); return store.write(ref, () => ref.set({ v, at: Date.now() })); },
    },
  };
  function fitConversation(conv) {
    let s = JSON.stringify(conv);
    if (s.length < 230000) return conv;
    conv = JSON.parse(s);
    for (const m of conv.messages) for (const p of m.parts || []) if (p.type === 'out' && p.data && p.data.ir && JSON.stringify(p.data.ir).length > 20000) { p.data = { title: (p.data.title || p.kind) + ' (too large to keep)' }; p.kind = 'md'; p.data.text = '_' + p.data.title + '_'; }
    while (JSON.stringify(conv).length > 230000 && conv.messages.length > 2) conv.messages.splice(0, 2);
    return conv;
  }

  // ────────────────────────────────────────────────────────────── editor
  const src = () => $('#src');
  function highlight(text) {
    const re = /(\/\/[^\n]*|\/\*[\s\S]*?\*\/)|(`(?:\\[\s\S]|[^`\\])*`|'(?:\\.|[^'\\\n])*'|"(?:\\.|[^"\\\n])*")|(\b\d+(?:\.\d+)?\b)|\b(const|let|var|return|async|await|function|new|if|else|for|of|in|true|false|null|undefined|typeof)\b|\b(rag|gui|log)\b|(\.[A-Za-z_$][\w$]*)(?=\s*\()/g;
    let out = '', last = 0, m;
    while ((m = re.exec(text))) {
      out += esc(text.slice(last, m.index));
      const cls = m[1] ? 'hl-c' : m[2] ? 'hl-s' : m[3] ? 'hl-n' : m[4] ? 'hl-k' : m[5] ? 'hl-b' : 'hl-f';
      out += `<span class="${cls}">${esc(m[0])}</span>`;
      last = re.lastIndex;
    }
    return out + esc(text.slice(last)) + '\n';
  }
  let errLines = new Set();
  function paintEditor() {
    const t = src().value;
    $('#hl').innerHTML = highlight(t);
    const n = t.split('\n').length;
    $('#gutter').innerHTML = Array.from({ length: n }, (_, i) => (errLines.has(i + 1) ? `<b>${i + 1}</b>` : String(i + 1))).join('\n');
    syncScroll();
    $('#code-dot').hidden = t === W.scriptBuiltText;
    const mod = $('#preset-mod');
    if (mod) mod.hidden = !W.preset || t === W.presetText;
    $('#script-meta').textContent = `${n} lines`;
  }
  function syncScroll() {
    const ta = src();
    $('#hl').style.transform = `translate(${-ta.scrollLeft}px, ${-ta.scrollTop}px)`;
    $('#gutter').style.transform = `translateY(${-ta.scrollTop}px)`;
  }
  function setEditor(text) { src().value = text; paintEditor(); }
  let autoT;
  function wireEditor() {
    const ta = src();
    ta.addEventListener('input', () => {
      requestAnimationFrame(paintEditor);
      clearTimeout(autoT);
      if ($('#auto-build').checked) autoT = setTimeout(() => buildScript({ auto: true }), 900);
    });
    ta.addEventListener('scroll', syncScroll);
    ta.addEventListener('keydown', e => {
      if ((e.metaKey || e.ctrlKey) && (e.key === 'Enter' || e.key === 's')) { e.preventDefault(); buildScript(); return; }
      if (e.key === 'Tab' && !e.metaKey && !e.ctrlKey) {
        e.preventDefault();
        const s = ta.selectionStart, en = ta.selectionEnd;
        ta.setRangeText('  ', s, en, 'end');
        ta.dispatchEvent(new Event('input'));
      }
    });
  }
  function showErrors(list) {
    const box = $('#errors');
    errLines = new Set();
    box.innerHTML = '';
    if (!list || !list.length) { box.hidden = true; paintEditor(); return; }
    for (const e of list) { box.append(h('div', { class: 'err', text: '✗ ' + e.text })); if (e.line) errLines.add(e.line); }
    box.hidden = false;
    paintEditor();
  }
  function lineOf(e, file) {
    const m = String(e && e.stack).match(new RegExp(file.replace('.', '\\.') + ':(\\d+)'));
    return m ? +m[1] - W.lineOffset : null;
  }
  async function measureOffset() {
    try {
      await new Function('rag', 'gui', 'log', 'kit', '"use strict";return (async () => {\nthrow new Error("probe")\n})();\n//# sourceURL=probe.js')();
    } catch (e) { const m = String(e.stack).match(/probe\.js:(\d+)/); if (m) W.lineOffset = +m[1] - 1; }
  }

  // ────────────────────────────────────────────────────────────── build
  let building = null;
  async function buildScript(opts = {}) {
    if (building) { await building.catch(() => {}); }
    building = buildScriptInner(opts);
    try { return await building; } finally { building = null; }
  }
  async function buildScriptInner(opts) {
    const text = src().value;
    const seq = ++W.buildSeq;
    let out;
    const t0 = performance.now();
    try {
      const fn = new Function('rag', 'gui', 'log', 'kit', `"use strict";return (async () => {\n${text}\n})();\n//# sourceURL=pipeline.js`);
      out = await fn(rag, gui, (...a) => clog('', ...a), kit);
      if (!out || !out.plan) throw new Error('pipeline.js should end with return { plan, evals, app, variants }');
    } catch (e) {
      const line = lineOf(e, 'pipeline.js');
      showErrors([{ text: `${e.name || 'Error'}: ${e.message}${line ? ` (line ${line})` : ''}`, line }]);
      setStatus('bad', 'pipeline.js failed · the last good build stays up');
      if (!opts.auto) clog('error', e.message);
      return { ok: false, error: e.message, line };
    }
    if (seq !== W.buildSeq) return { ok: false, error: 'superseded' };
    let ir, appIr = null;
    const errors = [];
    try { ir = rag.compile(out.plan); } catch (e) {
      const probs = (e.detail && e.detail.problems) || [e.message];
      showErrors(probs.map(p => ({ text: p })));
      setStatus('bad', 'The plan does not compile · the last good build stays up');
      return { ok: false, error: e.message, problems: probs };
    }
    const variants = [];
    for (const v of out.variants || []) {
      try { variants.push({ id: v.id, ir: rag.compile(v) }); } catch (e) { errors.push({ text: `variant '${v.id}': ${e.message}` }); }
    }
    try { appIr = out.app ? out.app.compile() : null; } catch (e) { errors.push({ text: 'app: ' + e.message }); }
    showErrors(errors);
    W.built = { plan: out.plan, evals: out.evals || null, variants, appIr, ir, ops: out.plan.st.ops || [], ms: performance.now() - t0 };
    W.scriptBuiltText = text;
    if (!W.ws) { W.ws = rag.workspace(ir, { engine: W.engine }); W.ws.on(onWsChange); }
    else W.ws.reset(ir, 'build from pipeline.js');
    paintEditor();
    const rep = await rebuildArtifacts();
    if (!rep.ok) return { ok: false, errors: rep.errors };
    mountSearch(appIr);
    if (store.ready && !opts.restoring) store.saveState({ preset: W.preset ? W.preset.id : null, script: text === W.presetText ? '' : text });
    renderInspector();
    updateTop();
    clog('ok', `built ${ir.id}: ${ir.nodes.length} nodes · ${rep.ms} ms`);
    return { ok: true, nodes: ir.nodes.length, ms: rep.ms, errors: errors.map(e => e.text) };
  }
  async function rebuildArtifacts() {
    setStatus('busy', 'Building…');
    const rep = await W.engine.build(W.ws.ir, { onProgress: p => setStatus('busy', `Building ${p.node} ${p.done}/${p.total}`) });
    W.lastBuild = rep;
    if (!rep.ok) {
      setStatus('bad', 'Build failed: ' + clip(rep.errors[0] || '', 120));
      showErrors(rep.errors.map(t => ({ text: t })));
      return rep;
    }
    W.lastArt = W.engine.artifacts(W.ws.ir);
    const a = W.lastArt;
    const pend = W.engine.pending(W.ws.ir);
    if (pend.length) setStatus('busy', pend.map(x => `${x.node}: ${x.items ?? 'all'} items wait for Claude`).join(' · ') + ' · press Build to spend those calls');
    else setStatus('ok', `Built · ${a.docs.size} docs · ${a.chunks.size} chunks · ${Math.round(rep.ms)} ms`);
    updateTop();
    return rep;
  }
  let wsT;
  function onWsChange(e) {
    updateTop();
    if (e.type === 'build') return;
    clearTimeout(wsT);
    wsT = setTimeout(async () => {
      if (W.engine.dirty(W.ws.ir).length) await rebuildArtifacts();
      if (W.searchRt) W.searchRt.runNow('change');
      renderInspector();
    }, 30);
  }
  // Build: rebuild from the script if it changed, then run any build steps that wait for Claude calls
  async function buildButton() {
    if (src().value !== W.scriptBuiltText) { const b = await buildScript(); if (!b.ok) return b; }
    const pend = W.engine.pending(W.ws.ir);
    if (!pend.length && !W.engine.dirty(W.ws.ir).length) { if (src().value === W.scriptBuiltText) toast('Up to date', 'ok'); return { ok: true }; }
    if (pend.length && !W.caps.sample) { toast('Claude is not available in this view, so those build steps cannot run', 'bad'); return { ok: false }; }
    setStatus('busy', pend.length ? `Building with Claude: ${pend.map(x => x.node).join(', ')}…` : 'Building…');
    const r = await W.ws.rebuild({ calls: true, onProgress: p => setStatus('busy', `Building ${p.node} ${p.done}/${p.total}`) });
    if (r.ok) { W.lastArt = W.engine.artifacts(W.ws.ir); setStatus('ok', `Built · ${W.lastArt.chunks.size} chunks · ${Math.round(r.ms)} ms${r.calls ? ` · ${r.calls} Claude calls` : ''}`); if (W.searchRt) W.searchRt.runNow('change'); }
    else setStatus('bad', 'Build failed: ' + r.errors[0]);
    renderInspector();
    return r;
  }

  // ────────────────────────────────────────────────────────────── presets
  async function loadPresetList() {
    W.presets = await fetch('presets/index.json').then(r => r.json()).catch(() => []);
    renderPresetMenu();
  }
  async function fetchPreset(p) {
    const r = await fetch('presets/' + p.file);
    if (!r.ok) throw new Error(`could not load preset ${p.file}`);
    return r.text();
  }
  function findPreset(id) {
    const p = W.presets.find(x => x.id === id);
    if (!p) throw new Error(`no preset '${id}'. Presets: ${W.presets.map(x => x.id).join(', ')}`);
    return p;
  }
  async function loadPreset(id, opts = {}) {
    const p = findPreset(id);
    const text = await fetchPreset(p);
    const cur = src().value;
    if (!opts.silent && cur.trim() && cur !== W.presetText) W.stash = { text: cur, label: W.preset ? W.preset.title : 'your script' };
    W.preset = p;
    W.presetText = text;
    W.freshMount = true;
    setEditor(opts.script || text);
    updatePresetHead();
    closePresetMenu();
    if (opts.build === false) return { ok: true };
    const r = await buildScript({ restoring: !!opts.restoring });
    if (r.ok && !opts.restoring) toast(`Loaded “${p.title}”`, 'ok');
    return r;
  }
  async function restoreStash() {
    if (!W.stash) return;
    const st = W.stash;
    W.stash = { text: src().value, label: W.preset ? W.preset.title : 'the preset' };
    setEditor(st.text);
    updatePresetHead();
    closePresetMenu();
    await buildScript();
  }
  function updatePresetHead() {
    const p = W.preset;
    const i = p ? W.presets.indexOf(p) : -1;
    $('#preset-k').textContent = p ? `Preset ${String(i + 1).padStart(2, '0')} of ${W.presets.length} · ${p.group}` : 'Preset';
    $('#preset-name').textContent = p ? p.title : 'Your script';
    $('#preset-mod').hidden = !p || src().value === W.presetText;
    const llm = p && p.llm === 'build' ? ' <b>Build spends Claude calls.</b>' : p && p.llm === 'enter' ? ' <b>Enter spends Claude calls.</b>' : '';
    $('#preset-desc').innerHTML = p ? esc(p.blurb) + llm : 'A pipeline you wrote. Pick a preset to start from one of the examples.';
    renderPresetMenu();
  }
  function renderPresetMenu() {
    const list = $('#preset-list');
    if (!list) return;
    const f = ($('#preset-filter').value || '').toLowerCase().trim();
    list.innerHTML = '';
    if (W.stash) list.append(h('button', { type: 'button', class: 'pm-stash', onclick: restoreStash }, `↺ Back to your edits of “${clip(W.stash.label, 40)}”`));
    const groups = new Map();
    W.presets.forEach((p, i) => {
      if (f && !`${p.title} ${p.blurb} ${p.group} ${p.id}`.toLowerCase().includes(f)) return;
      if (!groups.has(p.group)) groups.set(p.group, []);
      groups.get(p.group).push([p, i]);
    });
    for (const [g, items] of groups) {
      list.append(h('div', { class: 'pm-group' }, h('div', { class: 'pm-h' }, h('span', { text: g }), h('span', { class: 'meta', text: String(items.length) })),
        items.map(([p, i]) => h('button', { type: 'button', role: 'menuitem', class: 'pm-item' + (W.preset === p ? ' on' : ''), onclick: () => loadPreset(p.id).catch(e => toast(e.message, 'bad')) },
          h('span', { class: 'pm-n', text: String(i + 1).padStart(2, '0') }),
          h('span', { class: 'pm-t' }, h('b', { text: p.title }), h('span', { text: p.blurb })),
          h('span', { class: 'pm-r' }, h('span', { class: 'cx', title: `complexity ${p.level} of 3` }, [1, 2, 3].map(n => h('i', { class: n <= p.level ? 'f' : '' }))),
            p.llm ? h('i', { class: 'tag llm', text: p.llm === 'build' ? 'build' : 'enter', title: p.llm === 'build' ? 'Build spends Claude calls' : 'Enter spends Claude calls' }) : null)))));
    }
    if (!groups.size) list.append(h('div', { class: 'pm-empty', text: 'No preset matches.' }));
  }
  function openPresetMenu() {
    $('#preset-menu').hidden = false;
    $('#preset-btn').setAttribute('aria-expanded', 'true');
    renderPresetMenu();
    $('#preset-filter').focus();
  }
  function closePresetMenu() { $('#preset-menu').hidden = true; $('#preset-btn').setAttribute('aria-expanded', 'false'); }
  function wirePresets() {
    $('#preset-btn').addEventListener('click', () => ($('#preset-menu').hidden ? openPresetMenu() : closePresetMenu()));
    $('#preset-close').addEventListener('click', closePresetMenu);
    $('#preset-filter').addEventListener('input', renderPresetMenu);
    $('#preset-menu').addEventListener('keydown', e => {
      if (e.key === 'Escape') { closePresetMenu(); $('#preset-btn').focus(); }
      if (e.key === 'Enter' && e.target.id === 'preset-filter') { const first = $('#preset-list .pm-item'); if (first) first.click(); }
    });
    const step = d => { if (!W.presets.length) return; const i = W.preset ? W.presets.indexOf(W.preset) : -1; const n = W.presets[(i + d + W.presets.length) % W.presets.length]; loadPreset(n.id).catch(e => toast(e.message, 'bad')); };
    $('#preset-prev').addEventListener('click', () => step(-1));
    $('#preset-next').addEventListener('click', () => step(1));
    document.addEventListener('click', e => { if (!$('#preset-menu').hidden && !e.target.closest('#preset-menu, #preset-btn')) closePresetMenu(); });
  }

  // ────────────────────────────────────────────────────────────── surfaces
  function guiHost(opts = {}) {
    return {
      rag: {
        session: () => W.ws.session(),
        ir: () => W.ws.ir,
        chunk: id => (art() && art().chunks.get(id)) || null,
        doc: id => (art() && art().docs.get(id)) || null,
        chunks: f => (art() ? [...art().chunks.values()].filter(f || (() => true)) : []),
        docs: () => (art() ? [...art().docs.values()] : []),
        neighbors: id => {
          const a = art(), c = a && a.chunks.get(id);
          if (!c) return {};
          const list = a.order.get(c.docId) || [], i = list.indexOf(id);
          return { prev: i > 0 ? list[i - 1] : null, next: i >= 0 && i < list.length - 1 ? list[i + 1] : null };
        },
      },
      ctx: { ws: W.ws, evals: W.built && W.built.evals, ui: uiApi, lib: { rag, gui } },
      toast, copy, download, log: (...a) => clog('', ...a),
      renderDag,
      openChunk: id => uiApi.chunk(id),
      inspectChunk: id => { W.explainId = id; setInspTab('trace'); setPanel('inspect'); },
      onResult: opts.main ? r => { W.lastResult = r; if (['trace', 'dag'].includes(W.inspTab)) renderInspector(); updateTop(); } : null,
      onError: e => clog('error', (e && e.message) || String(e)),
    };
  }
  function fallbackApp() {
    return gui.app('search').title('Search').use(W.built.plan).state({ selected: null })
      .view(v => v.row(r => r.col(c => c.searchBox('q').status().facets('filters').results(), { grow: 3 }).col(c => c.answer().chunk(s => s.selected), { grow: 2 }))).compile();
  }
  function mountSearch(appIr) {
    const el = $('#sheet-search');
    const keep = {};
    const fresh = W.freshMount;
    W.freshMount = false;
    if (W.searchRt) { if (!fresh) for (const k of ['q', 'k', 'filters', 'selected']) if (k in W.searchRt.state) keep[k] = W.searchRt.state[k]; W.searchRt.destroy(); }
    const ir = appIr || fallbackApp();
    W.searchRt = new gui.Runtime(ir, guiHost({ main: true }));
    Object.assign(W.searchRt.state, keep);
    W.searchRt.computeDerived();
    W.searchRt.mount(el);
    $('#surface-meta').textContent = ir.title;
  }
  function mountApp() {
    const el = $('#sheet-app');
    if (W.appRt) W.appRt.destroy();
    W.appRt = null;
    const btn = $('#app-tab-btn');
    if (!W.appIr) { el.innerHTML = ''; btn.disabled = true; if (W.surface === 'app') showSurface('search'); return; }
    btn.disabled = false;
    W.appRt = new gui.Runtime(W.appIr, guiHost());
    W.appRt.mount(el);
  }
  function showSurface(name) {
    W.surface = name;
    $('#sheet-search').hidden = name !== 'search';
    $('#sheet-app').hidden = name !== 'app';
    document.querySelectorAll('[data-surface]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.surface === name)));
    $('#surface-meta').textContent = name === 'app' && W.appIr ? W.appIr.title : W.searchRt ? W.searchRt.ir.title : '';
  }
  function updateTop() {
    if (!W.ws) return;
    $('#rev-readout').textContent = 'rev ' + W.ws.revision;
    $('#tb-rev').textContent = W.ws.revision;
    $('#tb-plan').textContent = W.ws.ir.id;
    const a = art();
    if (a) { $('#tb-chunks').textContent = a.chunks.size; $('#corpus-readout').textContent = `${activeFiles().length} files · ${a.docs.size} docs · ${a.chunks.size} chunks`; }
    $('#tb-build').textContent = W.lastBuild ? (W.lastBuild.ok ? Math.round(W.lastBuild.ms) + ' ms' : 'failed') : '—';
    $('#undo-btn').disabled = !W.ws.history.length || W.ws.history[W.ws.history.length - 1].ops[0].op === 'reset';
  }

  // ────────────────────────────────────────────────────────────── DAG
  function renderDag(el, ir, o = {}) {
    el.innerHTML = '';
    if (!ir || !ir.nodes) return;
    const compact = !!o.compact;
    const NW = compact ? 108 : 136, NH = compact ? 30 : 38, GX = compact ? 24 : 36, GY = compact ? 8 : 12, PAD = 12, TOP = compact ? 10 : 26;
    let nodes = ir.nodes;
    if (o.liveOnly) {
      const keep = new Set(ir.nodes.filter(n => n.phase !== 'build').map(n => n.id));
      for (const [a, b, k] of ir.edges) if (k === 'artifact' && keep.has(b)) keep.add(a);
      nodes = ir.nodes.filter(n => keep.has(n.id));
    }
    const byId = new Map(nodes.map(n => [n.id, n]));
    const preds = new Map(nodes.map(n => [n.id, []])), succs = new Map(nodes.map(n => [n.id, []]));
    for (const [a, b] of ir.edges) if (byId.has(a) && byId.has(b)) { preds.get(b).push(a); succs.get(a).push(b); }
    const layer = new Map();
    for (const n of nodes) layer.set(n.id, Math.max(0, ...preds.get(n.id).map(p => (layer.get(p) ?? 0) + 1)));
    for (const n of [...nodes].reverse()) {
      if (n.phase === 'build') continue;
      const s = succs.get(n.id);
      if (s.length) layer.set(n.id, Math.max(layer.get(n.id), Math.min(...s.map(x => layer.get(x))) - 1));
    }
    const cols = new Map();
    for (const n of nodes) { const l = layer.get(n.id); if (!cols.has(l)) cols.set(l, []); cols.get(l).push(n); }
    const pos = new Map();
    const order = { build: 0, signal: 1, live: 2 };
    const Ls = [...cols.keys()].sort((a, b) => a - b);
    for (const l of Ls) {
      const col = cols.get(l);
      const bary = n => { const ps = preds.get(n.id).filter(p => pos.has(p)); return ps.length ? ps.reduce((a, p) => a + pos.get(p), 0) / ps.length : 99; };
      col.sort((a, b) => order[a.phase] - order[b.phase] || bary(a) - bary(b));
      col.forEach((n, i) => pos.set(n.id, i));
    }
    const maxRows = Math.max(...[...cols.values()].map(c => c.length));
    const xy = id => ({ x: PAD + Ls.indexOf(layer.get(id)) * (NW + GX), y: TOP + pos.get(id) * (NH + GY) });
    const width = PAD * 2 + Ls.length * (NW + GX) - GX, height = TOP + maxRows * (NH + GY) - GY + PAD;
    const box = h('div', { class: 'dag', style: { width: width + 'px', height: height + 'px' } });
    const trace = new Map(((o.result && o.result.trace) || []).map(t => [t.node, t]));
    const dirty = new Set(o.dirty || []);
    const hl = new Set(o.highlight || []);
    let paths = '';
    for (const [a, b, kind] of ir.edges) {
      if (!byId.has(a) || !byId.has(b)) continue;
      const p1 = xy(a), p2 = xy(b);
      const x1 = p1.x + NW, y1 = p1.y + NH / 2, x2 = p2.x, y2 = p2.y + NH / 2, mx = (x1 + x2) / 2;
      const d = x2 > x1 ? `M${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}` : `M${x1} ${y1} C ${x1 + 30} ${y1}, ${x2 - 30} ${y2}, ${x2} ${y2}`;
      const cls = kind === 'signal' ? 'signal' : kind === 'artifact' ? 'artifact' : byId.get(a).phase === 'build' ? 'build' : 'data';
      const hot = o.selected && (a === o.selected || b === o.selected) ? ' hot' : '';
      paths += `<path class="${cls}${hot}" d="${d}"/>`;
      const ta = trace.get(a);
      if (kind === 'data' && ta && (ta.status === 'ran' || ta.status === 'cached') && ta.out > 0 && byId.get(a).phase === 'live') paths += `<path class="run" d="${d}"/>`;
    }
    box.innerHTML = `<svg width="${width}" height="${height}" aria-hidden="true">${paths}</svg>`;
    if (!compact) {
      const firstLive = Ls.findIndex(l => cols.get(l).some(n => n.phase === 'live'));
      box.append(h('div', { class: 'colh', style: { left: PAD + 'px' }, text: 'build · per corpus' }));
      if (firstLive > 0) box.append(h('div', { class: 'colh', style: { left: (PAD + firstLive * (NW + GX)) + 'px' }, text: 'live · per query' }));
    }
    for (const n of nodes) {
      const { x, y } = xy(n.id);
      const t = trace.get(n.id);
      const lamd = Object.values(n.p || {}).some(v => v && typeof v === 'object' && (v.$fn || v.$bake));
      const submit = n.phase === 'live' && rag.util.triggerOf(n) === 'submit';
      const cls = ['dn', n.phase, lamd ? 'lam' : '', t ? t.status : '', dirty.has(n.id) ? 'dirty' : '', o.selected === n.id ? 'on' : '', hl.has(n.id) ? 'hot' : ''].filter(Boolean).join(' ');
      const tags = h('span', { class: 'dn-tags' }, submit ? h('i', { class: 'tag llm', text: 'llm', title: 'runs on submit (spends Claude calls)' }) : null, lamd ? h('i', { class: 'tag lam', text: 'λ', title: 'has a lambda' }) : null);
      const stat = n.phase === 'build' && W.lastBuild && W.lastBuild.nodes[n.id] ? `${W.lastBuild.nodes[n.id].count}` : t ? (t.status === 'skipped' ? 'skip' : `${t.in}→${t.out}`) : '';
      const attrs = { class: cls, style: { left: x + 'px', top: y + 'px', width: NW + 'px', height: NH + 'px' }, title: `${n.id} · ${n.t}${t ? ` · ${t.status}${t.error ? ' ✗ ' + t.error : ''}` : ''}` };
      const kids = [h('b', { text: n.id }), !compact || n.phase !== 'signal' ? h('small', { text: n.t === 'signal' ? 'signal' : n.t }) : null, tags, stat && !compact ? h('span', { class: 'stat', text: stat }) : null];
      box.append(o.onSelect ? h('button', { type: 'button', ...attrs, onclick: () => o.onSelect(n.id) }, ...kids) : h('div', attrs, ...kids));
    }
    el.append(box);
  }

  // ────────────────────────────────────────────────────────────── inspector
  function setInspTab(name) {
    W.inspTab = name;
    document.querySelectorAll('[data-insp]').forEach(b => b.classList.toggle('on', b.dataset.insp === name));
    renderInspector();
  }
  function renderInspector() {
    const body = $('#insp-body');
    if (!body || !W.ws) return;
    const keepScroll = body.scrollTop;
    body.innerHTML = '';
    const R = { dag: renderDagTab, trace: renderTraceTab, evals: renderEvalsTab, history: renderHistoryTab, corpus: renderCorpusTab, ir: renderIrTab }[W.inspTab];
    try { R(body); } catch (e) { body.append(h('div', { class: 'pad g-err-inline', text: '✗ ' + e.message })); console.error(e); }
    body.scrollTop = keepScroll;
  }
  const fmtP = v => JSON.stringify(v, (k, x) => (x && typeof x === 'object' && x.$fn ? `λ ${x.$fn.length > 60 ? x.$fn.slice(0, 57) + '…' : x.$fn}` : x));
  function renderDagTab(body) {
    const ir = W.ws.ir;
    const wrap = h('div', { class: 'dagwrap' });
    const left = h('div', { style: { display: 'flex', flexDirection: 'column', minWidth: 0, minHeight: 0 } });
    const scroll = h('div', { class: 'dag-scroll', style: { flex: '1' } });
    left.append(scroll, h('div', { class: 'legend' },
      h('span', {}, h('i'), 'data'), h('span', {}, h('i', { class: 'build' }), 'build / artifact'), h('span', {}, h('i', { class: 'signal' }), 'signal read'), h('span', {}, h('i', { class: 'run' }), 'carried data last run'),
      h('span', {}, h('i', { class: 'tag llm', text: 'llm', style: { width: 'auto', border: '1px solid var(--warn)' } }), 'Enter only')));
    renderDag(scroll, ir, { result: W.lastResult, selected: W.selectedNode, dirty: W.engine.dirty(ir), onSelect: id => { W.selectedNode = W.selectedNode === id ? null : id; renderInspector(); } });
    const det = h('div', { class: 'node-detail' });
    const n = W.selectedNode && ir.node(W.selectedNode);
    if (!n) {
      det.append(h('h5', { class: 'sec' }, 'Build passes', h('span', { class: 'meta', text: W.lastBuild ? `${Math.round(W.lastBuild.ms)} ms · ${W.lastBuild.calls} calls` : '' })));
      const ol = h('ol', { class: 'passes' });
      for (const p of (W.lastBuild && W.lastBuild.passes) || []) ol.append(h('li', { class: p.reused ? 'reused' : '' }, h('b', { text: p.pass }), h('span', { text: p.what }), h('span', { class: 'ms', text: p.ms + ' ms' })));
      det.append(ol, h('h5', { class: 'sec' }, 'Graph'), h('div', { class: 'stats' },
        h('div', {}, h('b', { text: ir.nodes.length }), h('span', { text: 'nodes' })), h('div', {}, h('b', { text: ir.edges.length }), h('span', { text: 'edges' })),
        h('div', {}, h('b', { text: Object.keys(ir.signals).length }), h('span', { text: 'signals' })), h('div', {}, h('b', { text: W.ws.revision }), h('span', { text: 'revision' }))),
        h('p', { class: 'empty', text: 'Select a node to see its params, last run and lambda source. Params can be edited here; each edit is a workspace op you can undo.' }));
    } else renderNodeDetail(det, n);
    wrap.append(left, det);
    body.append(wrap);
  }
  function renderNodeDetail(det, n) {
    const t = W.lastResult && W.lastResult.trace.find(x => x.node === n.id);
    const b = W.lastBuild && W.lastBuild.nodes[n.id];
    det.append(h('div', { class: 'nd-name' }, n.id, h('span', { class: 'tag ' + (n.phase === 'build' ? 'build' : n.phase === 'live' ? 'live' : ''), text: n.phase }),
      n.phase === 'live' && rag.util.triggerOf(n) === 'submit' ? h('span', { class: 'tag llm', text: 'on enter' }) : null));
    det.append(h('div', { class: 'meta', text: `${n.t}${rag.util.describeNode(n) ? ' · ' + rag.util.describeNode(n) : ''}` }));
    det.append(h('div', { class: 'meta', text: `in: ${n.in.join(', ') || '—'}${n.uses && n.uses.length ? ' · uses: ' + n.uses.join(', ') : ''}${n.reads && n.reads.length ? ' · reads s.' + n.reads.join(', s.') : ''}` }));
    if (n.t !== 'signal') {
      const plainP = {}, lams = {};
      for (const [k, v] of Object.entries(n.p)) (v && typeof v === 'object' && (v.$fn || v.$bake) ? lams : plainP)[k] = v;
      det.append(h('h5', { class: 'sec' }, 'Params', h('span', { class: 'meta', text: 'edits apply as ops' })));
      const ta = h('textarea', { class: 'pj', id: 'node-params', spellcheck: 'false', text: JSON.stringify(plainP, null, 2) });
      det.append(ta, h('div', { class: 'row-btns' },
        h('button', { type: 'button', class: 'btn tiny primary', onclick: async () => {
          let next;
          try { next = JSON.parse(ta.value); } catch (e) { toast('Params are not valid JSON: ' + e.message, 'bad'); return; }
          const ops = [];
          for (const k of new Set([...Object.keys(plainP), ...Object.keys(next)])) if (JSON.stringify(plainP[k]) !== JSON.stringify(next[k])) ops.push(next[k] === undefined ? { op: 'unset', node: n.id, path: k } : { op: 'set', node: n.id, path: k, value: next[k] });
          if (!ops.length) { toast('No changes'); return; }
          try { const r = await W.ws.apply(ops, { by: 'user' }); toast(`rev ${r.revision}: ${ops.length} op${ops.length > 1 ? 's' : ''}${r.dirty.length ? ' · rebuilding ' + r.dirty.join(', ') : ''}`, 'ok'); } catch (e) { toast(e.message, 'bad'); }
        } }, 'Apply'),
        n.phase === 'live' ? h('button', { type: 'button', class: 'btn tiny', onclick: () => W.ws.apply([{ op: 'trigger', node: n.id, value: rag.util.triggerOf(n) === 'submit' ? 'change' : 'submit' }]).catch(e => toast(e.message, 'bad')) }, rag.util.triggerOf(n) === 'submit' ? 'Run on every keystroke' : 'Run on Enter only') : null,
        n.phase === 'live' ? h('button', { type: 'button', class: 'btn tiny bad', onclick: () => W.ws.apply([{ op: 'remove', node: n.id }]).then(() => { W.selectedNode = null; }).catch(e => toast(e.message, 'bad')) }, 'Remove') : null));
      for (const [k, v] of Object.entries(lams)) det.append(h('h5', { class: 'sec' }, `${k} · ${v.$bake ? 'bake' : n.phase === 'build' ? 'build λ' : 'live λ'}`), h('pre', { class: 'snip lam', text: v.$fn || v.$bake }));
    }
    if (b) det.append(h('h5', { class: 'sec' }, 'Last build'), h('div', { class: 'stats' },
      h('div', {}, h('b', { text: b.count }), h('span', { text: 'items' })), h('div', {}, h('b', { text: b.reused ? 'reused' : b.ms + ' ms' }), h('span', { text: 'cost' })),
      h('div', {}, h('b', { text: b.computed + b.cached }), h('span', { text: 'mapped' })), h('div', {}, h('b', { text: b.calls }), h('span', { text: 'calls' }))),
      h('div', { class: 'meta', text: 'digest ' + b.digest }));
    if (t) {
      det.append(h('h5', { class: 'sec' }, 'Last run', h('span', { class: 'meta', text: `${W.lastResult.trigger} · ${t.status}` })),
        h('div', { class: 'stats' }, h('div', {}, h('b', { text: t.in }), h('span', { text: 'in' })), h('div', {}, h('b', { text: t.out }), h('span', { text: 'out' })),
          h('div', {}, h('b', { text: t.ms }), h('span', { text: 'ms' })), h('div', {}, h('b', { text: t.calls || 0 }), h('span', { text: 'calls' }))));
      if (t.error) det.append(h('div', { class: 'g-err-inline', text: '✗ ' + t.error }));
      if (t.notes) det.append(h('pre', { class: 'snip', text: t.notes.join('\n') }));
      if (t.top) det.append(hitList(t.top.slice(0, 8)));
    }
    det.append(h('div', { class: 'row-btns' }, h('button', { type: 'button', class: 'link muted', onclick: () => { W.selectedNode = null; renderInspector(); } }, 'Close')));
  }
  function hitList(top) {
    const a = art();
    return h('ol', { class: 'g-results compact' }, top.map((x, i) => {
      const c = a && a.chunks.get(x.id);
      return h('li', { class: 'g-result', tabindex: '0', onclick: () => uiApi.chunk(x.id) },
        h('div', { class: 'g-r-top' }, h('span', { class: 'g-rank', text: String(i + 1).padStart(2, '0') }), h('b', { text: c ? c.headingPath[c.headingPath.length - 1] || c.title : x.id }),
          h('span', { class: 'g-r-chips' }, h('i', { class: 'kchip', text: gui.fmtNum(+x.score) }))),
        x.why ? h('div', { class: 'g-r-doc', text: Object.entries(x.why).map(([k, v]) => `${k} ${v}`).join(' · ') }) : x.parts ? h('div', { class: 'g-r-doc', text: Object.entries(x.parts).map(([k, v]) => `${k} #${v}`).join(' · ') }) : null,
        x.reason ? h('div', { class: 'g-r-why', text: '↳ ' + x.reason }) : null);
    }));
  }
  function renderTraceTab(body) {
    const r = W.lastResult;
    if (!r) { body.append(h('div', { class: 'pad empty', text: 'Run a search to see how each node handled it.' })); return; }
    const split = h('div', { class: 'insp-split' });
    const left = h('div');
    left.append(h('h5', { class: 'sec' }, `Run · ${r.trigger === 'submit' ? 'Enter (all nodes)' : 'typing (change nodes)'}`, h('span', { class: 'meta', text: `${r.ms} ms · ${r.calls} calls · q “${clip(r.signals.q || '', 40)}”` })));
    const tbl = h('table', { class: 'g-table click' }, h('thead', {}, h('tr', {}, ['node', 'status', 'in → out', 'ms', 'calls'].map(x => h('th', { text: x })))),
      h('tbody', {}, r.trace.map(t => h('tr', { class: t.status + (W.traceNode === t.node ? ' on' : ''), onclick: () => { W.traceNode = t.node; W.explainId = null; renderInspector(); } },
        h('td', { text: t.node }), h('td', { text: t.status + (t.error ? ' ✗' : '') }), h('td', { class: 'num', text: `${t.in} → ${t.out}` }), h('td', { class: 'num', text: t.ms }), h('td', { class: 'num', text: t.calls || '' })))));
    left.append(tbl);
    if (r.errors.length) left.append(h('div', { class: 'g-err-inline', text: r.errors.map(e => '✗ ' + e).join('\n') }));
    const right = h('div');
    if (W.explainId) {
      const ex = r.explain(W.explainId);
      right.append(h('h5', { class: 'sec' }, 'Why this section ranked', h('span', { class: 'meta', text: ex.where ? clip(ex.where, 70) : W.explainId })));
      right.append(h('table', { class: 'g-table' }, h('thead', {}, h('tr', {}, ['node', 'rank', 'score', 'because'].map(x => h('th', { text: x })))),
        h('tbody', {}, ex.path.map(p => h('tr', { class: p.rank ? '' : 'skipped' }, h('td', { text: p.node }), h('td', { class: 'num', text: p.rank ?? '—' }), h('td', { class: 'num', text: p.score ?? '' }),
          h('td', { text: p.terms ? Object.entries(p.terms).map(([k, v]) => `${k} ${v}`).join(' · ') : p.parts ? Object.entries(p.parts).map(([k, v]) => `${k} #${v}`).join(' · ') : p.reason || '' }))))));
    } else {
      const t = r.trace.find(x => x.node === W.traceNode) || r.trace.filter(x => x.top).slice(-1)[0];
      if (t) {
        right.append(h('h5', { class: 'sec' }, `Output of ${t.node}`, h('span', { class: 'meta', text: t.top ? `top ${Math.min(10, t.top.length)} of ${t.out}` : '' })));
        if (t.top) right.append(hitList(t.top.slice(0, 10)));
        else if (t.node === (W.ws.ir.outputs.answer) && r.answer) right.append(h('div', { class: 'g-md', html: md(r.answer.text, { cite: true }) }));
        else if (t.node === W.ws.ir.outputs.context && r.context) right.append(h('div', { class: 'meta', text: `${r.context.evidence.length} sections · ${(r.context.bytes / 1000).toFixed(1)} KB` }), h('ol', {}, r.context.evidence.map(e => h('li', { text: `${e.label} ${e.headingPath.join(' › ')}` }))));
        else if (Array.isArray(r.outputs[t.node])) right.append(h('pre', { class: 'snip', text: r.outputs[t.node].join('\n') }));
        if (t.notes) right.append(h('pre', { class: 'snip', text: t.notes.join('\n') }));
      }
      right.append(h('p', { class: 'empty', text: 'Click a row for that node’s output. “Why this?” on a section explains its rank at every node.' }));
    }
    split.append(left, right);
    body.append(split);
  }
  async function runEvals(o = {}) {
    const ev = W.built && W.built.evals;
    if (!ev) throw new Error('pipeline.js returned no evals');
    const trigger = o.trigger || 'change';
    const names = o.variants || [];
    const targets = [W.ws, ...W.built.variants.filter(v => names.includes(v.id)).map(v => ({ ir: v.ir, engine: W.engine, name: v.id }))];
    W.evalProgress = { done: 0, total: ev.cases.length * targets.length, trigger };
    if (W.inspTab === 'evals') renderInspector();
    const onCase = () => { W.evalProgress.done++; if (W.inspTab === 'evals') { const m = $('#eval-progress'); if (m) m.textContent = `${W.evalProgress.done}/${W.evalProgress.total}`; } };
    try {
      if (targets.length > 1) {
        W.lastCompare = await ev.compare(targets, { trigger, judge: o.judge, onCase });
        W.lastEval = W.lastCompare.reports[0];
      } else { W.lastEval = await ev.run(W.ws, { trigger, judge: o.judge, onCase }); W.lastCompare = null; }
    } finally { W.evalProgress = null; }
    renderInspector();
    return W.lastCompare ? { rows: W.lastCompare.rows, delta: W.lastCompare.delta } : { summary: W.lastEval.summary, misses: W.lastEval.misses };
  }
  function renderEvalsTab(body) {
    const ev = W.built && W.built.evals;
    if (!ev) { body.append(h('div', { class: 'pad empty', text: 'pipeline.js returned no evals. Add rag.evals(…) and return it as evals.' })); return; }
    const picked = W.evalVariants || (W.evalVariants = new Set());
    const run = (trigger, judge) => runEvals({ trigger, judge, variants: [...picked] }).catch(e => toast(e.message, 'bad'));
    const bar = h('div', { class: 'toolbar' },
      h('button', { type: 'button', class: 'btn tiny primary', disabled: !!W.evalProgress, onclick: () => run('change') }, `Run ${ev.cases.length} cases`),
      h('button', { type: 'button', class: 'btn tiny', disabled: !!W.evalProgress || !W.caps.sample, title: W.caps.sample ? 'Runs LLM nodes and judges; spends Claude calls' : 'Claude is not available in this view', onclick: () => run('submit', true) }, 'Run with Claude'),
      h('span', { class: 'ctl-l', text: 'compare' }),
      W.built.variants.map(v => h('label', { class: 'chip' + (picked.has(v.id) ? ' on' : '') }, h('input', { type: 'checkbox', checked: picked.has(v.id), hidden: true, onchange: e => { e.target.checked ? picked.add(v.id) : picked.delete(v.id); renderInspector(); } }), v.id)),
      h('span', { class: 'meta', id: 'eval-progress', text: W.evalProgress ? `${W.evalProgress.done}/${W.evalProgress.total}` : W.lastEval ? `last run ${new Date(W.lastEval.at).toLocaleTimeString()} · ${W.lastEval.trigger === 'submit' ? 'with Claude' : 'lexical'} · on ${W.lastEval.target}` : 'lexical runs are free and fast' }));
    body.append(bar);
    if (!W.lastEval) { body.append(h('div', { class: 'pad empty', text: `${ev.cases.length} golden questions, each with the section that should come back. Run them to score the current workspace.` })); return; }
    const split = h('div', { class: 'insp-split' });
    const left = h('div'), right = h('div');
    const s = W.lastEval.summary;
    left.append(h('div', { class: 'stats' }, Object.entries(s).filter(([k]) => !['n'].includes(k)).map(([k, v]) => h('div', {}, h('b', { text: v == null ? '—' : gui.fmtNum(v) }), h('span', { text: k })))));
    if (W.lastCompare) {
      const metrics = Object.keys(W.lastCompare.rows[0]).filter(k => !['variant', 'p50ms', 'calls'].includes(k));
      left.append(h('h5', { class: 'sec' }, 'Compare'), h('table', { class: 'g-table' }, h('thead', {}, h('tr', {}, ['variant', ...metrics].map(x => h('th', { text: x })))),
        h('tbody', {}, W.lastCompare.rows.map(r => h('tr', {}, h('td', { text: r.variant }), metrics.map(m => h('td', { class: 'num', text: r[m] == null ? '—' : gui.fmtNum(r[m]) })))))));
      const m0 = metrics[1] || metrics[0];
      left.append(h('div', { html: gui.chart('bar', W.lastCompare.rows.map(r => [r.variant, r[m0] || 0]), { title: m0, height: 150 }) }));
    }
    if (W.lastEval.misses.length) left.append(h('h5', { class: 'sec' }, `Misses · ${W.lastEval.misses.length}`), h('ul', { class: 'g-list' }, W.lastEval.misses.map(m => h('li', {}, h('a', { href: '#', class: 'ref', onclick: e => { e.preventDefault(); uiApi.query(m.q); } }, m.q), h('div', { class: 'meta', text: `rank ${m.rank ?? '—'} · got ${clip(m.got || '—', 70)}` })))));
    const cases = W.lastEval.cases;
    right.append(h('h5', { class: 'sec' }, 'Cases', h('span', { class: 'meta', text: 'click to search' })), h('table', { class: 'g-table click' },
      h('thead', {}, h('tr', {}, ['#', 'question', 'rank', ...Object.keys(cases[0].m)].map(x => h('th', { text: x })))),
      h('tbody', {}, cases.map(c => h('tr', { class: c.rank && c.rank <= 5 ? '' : 'miss', onclick: () => uiApi.query(c.q) }, h('td', { class: 'num', text: c.i }), h('td', { text: clip(c.q, 64) + (c.synthetic ? ' ·syn' : '') }),
        h('td', { class: 'num', text: c.rank ?? '—' }), Object.values(c.m).map(v => h('td', { class: 'num', text: v == null ? '—' : gui.fmtNum(+(+v).toFixed(2)) })))))));
    split.append(left, right);
    body.append(split);
  }
  function opsSinceBuild() {
    const hist = W.ws.history;
    let i = hist.length - 1;
    while (i >= 0 && hist[i].ops[0].op !== 'reset') i--;
    return hist.slice(i + 1).flatMap(x => x.ops);
  }
  async function keepInScript() {
    const since = opsSinceBuild();
    if (!since.length) { toast('No workspace changes since the last build'); return; }
    const all = [...(W.built.ops || []), ...since];
    const text = src().value;
    const re = /const ops = [\s\S]*?; \/\/ end ops/;
    if (!re.test(text)) { toast('pipeline.js needs a "const ops = […]; // end ops" line to write into', 'bad'); return; }
    setEditor(text.replace(re, `const ops = ${JSON.stringify(all, null, 2)}; // end ops`));
    const r = await buildScript();
    if (r.ok) toast(`Wrote ${since.length} op${since.length > 1 ? 's' : ''} into pipeline.js`, 'ok');
  }
  function renderHistoryTab(body) {
    const ws = W.ws;
    const since = opsSinceBuild();
    body.append(h('div', { class: 'toolbar' },
      h('button', { type: 'button', class: 'btn tiny', disabled: !ws.history.length || ws.history[ws.history.length - 1].ops[0].op === 'reset', onclick: () => { ws.undo(); renderInspector(); } }, 'Undo'),
      h('button', { type: 'button', class: 'btn tiny', disabled: !ws.future.length, onclick: () => { ws.redo(); renderInspector(); } }, 'Redo'),
      h('button', { type: 'button', class: 'btn tiny primary', disabled: !since.length, onclick: keepInScript }, `Keep in script${since.length ? ` (${since.length})` : ''}`),
      h('span', { class: 'meta', text: `revision ${ws.revision} · ${since.length} op${since.length === 1 ? '' : 's'} since the last build${Object.keys(ws.branches).length ? ' · branches: ' + Object.keys(ws.branches).join(', ') : ''}` })));
    const ol = h('ol', { class: 'hist' });
    if (!ws.history.length) ol.append(h('li', {}, h('span'), h('span'), h('span', { class: 'empty', text: 'Changes to the workspace appear here: param edits, agent ops, merges and builds.' }), h('span')));
    [...ws.history].reverse().forEach((e, i) => {
      const summary = e.ops.map(o => o.op === 'reset' ? (e.note || 'reset') : `${o.op} ${o.node || o.key || o.after || ''}${o.path ? '.' + o.path : ''}${o.value !== undefined && typeof o.value !== 'object' ? ' = ' + o.value : ''}`).join(' · ');
      ol.append(h('li', { class: i === 0 ? 'cur' : '' }, h('b', { text: 'r' + e.revision }), h('span', { class: 'by ' + e.by, text: e.by }), h('span', { text: clip(summary, 200) }), h('span', { class: 'meta', text: new Date(e.at).toLocaleTimeString() })));
    });
    body.append(ol);
  }
  function renderCorpusTab(body) {
    const a = art();
    if (!a) { body.append(h('div', { class: 'pad empty', text: 'Build the pipeline to see the corpus.' })); return; }
    const split = h('div', { class: 'insp-split' });
    const left = h('div'), right = h('div');
    const counts = new Map();
    for (const c of a.chunks.values()) counts.set(c.docId, (counts.get(c.docId) || 0) + 1);
    left.append(h('h5', { class: 'sec' }, 'Documents', h('span', { class: 'meta', text: `${a.docs.size} docs · ${a.chunks.size} chunks` })),
      h('table', { class: 'g-table click' }, h('thead', {}, h('tr', {}, ['title', 'type', 'sections', 'chunks', 'links'].map(x => h('th', { text: x })))),
        h('tbody', {}, [...a.docs.values()].map(d => h('tr', { class: W.corpusDoc === d.id ? 'on' : '', onclick: () => { W.corpusDoc = d.id; renderInspector(); } },
          h('td', { text: clip(d.title, 60) }), h('td', { text: [].concat(d.meta.type || '—').join(', ') }), h('td', { class: 'num', text: d.sections.length }), h('td', { class: 'num', text: counts.get(d.id) || 0 }), h('td', { class: 'num', text: (a.links.get(d.id) || []).length }))))));
    const d = W.corpusDoc && a.docs.get(W.corpusDoc);
    if (d) {
      right.append(h('h5', { class: 'sec' }, clip(d.title, 60), h('span', { class: 'meta', text: Object.entries(d.meta).filter(([k, v]) => ['type', 'status', 'tags'].includes(k)).map(([k, v]) => `${k}: ${[].concat(v).join(', ')}`).join(' · ') })));
      right.append(h('ol', { class: 'g-results compact' }, [...a.chunks.values()].filter(c => c.docId === d.id).map((c, i) => h('li', { class: 'g-result', tabindex: '0', onclick: () => uiApi.chunk(c.id) },
        h('div', { class: 'g-r-top' }, h('span', { class: 'g-rank', text: String(i + 1).padStart(2, '0') }), h('b', { text: c.headingPath.join(' › ') || 'intro' }), h('span', { class: 'g-r-chips' }, c.kind !== 'section' ? h('i', { class: 'kchip alt', text: c.kind }) : null, h('i', { class: 'kchip', text: c.tokens + ' tok' })))))));
    } else right.append(h('p', { class: 'empty', text: 'Select a document to list its chunks. Clicking a chunk opens it next to the results.' }));
    split.append(left, right);
    body.append(split);
  }
  function renderIrTab(body) {
    const ir = W.ws.ir;
    const json = JSON.stringify(ir, null, 2);
    body.append(h('div', { class: 'toolbar' },
      h('button', { type: 'button', class: 'btn tiny', onclick: () => copy(json) }, 'Copy JSON'),
      h('button', { type: 'button', class: 'btn tiny', onclick: () => copy(ir.mermaid()) }, 'Copy mermaid'),
      h('button', { type: 'button', class: 'btn tiny', onclick: () => download(`${ir.id}-rev${W.ws.revision}.json`, json) }, 'Save JSON'),
      h('button', { type: 'button', class: 'btn tiny', onclick: async () => { try { const html = await rag.export(W.ws, { title: (W.searchRt && W.searchRt.ir.title) || ir.id }); download(`${ir.id}-search.html`, html); } catch (e) { toast(e.message, 'bad'); } } }, 'Export search page'),
      h('span', { class: 'meta', text: `${ir.nodes.length} nodes · ${ir.edges.length} edges · digest ${ir.digest}` })),
      h('pre', { class: 'g-code', style: { margin: '0', border: '0', maxHeight: 'none' }, text: json }));
  }

  // ────────────────────────────────────────────────────────────── files
  function renderFiles() {
    const box = $('#files');
    box.innerHTML = '';
    const a = art();
    const all = [...W.bundled, ...W.uploaded];
    const kb = all.filter(f => !W.disabled.has(f.name)).reduce((s, f) => s + f.text.length, 0) / 1024;
    box.append(h('div', { class: 'panel-h' }, h('span', { text: 'Corpus files' }), h('span', { class: 'meta', text: `${activeFiles().length} of ${all.length} on · ${kb.toFixed(0)} KB` })));
    for (const f of all) {
      const on = !W.disabled.has(f.name);
      const doc = a && [...a.docs.values()].find(d => d.name === f.name);
      box.append(h('div', { class: 'frow' },
        h('input', { type: 'checkbox', checked: on, 'aria-label': 'Include ' + f.name, onchange: e => toggleFile(f.name, e.target.checked) }),
        h('div', { style: { minWidth: 0 } }, h('b', { text: doc ? doc.title : f.name.replace(/\.md$/, '') }), h('small', { text: `${(f.text.length / 1024).toFixed(1)} KB${doc ? ` · ${doc.sections.length} sections` : ''} · ${f.source}` })),
        f.source === 'uploaded' ? h('button', { type: 'button', class: 'btn tiny bad', onclick: () => removeFile(f.name) }, 'Remove') : h('span')));
    }
  }
  async function addFiles(list) {
    for (const f of list) {
      if (f.text.length > 250000) { toast(`${f.name} is over 250 KB; split it first`, 'bad'); continue; }
      W.uploaded = W.uploaded.filter(x => x.name !== f.name);
      W.uploaded.push({ name: f.name, text: f.text, source: 'uploaded' });
      store.addFile(f).catch(e => clog('error', 'could not store ' + f.name + ': ' + (e.message || e.code)));
    }
    renderFiles();
    await rebuildArtifacts();
    if (W.searchRt) W.searchRt.runNow('change');
    renderFiles();
    renderInspector();
  }
  async function removeFile(name) {
    W.uploaded = W.uploaded.filter(x => x.name !== name);
    store.removeFile(name).catch(() => {});
    await rebuildArtifacts();
    if (W.searchRt) W.searchRt.runNow('change');
    renderFiles();
    renderInspector();
  }
  async function toggleFile(name, on) {
    if (on) W.disabled.delete(name); else W.disabled.add(name);
    store.saveState({ disabled: [...W.disabled] });
    await rebuildArtifacts();
    if (W.searchRt) W.searchRt.runNow('change');
    renderFiles();
    renderInspector();
  }
  function wireFiles() {
    const drop = $('#drop');
    const read = files => Promise.all([...files].filter(f => /\.(md|markdown|txt)$/i.test(f.name)).map(f => f.text().then(text => ({ name: f.name.replace(/\.(markdown|txt)$/i, '.md'), text }))));
    drop.addEventListener('dragover', e => { e.preventDefault(); drop.classList.add('on'); });
    drop.addEventListener('dragleave', () => drop.classList.remove('on'));
    drop.addEventListener('drop', async e => { e.preventDefault(); drop.classList.remove('on'); const fs = await read(e.dataTransfer.files); if (fs.length) addFiles(fs); else toast('Drop markdown (.md) files', 'bad'); });
    $('#file-in').addEventListener('change', async e => { const fs = await read(e.target.files); e.target.value = ''; if (fs.length) addFiles(fs); });
  }

  // ────────────────────────────────────────────────────────────── ui api (agent + refs)
  const uiApi = {
    async query(q, o = {}) {
      if (!W.searchRt) throw new Error('the search surface is not mounted yet');
      showSurface('search');
      setPanel('search', true);
      W.searchRt.set({ q: String(q), ...(o.filters ? { filters: o.filters } : {}) }, null, { noRun: true });
      const r = await W.searchRt.runNow(o.submit ? 'submit' : 'change');
      return r ? { q: r.signals.q, trigger: r.trigger, ms: r.ms, top: r.top(5), answer: r.answer ? clip(r.answer.text, 800) : null, errors: r.errors } : null;
    },
    filters(obj) { W.searchRt.set({ filters: obj || {} }); return 'filters set'; },
    signals(obj) { W.searchRt.set(obj || {}); return 'signals set'; },
    tab(name) { if (!['dag', 'trace', 'evals', 'history', 'corpus', 'ir'].includes(name)) throw new Error('tabs: dag trace evals history corpus ir'); setInspTab(name); setPanel('inspect', true); return 'showing ' + name; },
    select(id) { if (!W.ws.ir.node(id)) throw new Error(`no node '${id}'`); W.selectedNode = id; setInspTab('dag'); setPanel('inspect', true); return 'selected ' + id; },
    chunk(id) {
      const c = art() && art().chunks.get(id);
      if (!c) throw new Error(`no chunk '${id}'`);
      showSurface('search');
      setPanel('search', true);
      W.searchRt.set({ selected: id }, null, { noRun: true });
      return 'opened ' + id;
    },
    async app(spec) {
      W.appIr = spec ? (spec.kind === 'gui' ? spec : spec.compile()) : null;
      mountApp();
      if (W.appIr) { showSurface('app'); setPanel('search', true); }
      return W.appIr ? `mounted '${W.appIr.title}' in the App tab` : 'App tab cleared';
    },
    toast(msg) { toast(String(msg)); return 'ok'; },
  };
  const refApi = {
    query: q => 'wb:query?q=' + encodeURIComponent(q),
    chunk: id => 'wb:chunk?id=' + encodeURIComponent(id),
    node: id => 'wb:node?id=' + encodeURIComponent(id),
    tab: name => 'wb:tab?name=' + encodeURIComponent(name),
  };
  function handleRef(href) {
    const m = /^wb:([a-z]+)\??(.*)$/.exec(href || '');
    if (!m) return;
    const p = new URLSearchParams(m[2]);
    try {
      if (m[1] === 'query') uiApi.query(p.get('q') || '', { submit: p.get('submit') === '1' });
      else if (m[1] === 'chunk') uiApi.chunk(p.get('id'));
      else if (m[1] === 'node') uiApi.select(p.get('id'));
      else if (m[1] === 'tab') uiApi.tab(p.get('name'));
    } catch (e) { toast(e.message, 'bad'); }
  }
  function wbApi(readonly) {
    const block = name => () => { throw new rag.RagError('readonly', `${name} is blocked in Read-only mode. Describe the change instead, or ask the person to switch to Auto or Ask.`); };
    const api = {
      files: () => activeFiles().map(f => ({ name: f.name, kb: +(f.text.length / 1024).toFixed(1), source: f.source })),
      addFile: readonly ? block('wb.addFile') : async (name, text) => { await addFiles([{ name, text: String(text) }]); return `added ${name}`; },
      removeFile: readonly ? block('wb.removeFile') : async name => { await removeFile(name); return `removed ${name}`; },
      script: {
        get: () => src().value,
        set: readonly ? block('wb.script.set') : text => { setEditor(String(text)); W.ideTab = 'code'; return `${String(text).split('\n').length} lines in the editor (not built yet)`; },
        build: readonly ? block('wb.script.build') : () => buildScript(),
      },
      get plan() { return W.built && W.built.plan; },
      get evals() { return W.built && W.built.evals; },
      get variants() { return W.built ? W.built.variants.map(v => ({ id: v.id, ir: v.ir, engine: W.engine, name: v.id })) : []; },
      get corpus() { return W.built && W.built.plan.st.corpus; },
      get result() { return W.lastResult; },
      get lastEval() { return W.lastEval; },
      get app() { return W.searchRt && W.searchRt.ir; },
      runEvals: o => runEvals(o),
      search: (q, o = {}) => W.ws.run({ ...(o.signals || {}), q: String(q) }, { trigger: o.trigger || 'change' }),
      chunk: id => (art() && art().chunks.get(id)) || null,
      chunks: f => (art() ? [...art().chunks.values()].filter(f || (() => true)) : []),
      doc: id => (art() && art().docs.get(id)) || null,
      docs: () => (art() ? [...art().docs.values()].map(d => ({ id: d.id, title: d.title, type: d.meta.type, tags: d.meta.tags, sections: d.sections.length })) : []),
      state: () => stateObj(),
      kit,
      presets: {
        list: () => W.presets.map(p => ({ id: p.id, group: p.group, title: p.title, blurb: p.blurb, llm: p.llm || 'none' })),
        current: () => (W.preset ? { id: W.preset.id, title: W.preset.title, edited: src().value !== W.presetText } : null),
        text: id => fetchPreset(findPreset(id)),
        load: readonly ? block('wb.presets.load') : async id => { const r = await loadPreset(id); return { loaded: id, ...r }; },
      },
      build: readonly ? block('wb.build') : () => buildButton(),
    };
    return api;
  }
  function scopedWs(readonly) {
    if (!readonly) return W.ws;
    const blocked = new Set(['apply', 'rebuild', 'undo', 'redo', 'merge', 'reset', 'restore']);
    return new Proxy(W.ws, {
      get(t, k) {
        if (blocked.has(k)) return () => { throw new rag.RagError('readonly', `ws.${k} is blocked in Read-only mode. Describe the change instead, or measure it on a branch (ws.branch).`); };
        const v = Reflect.get(t, k, t);
        return typeof v === 'function' ? v.bind(t) : v;
      },
    });
  }
  function stateObj() {
    const a = art(), ir = W.ws.ir, r = W.lastResult;
    return {
      corpus: a ? { files: activeFiles().length, docs: a.docs.size, chunks: a.chunks.size, indexes: [...a.indexes.keys()] } : null,
      plan: ir.id, revision: W.ws.revision, nodes: ir.summary(), dirty: W.engine.dirty(ir), opsSinceBuild: opsSinceBuild().length,
      signals: W.searchRt ? { q: W.searchRt.state.q, k: W.searchRt.state.k, filters: W.searchRt.state.filters, selected: W.searchRt.state.selected } : null,
      lastSearch: r ? { trigger: r.trigger, ms: r.ms, top: r.top(3).map(x => `${x.id} (${x.heading})`), errors: r.errors } : null,
      evals: W.built && W.built.evals ? { id: W.built.evals.id, cases: W.built.evals.cases.length, last: W.lastEval ? { trigger: W.lastEval.trigger, summary: W.lastEval.summary, misses: W.lastEval.misses.map(m => `${m.q} → rank ${m.rank ?? '—'}`) } : 'not run yet' } : null,
      variants: W.built ? W.built.variants.map(v => v.id) : [], branches: Object.keys(W.ws.branches), appTab: W.appIr ? W.appIr.title : null,
      script: { lines: src().value.split('\n').length, built: src().value === W.scriptBuiltText, preset: W.preset ? W.preset.id + (src().value !== W.presetText ? ' (edited)' : '') : 'none' },
      pendingClaudeBuild: W.engine.pending(W.ws.ir),
      claude: W.caps.sample ? (W.caps.limits && W.caps.limits.tools ? 'available' : 'available, no tools') : 'unavailable',
    };
  }
  function takeSnapshot() {
    return { ws: W.ws.snapshot(), script: src().value, scriptBuilt: W.scriptBuiltText, uploaded: W.uploaded.slice(), disabled: [...W.disabled], appIr: W.appIr, preset: W.preset, presetText: W.presetText };
  }
  async function restoreSnapshot(s) {
    const filesChanged = JSON.stringify(W.uploaded.map(f => f.name)) !== JSON.stringify(s.uploaded.map(f => f.name)) || JSON.stringify([...W.disabled]) !== JSON.stringify(s.disabled);
    if (filesChanged) {
      for (const f of W.uploaded) if (!s.uploaded.find(x => x.name === f.name)) store.removeFile(f.name).catch(() => {});
      for (const f of s.uploaded) if (!W.uploaded.find(x => x.name === f.name)) store.addFile(f).catch(() => {});
      W.uploaded = s.uploaded.slice();
      W.disabled = new Set(s.disabled);
      store.saveState({ disabled: s.disabled });
    }
    if (W.preset !== s.preset) { W.preset = s.preset; W.presetText = s.presetText; updatePresetHead(); }
    if (src().value !== s.script || W.scriptBuiltText !== s.scriptBuilt) {
      setEditor(s.scriptBuilt || s.script);
      await buildScript({ restoring: true });
      if (s.script !== s.scriptBuilt) setEditor(s.script);
    }
    W.ws.restore(s.ws);
    if (filesChanged) await rebuildArtifacts();
    if (W.appIr !== s.appIr) { W.appIr = s.appIr; mountApp(); }
    renderFiles();
    renderInspector();
    updateTop();
  }

  // ────────────────────────────────────────────────────────────── layout
  function setPanel(name, fromCode) {
    const app = $('#app');
    if (fromCode && window.innerWidth > 980) return;
    app.dataset.panel = name;
    document.querySelectorAll('.mtabs [data-panel]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.panel === name)));
  }
  function wireLayout() {
    document.querySelectorAll('.mtabs [data-panel]').forEach(b => b.addEventListener('click', () => setPanel(b.dataset.panel)));
    document.querySelectorAll('[data-ide]').forEach(b => b.addEventListener('click', () => {
      W.ideTab = b.dataset.ide;
      document.querySelectorAll('[data-ide]').forEach(x => x.classList.toggle('on', x === b));
      $('#ide-code').hidden = W.ideTab !== 'code';
      $('#ide-files').hidden = W.ideTab !== 'files';
      $('#ide-ref').hidden = W.ideTab !== 'ref';
      if (W.ideTab === 'files') renderFiles();
    }));
    document.querySelectorAll('[data-insp]').forEach(b => b.addEventListener('click', () => setInspTab(b.dataset.insp)));
    document.querySelectorAll('[data-surface]').forEach(b => b.addEventListener('click', () => showSurface(b.dataset.surface)));
    $('#build-btn').addEventListener('click', buildButton);
    $('#undo-btn').addEventListener('click', () => { if (W.ws) { W.ws.undo(); renderInspector(); } });
    $('#clear-console').addEventListener('click', () => { $('#console').innerHTML = ''; });
    const app = $('#app');
    $('#ide-close').addEventListener('click', () => app.classList.add('ide-off'));
    $('#ide-open').addEventListener('click', () => app.classList.remove('ide-off'));
    $('#side-open').addEventListener('click', () => app.classList.remove('side-off'));
    if (window.innerWidth < 1240 && window.innerWidth > 980) app.classList.add('ide-off');
    const drag = (handle, onMove) => handle.addEventListener('pointerdown', e => {
      e.preventDefault();
      handle.setPointerCapture(e.pointerId);
      handle.classList.add('on');
      document.body.classList.add('resizing');
      const move = ev => onMove(ev);
      const up = () => { handle.classList.remove('on'); document.body.classList.remove('resizing'); handle.removeEventListener('pointermove', move); handle.removeEventListener('pointerup', up); };
      handle.addEventListener('pointermove', move);
      handle.addEventListener('pointerup', up);
    });
    const root = document.documentElement;
    drag($('#ide-resize'), e => { const r = $('#app').getBoundingClientRect(); root.style.setProperty('--ide-w', Math.max(260, Math.min(720, e.clientX - r.left)) + 'px'); });
    drag($('#side-resize'), e => { const r = $('#app').getBoundingClientRect(); root.style.setProperty('--side-w', Math.max(280, Math.min(720, r.right - e.clientX)) + 'px'); });
    drag($('#insp-resize'), e => { const r = document.querySelector('.center').getBoundingClientRect(); root.style.setProperty('--insp-h', Math.max(120, Math.min(r.height - 120, r.bottom - e.clientY)) + 'px'); });
  }
  function renderReference() {
    const comps = rag.components();
    $('#refdoc').innerHTML = md(agentKit.DOCS) + md('## Components\n| type | phase | in → out | trigger |\n|---|---|---|---|\n' + comps.map(c => `| \`${c.type}\` | ${c.phase} | ${[].concat(c.in).join(', ') || '—'} → ${c.out} | ${c.phase === 'live' ? c.trigger : ''} |`).join('\n'));
  }

  // ────────────────────────────────────────────────────────────── capabilities
  async function initCaps() {
    const c = window.claude;
    if (!c || typeof c.use !== 'function') return;
    const get = n => c.use(n).catch(() => null);
    const [sample, db, user, downloads] = await Promise.all([get('sample'), get('db'), get('user'), get('downloads')]);
    W.caps = { sample, db, user, downloads };
    if (sample && sample.limits) { try { W.caps.limits = await sample.limits(); } catch (_) { W.caps.limits = null; } }
    if (db && user) { try { W.caps.uid = await user.id(); } catch (_) { W.caps.uid = null; } }
  }

  // ────────────────────────────────────────────────────────────── boot
  async function boot() {
    wireLayout();
    wireEditor();
    wireFiles();
    renderReference();
    await measureOffset();
    const capsP = initCaps();
    wirePresets();
    const [corpus] = await Promise.all([
      fetch('corpus.json').then(r => r.json()).catch(() => ({ files: [] })),
      loadPresetList(),
    ]);
    W.bundled = (corpus.files || []).map(f => ({ ...f, source: 'bundled' }));
    rag.configure({ files: activeFiles, log: (...a) => clog('', ...a), librarySource: () => fetch('rag.js').then(r => r.text()), buildCalls: 'explicit' });
    W.engine = rag.engine();
    if (W.presets.length) await loadPreset(W.presets[0].id, { silent: true, build: false });
    const agentHost = {
      sample: null, limits: null, store: null, settings: {}, lineOffset: W.lineOffset,
      scope: ({ readonly }) => ({ rag, gui, wb: wbApi(readonly), ws: scopedWs(readonly), ui: uiApi, ref: refApi }),
      summary: () => agentKit.toYAML(stateObj()),
      snapshot: takeSnapshot, restore: restoreSnapshot,
      mountGui: (el, ir) => new gui.Runtime(ir, guiHost()).mount(el),
      renderDag, onRef: handleRef, currentIr: () => W.ws.ir, download, toast,
      afterStep: () => { renderInspector(); updateTop(); },
    };
    W.agent = new agentKit.Agent(agentHost).mount($('#agent'));
    const ah = $('#agent .panel-h .ag-head-btns');
    if (ah) ah.append(h('button', { type: 'button', class: 'btn tiny collapse-btn', 'aria-label': 'Hide agent', onclick: () => $('#app').classList.add('side-off') }, '›'));
    setStatus('busy', 'Building the pipeline…');
    await buildScript({ restoring: true });
    renderFiles();
    capsP.then(async () => {
      agentHost.sample = W.caps.sample;
      agentHost.limits = W.caps.limits;
      rag.configure({ sample: W.caps.sample || undefined });
      if (!W.caps.sample) clog('warn', 'Claude is not available in this view: LLM nodes are skipped with an error and the agent cannot run. Lexical search, evals and editing work.');
      else if (W.caps.limits && !W.caps.limits.tools) clog('warn', 'This view cannot run page tools, so the agent can talk but not act.');
      if (store.init()) {
        agentHost.store = store;
        rag.configure({ cache: store.cache });
        try {
          const st = await store.load();
          if (st.agent) { Object.assign(W.agent.settings, st.agent); W.agent.renderSettings(); }
          if (Array.isArray(st.disabled)) W.disabled = new Set(st.disabled);
          const files = await store.files().catch(() => []);
          W.uploaded = files.map(f => ({ name: f.name, text: f.text, source: 'uploaded' }));
          const filesChanged = W.uploaded.length || W.disabled.size;
          const savedPreset = st.preset && W.presets.find(p => p.id === st.preset);
          if (savedPreset && (savedPreset !== W.preset || st.script)) {
            await loadPreset(savedPreset.id, { silent: true, restoring: true, script: st.script || undefined });
            clog('', `restored preset “${savedPreset.title}”${st.script ? ' with your edits' : ''}`);
          } else if (!st.preset && st.script && st.script !== src().value) {
            W.preset = null; W.presetText = null;
            setEditor(st.script);
            updatePresetHead();
            await buildScript({ restoring: true });
            clog('', 'restored your pipeline.js');
          } else if (filesChanged) { await rebuildArtifacts(); if (W.searchRt) W.searchRt.runNow('change'); }
          renderFiles();
          updateTop();
        } catch (e) { clog('error', 'could not load saved state: ' + (e.message || e.code)); }
      }
    });
  }
  boot().catch(e => { setStatus('bad', 'Failed to start: ' + e.message); console.error(e); });
})();
