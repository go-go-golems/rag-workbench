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
	dir := "../../../examples/apps/reports"
	root := &cobra.Command{Use: "ragwb"}
	AddRootsFlag(root)
	verbs, err := NewVerbsCommand(dir)
	if err != nil {
		t.Fatal(err)
	}
	root.AddCommand(verbs)
	root.SetArgs([]string{"verbs", "--dir", dir, "reports", "search", "rebalance law", "--k", "2", "--output", "json"})
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
	if !strings.HasSuffix(rows[0]["id"].(string), "#5-9-the-rebalance-law") || rows[0]["score"].(float64) != 12.6758 {
		t.Fatalf("top row = %v", rows[0])
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
