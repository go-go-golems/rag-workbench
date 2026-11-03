// Quality gate, vault part (RAGWB-002 D2): index the stored vault chunks with both
// index.bm25 backends (parc's fields: title 3, headingPath 2, text 1) and compare,
// over ~200 queries, the top-10 overlap and a goldens-free signal: a chunk's own
// heading used as the query should retrieve that chunk (self-recall@10).
//
//	go run ./ttmp/…/scripts/02-vault-quality -chunks ~/.ragwb/store/<ns>/ed/<key>.bin [-n 200]
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/natives"
)

func main() {
	src := flag.String("chunks", "", "stored chunks artifact (codec chunks, gob)")
	n := flag.Int("n", 200, "heading queries to sample")
	show := flag.String("show", "", "queries (separated by |) whose top 5 to print for both backends, then exit")
	combined := flag.Bool("combined", false, "bleve: one combined BM25F-style field instead of one field per weighted field")
	flag.Parse()
	b, err := os.ReadFile(*src)
	if err != nil {
		panic(err)
	}
	chunks, err := natives.DecodeChunks(b)
	if err != nil {
		panic(err)
	}
	fields := []natives.Field{{Path: "title", Weight: 3}, {Path: "headingPath", Weight: 2}, {Path: "text", Weight: 1}}
	id := func(i int) string { return chunks[i].ID }
	doc := func(i int) string { return chunks[i].DocID }
	text := func(i, f int) (string, bool) {
		c := chunks[i]
		s := [...]string{c.Title, strings.Join(c.HeadingPath, " "), c.Text}[f]
		return s, s != ""
	}
	t0 := time.Now()
	mem := natives.NewBm25(len(chunks), id, doc, text, fields, 1.2, 0.75, true)
	tMem := time.Since(t0)
	dir, _ := os.MkdirTemp("", "ragwb-vault-quality-")
	defer os.RemoveAll(dir)
	t0 = time.Now()
	bl, err := natives.BuildBleveBm25(filepath.Join(dir, "ix"), len(chunks), id, doc, text, fields, natives.BleveOptions{Stem: true, Combined: *combined})
	if err != nil {
		panic(err)
	}
	tBl := time.Since(t0)
	defer bl.Close()
	fmt.Printf("bleve mode: combined=%v\n", *combined)
	fmt.Printf("chunks %d · build memory %v · bleve %v (%.0f MB on disk)\n", len(chunks), tMem.Round(time.Millisecond), tBl.Round(time.Millisecond), dirMB(dir))

	if *show != "" {
		label := func(h natives.Hit) string {
			for i := range chunks {
				if chunks[i].ID == h.ID {
					c := chunks[i]
					return fmt.Sprintf("%-46.46s › %.40s", c.Title, strings.Join(c.HeadingPath, " › "))
				}
			}
			return h.ID
		}
		for _, qq := range strings.Split(*show, "|") {
			m := mem.Search(qq, 5, nil, nil)
			bh, _ := bl.Search(qq, 5, nil, nil)
			fmt.Printf("\n%q\n", qq)
			for i := 0; i < 5; i++ {
				l, r := "", ""
				if i < len(m) {
					l = label(m[i])
				}
				if i < len(bh) {
					r = label(bh[i])
				}
				fmt.Printf("  mem   %d %s\n  bleve %d %s\n", i+1, l, i+1, r)
			}
		}
		return
	}
	// queries: the last heading of sampled section chunks (self-recall), plus hand-written ones
	rng := rand.New(rand.NewSource(7))
	type q struct{ text, want string }
	qs := []q{}
	for _, i := range rng.Perm(len(chunks)) {
		c := chunks[i]
		if len(qs) >= *n {
			break
		}
		if len(c.HeadingPath) == 0 || c.Kind != "section" {
			continue
		}
		h := c.HeadingPath[len(c.HeadingPath)-1]
		if len(strings.Fields(h)) < 2 {
			continue
		}
		qs = append(qs, q{h, c.ID})
	}
	for _, h := range []string{"obsidian vault search", "goja regexp unicode", "protobuf schema exchange", "remarkable upload",
		"go-go-goja module authoring", "rebalance law preserves placements", "sqlite forum for agents", "lunaroute embeddings cache"} {
		qs = append(qs, q{h, ""})
	}
	var jac []float64
	selfMem, selfBl, selfN := 0, 0, 0
	var tm, tb time.Duration
	type worst struct {
		q  string
		ov float64
	}
	ws := []worst{}
	for _, x := range qs {
		t1 := time.Now()
		m := mem.Search(x.text, 10, nil, nil)
		tm += time.Since(t1)
		t1 = time.Now()
		bh, err := bl.Search(x.text, 10, nil, nil)
		tb += time.Since(t1)
		if err != nil {
			panic(err)
		}
		a, c := set(m), set(bh)
		inter, union := 0, len(c)
		for k := range a {
			if c[k] {
				inter++
			} else {
				union++
			}
		}
		ov := 1.0
		if union > 0 {
			ov = float64(inter) / float64(union)
		}
		jac = append(jac, ov)
		ws = append(ws, worst{x.text, ov})
		if x.want != "" {
			selfN++
			if a[x.want] {
				selfMem++
			}
			if c[x.want] {
				selfBl++
			}
		}
	}
	sort.Float64s(jac)
	fmt.Printf("queries %d · median top-10 Jaccard %.2f · p10 %.2f · below 0.2: %d\n", len(qs), jac[len(jac)/2], jac[len(jac)/10], countBelow(jac, 0.2))
	fmt.Printf("self-recall@10 (heading → its chunk, %d queries): memory %.3f · bleve %.3f\n", selfN, float64(selfMem)/float64(selfN), float64(selfBl)/float64(selfN))
	fmt.Printf("mean query time: memory %.2f ms · bleve %.2f ms\n", ms(tm)/float64(len(qs)), ms(tb)/float64(len(qs)))
	sort.Slice(ws, func(i, j int) bool { return ws[i].ov < ws[j].ov })
	fmt.Println("lowest-overlap queries:")
	for _, w := range ws[:min(8, len(ws))] {
		fmt.Printf("  %.2f  %q\n", w.ov, w.q)
	}
}

func set(hs []natives.Hit) map[string]bool {
	m := map[string]bool{}
	for _, h := range hs {
		m[h.ID] = true
	}
	return m
}

func countBelow(xs []float64, t float64) int {
	n := 0
	for _, x := range xs {
		if x < t {
			n++
		}
	}
	return n
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func dirMB(p string) float64 {
	var n int64
	_ = filepath.Walk(p, func(_ string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	return float64(n) / (1 << 20)
}
