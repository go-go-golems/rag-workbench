// RAGWB-006 spike: does Geppetto's Pi provider stream token deltas from a Pi
// model reference, with the credentials Pi stores? Prints only model refs,
// timings and delta counts (never credentials).
//
//	go run ./ttmp/2026/11/04/RAGWB-006--*/scripts/01-geppetto-pi-spike [provider/model] [thinking]
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-go-golems/geppetto/pkg/events"
	"github.com/go-go-golems/geppetto/pkg/inference/engine"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/providers/pi"
	"github.com/go-go-golems/geppetto/pkg/turns"
)

type sink struct {
	mu     sync.Mutex
	t0     time.Time
	first  time.Duration
	n      int
	text   strings.Builder
	reason int
	stop   string
	usage  string
}

func (s *sink) PublishEvent(e events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e := e.(type) {
	case *events.EventTextDelta:
		if e.Delta == "" {
			return nil
		}
		if s.n == 0 {
			s.first = time.Since(s.t0)
		}
		s.n++
		s.text.WriteString(e.Delta)
	case *events.EventReasoningDelta:
		s.reason++
	case *events.EventProviderCallFinished:
		s.stop = e.StopReason
		if e.Usage != nil {
			s.usage = fmt.Sprintf("in=%d out=%d", e.Usage.InputTokens, e.Usage.OutputTokens)
		}
	}
	return nil
}

func main() {
	ref, level := "lunaroute/glm-5.3-flash", "off"
	if len(os.Args) > 1 {
		ref = os.Args[1]
	}
	if len(os.Args) > 2 {
		level = os.Args[2]
	}
	t0 := time.Now()
	dir, err := pi.LoadAgentDir(pi.DefaultDir(os.Getenv(pi.EnvAgentDir)))
	if err != nil {
		fmt.Println("load:", err)
		os.Exit(1)
	}
	fmt.Printf("agent dir loaded in %s; %d available models\n", time.Since(t0).Round(time.Millisecond), len(dir.Available()))
	ctx := context.Background()
	res, err := dir.Resolve(ctx, ref, pi.ResolveOptions{Thinking: level})
	if err != nil {
		fmt.Println("resolve:", err)
		os.Exit(1)
	}
	t := &turns.Turn{}
	turns.AppendBlock(t, turns.NewUserTextBlock("In about 80 words, explain what BM25 is."))
	maxTok := 600
	_ = engine.KeyInferenceConfig.Set(&t.Data, engine.InferenceConfig{MaxResponseTokens: &maxTok})
	eng, err := res.Engine()
	if err != nil {
		fmt.Println("engine:", err)
		os.Exit(1)
	}
	s := &sink{t0: time.Now()}
	_, err = eng.RunInference(events.WithEventSinks(ctx, s), t)
	total := time.Since(s.t0)
	if err != nil {
		fmt.Println("inference:", err)
	}
	fmt.Printf("model %s thinking=%s: %d text deltas, first after %s, total %s, reasoning deltas %d, stop %q, usage %s\n",
		ref, level, s.n, s.first.Round(time.Millisecond), total.Round(time.Millisecond), s.reason, s.stop, s.usage)
	fmt.Printf("text: %q\n", strings.TrimSpace(s.text.String()))
}
