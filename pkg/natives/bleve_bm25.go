package natives

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/whitespace"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search"
	"github.com/blevesearch/bleve/v2/search/query"
)

// BleveBm25 is the disk-backed backend of index.bm25 (RAGWB-002, D2/D3): chunks
// are indexed with natives.Tokenize, one bleve field per weighted plan field
// (f0, f1, …, whitespace-analyzed), and searched with a disjunction of term
// queries boosted by field weight × query-term weight. bleve's BM25 does the
// scoring, so rankings may differ from Bm25 (per-field scoring, coordination
// factor); a quality gate against the memory backend checks they stay sensible.
type BleveBm25 struct {
	mu   sync.Mutex
	idx  bleve.Index
	Dir  string
	temp bool // remove Dir on Close (an index that was never persisted)
	Meta BleveMeta
}

// BleveMeta is stored in the index (internal key ragwb.meta), so an opened index
// knows its fields and tokenizer settings.
type BleveMeta struct {
	N      int     `json:"n"`
	Stem   bool    `json:"stem"`
	Fields []Field `json:"fields"`
	Terms  int     `json:"terms,omitempty"`
}

const (
	bleveMetaKey     = "ragwb.meta"
	blevePretokenize = "ragwb-pretokenized"
	bleveDocIDField  = "docId"
)

// bleveField names the bleve field of the i-th weighted plan field. Plan field
// paths can contain dots (meta.summary), which bleve would read as nested
// documents, so fields are positional; BleveMeta.Fields keeps the order.
func bleveField(i int) string { return fmt.Sprintf("f%d", i) }

func bleveMapping(fields []Field) (*mapping.IndexMappingImpl, error) {
	m := bleve.NewIndexMapping()
	m.ScoringModel = "bm25"
	if err := m.AddCustomAnalyzer(blevePretokenize, map[string]any{"type": custom.Name, "tokenizer": whitespace.Name}); err != nil {
		return nil, err
	}
	doc := bleve.NewDocumentMapping()
	for i := range fields {
		fm := bleve.NewTextFieldMapping()
		fm.Analyzer = blevePretokenize
		fm.Store = false
		fm.IncludeInAll = false
		fm.IncludeTermVectors = true // term locations: which query terms matched (why)
		doc.AddFieldMappingsAt(bleveField(i), fm)
	}
	kw := bleve.NewKeywordFieldMapping()
	kw.Store = false
	kw.IncludeInAll = false
	doc.AddFieldMappingsAt(bleveDocIDField, kw)
	doc.Dynamic = false
	m.DefaultMapping = doc
	m.DefaultAnalyzer = blevePretokenize
	return m, nil
}

// BuildBleveBm25 indexes n chunks into a new index at dir (which must not exist).
// text(i, f) returns chunk i's text for fields[f] (false = absent), as for NewBm25.
func BuildBleveBm25(dir string, n int, id, docID func(i int) string, text func(i, f int) (string, bool), fields []Field, doStem bool, temp bool) (*BleveBm25, error) {
	m, err := bleveMapping(fields)
	if err != nil {
		return nil, err
	}
	idx, err := bleve.New(dir, m)
	if err != nil {
		return nil, fmt.Errorf("index.bm25 (bleve): create %s: %w", dir, err)
	}
	b := &BleveBm25{idx: idx, Dir: dir, temp: temp, Meta: BleveMeta{N: n, Stem: doStem, Fields: fields}}
	batch := idx.NewBatch()
	terms := map[string]struct{}{}
	for i := 0; i < n; i++ {
		d := map[string]any{bleveDocIDField: docID(i)}
		for fi := range fields {
			v, ok := text(i, fi)
			if !ok {
				continue
			}
			toks := Tokenize(v, doStem)
			if len(toks) == 0 {
				continue
			}
			for _, t := range toks {
				terms[t] = struct{}{}
			}
			d[bleveField(fi)] = strings.Join(toks, " ")
		}
		if err := batch.Index(id(i), d); err != nil {
			_ = b.Close()
			return nil, err
		}
		if batch.Size() >= 2000 {
			if err := idx.Batch(batch); err != nil {
				_ = b.Close()
				return nil, err
			}
			batch = idx.NewBatch()
		}
	}
	if err := idx.Batch(batch); err != nil {
		_ = b.Close()
		return nil, err
	}
	b.Meta.Terms = len(terms)
	meta, _ := json.Marshal(b.Meta)
	if err := idx.SetInternal([]byte(bleveMetaKey), meta); err != nil {
		_ = b.Close()
		return nil, err
	}
	return b, nil
}

// OpenBleveBm25 opens a stored index read-only (stored outputs are immutable).
func OpenBleveBm25(dir string) (*BleveBm25, error) {
	idx, err := bleve.OpenUsing(dir, map[string]any{"read_only": true})
	if err != nil {
		return nil, fmt.Errorf("index.bm25 (bleve): open %s: %w", dir, err)
	}
	raw, err := idx.GetInternal([]byte(bleveMetaKey))
	if err != nil || len(raw) == 0 {
		_ = idx.Close()
		return nil, fmt.Errorf("index.bm25 (bleve): %s has no ragwb metadata", dir)
	}
	b := &BleveBm25{idx: idx, Dir: dir}
	if err := json.Unmarshal(raw, &b.Meta); err != nil {
		_ = idx.Close()
		return nil, err
	}
	return b, nil
}

// Close releases the index; a temp index is deleted.
func (b *BleveBm25) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.idx == nil {
		return nil
	}
	err := b.idx.Close()
	b.idx = nil
	if b.temp {
		_ = os.RemoveAll(b.Dir)
	}
	return err
}

// Search returns the top k chunks for q. only (optional) restricts to chunk ids.
func (b *BleveBm25) Search(q string, k int, syn []Synonym, only []string) ([]Hit, error) {
	return b.search(q, k, syn, only, "")
}

// SearchIn searches the chunks of one document.
func (b *BleveBm25) SearchIn(docID, q string, k int, syn []Synonym) ([]Hit, error) {
	return b.search(q, k, syn, nil, docID)
}

func (b *BleveBm25) search(q string, k int, syn []Synonym, only []string, inDoc string) ([]Hit, error) {
	b.mu.Lock()
	idx := b.idx
	b.mu.Unlock()
	if idx == nil {
		return nil, fmt.Errorf("index.bm25 (bleve): index is closed")
	}
	if k <= 0 {
		return []Hit{}, nil
	}
	terms := queryTermsFor(q, syn, b.Meta.Stem)
	if len(terms) == 0 {
		return []Hit{}, nil
	}
	qw := make(map[string]float64, len(terms))
	dq := bleve.NewDisjunctionQuery()
	for _, qt := range terms {
		qw[qt.t] = qt.w
		for fi, f := range b.Meta.Fields {
			if f.Weight <= 0 {
				continue
			}
			tq := bleve.NewTermQuery(qt.t)
			tq.SetField(bleveField(fi))
			tq.SetBoost(f.Weight * qt.w)
			dq.AddQuery(tq)
		}
	}
	var qq query.Query = dq
	if only != nil {
		qq = bleve.NewConjunctionQuery(bleve.NewDocIDQuery(only), dq)
	}
	if inDoc != "" {
		tq := bleve.NewTermQuery(inDoc)
		tq.SetField(bleveDocIDField)
		qq = bleve.NewConjunctionQuery(tq, dq)
	}
	req := bleve.NewSearchRequestOptions(qq, k, 0, false)
	req.IncludeLocations = true
	res, err := idx.Search(req)
	if err != nil {
		return nil, err
	}
	weight := map[string]float64{}
	for fi, f := range b.Meta.Fields {
		weight[bleveField(fi)] = f.Weight
	}
	out := make([]Hit, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, Hit{ID: h.ID, Score: jsToFixed(h.Score, 4), Why: approxWhy(h.Score, h.Locations, weight, qw)})
	}
	return out, nil
}

// approxWhy splits a hit's score over its matched query terms in proportion to
// occurrences × field weight × query-term weight. Matched terms are exact; the
// shares approximate bleve's per-term contributions (exact ones need Explain,
// which costs time on every scored document).
func approxWhy(score float64, locs search.FieldTermLocationMap, weight map[string]float64, qw map[string]float64) []Why {
	share := map[string]float64{}
	var total float64
	for field, byTerm := range locs {
		for term, ls := range byTerm {
			w := float64(len(ls)) * weight[field] * qw[term]
			share[term] += w
			total += w
		}
	}
	why := make([]Why, 0, len(share))
	for t, w := range share {
		v := 0.0
		if total > 0 {
			v = score * w / total
		}
		why = append(why, Why{t, jsToFixed(v, 2)})
	}
	sort.Slice(why, func(i, j int) bool {
		if why[i].Score != why[j].Score {
			return why[i].Score > why[j].Score
		}
		return why[i].Term < why[j].Term
	})
	if len(why) > 5 {
		why = why[:5]
	}
	return why
}
