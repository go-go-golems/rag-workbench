// Probe: the stored vault chunks in a persistent bleve index (en analyzer, BM25).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/go-go-golems/rag-workbench/pkg/natives"
)

func heap() string {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return fmt.Sprintf("live heap %.0f MB, sys %.0f MB", float64(m.HeapAlloc)/(1<<20), float64(m.Sys)/(1<<20))
}

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

func main() {
	src, dir, mode := os.Args[1], os.Args[2], os.Args[3]
	if mode == "build" {
		b, err := os.ReadFile(src)
		if err != nil {
			panic(err)
		}
		chunks, err := natives.DecodeChunks(b)
		if err != nil {
			panic(err)
		}
		_ = os.RemoveAll(dir)
		text := bleve.NewTextFieldMapping()
		text.Analyzer = "en"
		text.Store = true
		kw := bleve.NewKeywordFieldMapping()
		kw.Store = true
		stored := bleve.NewTextFieldMapping()
		stored.Index = false
		stored.Store = true
		doc := bleve.NewDocumentMapping()
		doc.AddFieldMappingsAt("title", text)
		doc.AddFieldMappingsAt("heading", text)
		doc.AddFieldMappingsAt("text", text)
		doc.AddFieldMappingsAt("docId", kw)
		doc.AddFieldMappingsAt("tags", kw)
		doc.AddFieldMappingsAt("type", kw)
		m := bleve.NewIndexMapping()
		m.DefaultMapping = doc
		m.ScoringModel = "bm25"
		t0 := time.Now()
		idx, err := bleve.New(dir, m)
		if err != nil {
			panic(err)
		}
		batch := idx.NewBatch()
		for i, c := range chunks {
			d := map[string]any{"title": c.Title, "heading": strings.Join(c.HeadingPath, " › "), "text": c.Text, "docId": c.DocID}
			if v, ok := c.Meta.Values["tags"]; ok {
				d["tags"] = v
			}
			if v, ok := c.Meta.Values["type"]; ok {
				d["type"] = v
			}
			_ = batch.Index(c.ID, d)
			if batch.Size() >= 2000 || i == len(chunks)-1 {
				if err := idx.Batch(batch); err != nil {
					panic(err)
				}
				batch = idx.NewBatch()
			}
		}
		_ = idx.Close()
		fmt.Printf("build: %d chunks in %v, index on disk %.0f MB\n", len(chunks), time.Since(t0).Round(time.Millisecond), dirMB(dir))
		return
	}
	fmt.Println("before open:", heap())
	t0 := time.Now()
	idx, err := bleve.Open(dir)
	if err != nil {
		panic(err)
	}
	fmt.Printf("open: %v · %s\n", time.Since(t0).Round(time.Microsecond), heap())
	qs := []string{"obsidian vault search", "goja regexp unicode", "protobuf schema exchange", "remarkable upload", "go-go-goja module authoring", "rebalance law preserves placements", "xyzzy"}
	for round := 0; round < 2; round++ {
		for _, q := range qs {
			mq := bleve.NewMatchQuery(q)
			mq.Analyzer = "en"
			title := bleve.NewMatchQuery(q)
			title.SetField("title")
			title.SetBoost(3)
			heading := bleve.NewMatchQuery(q)
			heading.SetField("heading")
			heading.SetBoost(2)
			body := bleve.NewMatchQuery(q)
			body.SetField("text")
			req := bleve.NewSearchRequestOptions(bleve.NewDisjunctionQuery(title, heading, body), 30, 0, false)
			req.AddFacet("tags", bleve.NewFacetRequest("tags", 8))
			t1 := time.Now()
			res, err := idx.Search(req)
			if err != nil {
				panic(err)
			}
			took := time.Since(t1)
			// chunk bodies for the top 10, as the serve pack step needs
			t2 := time.Now()
			for i, h := range res.Hits {
				if i == 10 {
					break
				}
				d, err := idx.Document(h.ID)
				if err != nil || d == nil {
					panic(fmt.Sprint("fetch ", h.ID, err))
				}
			}
			fetch := time.Since(t2)
			if round == 1 {
				top := ""
				if len(res.Hits) > 0 {
					top = res.Hits[0].ID
				}
				fmt.Printf("%-36s hits %5d · search %6.2f ms · fetch 10 %5.2f ms · top %s\n", q, res.Total, float64(took.Microseconds())/1000, float64(fetch.Microseconds())/1000, top)
			}
		}
	}
	fmt.Println("after queries:", heap())
	_ = idx.Close()
}
