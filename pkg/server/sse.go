package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/runner"
)

// Server-Sent Events (RAGWB-004 §6.6). Engine events reach a streamSink on the
// runtime's owner goroutine; a pump goroutine writes them to the response, so a
// slow client never stalls the runtime for long:
//
//	event: <type>
//	id: <seq>
//	data: {"seq":…,"t":…,<the event's own fields>}
//
// Text deltas of one node that arrive close together are merged into one event
// (lossless: deltas concatenate). Idle streams get a ": ping" comment every
// heartbeat. Every stream ends with exactly one "result" or "error" event.

var (
	sseHeartbeat = 15 * time.Second
	sseCoalesce  = 30 * time.Millisecond
	sseSlow      = 100 * time.Millisecond // how long Emit waits on a full queue before giving up on the client
)

const sseQueue = 256

// streamSink queues events for the pump. If the queue stays full (a client that
// doesn't read), it cancels the request instead of blocking the runtime.
type streamSink struct {
	ch     chan runner.Event
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	slow   bool
}

func newStreamSink(cancel context.CancelFunc) *streamSink {
	return &streamSink{ch: make(chan runner.Event, sseQueue), cancel: cancel}
}

func (s *streamSink) Emit(e runner.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.slow {
		return
	}
	select {
	case s.ch <- e:
		return
	default:
	}
	t := time.NewTimer(sseSlow)
	defer t.Stop()
	select {
	case s.ch <- e:
	case <-t.C:
		s.slow = true
		s.cancel()
	}
}

func (s *streamSink) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

// sseWriter frames events on an http.ResponseWriter.
type sseWriter struct {
	w      http.ResponseWriter
	fl     http.Flusher
	t0     time.Time
	seq    int
	filter map[string]bool // nil = all; start/result/error always pass
	count  int
}

func newSSEWriter(w http.ResponseWriter, t0 time.Time, filter map[string]bool) *sseWriter {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	return &sseWriter{w: w, fl: fl, t0: t0, filter: filter}
}

// write sends one event; data must be a JSON object (seq and t are prepended).
func (s *sseWriter) write(typ string, data []byte) error {
	if s.filter != nil && !s.filter[typ] && typ != "start" && typ != "result" && typ != "error" {
		return nil
	}
	s.seq++
	s.count++
	t := float64(time.Since(s.t0).Microseconds()) / 1000
	body := strings.TrimSpace(string(data))
	head := fmt.Sprintf(`{"seq":%d,"t":%s`, s.seq, strconv.FormatFloat(t, 'f', 1, 64))
	if body == "{}" || body == "" {
		body = head + "}"
	} else {
		body = head + "," + strings.TrimPrefix(body, "{")
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\nid: %d\ndata: %s\n\n", typ, s.seq, body); err != nil {
		return err
	}
	if s.fl != nil {
		s.fl.Flush()
	}
	return nil
}

func (s *sseWriter) ping() error {
	if _, err := fmt.Fprint(s.w, ": ping\n\n"); err != nil {
		return err
	}
	if s.fl != nil {
		s.fl.Flush()
	}
	return nil
}

type textEvent struct {
	Type  string `json:"type"`
	Run   int    `json:"run,omitempty"`
	Node  string `json:"node"`
	Delta string `json:"delta"`
	Reset bool   `json:"reset,omitempty"`
}

// pump writes the sink's events until it is closed, merging text deltas and
// sending heartbeats. observe sees every event (before filtering), for summaries.
func (s *sseWriter) pump(sink *streamSink, observe func(runner.Event)) {
	hb := time.NewTicker(sseHeartbeat)
	defer hb.Stop()
	var pending *textEvent
	var flushAt <-chan time.Time
	flushText := func() {
		if pending == nil {
			return
		}
		b, _ := json.Marshal(pending)
		_ = s.write("text", b)
		pending = nil
		flushAt = nil
	}
	for {
		select {
		case e, ok := <-sink.ch:
			if !ok {
				flushText()
				return
			}
			if observe != nil {
				observe(e)
			}
			if e.Type == "text" {
				var te textEvent
				if json.Unmarshal(e.Data, &te) == nil && !te.Reset {
					if pending != nil && pending.Node == te.Node && pending.Run == te.Run {
						pending.Delta += te.Delta
						continue
					}
					flushText()
					pending = &te
					flushAt = time.After(sseCoalesce)
					continue
				}
			}
			flushText()
			_ = s.write(e.Type, e.Data)
		case <-flushAt:
			flushText()
		case <-hb.C:
			_ = s.ping()
		}
	}
}

// wantsSSE: Accept: text/event-stream, or ?stream=sse.
func wantsSSE(r *http.Request) bool {
	return r.URL.Query().Get("stream") == "sse" || strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// eventFilter parses ?events=node,hits,text (nil = all).
func eventFilter(r *http.Request) map[string]bool {
	v := r.URL.Query().Get("events")
	if v == "" {
		return nil
	}
	m := map[string]bool{}
	for _, t := range strings.Split(v, ",") {
		if t = strings.TrimSpace(t); t != "" {
			m[t] = true
		}
	}
	return m
}
