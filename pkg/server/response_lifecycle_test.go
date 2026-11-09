package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/runner"
)

func TestResponseStreamRejectsUnboundedWriter(t *testing.T) {
	_, err := newSSEWriter(httptest.NewRecorder(), time.Now(), nil)
	if err == nil {
		t.Fatal("writer without deadline support accepted")
	}
}

// A real TCP peer stops reading while the producer writes more than the socket
// buffers can hold. The response pump must exit and release admission.
func TestStalledReaderReleasesResponse(t *testing.T) {
	previous := sseWriteTimeout
	sseWriteTimeout = 100 * time.Millisecond
	defer func() { sseWriteTimeout = previous }()
	slot := make(chan struct{}, 1)
	completed := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slot <- struct{}{}:
		default:
			http.Error(w, "busy", 503)
			return
		}
		defer func() { <-slot }()
		if r.URL.Path == "/probe" {
			w.WriteHeader(204)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stream, err := startResponseStream(w, r, cancel, time.Now(), nil)
		if err != nil {
			completed <- err
			return
		}
		payload := []byte(`{"delta":"` + strings.Repeat("x", 64<<10) + `"}`)
		for i := 0; i < 512 && ctx.Err() == nil; i++ {
			stream.sink.Emit(runner.Event{Type: "payload", Data: payload})
		}
		completed <- stream.finish("result", []byte(`{}`))
	}))
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(1024)
	}
	if _, err = fmt.Fprintf(conn, "GET /stream HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-completed:
		if err == nil {
			t.Fatal("stalled write unexpectedly succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("response pump failed to exit")
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(server.URL + "/probe")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != 204 {
		t.Fatalf("admission not released: %d", response.StatusCode)
	}
}
