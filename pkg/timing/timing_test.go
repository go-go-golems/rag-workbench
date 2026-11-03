package timing

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRecorderPhasesAndDetails(t *testing.T) {
	r := New(time.Now().Add(-10*time.Millisecond), "startup")
	r.Phase("verb")
	r.Add("load", "chunks", 1200, "chunk.sections")
	r.Add("run", "text", 1.5, "retrieve")
	r.Phase("output")
	var buf bytes.Buffer
	r.Print(&buf)
	out := buf.String()
	for _, want := range []string{"timings (total", "startup", "verb", "load chunks", "1.20 s", "run text", "1.5 ms", "output"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// details print under their phase, slowest first
	if strings.Index(out, "load chunks") > strings.Index(out, "run text") {
		t.Errorf("details not sorted slowest first:\n%s", out)
	}
	es := r.Entries()
	if es[0].Kind != "phase" || es[0].Name != "startup" || es[0].MS < 10 {
		t.Errorf("first phase = %+v", es[0])
	}
}

func TestNilRecorderIsOff(t *testing.T) {
	var r *Recorder
	r.Phase("x")
	r.Add("load", "y", 1, "")
	r.Print(&bytes.Buffer{})
	if r.Enabled() || r.Entries() != nil {
		t.Fatal("nil recorder recorded something")
	}
}
