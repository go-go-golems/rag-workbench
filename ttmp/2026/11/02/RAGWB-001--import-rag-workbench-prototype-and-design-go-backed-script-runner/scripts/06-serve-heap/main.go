// Heap profile of one served app session (RAGWB-001 step 25).
//
//	go run ./ttmp/.../scripts/06-serve-heap -root ~/code/wesen/go-go-golems/go-go-parc -app examples/apps/parc -out /tmp/heap.pb.gz
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
	"github.com/go-go-golems/rag-workbench/pkg/runner"
)

func mb(b uint64) float64 { return float64(b) / (1 << 20) }

func stats(label string) {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("%-28s live heap %7.1f MB · heap objects %9d · sys %7.1f MB\n", label, mb(m.HeapAlloc), m.HeapObjects, mb(m.Sys))
}

func main() {
	root := flag.String("root", "", "allowlisted corpus root")
	app := flag.String("app", "examples/apps/parc", "app to open")
	store := flag.String("store", filepath.Join(os.Getenv("HOME"), ".ragwb", "store"), "artifact store")
	out := flag.String("out", "heap.pb.gz", "heap profile")
	flag.Parse()
	stats("start")
	st, err := artifacts.Open(*store, runner.ArtifactNamespace(false))
	if err != nil {
		panic(err)
	}
	base, _ := filepath.Abs(*app)
	f, err := runner.NewFactory(runner.Config{Roots: []string{*root, base}, Base: base, Artifacts: st, SourceDigestTTL: time.Hour})
	if err != nil {
		panic(err)
	}
	t0 := time.Now()
	a, err := runner.OpenApp(context.Background(), f, runner.AppSpec{Name: "x", Path: base})
	if err != nil {
		panic(err)
	}
	fmt.Printf("opened in %v\n", time.Since(t0).Round(time.Millisecond))
	stats("after open")
	if _, err := a.Run(context.Background(), []byte(`{"q":"goja unicode regexp"}`), "change", nil); err != nil {
		panic(err)
	}
	stats("after one query")
	w, _ := os.Create(*out)
	_ = pprof.WriteHeapProfile(w)
	_ = w.Close()
	a.Close()
	stats("after close")
}
