package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

type deltaRun struct {
	Nodes  map[string]map[string]any `json:"nodes"`
	Docs   string                    `json:"docs"`
	Chunks string                    `json:"chunks"`
	Order  string                    `json:"order"`
	Top    string                    `json:"top"`
	N      int                       `json:"n"`
}

const deltaScript = `(async (root) => {
	const rag = require("ragwb");
	const corpus = rag.corpus("d").from(rag.source.dir({ root, include: ["**/*.md"] }))
		.parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25());
	const plan = rag.plan("d").over(corpus).signals({ q: "", k: 8 }).retrieve("text", { k: 20 }).limit();
	const ir = rag.compile(plan), eng = rag.engine();
	const rep = await eng.build(ir);
	if (!rep.ok) throw new Error(rep.errors.join("\n"));
	const art = eng.artifacts(ir);
	const docs = [...art.docs.values()], chunks = [...art.chunks.values()];
	const order = docs.map(d => [d.id, art.order.get(d.id) || []]);
	const top = [];
	for (const q of ["rebalance law placements", "protojson schema", "marker zebra edited", "notes about widgets", "kafka"]) {
		const r = await rag.run(plan, { q });
		if (r.errors.length) throw new Error(r.errors.join("\n"));
		top.push(r.top(8).map(h => [h.id, h.score]));
	}
	const nodes = {};
	for (const [id, st] of Object.entries(rep.nodes)) nodes[id] = { incremental: !!st.incremental, reused: !!st.reused, count: st.count, delta: st.delta || null };
	return JSON.stringify({ nodes, docs: JSON.stringify(docs), chunks: JSON.stringify(chunks), order: JSON.stringify(order), top: JSON.stringify(top), n: chunks.length });
})(%q)`

func runDelta(t *testing.T, root string, store *artifacts.Store) deltaRun {
	t.Helper()
	factory, err := NewFactory(Config{Roots: []string{root}, Artifacts: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	v, err := ragjs.Eval(ctx, rt, "delta", func(vm *goja.Runtime) (goja.Value, error) {
		return vm.RunString(fmt.Sprintf(deltaScript, root))
	})
	if err != nil {
		t.Fatal(err)
	}
	var o deltaRun
	if err := json.Unmarshal([]byte(v.(string)), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

// RAGWB-003 §6.9: after any sequence of edits, an incremental build's docs,
// chunks, reading order and search results equal a full build's of the same tree.
func TestDeltaBuildEqualsFullBuild(t *testing.T) {
	src, _ := filepath.Abs("../../examples/apps/reports/corpus")
	root := t.TempDir()
	ents, _ := os.ReadDir(src)
	for _, e := range ents {
		if b, err := os.ReadFile(filepath.Join(src, e.Name())); err == nil && !e.IsDir() {
			_ = os.WriteFile(filepath.Join(root, e.Name()), b, 0o644)
		}
	}
	rng := rand.New(rand.NewSource(11))
	note := func(i int, tag string) string {
		return fmt.Sprintf("---\ntype: note\ntags: [w%d, %s]\n---\n# Note %d about widgets\n\nWidget %d text %s.\n\n## Details\n\nMore about widget %d and %s.\n\n## Kafka\n\nkafka topic %d\n", i%5, tag, i, i, tag, i, tag, i)
	}
	_ = os.MkdirAll(filepath.Join(root, "notes"), 0o755)
	for i := 0; i < 40; i++ {
		_ = os.WriteFile(filepath.Join(root, "notes", fmt.Sprintf("n%02d.md", i)), []byte(note(i, "v0")), 0o644)
	}
	store, err := artifacts.Open(t.TempDir(), ArtifactNamespace(false))
	if err != nil {
		t.Fatal(err)
	}
	first := runDelta(t, root, store)
	if first.Nodes["parse"]["incremental"] == true {
		t.Fatal("first build cannot be incremental")
	}
	list := func() []string {
		var out []string
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
				out = append(out, p)
			}
			return nil
		})
		sort.Strings(out)
		return out
	}
	tick := time.Now()
	bump := func(p string) { // distinct mtimes even on coarse filesystems
		tick = tick.Add(2 * time.Second)
		_ = os.Chtimes(p, tick, tick)
	}
	incremental := 0
	for step := 1; step <= 8; step++ {
		files := list()
		var did []string
		switch step % 4 {
		case 1: // edit one note, add a paragraph to another
			p := files[rng.Intn(len(files))]
			_ = os.WriteFile(p, []byte(note(step+100, fmt.Sprintf("edited%d marker zebra", step))), 0o644)
			bump(p)
			q := files[rng.Intn(len(files))]
			b, _ := os.ReadFile(q)
			_ = os.WriteFile(q, append(b, []byte(fmt.Sprintf("\n\n## Added %d\n\nzebra marker paragraph %d\n", step, step))...), 0o644)
			bump(q)
			did = append(did, "edit "+filepath.Base(p), "append "+filepath.Base(q))
		case 2: // add and remove
			p := filepath.Join(root, "notes", fmt.Sprintf("added-%d.md", step))
			_ = os.WriteFile(p, []byte(note(step+200, "added")), 0o644)
			bump(p)
			q := files[rng.Intn(len(files))]
			_ = os.Remove(q)
			did = append(did, "add "+filepath.Base(p), "remove "+filepath.Base(q))
		case 3: // rename, touch
			p := files[rng.Intn(len(files))]
			np := filepath.Join(filepath.Dir(p), "renamed-"+filepath.Base(p))
			_ = os.Rename(p, np)
			q := list()[rng.Intn(len(files)-1)]
			bump(q)
			did = append(did, "rename "+filepath.Base(p), "touch "+filepath.Base(q))
		case 0: // a note loses all its sections' text; a new subfolder note
			p := files[rng.Intn(len(files))]
			_ = os.WriteFile(p, []byte("---\ntype: empty\n---\n"), 0o644)
			bump(p)
			_ = os.MkdirAll(filepath.Join(root, "sub"), 0o755)
			q := filepath.Join(root, "sub", fmt.Sprintf("deep %d.md", step))
			_ = os.WriteFile(q, []byte(note(step+300, "deep")), 0o644)
			bump(q)
			did = append(did, "empty "+filepath.Base(p), "add "+filepath.Base(q))
		}
		inc := runDelta(t, root, store)
		fresh, err := artifacts.Open(t.TempDir(), ArtifactNamespace(false))
		if err != nil {
			t.Fatal(err)
		}
		full := runDelta(t, root, fresh)
		if full.Nodes["chunks"]["incremental"] == true {
			t.Fatal("a fresh store cannot build incrementally")
		}
		if inc.Nodes["chunks"]["incremental"] == true {
			incremental++
		}
		ids := make([]string, 0, len(inc.Nodes))
		for id, n := range inc.Nodes {
			ids = append(ids, fmt.Sprintf("%s inc=%v %v", id, n["incremental"], n["delta"]))
		}
		sort.Strings(ids)
		t.Logf("step %d (%s): %d chunks; %s", step, strings.Join(did, ", "), inc.N, strings.Join(ids, "; "))
		for _, c := range []struct{ name, a, b string }{{"docs", inc.Docs, full.Docs}, {"chunks", inc.Chunks, full.Chunks}, {"order", inc.Order, full.Order}, {"top", tiesSorted(t, inc.Top), tiesSorted(t, full.Top)}} {
			if c.a != c.b {
				t.Fatalf("step %d: %s differ between the incremental and the full build:\n inc  %.400s\n full %.400s", step, c.name, diffAt(c.a, c.b), diffAt(c.b, c.a))
			}
		}
	}
	if incremental < 6 {
		t.Fatalf("only %d of 8 steps were incremental", incremental)
	}
}

// tiesSorted orders hits of equal score by id and drops the last group of
// equal scores (which ids make the cut among ties is arbitrary): the
// equivalence contract allows reordering among equal scores (guide §6.9).
func tiesSorted(t *testing.T, top string) string {
	var qs [][][2]any
	if err := json.Unmarshal([]byte(top), &qs); err != nil {
		t.Fatal(err)
	}
	for qi, hits := range qs {
		sort.SliceStable(hits, func(i, j int) bool {
			a, b := hits[i][1].(float64), hits[j][1].(float64)
			if a != b {
				return a > b
			}
			return hits[i][0].(string) < hits[j][0].(string)
		})
		if n := len(hits); n > 0 {
			last := hits[n-1][1]
			for n > 0 && hits[n-1][1] == last {
				n--
			}
			hits = hits[:n]
		}
		qs[qi] = hits
	}
	b, _ := json.Marshal(qs)
	return string(b)
}

// diffAt returns a from shortly before its first difference with b.
func diffAt(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return a[max(0, i-80):]
}
