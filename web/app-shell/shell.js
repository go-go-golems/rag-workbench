// shell.js — mounts a served app's gui with a host.rag backed by the server's
// session (`ragwb serve`, guide P7/§9.8 "RemoteRag").
//
// The gui reads chunks synchronously while rendering, so every run response
// carries the bodies of the chunks it mentions plus the neighbors of its hits.
// A lookup that misses the cache returns null, fetches in the background and
// re-renders when the chunk arrives (e.g. walking ← → past the prefetched ones).
(async () => {
  'use strict';
  const $ = s => document.querySelector(s);
  const name = decodeURIComponent(location.pathname.split('/')[2] || '');
  const api = p => `/api/rag/${encodeURIComponent(name)}${p}`;
  const el = $('#app');

  const fail = msg => { el.innerHTML = ''; el.append(gui.h('div', { class: 'sh-err', text: msg })); };
  let info;
  try {
    const res = await fetch(api(''));
    info = await res.json();
    if (!res.ok) throw new Error((info.error && info.error.message) || res.statusText);
  } catch (e) { fail('Could not open the app: ' + e.message); return; }

  const ir = rag.ir(info.ir);
  document.title = info.title;
  $('#sh-title').textContent = info.title;
  $('#sh-meta').textContent = `plan ${info.id} · ${info.stats.chunks} chunks · ${info.stats.indexes.join(', ')} · server-side session`;

  // ── cache of what the server sent
  const chunks = new Map(), nbs = new Map(), docs = new Map(), missing = new Set(), inflight = new Set();
  const absorb = p => {
    for (const [id, c] of Object.entries(p.chunks || {})) chunks.set(id, c);
    for (const [id, n] of Object.entries(p.neighbors || {})) nbs.set(id, n);
  };
  let rt = null;
  const rerender = () => { if (rt) rt.refresh(new Set([...Object.keys(rt.state), ...Object.keys(rt.der)])); };
  const fetchMiss = (kind, id) => {
    const key = kind + '\0' + id;
    if (missing.has(key) || inflight.has(key)) return;
    inflight.add(key);
    fetch(api(`/${kind}?id=${encodeURIComponent(id)}`)).then(r => r.json()).then(p => {
      if (kind === 'doc') { if (p) docs.set(id, p); else missing.add(key); }
      else { absorb(p); if (!chunks.has(id)) missing.add(key); else if (!nbs.has(id)) nbs.set(id, { prev: null, next: null }); } // no links: the chunk has no reading order
      if (!missing.has(key)) rerender();
    }).catch(() => missing.add(key)).finally(() => inflight.delete(key));
  };
  const chunk = id => { const c = chunks.get(id); if (!c && id) fetchMiss('chunk', id); return c || null; };

  // ── results: the engine's result plus the methods rag.js decorates it with
  const decorate = result => {
    const def = (k, v) => Object.defineProperty(result, k, { value: v, enumerable: false });
    def('chunk', id => chunks.get(id) || null);
    def('top', (n = 5, from) => (from ? result.outputs[from] || [] : result.hits).slice(0, n).map(h => {
      const c = chunks.get(h.id) || {};
      return { id: h.id, score: h.score, doc: c.title, heading: (c.headingPath || []).join(' › '), ...(h.reason ? { reason: h.reason } : {}) };
    }));
    def('explain', id => rag.explain(result, typeof id === 'object' ? id.id : id));
    return result;
  };

  // ── per-run timings in the footer: server split and round trip
  const stats = { n: 0, rtt: [] };
  const showTiming = (t, rtt, trigger) => {
    stats.n++; stats.rtt.push(rtt); if (stats.rtt.length > 50) stats.rtt.shift();
    const med = [...stats.rtt].sort((a, b) => a - b)[Math.floor(stats.rtt.length / 2)];
    $('#sh-timing').innerHTML = '';
    $('#sh-timing').append(...[
      ['run', trigger], ['engine', t.engineMs.toFixed(1) + ' ms'], ['pack', t.packMs.toFixed(1) + ' ms'],
      ['server', t.totalMs.toFixed(1) + ' ms'], ['round trip', rtt.toFixed(0) + ' ms'],
      ['median rtt', `${med.toFixed(0)} ms (${stats.rtt.length})`], ['payload', `${(t.bytes / 1024).toFixed(1)} KB · ${t.chunks} chunks`],
    ].map(([k, v]) => gui.h('span', {}, k + ' ', gui.h('b', { text: v }))));
  };

  // Server-Sent Events over fetch (EventSource can't POST): yields {event, data}
  async function* sse(res) {
    const rd = res.body.getReader(), dec = new TextDecoder();
    let buf = '';
    for (;;) {
      const { done, value } = await rd.read();
      if (value) buf += dec.decode(value, { stream: true });
      let i;
      while ((i = buf.indexOf('\n\n')) >= 0) {
        const block = buf.slice(0, i); buf = buf.slice(i + 2);
        let event = 'message', data = '';
        for (const line of block.split('\n')) {
          if (line.startsWith('event:')) event = line.slice(6).trim();
          else if (line.startsWith('data:')) data += line.slice(5).trim();
        }
        if (data) yield { event, data: JSON.parse(data) };
      }
      if (done) return;
    }
  }

  // o.onText(node, textSoFar) for LLM nodes; o.onEvent(event) for every engine event
  // (node start/end, hits, text deltas, tools, progress; RAGWB-004 §6.4)
  async function run(signals, o = {}) {
    const trigger = o.trigger || 'change';
    const stream = (!!o.onText && trigger === 'submit') || !!o.onEvent;
    const t0 = performance.now();
    const res = await fetch(api('/run'), {
      method: 'POST', signal: o.signal,
      headers: { 'Content-Type': 'application/json', ...(stream ? { Accept: 'text/event-stream' } : {}) },
      body: JSON.stringify({ signals, trigger }),
    });
    let out;
    if (!stream || !(res.headers.get('Content-Type') || '').startsWith('text/event-stream')) {
      out = await res.json();
      if (!res.ok) throw new Error((out.error && out.error.message) || res.statusText);
    } else {
      const texts = {};
      for await (const m of sse(res)) {
        if (m.event === 'result') { out = m.data; continue; }
        if (m.event === 'error') throw new Error(m.data.message || 'run failed');
        if (m.event === 'text' && o.onText) {
          texts[m.data.node] = (m.data.reset ? '' : texts[m.data.node] || '') + m.data.delta;
          o.onText(m.data.node, texts[m.data.node]);
        }
        if (o.onEvent) try { o.onEvent({ type: m.event, ...m.data }); } catch (_) { /* page callback */ }
      }
      if (!out) throw new Error('the run stream ended without a result');
    }
    absorb(out);
    showTiming(out.timing, performance.now() - t0, trigger);
    return decorate(out.result);
  }

  const host = {
    rag: {
      session: () => ({ run }),
      ir: () => ir,
      chunk,
      doc: id => { const d = docs.get(id); if (!d && id) fetchMiss('doc', id); return d || null; },
      chunks: f => [...chunks.values()].filter(f || (() => true)), // what this page has seen, not the corpus
      docs: () => [...docs.values()],
      // prefetched neighbor bodies come without their own links: fetch those on first look
      neighbors: id => { const n = nbs.get(id); if (!n) { if (id) fetchMiss('chunk', id); return { prev: null, next: null }; } return n; },
    },
    toast: msg => {
      document.querySelectorAll('.toast').forEach(t => t.remove());
      const t = gui.h('div', { class: 'toast', role: 'status', text: msg });
      document.body.append(t);
      setTimeout(() => t.remove(), 2600);
    },
    copy: text => navigator.clipboard && navigator.clipboard.writeText(text),
    download: (file, data, mime = 'text/plain') => {
      const a = gui.h('a', { href: URL.createObjectURL(new Blob([data], { type: mime })), download: file });
      document.body.append(a); a.click(); a.remove();
    },
    log: (...a) => console.log('[app]', ...a),
    onError: e => console.error(e),
  };
  try {
    rt = new gui.Runtime(info.app, host);
    window.ragwbShell = { rt, info, cache: { chunks, nbs, docs } }; // for debugging and tests
    rt.mount(el);
  } catch (e) { fail('Could not mount the app: ' + e.message); }
})();
