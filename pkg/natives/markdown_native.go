package natives

import (
	"fmt"
	"github.com/go-go-golems/rag-workbench/pkg/rtenv"

	"github.com/dop251/goja"
)

// Markdown installs Go implementations of parse.markdown and chunk.sections
// under their prototype type names, so existing scripts get them unchanged.
// Outputs equal the JS implementations (see markdown_parity_test.go) plus
// `range` objects on blocks and chunks.
type Markdown struct{}

func (Markdown) Name() string { return "markdown" }

const goDocKey = "__go"

const markdownJS = `(function (rag, h) {
  // id: the op layer names inserted nodes def.id(p) || family; keep the built-ins' family ids.
  rag.define('parse.markdown', {
    id: () => 'parse', phase: 'build', in: ['files'], out: 'docs', describe: () => 'frontmatter · headings · fences · tables (Go)',
    all: ([files], ctx, p) => {
      const docs = h.parse(files || [], p || {});
      // ids come from paths (docIdFor), so a duplicate means two paths with the same slug
      const seen = new Map(), dup = [];
      for (const d of docs) { if (seen.has(d.id)) dup.push(seen.get(d.id) + ' / ' + d.name + ' → ' + d.id); else seen.set(d.id, d.name); }
      if (dup.length) ctx.log('duplicate doc ids (' + dup.length + '; the first file keeps the id): ' + dup.slice(0, 5).join('; '));
      return docs;
    },
    // delta build: parse only added and changed files, copy-on-write the base docs
    delta: {
      apply(base, [filesIn], ctx, p) {
        const d = filesIn.delta;
        const r = h.parseDelta(base.load(), [...d.added, ...d.changed], d.removed, p || {}, ctx.store.tempDir());
        if (r.full) { ctx.log('full build: ' + r.full); return null; }
        return {
          value: ctx.store.view(r.records), count: r.count,
          delta: { full: false, upsert: r.upsert, remove: r.remove },
          summary: { upserts: r.upsert.length, removes: r.remove.length },
        };
      },
    },
  });
  rag.define('chunk.sections', {
    id: () => 'chunk', phase: 'build', in: ['docs'], out: 'chunks',
    describe: p => 'depth ' + (p.depth ?? 3) + ' · ≤' + (p.maxTokens ?? 450) + ' tok (Go)',
    all: ([docs], ctx, p) => h.chunk(docs.materialize(), p || {}),
    // delta build: chunk only the upserted docs (per-doc ids), copy-on-write the base records
    delta: {
      apply(base, [docsIn], ctx, p) {
        const d = docsIn.delta;
        const r = h.chunkDelta(base.load(), d.upsert, d.remove, p || {}, ctx.store.tempDir());
        if (r.full) { ctx.log('full build: ' + r.full); return null; }
        return {
          value: ctx.store.view(r.records), count: r.count,
          delta: { full: false, upsert: r.upsert, removeDocs: r.removeDocs, removeIds: r.removeIds, removed: r.removed },
          summary: { upserts: r.upsert.length, removes: r.removeIds.length, docs: r.docs },
        };
      },
    },
  });
})`

func (m Markdown) Install(vm *goja.Runtime, rag *goja.Object) error {
	return m.InstallEnv(vm, rag, rtenv.Env{})
}

func (m Markdown) InstallEnv(vm *goja.Runtime, rag *goja.Object, env rtenv.Env) error {
	if env.Resources == nil {
		return fmt.Errorf("markdown requires a runtime resource registry")
	}
	h := vm.NewObject()
	_ = h.Set("parse", func(call goja.FunctionCall) goja.Value {
		files := call.Argument(0).ToObject(vm)
		p := optObject(vm, call.Argument(1))
		frontmatter := true
		if v := p.Get("frontmatter"); v != nil && !goja.IsUndefined(v) && !v.ToBoolean() {
			frontmatter = false
		}
		n := int(files.Get("length").ToInteger())
		docs := make([]*Doc, 0, n)
		for i := 0; i < n; i++ {
			f := files.Get(fmt.Sprint(i)).ToObject(vm)
			name, text := f.Get("name"), f.Get("text")
			if name == nil || text == nil || goja.IsUndefined(text) {
				panic(vm.NewTypeError("parse.markdown: file %d has no name/text", i))
			}
			docs = append(docs, ParseMarkdown(name.String(), text.String(), frontmatter))
		}
		return docsArray(vm, docs)
	})
	_ = h.Set("chunk", func(call goja.FunctionCall) goja.Value {
		docs := call.Argument(0).ToObject(vm)
		p := chunkParams(vm, optObject(vm, call.Argument(1)))
		n := int(docs.Get("length").ToInteger())
		all := []Chunk{}
		for i := 0; i < n; i++ {
			d := docs.Get(fmt.Sprint(i)).ToObject(vm)
			doc := docFromJS(vm, d)
			cs := ChunkSections(doc, p)
			for j := range cs {
				cs[j].DocName = doc.Name
			}
			all = append(all, cs...)
		}
		AssignIDs(all)
		return chunksArray(vm, all)
	})
	// parseDelta(baseDocsView, files, removedNames, p, dir) → {records, count, upsert, remove} | {full: reason}
	_ = h.Set("parseDelta", func(call goja.FunctionCall) goja.Value {
		base := records2Handle(call.Argument(0))
		if base == nil || base.Kind() != kindDocs {
			return fullResult(vm, "base docs are not records2")
		}
		files := call.Argument(1).ToObject(vm)
		removed := strsFromJS(vm, call.Argument(2))
		frontmatter := true
		if v := optObject(vm, call.Argument(3)).Get("frontmatter"); v != nil && !goja.IsUndefined(v) && !v.ToBoolean() {
			frontmatter = false
		}
		dir := call.Argument(4).String()
		n := int(files.Get("length").ToInteger())
		docs := make([]*Doc, 0, n)
		for i := 0; i < n; i++ {
			f := files.Get(fmt.Sprint(i)).ToObject(vm)
			docs = append(docs, ParseMarkdown(str(f.Get("name")), str(f.Get("text")), frontmatter))
		}
		r, remove, reason, err := ParseDelta(base, docs, removed, dir)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		if reason != "" {
			return fullResult(vm, reason)
		}
		o := vm.NewObject()
		_ = o.Set("records", ownResource(vm, env, records2Object(vm, r), r))
		_ = o.Set("count", r.Len())
		_ = o.Set("upsert", docsArray(vm, docs))
		_ = o.Set("remove", strArray(vm, remove))
		return o
	})
	// chunkDelta(baseChunksView, upsertDocs, removeDocIds, p, dir) → {records, count, upsert, removeDocs, removeIds, docs} | {full: reason}
	_ = h.Set("chunkDelta", func(call goja.FunctionCall) goja.Value {
		base := records2Handle(call.Argument(0))
		if base == nil || base.Kind() != kindChunks {
			return fullResult(vm, "base chunks are not records2")
		}
		up := call.Argument(1).ToObject(vm)
		n := int(up.Get("length").ToInteger())
		docs := docsHandle(up)
		if docs == nil {
			docs = make([]*Doc, n)
			for i := range docs {
				docs[i] = docFromJS(vm, up.Get(fmt.Sprint(i)).ToObject(vm))
			}
		}
		remove := strsFromJS(vm, call.Argument(2))
		p := chunkParams(vm, optObject(vm, call.Argument(3)))
		dir := call.Argument(4).String()
		r, chunks, removeDocs, removed, reason, err := ChunkDelta(base, docs, remove, p, dir)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		if reason != "" {
			return fullResult(vm, reason)
		}
		o := vm.NewObject()
		_ = o.Set("records", ownResource(vm, env, records2Object(vm, r), r))
		_ = o.Set("count", r.Len())
		_ = o.Set("upsert", chunksArray(vm, chunks))
		_ = o.Set("removeDocs", strArray(vm, removeDocs))
		removeIDs := make([]string, len(removed))
		for i := range removed {
			removeIDs[i] = removed[i].ID
		}
		_ = o.Set("removeIds", strArray(vm, removeIDs))
		_ = o.Set("removed", chunksArray(vm, removed))
		_ = o.Set("docs", len(docs))
		return o
	})
	v, err := vm.RunString(markdownJS)
	if err != nil {
		return err
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		return fmt.Errorf("markdown installer is not a function")
	}
	_, err = fn(goja.Undefined(), rag, h)
	return err
}

func optObject(vm *goja.Runtime, v goja.Value) *goja.Object {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return vm.NewObject()
	}
	return v.ToObject(vm)
}

func present(v goja.Value) bool { return v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) }

func chunkParams(vm *goja.Runtime, p *goja.Object) ChunkParams {
	cp := DefaultChunkParams()
	if v := p.Get("depth"); present(v) { // p.depth ?? 3
		cp.Depth = int(v.ToInteger())
	}
	if v := p.Get("maxTokens"); present(v) {
		cp.MaxTokens = int(v.ToInteger())
	}
	if v := p.Get("minTokens"); present(v) {
		cp.MinTokens = int(v.ToInteger())
	}
	if v := p.Get("dropMermaid"); v != nil && !goja.IsUndefined(v) { // p.dropMermaid !== false
		cp.DropMermaid = !(v.ExportType() != nil && v.Export() == false)
	}
	if v := p.Get("prefixHeading"); v != nil {
		cp.PrefixHeading = v.ToBoolean()
	}
	if v := p.Get("overlap"); present(v) && v.ToBoolean() {
		cp.Overlap = int(v.ToInteger())
	}
	return cp
}

// ── Go → JS (plain JS objects, key order as in rag.js)

func strArray(vm *goja.Runtime, xs []string) *goja.Object {
	vals := make([]any, len(xs))
	for i, x := range xs {
		vals[i] = x
	}
	return vm.NewArray(vals...)
}

func metaToJS(vm *goja.Runtime, m Meta) *goja.Object {
	o := vm.NewObject()
	for _, k := range m.Keys {
		_ = o.Set(k, scalarToJS(vm, m.Values[k]))
	}
	return o
}

func scalarToJS(vm *goja.Runtime, v any) goja.Value {
	if arr, ok := v.([]any); ok {
		vals := make([]any, len(arr))
		for i, x := range arr {
			vals[i] = scalarToJS(vm, x)
		}
		return vm.NewArray(vals...)
	}
	return vm.ToValue(v)
}

func rangeToJS(vm *goja.Runtime, r Range) *goja.Object {
	o := vm.NewObject()
	_ = o.Set("byteStart", r.ByteStart)
	_ = o.Set("byteEnd", r.ByteEnd)
	_ = o.Set("lineStart", r.LineStart)
	_ = o.Set("lineEnd", r.LineEnd)
	return o
}

func blockToJS(vm *goja.Runtime, b *Block) *goja.Object {
	o := vm.NewObject()
	_ = o.Set("kind", b.Kind)
	switch b.Kind {
	case "fence":
		_ = o.Set("lang", b.Lang)
	case "table":
		_ = o.Set("header", strArray(vm, b.Header))
		rows := make([]any, len(b.Rows))
		for i, r := range b.Rows {
			rows[i] = strArray(vm, r)
		}
		_ = o.Set("rows", vm.NewArray(rows...))
	case "callout":
		_ = o.Set("variant", b.Variant)
	}
	_ = o.Set("text", b.Text)
	_ = o.Set("range", rangeToJS(vm, b.Range))
	return o
}

func docToJS(vm *goja.Runtime, d *Doc) *goja.Object {
	o := vm.NewObject()
	_ = o.Set("id", d.ID)
	_ = o.Set("name", d.Name)
	_ = o.Set("title", d.Title)
	_ = o.Set("meta", metaToJS(vm, d.Meta))
	if d.H1 != nil {
		_ = o.Set("h1", *d.H1)
	} else {
		_ = o.Set("h1", goja.Null())
	}
	secs := make([]any, len(d.Sections))
	for i := range d.Sections {
		s := &d.Sections[i]
		so := vm.NewObject()
		_ = so.Set("level", s.Level)
		_ = so.Set("title", s.Title)
		_ = so.Set("path", strArray(vm, s.Path))
		blocks := make([]any, len(s.Blocks))
		for j := range s.Blocks {
			blocks[j] = blockToJS(vm, &s.Blocks[j])
		}
		_ = so.Set("blocks", vm.NewArray(blocks...))
		_ = so.Set("id", s.ID)
		_ = so.Set("text", s.Text)
		secs[i] = so
	}
	_ = o.Set("sections", vm.NewArray(secs...))
	_ = o.Set("links", strArray(vm, d.Links))
	// Hidden fast path for chunk.sections: non-enumerable, so stable(), JSON and {...doc} ignore it.
	_ = o.DefineDataProperty(goDocKey, vm.ToValue(d), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	return o
}

func chunkToJS(vm *goja.Runtime, c *Chunk) *goja.Object {
	o := vm.NewObject()
	_ = o.Set("id", c.ID)
	_ = o.Set("docId", c.DocID)
	_ = o.Set("title", c.Title)
	_ = o.Set("headingPath", strArray(vm, c.HeadingPath))
	_ = o.Set("kind", c.Kind)
	_ = o.Set("text", c.Text)
	_ = o.Set("meta", metaToJS(vm, c.Meta))
	_ = o.Set("callout", c.Callout)
	_ = o.Set("hasCode", c.HasCode)
	_ = o.Set("hasTable", c.HasTable)
	_ = o.Set("tokens", c.Tokens)
	_ = o.Set("part", c.Part)
	_ = o.Set("parts", c.Parts)
	_ = o.Set("ord", c.Ord)
	_ = o.Set("range", rangeToJS(vm, c.Range))
	return o
}

// ── JS → Go (docs made by the JS parser, custom components or copies)

func docFromJS(vm *goja.Runtime, o *goja.Object) *Doc {
	if v := o.Get(goDocKey); v != nil {
		if d, ok := v.Export().(*Doc); ok {
			return d
		}
	}
	d := &Doc{ID: str(o.Get("id")), Name: str(o.Get("name")), Title: str(o.Get("title")), Meta: metaFromJS(vm, o.Get("meta"))}
	if secs := o.Get("sections"); present(secs) {
		so := secs.ToObject(vm)
		n := int(so.Get("length").ToInteger())
		for i := 0; i < n; i++ {
			s := so.Get(fmt.Sprint(i)).ToObject(vm)
			sec := Section{Level: int(s.Get("level").ToInteger()), Title: str(s.Get("title")), Path: strsFromJS(vm, s.Get("path"))}
			if bs := s.Get("blocks"); present(bs) {
				bo := bs.ToObject(vm)
				m := int(bo.Get("length").ToInteger())
				for j := 0; j < m; j++ {
					b := bo.Get(fmt.Sprint(j)).ToObject(vm)
					sec.Blocks = append(sec.Blocks, Block{Kind: str(b.Get("kind")), Lang: str(b.Get("lang")), Variant: str(b.Get("variant")), Text: str(b.Get("text")), Range: rangeFromJS(vm, b.Get("range"))})
				}
			}
			d.Sections = append(d.Sections, sec)
		}
	}
	return d
}

func str(v goja.Value) string {
	if !present(v) {
		return ""
	}
	return v.String()
}

func strsFromJS(vm *goja.Runtime, v goja.Value) []string {
	if !present(v) {
		return []string{}
	}
	o := v.ToObject(vm)
	n := int(o.Get("length").ToInteger())
	out := make([]string, n)
	for i := range out {
		out[i] = str(o.Get(fmt.Sprint(i)))
	}
	return out
}

func rangeFromJS(vm *goja.Runtime, v goja.Value) Range {
	if !present(v) {
		return Range{}
	}
	o := v.ToObject(vm)
	return Range{int(o.Get("byteStart").ToInteger()), int(o.Get("byteEnd").ToInteger()), int(o.Get("lineStart").ToInteger()), int(o.Get("lineEnd").ToInteger())}
}

func metaFromJS(vm *goja.Runtime, v goja.Value) Meta {
	m := Meta{Values: map[string]any{}}
	if !present(v) {
		return m
	}
	o := v.ToObject(vm)
	for _, k := range o.Keys() {
		m.set(k, scalarFromJS(o.Get(k).Export()))
	}
	return m
}

func scalarFromJS(v any) any {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = scalarFromJS(e)
		}
		return out
	}
	return v
}

func fullResult(vm *goja.Runtime, reason string) goja.Value {
	o := vm.NewObject()
	_ = o.Set("full", reason)
	return o
}
