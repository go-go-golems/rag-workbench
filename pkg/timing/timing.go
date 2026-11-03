// Package timing records where a ragwb invocation spends its time: sequential
// phases (process setup, runtime creation, the verb, output) and, inside them,
// details reported by the engine (compile, build nodes, artifact loads, query
// nodes). ragwb --timings prints the breakdown to stderr.
package timing

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one measured span.
type Entry struct {
	Phase  string  `json:"phase"`  // the sequential phase it belongs to
	Kind   string  `json:"kind"`   // phase | compile | build | load | artifacts | run | store
	Name   string  `json:"name"`   // node id, file, …
	MS     float64 `json:"ms"`     // duration
	Detail string  `json:"detail"` // e.g. "stored", "computed", "read 46ms decode 1.13s, 142 MB"
}

// Recorder collects entries. The zero value is disabled; use New.
type Recorder struct {
	mu      sync.Mutex
	on      bool
	start   time.Time
	last    time.Time
	phase   string
	entries []Entry
}

// New returns an enabled recorder whose clock starts at start (e.g. process start),
// in a first phase called phase.
func New(start time.Time, phase string) *Recorder {
	return &Recorder{on: true, start: start, last: start, phase: phase}
}

// Default is the process-wide recorder (nil = timings off).
var Default *Recorder

// Enabled reports whether r records.
func (r *Recorder) Enabled() bool { return r != nil && r.on }

// Phase closes the running phase at now and starts the next one.
func (r *Recorder) Phase(next string) {
	if !r.Enabled() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.entries = append(r.entries, Entry{Phase: r.phase, Kind: "phase", Name: r.phase, MS: ms(now.Sub(r.last))})
	r.phase, r.last = next, now
}

// Add records a detail inside the running phase.
func (r *Recorder) Add(kind, name string, msec float64, detail string) {
	if !r.Enabled() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, Entry{Phase: r.phase, Kind: kind, Name: name, MS: msec, Detail: detail})
}

// Entries returns a copy of what was recorded.
func (r *Recorder) Entries() []Entry {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Entry(nil), r.entries...)
}

// Total is the time since start.
func (r *Recorder) Total() time.Duration { return time.Since(r.start) }

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Print writes the breakdown: each phase with its share, then the details
// recorded inside it, slowest first.
func (r *Recorder) Print(w io.Writer) {
	if !r.Enabled() {
		return
	}
	r.Phase("end")
	es := r.Entries()
	total := ms(r.Total())
	fmt.Fprintf(w, "\ntimings (total %s)\n", fmtMS(total))
	byPhase := map[string][]Entry{}
	for _, e := range es {
		if e.Kind != "phase" {
			byPhase[e.Phase] = append(byPhase[e.Phase], e)
		}
	}
	for _, e := range es {
		if e.Kind != "phase" {
			continue
		}
		fmt.Fprintf(w, "  %-34s %9s  %5.1f%%\n", e.Name, fmtMS(e.MS), 100*e.MS/total)
		ds := byPhase[e.Name]
		sort.SliceStable(ds, func(i, j int) bool { return ds[i].MS > ds[j].MS })
		for _, d := range ds {
			label := d.Kind + " " + d.Name
			fmt.Fprintf(w, "      %-30s %9s  %s\n", clip(label, 30), fmtMS(d.MS), d.Detail)
		}
	}
}

func fmtMS(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.2f s", v/1000)
	}
	return fmt.Sprintf("%.1f ms", v)
}

func clip(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// Line formats entries as one compact line (for logs and HTTP responses).
func Line(es []Entry) string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, fmt.Sprintf("%s %s=%s", e.Kind, e.Name, fmtMS(e.MS)))
	}
	return strings.Join(parts, " · ")
}
