package natives

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
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
	mu      sync.RWMutex
	idx     bleve.Index
	path    string // the index directory
	cleanup string // removed on Close: the temp build area of an index that was never stored
	Meta    BleveMeta
}

// BleveMeta is stored in the index (internal key ragwb.meta), so an opened index
// knows its fields and tokenizer settings.
type BleveMeta struct {
	N      int     `json:"n"`
	Stem   bool    `json:"stem"`
	Fields []Field `json:"fields"`
	Terms  int     `json:"terms,omitempty"`
	// Combined indexes one field ("all") holding every weighted field's tokens
	// repeated round(weight) times: BM25F-style term frequencies and lengths, one
	// IDF. Otherwise each field is its own bleve field (per-field IDF) and weights
	// are query boosts.
	Combined bool `json:"combined,omitempty"`
	// FieldLen is the total token count per bleve field over the indexed chunks
	// (RAGWB-003). Searches pass it to bleve's BM25 as the field statistics, so
	// the average field length is ceil(tokens / N), exact, and independent of
	// how scorch happens to have split the index into segments: bleve's own
	// statistic sums each segment's distinct terms, which a delta build (or a
	// different merge) changes. Absent in indexes from before RAGWB-003.
	FieldLen map[string]int64 `json:"fieldLen,omitempty"`
}

// BleveOptions configures BuildBleveBm25.
type BleveOptions struct {
	Stem     bool
	Temp     bool // delete the index directory on Close
	Combined bool // see BleveMeta.Combined
}

const bleveAllField = "all"

// repeats is how often a field's tokens go into the combined field.
func repeats(w float64) int {
	if w <= 0 {
		return 0
	}
	return max(1, int(math.Round(w)))
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

func bleveMapping(fields []Field, combined bool) (*mapping.IndexMappingImpl, error) {
	m := bleve.NewIndexMapping()
	m.ScoringModel = "bm25"
	if err := m.AddCustomAnalyzer(blevePretokenize, map[string]any{"type": custom.Name, "tokenizer": whitespace.Name}); err != nil {
		return nil, err
	}
	doc := bleve.NewDocumentMapping()
	names := []string{bleveAllField}
	if !combined {
		names = names[:0]
		for i := range fields {
			names = append(names, bleveField(i))
		}
	}
	for _, name := range names {
		fm := bleve.NewTextFieldMapping()
		fm.Analyzer = blevePretokenize
		fm.Store = false
		fm.IncludeInAll = false
		fm.IncludeTermVectors = true // term locations: which query terms matched (why)
		doc.AddFieldMappingsAt(name, fm)
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
func BuildBleveBm25(dir string, n int, id, docID func(i int) string, text func(i, f int) (string, bool), fields []Field, o BleveOptions) (*BleveBm25, error) {
	doStem, temp := o.Stem, o.Temp
	m, err := bleveMapping(fields, o.Combined)
	if err != nil {
		return nil, err
	}
	idx, err := bleve.New(dir, m)
	if err != nil {
		return nil, fmt.Errorf("index.bm25 (bleve): create %s: %w", dir, err)
	}
	b := &BleveBm25{idx: idx, path: dir, Meta: BleveMeta{N: n, Stem: doStem, Fields: fields, Combined: o.Combined, FieldLen: map[string]int64{}}}
	if temp {
		b.cleanup = dir
	}
	batch := idx.NewBatch()
	terms := map[string]struct{}{}
	for i := 0; i < n; i++ {
		d := bleveDoc(docID(i), fields, func(f int) (string, bool) { return text(i, f) }, doStem, o.Combined, terms, b.Meta.FieldLen)
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

// bleveDoc is the bleve document of one chunk: its fields' tokens (one bleve
// field per plan field, or the combined field) and its doc id.
func bleveDoc(docID string, fields []Field, text func(f int) (string, bool), doStem, combined bool, terms map[string]struct{}, lens map[string]int64) map[string]any {
	d := map[string]any{bleveDocIDField: docID}
	var all strings.Builder
	for fi := range fields {
		v, ok := text(fi)
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
		joined := strings.Join(toks, " ")
		if !combined {
			d[bleveField(fi)] = joined
			lens[bleveField(fi)] += int64(len(toks))
			continue
		}
		lens[bleveAllField] += int64(len(toks) * repeats(fields[fi].Weight))
		for r := 0; r < repeats(fields[fi].Weight); r++ {
			all.WriteString(joined)
			all.WriteByte(' ')
		}
	}
	if combined && all.Len() > 0 {
		d[bleveAllField] = all.String()
	}
	return d
}

// cloneBleveDir copies a scorch index directory to dst, hard-linking its
// segment files (*.zap, immutable once written) and copying the rest (the
// root.bolt snapshot list, index_meta.json). The clone can then be opened
// writable: scorch only adds segments and replaces its own root.bolt, and a
// merge that drops a segment unlinks the clone's link, not the base's file.
func cloneBleveDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if strings.HasSuffix(p, ".zap") {
			return linkOrCopy(p, out)
		}
		return copyFile(p, out)
	})
}

// ApplyDelta (RAGWB-003 §6.6) makes a new index at dst from this one: a clone
// that deletes removeIDs and indexes n new chunks (as BuildBleveBm25 would).
// The result is open writable until stored (Relocate reopens it read-only).
// removedText gives the removed chunks' field texts (as text does for new
// ones), so the field statistics can be kept exact.
func (b *BleveBm25) ApplyDelta(dst string, removeIDs []string, removedText func(i, f int) (string, bool), n int, id, docID func(i int) string, text func(i, f int) (string, bool)) (*BleveBm25, error) {
	b.mu.RLock()
	src, meta := b.path, b.Meta
	b.mu.RUnlock()
	if meta.FieldLen == nil {
		return nil, fmt.Errorf("index.bm25 (bleve): base index has no field statistics (built before RAGWB-003)")
	}
	lens := make(map[string]int64, len(meta.FieldLen))
	for k, v := range meta.FieldLen {
		lens[k] = v
	}
	gone := map[string]int64{}
	for i := range removeIDs {
		_ = bleveDoc("", meta.Fields, func(f int) (string, bool) { return removedText(i, f) }, meta.Stem, meta.Combined, map[string]struct{}{}, gone)
	}
	for k, v := range gone {
		lens[k] -= v
	}
	if err := cloneBleveDir(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return nil, fmt.Errorf("index.bm25 (bleve): clone: %w", err)
	}
	idx, err := bleve.Open(dst)
	if err != nil {
		_ = os.RemoveAll(dst)
		return nil, fmt.Errorf("index.bm25 (bleve): open clone: %w", err)
	}
	nb := &BleveBm25{idx: idx, path: dst, Meta: meta}
	fail := func(err error) (*BleveBm25, error) {
		_ = nb.Close()
		_ = os.RemoveAll(dst)
		return nil, err
	}
	batch := idx.NewBatch()
	for _, rid := range removeIDs {
		batch.Delete(rid)
	}
	terms := map[string]struct{}{}
	for i := 0; i < n; i++ {
		d := bleveDoc(docID(i), meta.Fields, func(f int) (string, bool) { return text(i, f) }, meta.Stem, meta.Combined, terms, lens)
		if err := batch.Index(id(i), d); err != nil {
			return fail(err)
		}
	}
	if err := idx.Batch(batch); err != nil {
		return fail(err)
	}
	count, err := idx.DocCount()
	if err != nil {
		return fail(err)
	}
	// the removed ids are all of the touched docs' old chunks, so no new id survives from the base
	if want := meta.N - len(removeIDs) + n; int(count) != want {
		return fail(fmt.Errorf("index.bm25 (bleve): delta left %d chunks, expected %d", count, want))
	}
	nb.Meta.N = int(count)
	nb.Meta.FieldLen = lens
	// Terms (distinct terms, shown by toJSON) is not tracked through deletions:
	// it stays the base's count plus the new chunks' terms it can't rule out.
	nb.Meta.Terms = max(meta.Terms, len(terms))
	raw, _ := json.Marshal(nb.Meta)
	if err := idx.SetInternal([]byte(bleveMetaKey), raw); err != nil {
		return fail(err)
	}
	return nb, nil
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
	b := &BleveBm25{idx: idx, path: dir}
	if err := json.Unmarshal(raw, &b.Meta); err != nil {
		_ = idx.Close()
		return nil, err
	}
	return b, nil
}

// Path is the index directory.
func (b *BleveBm25) Path() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.path
}

// SetCleanup makes Close remove dir (the temp area the index was built in).
func (b *BleveBm25) SetCleanup(dir string) {
	b.mu.Lock()
	b.cleanup = dir
	b.mu.Unlock()
}

// Close releases the index; a never-stored index's temp area is deleted.
func (b *BleveBm25) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var err error
	if b.idx != nil {
		err = b.idx.Close()
		b.idx = nil
	}
	if b.cleanup != "" {
		_ = os.RemoveAll(b.cleanup)
		b.cleanup = ""
	}
	return err
}

// Relocate closes the index, hands its directory to move (which stores it and
// returns where it now lives) and reopens it read-only there. The same handle
// keeps working, so JS objects that wrap it need not change. If move fails the
// index is reopened where it was.
func (b *BleveBm25) Relocate(move func(dir string) (string, error)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.idx == nil {
		return fmt.Errorf("index.bm25 (bleve): index is closed")
	}
	if err := b.idx.Close(); err != nil {
		return err
	}
	b.idx = nil
	dst, err := move(b.path)
	if err != nil {
		if idx, oerr := bleve.Open(b.path); oerr == nil {
			b.idx = idx
		}
		return err
	}
	idx, err := bleve.OpenUsing(dst, map[string]any{"read_only": true})
	if err != nil {
		return fmt.Errorf("index.bm25 (bleve): reopen %s: %w", dst, err)
	}
	if b.cleanup != "" {
		_ = os.RemoveAll(b.cleanup)
		b.cleanup = ""
	}
	b.idx, b.path = idx, dst
	return nil
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
	b.mu.RLock()
	defer b.mu.RUnlock()
	idx := b.idx
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
		if b.Meta.Combined {
			tq := bleve.NewTermQuery(qt.t)
			tq.SetField(bleveAllField)
			tq.SetBoost(qt.w)
			dq.AddQuery(tq)
			continue
		}
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
	if b.Meta.FieldLen != nil {
		// every queried field needs a statistic: the plan fields, and docId (one token per chunk) for searchIn
		card := map[string]int{bleveDocIDField: b.Meta.N, bleveAllField: 0}
		for fi := range b.Meta.Fields {
			card[bleveField(fi)] = 0
		}
		for f, n := range b.Meta.FieldLen {
			card[f] = int(n)
		}
		req.PreSearchData = map[string]any{search.BM25PreSearchDataKey: &search.BM25Stats{DocCount: float64(b.Meta.N), FieldCardinality: card}}
	}
	res, err := idx.Search(req)
	if err != nil {
		return nil, err
	}
	weight := map[string]float64{bleveAllField: 1}
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
