package cmds

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The split-file example app must expose its verbs and reproduce the browser
// engine's worked example through source.dir → parse → chunk → bm25.
func TestReportsAppVerbs(t *testing.T) {
	// the memory backend reproduces rag.js exactly (the browser's 12.6758); the
	// default bleve backend may score on its own scale but must find the same section
	for _, backend := range []string{"memory", "bleve"} {
		t.Run(backend, func(t *testing.T) {
			old := indexBackend
			indexBackend = backend
			defer func() { indexBackend = old }()
			dir := "../../../examples/apps/reports"
			root := &cobra.Command{Use: "ragwb"}
			AddRootsFlag(root)
			verbs, err := NewVerbsCommand(dir)
			if err != nil {
				t.Fatal(err)
			}
			root.AddCommand(verbs)
			root.SetArgs([]string{"verbs", "--dir", dir, "reports", "search", "rebalance law", "--k", "2", "--format", "json"})
			// Glazed writes rows to os.Stdout directly (not cobra's OutOrStdout), so capture the fd.
			out := captureStdout(t, func() {
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
			})
			var rows []map[string]any
			if err := json.Unmarshal(out, &rows); err != nil {
				t.Fatalf("decode %q: %v", out, err)
			}
			if len(rows) != 2 {
				t.Fatalf("rows = %v", rows)
			}
			var ref []string
			if err := json.Unmarshal([]byte(rows[0]["id"].(string)), &ref); err != nil {
				t.Fatal(err)
			}
			if len(ref) != 2 || !strings.HasSuffix(ref[1], "#5-9-the-rebalance-law") {
				t.Fatalf("top row = %v", rows[0])
			}
			if backend == "memory" && rows[0]["score"].(float64) != 12.6758 {
				t.Fatalf("memory backend score = %v, want rag.js's 12.6758", rows[0]["score"])
			}
		})
	}
}

func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.Bytes()
	}()
	defer func() { os.Stdout = orig }()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}
