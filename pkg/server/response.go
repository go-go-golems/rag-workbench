package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/runner"
	"golang.org/x/sync/errgroup"
)

// responseStream owns the only writer and joins it before the handler returns.
// Every write is bounded by a socket deadline, including terminal/heartbeat frames.
type responseStream struct {
	sink   *streamSink
	writer *sseWriter
	tasks  errgroup.Group
}

func startResponseStream(w http.ResponseWriter, r *http.Request, cancel context.CancelFunc, started time.Time, observe func(runner.Event)) (*responseStream, error) {
	writer, err := newSSEWriter(w, started, eventFilter(r))
	if err != nil {
		return nil, err
	}
	stream := &responseStream{sink: newStreamSink(cancel), writer: writer}
	stream.sink.observe = observe
	stream.tasks.Go(func() error { writer.pump(stream.sink, nil); return writer.err })
	return stream, nil
}
func (s *responseStream) finish(kind string, body []byte) error {
	if kind != "" {
		s.sink.Emit(runner.Event{Type: kind, Data: body})
	}
	s.sink.close()
	return s.tasks.Wait()
}
