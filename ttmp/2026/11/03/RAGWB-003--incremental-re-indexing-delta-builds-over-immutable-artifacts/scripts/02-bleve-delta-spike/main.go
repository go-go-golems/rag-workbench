// Phase 0 spike (RAGWB-003): is a bleve index made by cloning a stored index and
// applying a delta equivalent to a fresh build of the same chunk set?
//
//	go run ./ttmp/…/scripts/02-bleve-delta-spike -records ~/.ragwb/store/<ns>/<k2>/<key>.records [-edits 30]
package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
	index "github.com/blevesearch/bleve_index_api"

	"github.com/go-go-golems/rag-workbench/pkg/natives"
)

var fields = []natives.Field{{Path: "title", Weight: 3}, {Path: "headingPath", Weight: 2}, {Path: "text", Weight: 1}}

func texts(c natives.Chunk) []string {
	return []string{c.Title, strings.Join(c.HeadingPath, " "), c.Text}
}

func build(dir string, cs []natives.Chunk) *natives.BleveBm25 {
	b, err := natives.BuildBleveBm25(dir, len(cs), func(i int) string { return cs[i].ID }, func(i int) string { return cs[i].DocID },
		func(i, f int) (string, bool) { s := texts(cs[i])[f]; return s, s != "" }, fields, natives.BleveOptions{Stem: true})
	if err != nil {
		panic(err)
	}
	return b
}

// the document BuildBleveBm25 indexes for a chunk (mirrors bleve_bm25.go)
func doc(c natives.Chunk) map[string]any {
	d := map[string]any{"docId": c.DocID}
	for fi, s := range texts(c) {
		if s == "" {
			continue
		}
		if toks := natives.Tokenize(s, true); len(toks) > 0 {
			d[fmt.Sprintf("f%d", fi)] = strings.Join(toks, " ")
		}
	}
	return d
}

func cloneDir(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if strings.HasSuffix(p, ".zap") {
			return os.Link(p, out) // immutable segment: share it
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		o, err := os.Create(out)
		if err != nil {
			return err
		}
		defer o.Close()
		_, err = io.Copy(o, in)
		return err
	})
}

func stats(dir string) (uint64, []int) {
	idx, err := bleve.OpenUsing(dir, map[string]any{"read_only": true})
	if err != nil {
		panic(err)
	}
	defer idx.Close()
	n, _ := idx.DocCount()
	adv, _ := idx.Advanced()
	r, _ := adv.Reader()
	defer r.Close()
	card := []int{}
	for i := range fields {
		c, err := r.(index.BM25Reader).FieldCardinality(fmt.Sprintf("f%d", i))
		if err != nil {
			panic(err)
		}
		card = append(card, c)
	}
	return n, card
}

func main() {
	recs := flag.String("records", "", "stored chunk records file")
	edits := flag.Int("edits", 30, "docs to change: a third removed, a third modified, a third added")
	flag.Parse()
	r, err := natives.OpenChunkRecords(*recs)
	if err != nil {
		panic(err)
	}
	all, err := r.Slice(0, r.Len())
	if err != nil {
		panic(err)
	}
	_ = r.Close()
	byDoc := map[string][]natives.Chunk{}
	docs := []string{}
	for _, c := range all {
		if _, ok := byDoc[c.DocID]; !ok {
			docs = append(docs, c.DocID)
		}
		byDoc[c.DocID] = append(byDoc[c.DocID], c)
	}
	rng := rand.New(rand.NewSource(3))
	perm := rng.Perm(len(docs))
	k := *edits / 3
	removed, modified, donors := map[string]bool{}, map[string]bool{}, []string{}
	for _, i := range perm[:k] {
		removed[docs[i]] = true
	}
	for _, i := range perm[k : 2*k] {
		modified[docs[i]] = true
	}
	for _, i := range perm[2*k : 3*k] {
		donors = append(donors, docs[i])
	}
	// final chunk set B, and the delta from A
	var B, add []natives.Chunk
	var del []string
	for _, d := range docs {
		switch {
		case removed[d]:
			for _, c := range byDoc[d] {
				del = append(del, c.ID)
			}
		case modified[d]:
			for _, c := range byDoc[d] {
				del = append(del, c.ID)
				c.Text += " incremental zebra marker"
				B = append(B, c)
				add = append(add, c)
			}
		default:
			B = append(B, byDoc[d]...)
		}
	}
	for n, d := range donors {
		for _, c := range byDoc[d] {
			c.DocID = fmt.Sprintf("spike-new-%d", n)
			c.ID = c.DocID + "#" + strings.SplitN(c.ID, "#", 2)[1]
			B = append(B, c)
			add = append(add, c)
		}
	}
	tmp, _ := os.MkdirTemp(filepath.Dir(*recs), ".tmp-spike-")
	defer os.RemoveAll(tmp)
	fmt.Printf("A: %d chunks · B: %d chunks · delta: delete %d, index %d (docs: %d removed, %d modified, %d added)\n",
		len(all), len(B), len(del), len(add), len(removed), len(modified), len(donors))

	t0 := time.Now()
	base := build(filepath.Join(tmp, "base"), all)
	_ = base.Close()
	fmt.Printf("fresh build A: %v\n", time.Since(t0).Round(time.Millisecond))

	t0 = time.Now()
	clone := filepath.Join(tmp, "clone")
	if err := cloneDir(filepath.Join(tmp, "base"), clone); err != nil {
		panic(err)
	}
	tClone := time.Since(t0)
	t0 = time.Now()
	idx, err := bleve.Open(clone)
	if err != nil {
		panic(err)
	}
	batch := idx.NewBatch()
	for _, id := range del {
		batch.Delete(id)
	}
	for _, c := range add {
		_ = batch.Index(c.ID, doc(c))
	}
	if err := idx.Batch(batch); err != nil {
		panic(err)
	}
	if err := idx.Close(); err != nil {
		panic(err)
	}
	tApply := time.Since(t0)
	fmt.Printf("clone (hard links): %v · open+apply+close: %v\n", tClone.Round(time.Microsecond), tApply.Round(time.Millisecond))

	t0 = time.Now()
	fresh := build(filepath.Join(tmp, "fresh"), B)
	_ = fresh.Close()
	fmt.Printf("fresh build B: %v\n", time.Since(t0).Round(time.Millisecond))

	nC, cardC := stats(clone)
	nF, cardF := stats(filepath.Join(tmp, "fresh"))
	fmt.Printf("DocCount clone %d fresh %d · FieldCardinality clone %v fresh %v\n", nC, nF, cardC, cardF)
	// the base dir must be untouched
	nB, _ := stats(filepath.Join(tmp, "base"))
	fmt.Printf("base after delta: DocCount %d (want %d)\n", nB, len(all))

	cl, _ := natives.OpenBleveBm25(clone)
	fr, _ := natives.OpenBleveBm25(filepath.Join(tmp, "fresh"))
	defer cl.Close()
	defer fr.Close()
	qs := []string{"incremental zebra marker", "goja regexp unicode", "obsidian vault search", "protobuf schema", "remarkable upload"}
	for _, i := range rng.Perm(len(B))[:45] {
		if h := B[i].HeadingPath; len(h) > 0 {
			qs = append(qs, h[len(h)-1])
		}
	}
	sameIDs, sameScores, maxRel := 0, 0, 0.0
	for _, q := range qs {
		a, _ := cl.Search(q, 10, nil, nil)
		b, _ := fr.Search(q, 10, nil, nil)
		ids, sc := len(a) == len(b), len(a) == len(b)
		for i := range a {
			if i >= len(b) {
				break
			}
			if a[i].ID != b[i].ID {
				ids = false
			}
			if d := math.Abs(a[i].Score-b[i].Score) / math.Max(1e-9, math.Abs(b[i].Score)); d > 0 {
				sc = sc && d < 1e-9
				maxRel = math.Max(maxRel, d)
			}
		}
		if ids {
			sameIDs++
		}
		if sc {
			sameScores++
		}
	}
	fmt.Printf("queries %d · identical top-10 ids %d · identical scores %d · max relative score diff %.3g\n", len(qs), sameIDs, sameScores, maxRel)
}
