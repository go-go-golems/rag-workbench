package server

import (
	"context"
	"fmt"
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
	s.writer.drainDeadline.Store(time.Now().Add(sseWriteTimeout).UnixNano())
	if kind != "" {
		s.sink.Emit(runner.Event{Type: kind, Data: body})
	}
	s.sink.close()
	return s.tasks.Wait()
}

// writeOperationJSON shares the transport deadline and payload budget used by
// terminal stream responses. The handler retains admission until Flush returns.
func writeOperationJSON(w http.ResponseWriter, body []byte) error {
	if len(body) > sseMaxResultBytes {
		err := fmt.Errorf("response exceeds 32 MiB")
		writeErr(w, http.StatusInternalServerError, "result_limit", err.Error())
		return err
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil {
		writeErr(w, http.StatusInternalServerError, "transport_unavailable", err.Error())
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		return err
	}
	return controller.Flush()
}
