import { resolveTarget } from '../api/target.js';
import { portsOf } from '../components/registry.js';
import { RagError } from '../core/errors.ts';
import { applyOps, projectLexical } from '../graph/graph.js';

async function exportHtml(target, opts = {}) {
  const { ir, engine } = resolveTarget(target);
  if (!engine.artifacts(ir) || engine.dirty(ir).length) {
    const report = await engine.buildShared(ir);
    if (!report.ok) throw new RagError('export', report.errors.join('; '));
  }
  const snapshot = engine.artifacts(ir);
  if (!snapshot) throw new RagError('export', 'a complete build is required');
  const release = engine.acquire(snapshot);
  let pruned;
  try {
    const lexical = projectLexical(ir);
    const changes = [];
    for (const node of lexical.nodes) {
      if (node.phase !== 'build' || node.out === 'index') continue;
      const value =
        node.out === 'files'
          ? []
          : node.out === 'docs' || node.out === 'chunks'
            ? engine.inputDataset(node.id, snapshot.byId, ir).materialize()
            : snapshot.byId.get(node.id);
      changes.push({
        op: 'replace',
        node: node.id,
        with: { t: 'snapshot.value', p: { kind: node.out, inputs: portsOf(node).in, value } },
      });
    }
    pruned = applyOps(lexical, changes);
  } finally {
    release();
  }
  const src = engine.env.librarySource ? await engine.env.librarySource() : '';
  const title = opts.title || ir.id;
  const esc = (s) =>
    String(s).replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[c]);
  const json = (s) => JSON.stringify(s).replace(/</g, '\\u003c');
  return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${esc(title)}</title>
<style>:root{--paper:#FAFAF8;--ink:#111417;--muted:#5D646A;--hair:#E3E6E8;--sig:#1F4FD1}@media(prefers-color-scheme:dark){:root{--paper:#121518;--ink:#E3E6E9;--muted:#98A0A7;--hair:#2A3035;--sig:#86A2F7}}
body{margin:0;background:var(--paper);color:var(--ink);font:14px/1.5 Arial,Helvetica,sans-serif}main{max-width:760px;margin:0 auto;padding:24px max(16px,env(safe-area-inset-left))}
h1{font-size:20px;margin:0 0 12px}input{width:100%;box-sizing:border-box;font:15px Arial,sans-serif;padding:10px;border:1px solid var(--ink);background:transparent;color:var(--ink)}
.r{border-bottom:1px solid var(--hair);padding:10px 0}.r b{display:block}.r small{color:var(--muted);font:11px ui-monospace,monospace}.r p{margin:4px 0 0;color:var(--muted)}mark{background:none;color:var(--sig);font-weight:700}</style></head>
<body><main><h1>${esc(title)}</h1><input id="q" placeholder="Search…" autofocus><div id="out"></div></main>
<script>${src.replace(/<\/script/gi, '<\\/script')}<\/script>
<script>
const ir=rag.ir(${json(pruned)});
const ws = rag.workspace(ir), out = document.getElementById('out'), q = document.getElementById('q');
const esc = s => String(s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'})[c]);
let t; q.addEventListener('input', () => { clearTimeout(t); t = setTimeout(go, 150); });
async function go(){ const r = await ws.run({ q: q.value }, { trigger: 'change' });
const words = q.value.toLowerCase().split(/\\W+/).filter(w => w.length > 2);
out.innerHTML = r.hits.slice(0, 20).map(h => { const c = r.chunk(h.id); let s = esc(c.text.replace(/\\s+/g,' ').slice(0, 300)); for (const w of words) s = s.replace(new RegExp('(' + w.replace(/[^a-z0-9]/g,'') + ')', 'gi'), '<mark>$1</mark>');
  return '<div class="r"><b>' + esc(c.headingPath.join(' › ') || c.title) + '</b><small>' + esc(c.title) + '</small><p>' + s + '…</p></div>'; }).join(''); }
q.value = ${json(ir.signals.q || '')}; go();
<\/script></body></html>`;
}

export { exportHtml };
