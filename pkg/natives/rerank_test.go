package natives

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

// overlapRanker scores a doc by how many query words it contains (per pair, as a cross-encoder does).
type overlapRanker struct {
	mu    sync.Mutex
	sizes []int
	fail  int // fail the call whose batch starts with this doc text index (-1: never)
}

func (o *overlapRanker) Embed(context.Context, []string, llm.EmbedOptions) ([][]float32, error) {
	return nil, errors.New("not used")
}

func (o *overlapRanker) Rerank(ctx context.Context, _, q string, docs []string, topN int) ([]llm.RerankResult, error) {
	o.mu.Lock()
	o.sizes = append(o.sizes, len(docs))
	o.mu.Unlock()
	if o.fail >= 0 && len(docs) > 0 && docs[0] == "doc-fail" {
		return nil, errors.New("backend down")
	}
	res := make([]llm.RerankResult, len(docs))
	for i, d := range docs {
		n := 0
		for _, w := range strings.Fields(q) {
			if strings.Contains(d, w) {
				n++
			}
		}
		res[i] = llm.RerankResult{Index: i, Score: float64(n)}
	}
	// a backend returns best first; the order inside a batch must not matter
	for i, j := 0, len(res)-1; i < j; i, j = i+1, j-1 {
		res[i], res[j] = res[j], res[i]
	}
	return res[:min(topN, len(res))], nil
}

func TestRerankBatchedEqualsOneRequest(t *testing.T) {
	docs := []string{"alpha", "rebalance law", "law", "beta", "the rebalance law preserves", "gamma law", "rebalance", "delta", "law law", "epsilon", "rebalance law again"}
	one, err := RerankBatched(context.Background(), &overlapRanker{fail: -1}, "m", "rebalance law", docs, 99, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &overlapRanker{fail: -1}
	var batches [][]llm.RerankResult
	got, err := RerankBatched(context.Background(), r, "m", "rebalance law", docs, 3, 2, func(p []llm.RerankResult) { batches = append(batches, p) })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, one) {
		t.Fatalf("batched %v\nsingle  %v", got, one)
	}
	if len(batches) != 4 || len(r.sizes) != 4 {
		t.Fatalf("%d batches reported, %d requests (sizes %v)", len(batches), len(r.sizes), r.sizes)
	}
	seen := map[int]bool{}
	for _, b := range batches {
		for _, x := range b {
			seen[x.Index] = true
		}
	}
	if len(seen) != len(docs) {
		t.Fatalf("batches cover %d of %d docs (indexes must be global)", len(seen), len(docs))
	}
	// equal scores keep the original order
	for i := 1; i < len(got); i++ {
		if got[i-1].Score == got[i].Score && got[i-1].Index > got[i].Index {
			t.Fatalf("tie order: %v", got)
		}
	}
}

func TestRerankBatchedFailsOnABatchError(t *testing.T) {
	docs := []string{"a", "b", "c", "doc-fail", "e", "f"}
	_, err := RerankBatched(context.Background(), &overlapRanker{fail: 3}, "m", "q", docs, 3, 1, nil)
	if err == nil || !strings.Contains(err.Error(), "backend down") {
		t.Fatalf("err = %v", err)
	}
}
