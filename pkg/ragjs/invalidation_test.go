package ragjs

import "testing"

// PR #1 review: editing a custom component's implementation (same type, same
// params) must invalidate its node AND everything downstream of it, not only
// its own stored output. Here the chunker changes and the BM25 index built on
// its chunks must follow.
func TestComponentSourceChangeInvalidatesDownstream(t *testing.T) {
	rt := newRuntime(t, Options{})
	var out struct {
		First, Second     []string
		FirstDigest       string
		SecondDigest      string
		FirstIx, SecondIx string
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		rag.configure({ files: () => [{ name: "a.md", text: "# A\n\nalpha text" }] });
		const chunker = word => ({ phase: "build", in: ["docs"], out: "chunks",
			all: new Function("[docs]", "return docs.map((d, i) => ({ id: d.id + '#' + i, docId: d.id, title: d.title, headingPath: [], text: '" + word + "', blocks: [], meta: {} }));") });
		const plan = () => rag.plan("p").over(rag.corpus("c").from(rag.files("*.md")).parse(rag.markdown()).chunk(rag.chunk.tagged()).index("text", rag.bm25()))
			.signals({ q: "", k: 5 }).retrieve("text", { k: 5 }).limit();
		const eng = rag.engine();
		const search = async q => (await eng.run(rag.compile(plan()), { q })).hits.map(h => h.id);
		rag.define("chunk.tagged", chunker("apples"));
		let ir = rag.compile(plan());
		const first = await search("apples"), d1 = eng.digests(ir).get("chunks"), x1 = eng.digests(ir).get("ix-text");
		rag.define("chunk.tagged", chunker("bananas")); // same type and params, new body
		ir = rag.compile(plan());
		const second = await search("bananas"), d2 = eng.digests(ir).get("chunks"), x2 = eng.digests(ir).get("ix-text");
		return JSON.stringify({ First: first, Second: second, FirstDigest: d1, SecondDigest: d2, FirstIx: x1, SecondIx: x2 });
	})()`, &out)
	if len(out.First) == 0 {
		t.Fatalf("first search found nothing: %+v", out)
	}
	if out.FirstDigest == out.SecondDigest || out.FirstIx == out.SecondIx {
		t.Fatalf("digests did not change with the component body: chunks %s→%s, index %s→%s", out.FirstDigest, out.SecondDigest, out.FirstIx, out.SecondIx)
	}
	if len(out.Second) == 0 {
		t.Fatalf("after redefining the chunker, the index still holds the old chunks (search for the new text found nothing)")
	}
}

// PR #1 review: a session's live-node memo must not survive a change of the
// artifacts it ran against. Replacing the corpus (as `ragwb serve --watch` does)
// and repeating the same query must not return cached hits from the old index.
func TestSessionMemoFollowsArtifacts(t *testing.T) {
	rt := newRuntime(t, Options{})
	var out struct {
		Before, After []string
		Status        string
		Fresh         []string
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		let files = [{ name: "fruit.md", text: "# Fruit\n\napples are red" }];
		rag.configure({ files: () => files });
		const plan = rag.plan("p").over(rag.corpus("c").from(rag.files("*.md")).parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25()))
			.signals({ q: "", k: 5 }).retrieve("text", { k: 5 }).limit();
		const ir = rag.compile(plan), eng = rag.engine(), session = eng.session(ir);
		const before = (await session.run({ q: "apples" }, { trigger: "change" })).hits.map(h => h.id);
		files = [{ name: "fruit.md", text: "# Fruit\n\nbananas are yellow" }];
		const r = await session.run({ q: "apples" }, { trigger: "change" });
		const fresh = (await eng.session(ir).run({ q: "apples" }, { trigger: "change" })).hits.map(h => h.id);
		return JSON.stringify({ Before: before, After: r.hits.map(h => h.id), Status: r.trace.find(t => t.node === "retrieve" || t.node === "text").status, Fresh: fresh });
	})()`, &out)
	if len(out.Before) == 0 {
		t.Fatalf("no hits before the change: %+v", out)
	}
	if len(out.Fresh) != 0 {
		t.Fatalf("test setup: a fresh session should find no apples after the change, got %v", out.Fresh)
	}
	if len(out.After) != 0 {
		t.Fatalf("the existing session returned %v (status %s) from the old index", out.After, out.Status)
	}
}
