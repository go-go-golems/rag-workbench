/* gui.js — generative UI for the workbench.
 *
 *   gui.app(id).title(t).use(plan).state({…}).derive(k, fn).watch(k, fn).view(v => v.col(c => …))
 *
 * The build callback runs once and produces a node tree. Props are values,
 * runtime lambdas (s, ctx) => value, or gui.bake(ctx => value) evaluated at build.
 * Runtime lambdas are stored as source and run closed, like rag lambdas.
 * The runtime tracks which state keys each component read and re-renders
 * only those components when the keys change.
 */
(function (root) {
  'use strict';
  const rag = root.rag;
  const { lam, fnOf, clip, tokenize } = rag.util;
  const isLambda = v => v && typeof v === 'object' && typeof v.$fn === 'string';

  // ────────────────────────────────────────────────────────────── dom helpers
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
  function h(tag, attrs, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'html') el.innerHTML = v;
      else if (k === 'text') el.textContent = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const kid of kids.flat()) if (kid != null && kid !== false) el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
    return el;
  }

  // ────────────────────────────────────────────────────────────── markdown
  function md(src, o = {}) {
    const lines = String(src == null ? '' : src).replace(/\r/g, '').split('\n');
    const out = [];
    const link = (txt, href) => {
      if (/^https?:\/\//.test(href)) return `<a href="${href}" target="_blank" rel="noopener">${txt}</a>`;
      if (/^wb:/.test(href)) return `<a href="#" class="ref" data-ref="${href}">${txt}</a>`;
      return txt;
    };
    const inline = t => {
      t = esc(t);
      const codes = [];
      t = t.replace(/`([^`]+)`/g, (_, c) => { codes.push(c); return `\u0000${codes.length - 1}\u0000`; });
      t = t.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (m, txt, href) => link(txt, href));
      t = t.replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>').replace(/(^|[\s(])\*([^*\s][^*]*?)\*(?=[\s).,;:!?]|$)/g, '$1<i>$2</i>');
      if (o.cite) t = t.replace(/\[(E\d+(?:\s*[,;]\s*E\d+)*)\]/g, (m, list) => list.split(/\s*[,;]\s*/).map(e => `<button type="button" class="cite" data-cite="${e}">${e}</button>`).join(''));
      t = t.replace(/\u0000(\d+)\u0000/g, (_, n) => `<code>${codes[n]}</code>`);
      return t;
    };
    let i = 0;
    while (i < lines.length) {
      const line = lines[i];
      const fence = /^\s*(```+|~~~+)\s*([\w+-]*)/.exec(line);
      if (fence) {
        const body = [];
        i++;
        while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++]);
        i++;
        out.push(`<pre class="md-pre"${fence[2] ? ` data-lang="${esc(fence[2])}"` : ''}><code>${esc(body.join('\n'))}</code></pre>`);
        continue;
      }
      const hd = /^(#{1,6})\s+(.*)$/.exec(line);
      if (hd) { const lv = Math.min(6, hd[1].length + 2); out.push(`<h${lv}>${inline(hd[2])}</h${lv}>`); i++; continue; }
      if (/^\s*\|/.test(line)) {
        const rows = [];
        while (i < lines.length && /^\s*\|/.test(lines[i])) rows.push(lines[i++]);
        const cells = r => r.trim().replace(/^\||\|$/g, '').split('|').map(c => c.trim());
        const data = rows.filter(r => !/^\s*\|[\s:|-]+\|\s*$/.test(r)).map(cells);
        out.push('<div class="md-table"><table><thead><tr>' + (data[0] || []).map(c => `<th>${inline(c)}</th>`).join('') + '</tr></thead><tbody>' +
          data.slice(1).map(r => '<tr>' + r.map(c => `<td>${inline(c)}</td>`).join('') + '</tr>').join('') + '</tbody></table></div>');
        continue;
      }
      if (/^\s*>/.test(line)) {
        const q = [];
        while (i < lines.length && /^\s*>/.test(lines[i])) q.push(lines[i++].replace(/^\s*>\s?/, ''));
        const c = /^\[!(\w+)\]\s*(.*)$/.exec(q[0] || '');
        out.push(`<blockquote${c ? ` class="callout" data-variant="${esc(c[1].toLowerCase())}"` : ''}>${md(c ? [c[2], ...q.slice(1)].join('\n') : q.join('\n'), o)}</blockquote>`);
        continue;
      }
      if (/^\s*([-*+]|\d+\.)\s+/.test(line)) {
        const ordered = /^\s*\d+\./.test(line);
        const items = [];
        while (i < lines.length && (/^\s*([-*+]|\d+\.)\s+/.test(lines[i]) || (/^\s{2,}\S/.test(lines[i]) && items.length))) {
          const m = /^(\s*)([-*+]|\d+\.)\s+(.*)$/.exec(lines[i]);
          if (m && m[1].length < 2) items.push([m[3]]);
          else items[items.length - 1].push(lines[i].replace(/^\s{2,4}/, ''));
          i++;
        }
        out.push(`<${ordered ? 'ol' : 'ul'}>` + items.map(it => `<li>${it.length > 1 ? inline(it[0]) + md(it.slice(1).join('\n'), o) : inline(it[0])}</li>`).join('') + `</${ordered ? 'ol' : 'ul'}>`);
        continue;
      }
      if (/^\s*(---|\*\*\*)\s*$/.test(line)) { out.push('<hr>'); i++; continue; }
      if (!line.trim()) { i++; continue; }
      const para = [];
      while (i < lines.length && lines[i].trim() && !/^\s*(```|~~~|#{1,6}\s|\||>|([-*+]|\d+\.)\s)/.test(lines[i])) para.push(lines[i++]);
      out.push(`<p>${para.map(inline).join(' ')}</p>`);
    }
    return out.join('');
  }

  // ────────────────────────────────────────────────────────────── charts
  const PALETTE = ['var(--sig)', 'var(--ok)', 'var(--warn)', 'var(--bad)', 'var(--ink)', 'var(--muted)'];
  function niceTicks(min, max, n = 5) {
    if (!isFinite(min) || !isFinite(max)) return [0, 1];
    if (min === max) { const d = Math.abs(min) || 1; min -= d / 2; max += d / 2; }
    const span = max - min, step0 = span / n, mag = Math.pow(10, Math.floor(Math.log10(step0)));
    const step = [1, 2, 2.5, 5, 10].map(m => m * mag).find(s => span / s <= n) || 10 * mag;
    const lo = Math.floor(min / step) * step, hi = Math.ceil(max / step) * step, out = [];
    for (let v = lo; v <= hi + step / 2; v += step) out.push(+v.toFixed(10));
    return out;
  }
  const fmtNum = v => (Math.abs(v) >= 1000 ? (v / 1000).toFixed(Math.abs(v) >= 10000 ? 0 : 1) + 'k' : Number.isInteger(v) ? String(v) : (+v.toFixed(3)).toString());
  function seriesOf(data) {
    if (Array.isArray(data) && data.length && Array.isArray(data[0]) && typeof data[0][0] !== 'string') return [{ name: '', points: data }];
    if (Array.isArray(data)) return data.map((s, i) => ({ name: s.name || 'series ' + (i + 1), points: s.points || s.data || [] }));
    return Object.entries(data || {}).map(([name, points]) => ({ name, points }));
  }
  function chartSvg(kind, data, o = {}) {
    const W = o.width || 560, H = o.height || 220, L = 46, R = 14, T = 14, B = o.xLabel ? 40 : 28;
    if (kind === 'bar') {
      const rows = Array.isArray(data) ? data.map(d => (Array.isArray(d) ? d : [d.label ?? d.name, d.value])) : Object.entries(data || {});
      if (!rows.length) return '<svg class="g-chart-svg"></svg>';
      const max = Math.max(0, ...rows.map(r => +r[1] || 0)), min = Math.min(0, ...rows.map(r => +r[1] || 0));
      const ticks = niceTicks(min, max, 4), y0 = ticks[0], y1 = ticks[ticks.length - 1];
      const y = v => T + (H - T - B) * (1 - (v - y0) / (y1 - y0 || 1));
      const bw = (W - L - R) / rows.length;
      let s = `<svg class="g-chart-svg" viewBox="0 0 ${W} ${H}" role="img" aria-label="${esc(o.title || 'bar chart')}">`;
      for (const t of ticks) s += `<line class="g-grid" x1="${L}" x2="${W - R}" y1="${y(t)}" y2="${y(t)}"/><text class="g-tick" x="${L - 6}" y="${y(t) + 4}" text-anchor="end">${fmtNum(t)}</text>`;
      rows.forEach(([label, v], i) => {
        const x = L + i * bw + bw * 0.15, top = y(Math.max(0, v)), bot = y(Math.min(0, v));
        s += `<rect class="g-bar" x="${x}" y="${top}" width="${bw * 0.7}" height="${Math.max(1, bot - top)}" fill="${o.color || 'var(--sig)'}"><title>${esc(label)}: ${esc(v)}</title></rect>`;
        s += `<text class="g-tick" x="${x + bw * 0.35}" y="${H - B + 14}" text-anchor="middle">${esc(clip(String(label), Math.max(4, Math.floor(bw / 6.5))))}</text>`;
        if (o.labels !== false && rows.length <= 16) s += `<text class="g-val" x="${x + bw * 0.35}" y="${top - 4}" text-anchor="middle">${fmtNum(+v)}</text>`;
      });
      s += `<line class="g-axis" x1="${L}" x2="${W - R}" y1="${y(0)}" y2="${y(0)}"/>`;
      if (o.yLabel) s += `<text class="g-axl" x="12" y="${T + 4}">${esc(o.yLabel)}</text>`;
      return s + '</svg>';
    }
    const series = seriesOf(data).map(s => ({ ...s, points: (s.points || []).map(p => (Array.isArray(p) ? p : [p.x, p.y])).filter(p => isFinite(p[0]) && isFinite(p[1])) }));
    const all = series.flatMap(s => s.points);
    if (!all.length) return '<svg class="g-chart-svg"></svg>';
    const xt = niceTicks(Math.min(...all.map(p => p[0])), Math.max(...all.map(p => p[0])), 6);
    const yt = niceTicks(o.yMin ?? Math.min(...all.map(p => p[1])), o.yMax ?? Math.max(...all.map(p => p[1])), 4);
    const x = v => L + (W - L - R) * (v - xt[0]) / (xt[xt.length - 1] - xt[0] || 1);
    const y = v => T + (H - T - B) * (1 - (v - yt[0]) / (yt[yt.length - 1] - yt[0] || 1));
    let s = `<svg class="g-chart-svg" viewBox="0 0 ${W} ${H}" role="img" aria-label="${esc(o.title || kind + ' chart')}">`;
    for (const t of yt) s += `<line class="g-grid" x1="${L}" x2="${W - R}" y1="${y(t)}" y2="${y(t)}"/><text class="g-tick" x="${L - 6}" y="${y(t) + 4}" text-anchor="end">${fmtNum(t)}</text>`;
    for (const t of xt) s += `<text class="g-tick" x="${x(t)}" y="${H - B + 14}" text-anchor="middle">${fmtNum(t)}</text>`;
    s += `<line class="g-axis" x1="${L}" x2="${W - R}" y1="${H - B}" y2="${H - B}"/>`;
    series.forEach((se, i) => {
      const c = se.color || PALETTE[i % PALETTE.length];
      const pts = [...se.points].sort((a, b) => a[0] - b[0]);
      if (kind === 'line' && pts.length > 1) s += `<polyline class="g-line" fill="none" stroke="${c}" points="${pts.map(p => x(p[0]) + ',' + y(p[1])).join(' ')}"/>`;
      for (const p of pts) s += `<circle class="g-dot" cx="${x(p[0])}" cy="${y(p[1])}" r="${kind === 'scatter' ? 3.5 : 2.6}" fill="${c}"><title>${esc(se.name ? se.name + ': ' : '')}${fmtNum(p[0])}, ${fmtNum(p[1])}</title></circle>`;
    });
    if (o.xLabel) s += `<text class="g-axl" x="${(L + W - R) / 2}" y="${H - 6}" text-anchor="middle">${esc(o.xLabel)}</text>`;
    if (o.yLabel) s += `<text class="g-axl" x="12" y="${T + 4}">${esc(o.yLabel)}</text>`;
    s += '</svg>';
    if (series.length > 1 || series[0].name) s += '<div class="g-legend">' + series.map((se, i) => `<span><i style="background:${se.color || PALETTE[i % PALETTE.length]}"></i>${esc(se.name)}</span>`).join('') + '</div>';
    return s;
  }

  // ────────────────────────────────────────────────────────────── builder
  // Props that are called by the component (with extra arguments) rather than evaluated for a value.
  const HANDLERS = {
    button: ['onClick'], table: ['onRowClick'], canvas: ['draw', 'onClick', 'onHover', 'onKey'],
    results: ['onSelect', 'item'], answer: ['onCite'], searchBox: ['onSubmit'], list: ['onClick'], hits: ['onSelect'],
  };
  const normProp = v => (typeof v === 'function' ? lam(v) : v instanceof RegExp ? v.source : Array.isArray(v) ? v.map(normProp) : v && typeof v === 'object' && !isLambda(v) && !(typeof Node !== 'undefined' && v instanceof Node) ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, normProp(x)])) : v);

  class View {
    constructor(app, list) { this._app = app; this._list = list; }
    _node(t, p = {}, c) {
      const node = { id: 'n' + (++this._app._seq), t, p: {} };
      for (const [k, v] of Object.entries(p || {})) if (v !== undefined) node.p[k] = normProp(v);
      if (c) node.c = c;
      this._list.push(node);
      return this;
    }
    _kids(b) { const list = []; if (b) b(new View(this._app, list)); return list; }
    // containers
    col(b, o) { return this._node('col', o, this._kids(b)); }
    row(b, o) { return this._node('row', o, this._kids(b)); }
    grid(cols, b, o) { return this._node('grid', { cols, ...o }, this._kids(b)); }
    card(title, b, o) { return this._node('card', { title, ...o }, this._kids(b)); }
    tabs(b, o) {
      const tabs = [];
      const tb = { tab: (title, bb) => { tabs.push({ id: 'n' + (++this._app._seq), t: 'tab', p: { title }, c: this._kids(bb) }); return tb; } };
      b(tb);
      return this._node('tabs', { ...o, key: (o && o.key) || 'tab_' + this._app._seq }, tabs);
    }
    when(cond, b, o) { return this._node('when', { cond, ...o }, this._kids(b)); }
    divider() { return this._node('divider'); }
    // content
    h(text, level = 2) { return this._node('h', { text, level }); }
    text(md2, o) { return this._node('text', { md: md2, ...o }); }
    note(md2, tone = 'info') { return this._node('note', { md: md2, tone }); }
    badge(text, tone) { return this._node('badge', { text, tone }); }
    code(text, lang) { return this._node('code', { text, lang }); }
    stat(label, value, o) { return this._node('stat', { label, value, ...o }); }
    stats(items, o) { return this._node('stats', { items, ...o }); }
    kv(obj) { return this._node('kv', { obj }); }
    table(rows, o) { return this._node('table', { rows, ...o }); }
    list(items, o) { return this._node('list', { items, ...o }); }
    json(value) { return this._node('json', { value }); }
    link(text, href) { return this._node('link', { text, href }); }
    // charts
    chart(kind, data, o) { return this._node('chart', { kind, data, ...o }); }
    line(data, o) { return this.chart('line', data, o); }
    bar(data, o) { return this.chart('bar', data, o); }
    scatter(data, o) { return this.chart('scatter', data, o); }
    // inputs bound to a state key
    slider(key, o) { return this._node('slider', { key, ...o }); }
    number(key, o) { return this._node('number', { key, ...o }); }
    select(key, options, o) { return this._node('select', { key, options, ...o }); }
    toggle(key, o) { return this._node('toggle', { key, ...(typeof o === 'string' ? { label: o } : o) }); }
    input(key, o) { return this._node('input', { key, ...o }); }
    button(label, onClick, o) { return this._node('button', { label, onClick, ...o }); }
    canvas(draw, o) { return this._node('canvas', { draw, ...o }); }
    // search components (need .use(plan))
    searchBox(key = 'q', o) { return this._node('searchBox', { key, ...o }); }
    facets(key = 'filters', o) { return this._node('facets', { key, ...o }); }
    results(o) { return this._node('results', { ...o }); }
    hits(list, o) { return this._node('hits', { list, ...o }); }
    answer(o) { return this._node('answer', { ...o }); }
    chunk(id, o) { return this._node('chunk', { id, ...o }); }
    trace(o) { return this._node('trace', { ...o }); }
    dag(o) { return this._node('dag', { ...o }); }
    status(o) { return this._node('status', { ...o }); }
  }

  class AppSpec {
    constructor(id) {
      this._seq = 0;
      this.ir = { kind: 'gui', version: 1, id: id || 'app', title: id || 'app', state: {}, derived: {}, watchers: [], uses: null, root: { id: 'root', t: 'col', p: {}, c: [] } };
    }
    title(t) { this.ir.title = t; return this; }
    state(obj) { this.ir.state = { ...this.ir.state, ...JSON.parse(JSON.stringify(obj)) }; return this; }
    derive(key, f) { this.ir.derived[key] = lam(f); return this; }
    watch(key, f) { this.ir.watchers.push({ key, fn: lam(f) }); return this; }
    use(target) { this.ir.uses = { plan: target ? target.id || (target.ir && target.ir.id) || String(target) : null }; return this; }
    view(b) {
      const list = [];
      b(new View(this, list));
      this.ir.root = list.length === 1 && ['col', 'row', 'grid', 'tabs', 'card'].includes(list[0].t) ? list[0] : { id: 'root', t: 'col', p: {}, c: list };
      return this;
    }
    compile() { const ir = JSON.parse(JSON.stringify(this.ir)); ir.digest = rag.util.hash(ir); return ir; }
    toJSON() { return this.compile(); }
  }

  // ────────────────────────────────────────────────────────────── runtime
  const toneClass = t => ({ ok: 'ok', good: 'ok', warn: 'warn', warning: 'warn', bad: 'bad', error: 'bad', danger: 'bad', info: 'sig', sig: 'sig' })[t] || '';
  class Runtime {
    constructor(ir, host = {}) {
      this.ir = ir.kind === 'gui' ? ir : ir.compile();
      this.host = host;
      this.mounted = new Map();
      this.pass = 0;
      this.runSeq = 0;
      this.timers = [];
      const r = host.rag;
      this.bound = !!(this.ir.uses && r);
      this.session = this.bound ? r.session() : null;
      const sig = this.bound ? r.ir().signals : {};
      this.signalKeys = new Set(Object.keys(sig));
      this.state = { ...JSON.parse(JSON.stringify(sig)), ...JSON.parse(JSON.stringify(this.ir.state || {})), out: {}, run: { status: 'idle' }, stream: null, result: null };
      this.der = {};
      this.computeDerived();
    }
    // a read-tracking view of state + derived
    view(deps) {
      return new Proxy({}, {
        get: (t, k) => { if (typeof k !== 'string') return undefined; if (deps) deps.add(k); return k in this.der ? this.der[k] : this.state[k]; },
        has: (t, k) => k in this.der || k in this.state,
        ownKeys: () => [...new Set([...Object.keys(this.state), ...Object.keys(this.der)])],
        getOwnPropertyDescriptor: (t, k) => ({ enumerable: true, configurable: true, value: k in this.der ? this.der[k] : this.state[k] }),
      });
    }
    ctx() {
      const self = this;
      return {
        set: patch => self.set(patch),
        get: k => (k in self.der ? self.der[k] : self.state[k]),
        run: (trigger = 'submit') => self.runNow(trigger),
        submit: patch => { if (patch) self.set(patch, null, { noRun: true }); return self.runNow('submit'); },
        rag: self.host.rag ? {
          chunk: id => self.host.rag.chunk(id), doc: id => self.host.rag.doc(id),
          chunks: f => (self.host.rag.chunks ? self.host.rag.chunks(f) : []), docs: () => (self.host.rag.docs ? self.host.rag.docs() : []),
          explain: id => self.state.result && self.state.result.explain(id), result: () => self.state.result, ir: () => self.host.rag.ir(),
        } : null,
        toast: m => self.host.toast && self.host.toast(m),
        log: (...a) => self.host.log && self.host.log(...a),
        copy: text => self.host.copy && self.host.copy(String(text)),
        download: (name, data, mime) => self.host.download && self.host.download(name, data, mime),
        ...(self.host.ctx || {}),
      };
    }
    val(v, deps, ...extra) {
      if (!isLambda(v)) return v;
      return fnOf(v, `gui ${this.ir.id}`)(this.view(deps), this.ctx(), ...extra);
    }
    async call(v, ...extra) {
      if (!isLambda(v)) return;
      try { await fnOf(v, `gui ${this.ir.id} handler`)(this.view(), this.ctx(), ...extra); } catch (e) { this.showError(e); }
    }
    computeDerived() {
      for (let pass = 0; pass < 4; pass++) {
        let changed = false;
        for (const [k, f] of Object.entries(this.ir.derived || {})) {
          let v;
          try { v = fnOf(f, `derived '${k}'`)(this.view(), this.ctx()); } catch (e) { v = undefined; this.errors = [e.message]; }
          if (v !== this.der[k] && JSON.stringify(v) !== JSON.stringify(this.der[k])) { this.der[k] = v; changed = true; }
        }
        if (!changed) break;
      }
    }
    set(patch, source, opts = {}) {
      const before = {};
      for (const k of Object.keys(patch)) before[k] = this.state[k];
      const derBefore = { ...this.der };
      Object.assign(this.state, patch);
      this.computeDerived();
      const changed = new Set(Object.keys(patch).filter(k => before[k] !== this.state[k]));
      for (const k of Object.keys(this.der)) if (derBefore[k] !== this.der[k]) changed.add(k);
      if (!changed.size) return;
      this.refresh(changed, source);
      for (const w of this.ir.watchers || []) if (changed.has(w.key)) this.call(w.fn, this.state[w.key]);
      if (this.bound && !opts.noRun && [...changed].some(k => this.signalKeys.has(k))) this.schedule(opts.trigger || 'change');
      if (this.host.onState) this.host.onState(this, changed);
    }
    schedule(trigger) {
      clearTimeout(this.debounce);
      if (trigger === 'submit') return this.runNow('submit');
      this.debounce = setTimeout(() => this.runNow('change'), 140);
    }
    signals() { const o = {}; for (const k of this.signalKeys) o[k] = this.state[k]; return o; }
    async runNow(trigger = 'change') {
      if (!this.bound) return null;
      const seq = ++this.runSeq;
      if (this.ctl) this.ctl.abort();
      const ctl = this.ctl = new AbortController();
      this.set({ run: { status: 'running', trigger, at: Date.now() }, ...(trigger === 'submit' ? { stream: null } : {}) }, null, { noRun: true });
      let lastPaint = 0;
      try {
        const r = await this.session.run(this.signals(), {
          trigger, signal: ctl.signal,
          onText: (node, text) => { const t = Date.now(); if (t - lastPaint > 60 && seq === this.runSeq) { lastPaint = t; this.set({ stream: { node, text } }, null, { noRun: true }); } },
        });
        if (seq !== this.runSeq) return r;
        this.set({ out: r.outputs, result: r, stream: null, run: { status: r.errors.length ? 'partial' : 'done', trigger, ms: r.ms, calls: r.calls, errors: r.errors, at: Date.now() } }, null, { noRun: true });
        if (this.host.onResult) this.host.onResult(r, this);
        return r;
      } catch (e) {
        if (seq !== this.runSeq) return null;
        this.set({ run: { status: 'error', trigger, error: e.message || e.code, at: Date.now() } }, null, { noRun: true });
        return null;
      }
    }
    showError(e) {
      const msg = (e && (e.message || e.code)) || String(e);
      if (this.errEl) { this.errEl.hidden = false; this.errEl.textContent = '✗ ' + msg; }
      if (this.host.onError) this.host.onError(e);
    }
    mount(el) {
      this.el = el;
      el.innerHTML = '';
      el.classList.add('g-app');
      this.errEl = h('div', { class: 'g-err', hidden: true });
      el.append(this.errEl, this.render(this.ir.root));
      if (this.bound && this.host.autorun !== false) this.runNow('change');
      return this;
    }
    destroy() { if (this.ctl) this.ctl.abort(); clearTimeout(this.debounce); this.mounted.clear(); this.runSeq++; }
    render(node) {
      const entry = { node, deps: new Set(), el: null, pass: this.pass };
      this.mounted.set(node.id, entry);
      const R = RENDER[node.t] || RENDER.unknown;
      let el;
      try { el = R(node, this, entry.deps); } catch (e) { el = h('div', { class: 'g-err-inline', text: `✗ ${node.t}: ${e.message}` }); }
      el.dataset.gid = node.id;
      entry.el = el;
      return el;
    }
    kids(node) { return (node.c || []).map(c => this.render(c)); }
    refresh(changed, source) {
      this.pass++;
      for (const e of [...this.mounted.values()]) {
        if (!e.el || !e.el.isConnected || e.pass === this.pass || e.node.id === source) continue;
        let hit = false;
        for (const d of e.deps) if (changed.has(d)) { hit = true; break; }
        if (!hit) continue;
        if (e.el._redraw) { e.deps.clear(); e.el._redraw(e.deps); continue; }
        const fresh = this.render(e.node);
        e.el.replaceWith(fresh);
      }
    }
    // hits of a node's output, newest result
    hitsFrom(from, deps) {
      deps.add('out');
      const out = this.state.out || {};
      const id = from || (this.host.rag && this.host.rag.ir().outputs.hits);
      return Array.isArray(out[id]) ? out[id] : [];
    }
  }

  const words = q => [...new Set((String(q || '').toLowerCase().match(/[\p{L}\p{N}]{3,}/gu) || []))].filter(w => !['the', 'and', 'why', 'how', 'what', 'did', 'does', 'was', 'for', 'with', 'are', 'that', 'this', 'when', 'after', 'from'].includes(w));
  function snippet(text, q, n = 260) {
    const plain = String(text || '').replace(/```[\s\S]*?```/g, ' ').replace(/[#>*`|]/g, ' ').replace(/\s+/g, ' ').trim();
    const ws = words(q);
    const low = plain.toLowerCase();
    let at = -1;
    for (const w of ws) { const i = low.indexOf(w.slice(0, Math.max(4, w.length - 2))); if (i >= 0 && (at < 0 || i < at)) at = i; }
    const start = at > 60 ? plain.lastIndexOf(' ', at - 40) + 1 : 0;
    let s = esc(plain.slice(start, start + n)) + (start + n < plain.length ? '…' : '');
    if (start > 0) s = '…' + s;
    for (const w of ws) s = s.replace(new RegExp('\\b(' + w.slice(0, Math.max(4, w.length - 2)).replace(/[^\p{L}\p{N}]/gu, '') + '[\\p{L}\\p{N}]*)', 'giu'), '<mark>$1</mark>');
    return s;
  }

  // ────────────────────────────────────────────────────────────── components
  const RENDER = {
    unknown: n => h('div', { class: 'g-err-inline', text: `unknown component '${n.t}'` }),
    col: (n, rt, d) => h('div', { class: 'g-col' + (rt.val(n.p.gap, d) === 'tight' ? ' tight' : ''), style: n.p.grow ? { flexGrow: String(n.p.grow) } : null }, rt.kids(n)),
    row: (n, rt) => h('div', { class: 'g-row', style: n.p.grow ? { flexGrow: String(n.p.grow) } : null }, rt.kids(n).map((k, i) => { const g = (n.c[i].p || {}).grow; if (g) k.style.flexGrow = String(g); return k; })),
    grid: (n, rt, d) => h('div', { class: 'g-grid-c', style: { gridTemplateColumns: `repeat(auto-fit, minmax(${Math.max(120, Math.floor(560 / (rt.val(n.p.cols, d) || 2)))}px, 1fr))` } }, rt.kids(n)),
    card: (n, rt, d) => h('section', { class: 'g-card' }, n.p.title ? h('div', { class: 'g-card-h', text: rt.val(n.p.title, d) }) : null, h('div', { class: 'g-card-b' }, rt.kids(n))),
    tabs: (n, rt, d) => {
      const key = n.p.key;
      d.add(key);
      const cur = Math.min(rt.state[key] | 0, n.c.length - 1);
      return h('div', { class: 'g-tabs' },
        h('div', { class: 'g-tabbar', role: 'tablist' }, n.c.map((t, i) => h('button', { type: 'button', role: 'tab', 'aria-selected': String(i === cur), class: i === cur ? 'on' : '', onclick: () => rt.set({ [key]: i }) }, t.p.title))),
        h('div', { class: 'g-tabbody' }, n.c[cur] ? rt.kids(n.c[cur]) : null));
    },
    when: (n, rt, d) => (rt.val(n.p.cond, d) ? h('div', { class: 'g-when' }, rt.kids(n)) : h('div', { class: 'g-when', hidden: true })),
    divider: () => h('hr', { class: 'g-hr' }),
    h: (n, rt, d) => h('h' + Math.min(6, Math.max(2, (n.p.level | 0) + 1)), { class: 'g-h' }, String(rt.val(n.p.text, d) ?? '')),
    text: (n, rt, d) => h('div', { class: 'g-md', html: md(rt.val(n.p.md, d), { cite: true }) }),
    note: (n, rt, d) => h('div', { class: 'g-note ' + toneClass(rt.val(n.p.tone, d)), html: md(rt.val(n.p.md, d)) }),
    badge: (n, rt, d) => h('span', { class: 'g-badge ' + toneClass(rt.val(n.p.tone, d)), text: rt.val(n.p.text, d) }),
    code: (n, rt, d) => h('pre', { class: 'g-code' }, h('code', { text: String(rt.val(n.p.text, d) ?? '') })),
    stat: (n, rt, d) => {
      const v = rt.val(n.p.value, d), delta = rt.val(n.p.delta, d);
      return h('div', { class: 'g-stat' }, h('b', {}, typeof v === 'number' ? fmtNum(v) : String(v ?? '—'), n.p.unit ? h('small', { text: ' ' + n.p.unit }) : null),
        h('span', { text: rt.val(n.p.label, d) }), delta != null ? h('em', { class: delta > 0 ? 'up' : delta < 0 ? 'down' : '', text: (delta > 0 ? '+' : '') + fmtNum(delta) }) : null);
    },
    stats: (n, rt, d) => {
      const items = rt.val(n.p.items, d) || [];
      const list = Array.isArray(items) ? items : Object.entries(items).map(([label, value]) => ({ label, value }));
      return h('div', { class: 'g-stats' }, list.map(it => h('div', {}, h('b', { text: typeof it.value === 'number' ? fmtNum(it.value) : String(it.value ?? '—') }), h('span', { text: it.label }))));
    },
    kv: (n, rt, d) => {
      const o = rt.val(n.p.obj, d) || {};
      return h('table', { class: 'g-kv' }, Object.entries(o).map(([k, v]) => h('tr', {}, h('td', { class: 'k', text: k }), h('td', { class: 'v', text: typeof v === 'object' ? JSON.stringify(v) : String(v) }))));
    },
    table: (n, rt, d) => {
      const rows = rt.val(n.p.rows, d) || [];
      const max = n.p.max || 200;
      let cols = rt.val(n.p.columns, d);
      if (!cols) cols = [...new Set(rows.slice(0, 50).flatMap(r => (r && typeof r === 'object' ? Object.keys(r) : [])))];
      cols = cols.map(c => (typeof c === 'string' ? { key: c, label: c } : c));
      const cell = v => (v == null ? '—' : typeof v === 'number' ? fmtNum(v) : typeof v === 'object' ? JSON.stringify(v) : String(v));
      const t = h('table', { class: 'g-table' + (n.p.onRowClick ? ' click' : '') },
        h('thead', {}, h('tr', {}, cols.map(c => h('th', { text: c.label || c.key, class: typeof (rows[0] || {})[c.key] === 'number' ? 'num' : '' })))),
        h('tbody', {}, rows.slice(0, max).map(r => h('tr', { onclick: n.p.onRowClick ? () => rt.call(n.p.onRowClick, r) : null },
          cols.map(c => h('td', { class: typeof (r || {})[c.key] === 'number' ? 'num' : '', text: cell((r || {})[c.key]) }))))));
      return h('div', { class: 'g-table-wrap' }, t, rows.length > max ? h('div', { class: 'g-more', text: `${rows.length - max} more rows` }) : null);
    },
    list: (n, rt, d) => h('ul', { class: 'g-list' }, (rt.val(n.p.items, d) || []).map(it => h('li', { html: md(typeof it === 'string' ? it : JSON.stringify(it)) }))),
    json: (n, rt, d) => h('pre', { class: 'g-code', text: JSON.stringify(rt.val(n.p.value, d), null, 2) }),
    link: (n, rt, d) => h('div', { class: 'g-md', html: md(`[${rt.val(n.p.text, d)}](${rt.val(n.p.href, d)})`) }),
    chart: (n, rt, d) => {
      const kind = rt.val(n.p.kind, d), data = rt.val(n.p.data, d);
      return h('figure', { class: 'g-chart' }, n.p.title ? h('figcaption', { text: rt.val(n.p.title, d) }) : null,
        h('div', { html: chartSvg(kind, data, { xLabel: rt.val(n.p.xLabel, d), yLabel: rt.val(n.p.yLabel, d), yMin: rt.val(n.p.yMin, d), yMax: rt.val(n.p.yMax, d), title: n.p.title }) }));
    },
    slider: (n, rt, d) => {
      d.add(n.p.key);
      const v = rt.state[n.p.key] ?? n.p.min ?? 0;
      const out = h('span', { class: 'g-slider-v', text: fmtNum(+v) });
      const input = h('input', { type: 'range', id: `g-${rt.ir.id}-${n.id}`, min: n.p.min ?? 0, max: n.p.max ?? 1, step: n.p.step ?? 0.01, value: v,
        oninput: e => { out.textContent = fmtNum(+e.target.value); rt.set({ [n.p.key]: +e.target.value }, n.id); } });
      return h('label', { class: 'g-slider' }, h('span', { class: 'g-lab' }, n.p.label || n.p.key, out), input);
    },
    number: (n, rt, d) => {
      d.add(n.p.key);
      return h('label', { class: 'g-field' }, h('span', { class: 'g-lab', text: n.p.label || n.p.key }),
        h('input', { type: 'number', id: `g-${rt.ir.id}-${n.id}`, value: rt.state[n.p.key] ?? '', step: n.p.step ?? 'any', onchange: e => rt.set({ [n.p.key]: +e.target.value }, n.id) }));
    },
    select: (n, rt, d) => {
      d.add(n.p.key);
      const opts = (rt.val(n.p.options, d) || []).map(o => (typeof o === 'object' ? o : { value: o, label: String(o) }));
      return h('label', { class: 'g-field' }, h('span', { class: 'g-lab', text: n.p.label || n.p.key }),
        h('select', { id: `g-${rt.ir.id}-${n.id}`, onchange: e => rt.set({ [n.p.key]: opts[e.target.selectedIndex].value }, n.id) },
          opts.map(o => h('option', { selected: String(o.value) === String(rt.state[n.p.key]) }, o.label))));
    },
    toggle: (n, rt, d) => {
      d.add(n.p.key);
      const on = !!rt.state[n.p.key];
      return h('button', { type: 'button', class: 'g-toggle', role: 'switch', 'aria-checked': String(on), onclick: () => rt.set({ [n.p.key]: !on }) }, h('i'), n.p.label || n.p.key);
    },
    input: (n, rt, d) => {
      d.add(n.p.key);
      return h('label', { class: 'g-field' }, n.p.label ? h('span', { class: 'g-lab', text: n.p.label }) : null,
        h('input', { type: 'text', id: `g-${rt.ir.id}-${n.id}`, value: rt.state[n.p.key] ?? '', placeholder: n.p.placeholder || '', oninput: e => rt.set({ [n.p.key]: e.target.value }, n.id) }));
    },
    button: (n, rt, d) => h('button', { type: 'button', class: 'btn' + (n.p.tone === 'primary' ? ' primary' : ''), disabled: !!rt.val(n.p.disabled, d), onclick: () => rt.call(n.p.onClick) }, rt.val(n.p.label, d)),
    canvas: (n, rt, d) => {
      const W = n.p.width || 300, H = n.p.height || 200;
      const cv = h('canvas', { class: 'g-canvas', role: 'img', 'aria-label': n.p.label || 'drawing', tabindex: n.p.onKey ? '0' : null });
      let shapes = [], hover = null;
      const theme = () => { const cs = getComputedStyle(document.documentElement); const g = k => cs.getPropertyValue(k).trim(); return { ink: g('--ink'), muted: g('--muted'), line: g('--hair'), panel: g('--grid'), bg: g('--sheet'), accent: g('--sig'), ok: g('--ok'), warn: g('--warn'), alert: g('--bad') }; };
      const draw = deps => {
        const dpr = window.devicePixelRatio || 1, cw = cv.clientWidth || W, scale = cw / W;
        cv.width = Math.round(W * scale * dpr); cv.height = Math.round(H * scale * dpr);
        const c = cv.getContext('2d');
        c.setTransform(scale * dpr, 0, 0, scale * dpr, 0, 0);
        shapes = [];
        const num = (sig, ...xs) => { for (const x of xs) if (typeof x !== 'number' || !isFinite(x)) throw new Error(`${sig}: coordinates must be finite numbers`); };
        const style = (o, fill) => { c.globalAlpha = o.alpha ?? 1; c.lineWidth = o.width ?? 1.5; c.setLineDash(o.dash || []); c.strokeStyle = o.stroke || o.color || g.theme.ink; c.fillStyle = o.fill || g.theme.ink; return fill; };
        const g = {
          width: W, height: H, theme: theme(), raw: c, t: performance.now() / 1000,
          clear(col) { c.clearRect(0, 0, W, H); if (col) { c.fillStyle = col; c.fillRect(0, 0, W, H); } },
          line(x1, y1, x2, y2, o = {}) { num('g.line(x1, y1, x2, y2, o)', x1, y1, x2, y2); style(o); c.beginPath(); c.moveTo(x1, y1); c.lineTo(x2, y2); c.stroke(); if (o.id != null) shapes.push({ id: o.id, hit: (x, y) => distSeg(x, y, x1, y1, x2, y2) < 5 }); },
          polyline(pts, o = {}) { style(o); c.beginPath(); pts.forEach(([x, y], i) => (i ? c.lineTo(x, y) : c.moveTo(x, y))); c.stroke(); },
          rect(x, y, w, h2, o = {}) { num('g.rect(x, y, w, h, o)', x, y, w, h2); style(o); c.beginPath(); c.rect(x, y, w, h2); if (o.fill) c.fill(); if (o.stroke || !o.fill) c.stroke(); if (o.id != null) shapes.push({ id: o.id, hit: (px, py) => px >= x && px <= x + w && py >= y && py <= y + h2 }); },
          circle(x, y, r, o = {}) { num('g.circle(x, y, r, o)', x, y, r); style(o); c.beginPath(); c.arc(x, y, r, 0, Math.PI * 2); if (o.fill) c.fill(); if (o.stroke || !o.fill) c.stroke(); if (o.id != null) shapes.push({ id: o.id, hit: (px, py) => (px - x) ** 2 + (py - y) ** 2 <= r * r }); },
          text(t, x, y, o = {}) { num('g.text(text, x, y, o)', x, y); c.globalAlpha = o.alpha ?? 1; c.font = `${o.weight || 500} ${o.size || 12}px ${o.mono ? 'IBM Plex Mono, monospace' : 'Arial, sans-serif'}`; c.textAlign = o.align || 'left'; c.textBaseline = o.baseline || 'alphabetic'; c.fillStyle = o.fill || o.color || g.theme.ink; c.fillText(String(t), x, y); },
        };
        try { rt.val(n.p.draw, deps, g, { hover }); } catch (e) { c.setTransform(scale * dpr, 0, 0, scale * dpr, 0, 0); c.fillStyle = theme().alert || 'red'; c.font = '12px monospace'; c.fillText('✗ ' + e.message, 8, 18); }
      };
      const distSeg = (px, py, x1, y1, x2, y2) => { const dx = x2 - x1, dy = y2 - y1, l = dx * dx + dy * dy || 1, t = Math.max(0, Math.min(1, ((px - x1) * dx + (py - y1) * dy) / l)); return Math.hypot(px - x1 - t * dx, py - y1 - t * dy); };
      const pos = e => { const r = cv.getBoundingClientRect(), s = W / r.width; return { x: (e.clientX - r.left) * s, y: (e.clientY - r.top) * s }; };
      const hitAt = p => { for (let i = shapes.length - 1; i >= 0; i--) if (shapes[i].hit(p.x, p.y)) return shapes[i].id; return null; };
      if (n.p.onClick) cv.addEventListener('click', e => { const p = pos(e); rt.call(n.p.onClick, { ...p, hit: hitAt(p) }); });
      cv.addEventListener('mousemove', e => { const p = pos(e), hit = hitAt(p); cv.style.cursor = hit != null && n.p.onClick ? 'pointer' : ''; if (hit !== hover) { hover = hit; if (n.p.onHover) rt.call(n.p.onHover, { ...p, hit }); } });
      if (n.p.onKey) cv.addEventListener('keydown', e => rt.call(n.p.onKey, { key: e.key }));
      cv.style.aspectRatio = `${W} / ${H}`;
      cv._redraw = deps => draw(deps);
      requestAnimationFrame(() => draw(d));
      if (n.p.animate) { const loop = () => { if (!cv.isConnected) return; draw(new Set()); requestAnimationFrame(loop); }; requestAnimationFrame(loop); }
      return cv;
    },

    // ── search components
    status: (n, rt, d) => {
      d.add('run'); d.add('out');
      const r = rt.state.run || {};
      const hits = rt.hitsFrom(n.p.from, d);
      const txt = r.status === 'running' ? (r.trigger === 'submit' ? 'Answering…' : 'Searching…') : r.status === 'error' ? '✗ ' + r.error : r.status === 'idle' ? '' :
        `${hits.length} results · ${r.trigger === 'submit' ? 'reranked' : 'lexical'} · ${r.ms} ms${r.calls ? ` · ${r.calls} Claude call${r.calls > 1 ? 's' : ''}` : ''}`;
      return h('div', { class: 'g-status' + (r.status === 'error' ? ' bad' : r.status === 'running' ? ' busy' : ''), text: txt });
    },
    searchBox: (n, rt, d) => {
      const key = n.p.key || 'q';
      d.add('run'); d.add(key);
      let running = rt.state.run && rt.state.run.status === 'running' && rt.state.run.trigger === 'submit';
      const input = h('input', { type: 'search', id: `g-${rt.ir.id}-${n.id}`, class: 'g-search-in', value: rt.state[key] ?? '', placeholder: n.p.placeholder || 'Search…', 'aria-label': n.p.placeholder || 'Search',
        oninput: e => rt.set({ [key]: e.target.value }, n.id),
        onkeydown: e => { if (e.key === 'Enter') { e.preventDefault(); rt.set({ [key]: e.target.value }, n.id, { noRun: true }); rt.runNow('submit'); } } });
      const btn = h('button', { type: 'button', class: 'btn primary', onclick: () => { if (running) { rt.ctl && rt.ctl.abort(); rt.runSeq++; rt.set({ run: { status: 'done', trigger: 'change', ms: 0 } }, null, { noRun: true }); } else { rt.set({ [key]: input.value }, n.id, { noRun: true }); rt.runNow('submit'); } } },
        running ? 'Stop' : (n.p.submitLabel || 'Ask'));
      const wrap = h('div', { class: 'g-search' }, input, btn);
      wrap._focus = () => input.focus();
      // update in place so the input keeps focus while results stream in
      wrap._redraw = deps => {
        deps.add('run'); deps.add(key);
        running = rt.state.run && rt.state.run.status === 'running' && rt.state.run.trigger === 'submit';
        btn.textContent = running ? 'Stop' : (n.p.submitLabel || 'Ask');
        const v = rt.state[key] ?? '';
        if (input.value !== v && document.activeElement !== input) input.value = v;
      };
      return wrap;
    },
    facets: (n, rt, d) => {
      const key = n.p.key || 'filters';
      d.add(key);
      const sel = rt.state[key] || {};
      const ir = rt.host.rag.ir();
      const from = n.p.from || (ir.nodes.find(x => /^filter/.test(x.t)) || {}).in?.[0] || ir.outputs.hits;
      const hits = rt.hitsFrom(from, d);
      const fields = n.p.fields || ['type', 'tags'];
      const box = h('div', { class: 'g-facets' });
      for (const f of fields) {
        const counts = new Map();
        for (const hit of hits) { const c = rt.host.rag.chunk(hit.id); if (!c) continue; for (const v of [].concat(c.meta[f] ?? [])) counts.set(String(v), (counts.get(String(v)) || 0) + 1); }
        for (const v of [].concat(sel[f] ?? [])) if (!counts.has(String(v))) counts.set(String(v), 0);
        if (!counts.size) continue;
        const top = [...counts].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, n.p.max || 8);
        box.append(h('div', { class: 'g-facet' }, h('span', { class: 'g-lab', text: f }),
          top.map(([v, c]) => {
            const on = [].concat(sel[f] ?? []).map(String).includes(v);
            return h('button', { type: 'button', class: 'chip' + (on ? ' on' : ''), 'aria-pressed': String(on), onclick: () => {
              const cur = [].concat(sel[f] ?? []).map(String);
              const next = on ? cur.filter(x => x !== v) : [...cur, v];
              const patch = { ...sel };
              if (next.length) patch[f] = next; else delete patch[f];
              rt.set({ [key]: patch });
            } }, v, h('small', { text: ' ' + c }));
          })));
      }
      if (!box.children.length) box.append(h('div', { class: 'g-empty', text: 'Facets appear once there are results.' }));
      return box;
    },
    results: (n, rt, d) => {
      d.add('run'); d.add('q'); d.add(n.p.selectedKey || 'selected');
      const ir = rt.host.rag.ir();
      const hits = rt.hitsFrom(n.p.from, d);
      const q = rt.state.q;
      const sel = rt.state[n.p.selectedKey || 'selected'];
      const limit = n.p.limit || 12;
      const r = rt.state.run || {};
      const list = h('ol', { class: 'g-results' });
      if (!hits.length) {
        list.append(h('li', { class: 'g-empty', text: r.status === 'running' ? 'Searching…' : q ? 'No sections match. Try other words, or clear a filter.' : 'Type a question about the reports.' }));
      }
      hits.slice(0, limit).forEach((hit, i) => {
        const c = rt.host.rag.chunk(hit.id);
        if (!c) return;
        let item = null;
        if (n.p.item) { try { item = rt.val(n.p.item, d, hit); } catch (e) { item = null; } }
        const via = hit.parts ? Object.keys(hit.parts) : hit.via ? [hit.via] : [];
        const li = h('li', { class: 'g-result' + (sel === hit.id ? ' on' : ''), tabindex: '0', role: 'button',
          onclick: () => (n.p.onSelect ? rt.call(n.p.onSelect, { ...hit, ...c }) : rt.set({ [n.p.selectedKey || 'selected']: hit.id })),
          onkeydown: e => { if (e.key === 'Enter') li.click(); } },
          h('div', { class: 'g-r-top' }, h('span', { class: 'g-rank', text: String(i + 1).padStart(2, '0') }),
            h('b', { text: item && item.title || c.headingPath[c.headingPath.length - 1] || c.title }),
            h('span', { class: 'g-r-chips' }, via.map(v => h('i', { class: 'kchip', text: v })), c.kind !== 'section' ? h('i', { class: 'kchip alt', text: c.kind }) : null)),
          h('div', { class: 'g-r-doc', text: item && item.sub || (c.title + (c.headingPath.length > 1 ? ' › ' + c.headingPath.slice(0, -1).join(' › ') : '')) }),
          h('p', { class: 'g-r-snip', html: item && item.snippet ? esc(item.snippet) : snippet(c.text, q) }),
          hit.reason ? h('div', { class: 'g-r-why', text: '↳ ' + hit.reason }) : null);
        list.append(li);
      });
      return h('div', { class: 'g-results-wrap' }, list, hits.length > limit ? h('div', { class: 'g-more', text: `${hits.length - limit} more` }) : null);
    },
    hits: (n, rt, d) => {
      const list = rt.val(n.p.list, d) || [];
      return h('ol', { class: 'g-results compact' }, list.map((hit, i) => {
        const c = rt.host.rag && rt.host.rag.chunk(hit.id || hit);
        if (!c) return h('li', { class: 'g-empty', text: String(hit.id || hit) });
        return h('li', { class: 'g-result', tabindex: '0', role: 'button', onclick: () => (n.p.onSelect ? rt.call(n.p.onSelect, { ...hit, ...c }) : rt.host.openChunk && rt.host.openChunk(c.id)) },
          h('div', { class: 'g-r-top' }, h('span', { class: 'g-rank', text: String(i + 1).padStart(2, '0') }), h('b', { text: c.headingPath[c.headingPath.length - 1] || c.title }), hit.score != null ? h('span', { class: 'g-r-chips' }, h('i', { class: 'kchip', text: fmtNum(+hit.score) })) : null),
          h('div', { class: 'g-r-doc', text: c.title }), hit.reason ? h('div', { class: 'g-r-why', text: '↳ ' + hit.reason }) : null);
      }));
    },
    answer: (n, rt, d) => {
      d.add('out'); d.add('stream'); d.add('run');
      const ir = rt.host.rag.ir();
      const from = n.p.from || ir.outputs.answer;
      const a = (rt.state.out || {})[from];
      const st = rt.state.stream && rt.state.stream.node === from ? rt.state.stream.text : null;
      const r = rt.state.run || {};
      const box = h('section', { class: 'g-answer' }, h('div', { class: 'g-card-h' }, h('span', { text: n.p.title || 'Answer' }), a && a.evidence ? h('span', { class: 'meta', text: `${(a.cited || []).length} of ${a.evidence.length} sources cited` }) : null));
      const body = h('div', { class: 'g-answer-b' });
      if (!from) body.append(h('div', { class: 'g-empty', text: 'This plan has no answer node.' }));
      else if (st || a) {
        const text = st || a.text;
        body.innerHTML = md(text, { cite: true });
        if (a && a.evidence) {
          const ev = h('ol', { class: 'g-evidence' }, a.evidence.filter(e => !a.cited || !a.cited.length || a.cited.includes(e.label)).map(e => h('li', {},
            h('button', { type: 'button', class: 'cite', 'data-cite': e.label, text: e.label }), h('span', { text: ` ${e.headingPath[e.headingPath.length - 1] || e.title}` }), h('small', { text: ' · ' + clip(e.title, 48) }))));
          body.append(ev);
        }
        body.addEventListener('click', e => {
          const b = e.target.closest('[data-cite]');
          if (!b || !a) return;
          const ev2 = (a.evidence || []).find(x => x.label === b.dataset.cite);
          if (!ev2) return;
          if (n.p.onCite) rt.call(n.p.onCite, ev2); else rt.set({ [n.p.selectedKey || 'selected']: ev2.id });
        });
      } else if (r.status === 'running' && r.trigger === 'submit') body.append(h('div', { class: 'g-thinking', text: 'Thinking…' }));
      else body.append(h('div', { class: 'g-empty', html: 'Press <kbd>Enter</kbd> in the search box for a cited answer. Typing only runs the lexical nodes.' }));
      if (r.errors && r.errors.length) body.append(h('div', { class: 'g-err-inline', text: '✗ ' + r.errors.join(' · ') }));
      box.append(body);
      return box;
    },
    chunk: (n, rt, d) => {
      const id = rt.val(n.p.id, d);
      const c = id && rt.host.rag.chunk(id);
      if (!c) return h('div', { class: 'g-empty', text: 'Select a result to read the section.' });
      const nb = rt.host.rag.neighbors ? rt.host.rag.neighbors(c.id) : { prev: null, next: null };
      const key = n.p.selectedKey || 'selected';
      return h('section', { class: 'g-chunk' },
        h('div', { class: 'g-card-h' }, h('span', { text: 'Section' }), h('span', { class: 'meta', text: `${c.tokens} tok · ${c.id.split('#')[1] || c.id}` })),
        h('div', { class: 'g-chunk-b' },
          h('div', { class: 'g-crumb', text: [c.title, ...c.headingPath].join(' › ') }),
          h('div', { class: 'g-md', html: md(c.text) }),
          h('div', { class: 'g-chunk-nav' },
            h('button', { type: 'button', class: 'btn tiny', disabled: !nb.prev, onclick: () => rt.set({ [key]: nb.prev }) }, '← prev'),
            h('button', { type: 'button', class: 'btn tiny', disabled: !nb.next, onclick: () => rt.set({ [key]: nb.next }) }, 'next →'),
            rt.host.openChunk ? h('button', { type: 'button', class: 'btn tiny', onclick: () => rt.host.inspectChunk && rt.host.inspectChunk(c.id, rt.state.result) }, 'Why this?') : null)));
    },
    trace: (n, rt, d) => {
      d.add('result');
      const r = rt.state.result;
      if (!r) return h('div', { class: 'g-empty', text: 'No run yet.' });
      return h('table', { class: 'g-table' }, h('thead', {}, h('tr', {}, ['node', 'status', 'in', 'out', 'ms'].map(x => h('th', { text: x })))),
        h('tbody', {}, r.trace.map(t => h('tr', { class: t.status }, h('td', { text: t.node }), h('td', { text: t.status }), h('td', { class: 'num', text: t.in }), h('td', { class: 'num', text: t.out }), h('td', { class: 'num', text: t.ms })))));
    },
    dag: (n, rt, d) => {
      d.add('result');
      const el = h('div', { class: 'g-dag' });
      if (rt.host.renderDag) requestAnimationFrame(() => rt.host.renderDag(el, rt.host.rag.ir(), { compact: true, result: rt.state.result, liveOnly: n.p.all !== true }));
      return el;
    },
  };

  const gui = {
    app: id => new AppSpec(id),
    bake: f => f(gui.bakeCtx || {}),
    mount: (app, el, host) => new Runtime(app.kind === 'gui' ? app : app.compile(), host).mount(el),
    md, chart: chartSvg, h, esc, snippet, fmtNum,
    Runtime, AppSpec,
    components: () => Object.keys(RENDER).filter(k => k !== 'unknown'),
  };
  root.gui = gui;
})(typeof globalThis !== 'undefined' ? globalThis : this);
