/* claude.js — a local window.claude runtime for the RAG workbench served by `ragwb workbench`.
 *
 * Implements the capabilities the prototype uses (app.js initCaps):
 *   sample   — LLM calls with page-executed tools (SSE protocol modelled on slop-computer)
 *   db       — Firestore-style docs: db.doc(path).get/set/delete/collection(name)
 *   user     — user.id() → "local"
 *   downloads— downloads.save({filename, data}) → browser download
 */
(function () {
  'use strict';
  const rpc = async (path, body) => {
    const r = await fetch(path, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body || {}) });
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw (j && j.error) || { code: 'upstream_error', message: 'HTTP ' + r.status };
    return j;
  };

  // ── db (Firestore-like subset)
  const snap = (path, data) => ({ id: path.split('/').pop(), ref: docRef(path), exists: data !== null && data !== undefined, data: () => data });
  function docRef(path) {
    return {
      id: path.split('/').pop(), path,
      get: () => rpc('/_rt/db/get', { path }).then(r => snap(path, r.data)),
      set: data => rpc('/_rt/db/set', { path, data: JSON.parse(JSON.stringify(data)) }).then(() => undefined),
      delete: () => rpc('/_rt/db/delete', { path }).then(() => undefined),
      collection: name => collRef(path + '/' + name),
    };
  }
  function collRef(path) {
    return {
      path,
      doc: id => docRef(path + '/' + id),
      get: () => rpc('/_rt/db/list', { path }).then(r => ({ docs: r.docs.map(d => snap(d.path, d.data)), size: r.docs.length, empty: !r.docs.length })),
    };
  }
  const db = { doc: docRef, collection: collRef };

  const user = { id: () => Promise.resolve('local'), name: () => Promise.resolve('local user') };

  const downloads = {
    save: ({ filename, data }) => {
      const blob = data instanceof Blob ? data : new Blob([data], { type: 'application/octet-stream' });
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = filename || 'download';
      document.body.append(a);
      a.click();
      setTimeout(() => { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
      return Promise.resolve();
    },
  };

  // ── sample: POST /_rt/sample streams SSE events call/text/tool/result/error.
  // A `tool` event runs the page tool here and POSTs its result back.
  const fail = (code, message, text) => Object.assign({ code, message }, text ? { text } : {});
  async function call(input, o, json) {
    o = o || {};
    if (typeof input !== 'string' && !Array.isArray(input)) throw fail('invalid_request', 'input must be a string or an array of turns');
    if (o.signal && o.signal.aborted) throw fail('cancelled', 'the call was aborted');
    const tools = Array.isArray(o.tools) ? o.tools : [];
    for (const t of tools) if (!t || typeof t.name !== 'string' || typeof t.execute !== 'function') throw fail('invalid_request', 'a tool is {name, description, execute}');
    const byName = new Map(tools.map(t => [t.name, t]));
    const body = {
      input, modelTier: o.modelTier || 'default', model: o.model || '', json: !!json,
      tools: tools.map(t => ({ name: t.name, description: String(t.description || ''), inputSchema: t.inputSchema || null })),
    };
    const ctrl = new AbortController();
    let callId = '', text = '';
    const onAbort = () => { if (callId) rpc('/_rt/sample/' + callId + '/cancel', {}).catch(() => {}); ctrl.abort(); };
    if (o.signal) o.signal.addEventListener('abort', onAbort, { once: true });
    try {
      const r = await fetch('/_rt/sample', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body), signal: ctrl.signal });
      if (!r.ok || !r.body) { const e = await r.json().catch(() => null); throw (e && e.error) || fail('upstream_error', 'HTTP ' + r.status); }
      const reader = r.body.getReader(), dec = new TextDecoder();
      let buf = '';
      for (;;) {
        const x = await reader.read();
        if (x.done) break;
        buf += dec.decode(x.value, { stream: true });
        let i;
        while ((i = buf.indexOf('\n\n')) >= 0) {
          const block = buf.slice(0, i); buf = buf.slice(i + 2);
          let type = '', data = '';
          for (const line of block.split('\n')) {
            if (line.startsWith('event: ')) type = line.slice(7);
            else if (line.startsWith('data: ')) data += line.slice(6);
          }
          const d = data ? JSON.parse(data) : {};
          if (type === 'call') callId = d.id;
          else if (type === 'text') { text = d.text; if (o.onText) try { o.onText({ text: d.text, delta: d.delta }); } catch (_) { /* page callback */ } }
          else if (type === 'tool') {
            const t = byName.get(d.name);
            let value, isError = false;
            try {
              if (!t) throw new Error('unknown tool ' + d.name);
              value = await t.execute(d.input || {}, { signal: ctrl.signal });
            } catch (e) { value = String((e && e.message) || e); isError = true; }
            await rpc('/_rt/sample/' + callId + '/tools/' + encodeURIComponent(d.use), { value: value === undefined ? null : value, isError });
          } else if (type === 'result') return json ? d.json : { text: d.text, truncated: !!d.truncated, modelTierApplied: d.modelTierApplied };
          else if (type === 'error') throw fail(d.code || 'upstream_error', d.message || '', d.text || text);
        }
      }
      throw fail('upstream_error', 'stream ended without a result', text);
    } catch (e) {
      if (ctrl.signal.aborted) throw fail('cancelled', 'the call was aborted', text);
      throw e && e.code ? e : fail('upstream_error', String((e && e.message) || e), text);
    } finally {
      if (o.signal) o.signal.removeEventListener('abort', onAbort);
    }
  }
  const sample = (input, o) => call(input, o, false);
  sample.json = (input, o) => call(input, o, true);
  sample.limits = () => Promise.resolve({ tools: { maxCount: 16 }, maxInputBytes: 262144 });

  const caps = { sample, db, user, downloads };
  window.claude = {
    use: name => (caps[name] ? Promise.resolve(caps[name]) : Promise.reject(fail('not_declared', 'capability ' + name + ' is not available in ragwb'))),
  };
})();
