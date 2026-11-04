package ragjs

import (
	"strings"
	"testing"

	"github.com/go-go-golems/geppetto/pkg/steps/ai/providers/pi"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
	"github.com/go-go-golems/rag-workbench/pkg/llm/gepp"
	"github.com/go-go-golems/rag-workbench/pkg/llm/gepp/gepptest"
)

// RAGWB-006: with the Geppetto backend, an answer step's text reaches the
// run's events as the provider streams it: one text event per provider delta,
// adding up to the answer, with its citations.
func TestAnswerStreamsProviderDeltas(t *testing.T) {
	p := gepptest.NewProvider(t, gepptest.Text("The rebalance law ", "preserves placements ", "of every product [E1]."))
	b, err := gepp.New(gepp.Config{AgentDir: gepptest.AgentDir(t, p.URL), Tiers: llm.TierModels{Default: "lunaroute/big"}, Options: []pi.Option{pi.WithLocalProviders()}})
	if err != nil {
		t.Fatal(err)
	}
	rt := newSampleRuntime(t, b)
	var got struct {
		Deltas []string `json:"deltas"`
		Answer string   `json:"answer"`
		Cited  []string `json:"cited"`
		Errors []string `json:"errors"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		`+eventsCorpus+`
		const evs = [];
		const sc = rag.newScope(ev => evs.push(ev));
		const r = await rag.withScope(sc, () => rag.run(plan, { q: 'rebalance law' }, { trigger: 'submit' }));
		return JSON.stringify({ deltas: evs.filter(e => e.type === 'text' && e.node === 'answer').map(e => e.delta),
			answer: r.answer.text, cited: r.answer.cited, errors: r.errors });
	})()`, &got)
	if len(got.Errors) > 0 {
		t.Fatalf("errors: %v", got.Errors)
	}
	if len(got.Deltas) != 3 || strings.Join(got.Deltas, "") != got.Answer || got.Answer != "The rebalance law preserves placements of every product [E1]." {
		t.Fatalf("deltas %q answer %q", got.Deltas, got.Answer)
	}
	if len(got.Cited) != 1 || got.Cited[0] != "E1" {
		t.Fatalf("cited %v", got.Cited)
	}
	if p.Requests() != 1 {
		t.Fatalf("provider requests: %d", p.Requests())
	}
}
