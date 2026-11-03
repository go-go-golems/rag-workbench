// Result map · a canvas drawing of where the hits come from.
// One row per document, one dot per hit (left = better rank). Shapes with an id are clickable;
// the draw lambda re-runs when the state it reads changes.
// Try: search, hover the dots, click one to read the section.

const plan = rag.plan('map').over(kit.reports())
  .signals({ q: 'verification gates and tests', k: 40 })
  .retrieve('text', { k: 40 });

const app = gui.app('map').title('Result map').use(plan).state({ selected: null, hover: null })
  .view(v => v.col(c => c
    .searchBox('q')
    .canvas((s, ctx, g) => {
      g.clear();
      const docs = ctx.rag.docs();
      const hits = ((s.out || {}).text || []).slice(0, 40);
      const L = 150, T = 14, rowH = (g.height - T - 8) / Math.max(1, docs.length);
      docs.forEach((d, i) => {
        const y = T + i * rowH + rowH / 2;
        g.text(d.title.replace(/^PROJECT REPORT - |^PROJ - /, '').slice(0, 24), 4, y + 4, { size: 10, fill: g.theme.muted });
        g.line(L, y, g.width - 10, y, { stroke: g.theme.line, width: 1 });
      });
      hits.forEach((h, r) => {
        const ch = ctx.rag.chunk(h.id);
        if (!ch) return;
        const i = docs.findIndex(d => d.id === ch.docId);
        const y = T + i * rowH + rowH / 2, x = L + 8 + (r / 40) * (g.width - L - 30);
        const on = s.selected === h.id || s.hover === h.id;
        g.circle(x, y, Math.max(3, 8 - r / 6), { fill: on ? g.theme.accent : g.theme.ink, id: h.id });
      });
    }, { width: 720, height: 300, label: 'Hits by document and rank',
      onClick: (s, ctx, ev) => { if (ev.hit) ctx.set({ selected: ev.hit }); },
      onHover: (s, ctx, ev) => ctx.set({ hover: ev.hit }) })
    .text(s => { const h = s.hover || s.selected; return h ? '`' + h + '`' : '_Hover a dot to see which section it is; click to read it._'; })
    .chunk(s => s.selected)));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
