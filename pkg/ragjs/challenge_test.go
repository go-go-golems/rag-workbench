package ragjs

import (
	"os"
	"strings"
	"testing"

	"github.com/go-go-golems/rag-workbench/pkg/decide"
)

func TestReportsChallenge(t *testing.T) {
	src, err := os.ReadFile("../../examples/apps/reports/lib/challenge.js")
	if err != nil {
		t.Fatal(err)
	}
	rt := newOptsRuntime(t, Options{Decide: &decide.Fake{}})
	var got struct {
		Summary struct {
			Answerable, Unanswerable, Rejected int
			FalseAccepts                       int `json:"false_accepts"`
			Hit5, Mrr                          float64
		} `json:"summary"`
		Invalid, Missing string
		Relevant         []int
	}
	evalJSON(t, rt, map[string]any{"__files": readCorpus(t)}, `(async()=>{
 const rag=require('ragwb'), kit=require('ragwb/kit');
 rag.configure({files:()=>__files});
 const m={exports:{}}; (function(module,exports){`+string(src)+`})(m,m.exports);
 const c=m.exports;
 const base=rag.plan('challenge-test').over(kit.reports()).signals({q:'',k:10}).retrieve('text',{k:30});
 const report=await c.measure(rag,base.filter.decide({threshold:0.5}),{engine:rag.engine()});
 let invalid='',missing='';
 try {await c.measure(rag,base,{trigger:'change'});} catch(e) {invalid=String(e);}
 c.answerable.splice(0,c.answerable.length,{id:'missing',q:'anything',doc:'not a real document',section:/absent/});
 try {await c.measure(rag,base);} catch(e) {missing=String(e);}
 return JSON.stringify({summary:report.summary,relevant:report.answerable.map(x=>x.R),invalid,missing});
 })()`, &got)
	if got.Summary.Answerable != 12 || got.Summary.Unanswerable != 8 || got.Summary.Rejected+got.Summary.FalseAccepts != 8 {
		t.Fatalf("unexpected counts: %+v", got.Summary)
	}
	if got.Summary.Hit5 < 0 || got.Summary.Hit5 > 1 || got.Summary.Mrr < 0 || got.Summary.Mrr > 1 {
		t.Fatalf("invalid scores: %+v", got.Summary)
	}
	for _, n := range got.Relevant {
		if n == 0 {
			t.Fatal("answerable label does not match a section")
		}
	}
	if !strings.Contains(got.Invalid, "requires submit") || !strings.Contains(got.Missing, "label matches no corpus section") {
		t.Fatalf("expected invalid runs to fail: %+v", got)
	}
}

func TestReportsChallengeRejectsProviderErrors(t *testing.T) {
	src, err := os.ReadFile("../../examples/apps/reports/lib/challenge.js")
	if err != nil {
		t.Fatal(err)
	}
	rt := newOptsRuntime(t, Options{})
	var message string
	evalJSON(t, rt, map[string]any{"__files": readCorpus(t)}, `(async()=>{
 const rag=require('ragwb'), kit=require('ragwb/kit');
 rag.configure({files:()=>__files});
 const m={exports:{}}; (function(module,exports){`+string(src)+`})(m,m.exports);
 m.exports.answerable.splice(1);
 const plan=rag.plan('missing-provider').over(kit.reports()).signals({q:'',k:10}).retrieve('text',{k:30}).filter.decide({threshold:0.5});
 try {await m.exports.measure(rag,plan);} catch(e) {return JSON.stringify(String(e));}
 return JSON.stringify('unexpected success');
 })()`, &message)
	if !strings.Contains(message, "answerable evaluation failed") {
		t.Fatalf("provider failure counted as a result: %s", message)
	}
}
