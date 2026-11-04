package cmds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-go-golems/rag-workbench/pkg/runner"
	"github.com/go-go-golems/rag-workbench/pkg/server"
	"github.com/go-go-golems/rag-workbench/pkg/store"
	"github.com/go-go-golems/rag-workbench/pkg/timing"
)

// NewServeCommand serves apps: each app's plan is built once in a long-lived
// runtime and its gui runs in the browser against that session (guide P7).
func NewServeCommand() *cobra.Command {
	var addr, files string
	var quiet bool
	var timeout, watch time.Duration
	cmd := &cobra.Command{
		Use:   "serve <app>...",
		Short: "Serve apps: build each plan once server-side and mount its gui in the browser",
		Long: "Each argument is an app directory with app.js (exporting { app } built with gui.app(...).use(plan)),\n" +
			"a workbench script (*.wb.js returning { plan, app? }), or the name of an app in $RAGWB_APPS / ~/.ragwb/apps.\n" +
			"The plan is built once (loading stored artifacts, see `ragwb store`); every query runs only the live nodes.\n" +
			"Open http://ADDR/apps/<name>/. Example:\n" +
			"  ragwb --root ~/code/wesen/go-go-golems/go-go-parc serve examples/apps/parc",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := LLM()
			if err != nil {
				return err
			}
			embedder, embedCache, err := Embedder()
			if err != nil {
				return err
			}
			artStore, err := Artifacts()
			if err != nil {
				return err
			}
			var corpus []runner.File
			if files != "" {
				if corpus, err = runner.ReadFlatCorpus(files); err != nil {
					return err
				}
			}
			stderr := cmd.ErrOrStderr()
			var opened []*server.RagApp
			defer func() {
				for _, a := range opened {
					a.Close()
				}
			}()
			seen := map[string]bool{}
			for _, arg := range args {
				path, name := resolveServed(arg)
				if seen[name] {
					return fmt.Errorf("two apps are named %q; serve them from separate processes or rename one", name)
				}
				seen[name] = true
				base := path
				if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
					base = filepath.Dir(path)
				}
				rec := timing.New(time.Now(), "open "+name+" (runtime, require, build, load artifacts)")
				factory, err := runner.NewFactory(runner.Config{Roots: Roots(base), Base: base, JSComponents: JSComponents(), IndexBackend: IndexBackend(), LLM: backend, BuildCalls: BuildCalls(), Embedder: embedder, EmbedCache: embedCache, Artifacts: artStore, Timing: rec, SourceDigestTTL: sourceTTL(watch)})
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				app, err := runner.OpenApp(ctx, factory, runner.AppSpec{Name: name, Path: path, Files: corpus})
				cancel()
				if err != nil {
					return err
				}
				rec.Phase("serving")
				opened = append(opened, &server.RagApp{ServedApp: app, Timing: rec})
				fmt.Fprintln(stderr, openSummary(name, app.Info, rec))
				if timing.Default.Enabled() {
					rec.Print(stderr)
				}
			}
			rs := server.NewRagServer(opened...)
			if !quiet {
				rs.Log = stderr
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			srv := &http.Server{Handler: rs.Handler(), ReadHeaderTimeout: 10 * time.Second}
			for _, a := range opened {
				fmt.Fprintf(cmd.OutOrStdout(), "ragwb serve: http://%s/apps/%s/\n", ln.Addr(), a.Name)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			go func() {
				<-ctx.Done()
				sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
				defer c()
				_ = srv.Shutdown(sctx)
			}()
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8791", "Listen address (keep it on loopback: there is no auth)")
	cmd.Flags().StringVar(&files, "files", "", "Directory of .md/.markdown/.txt files served to rag.files(...) sources")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Do not log one line per query")
	cmd.Flags().DurationVar(&watch, "watch", 0, "Re-check source.dir trees for changes at most this often (a change rebuilds on the next query); 0 = snapshot at open, restart to pick up edits")
	cmd.Flags().DurationVar(&timeout, "open-timeout", 10*time.Minute, "Abort opening an app (build) after this long")
	return cmd
}

// resolveServed maps an argument to a path and a URL name: an existing path wins,
// otherwise an app name (optionally app/file.wb.js) in the apps directory.
func resolveServed(arg string) (path, name string) {
	path = arg
	if _, err := os.Stat(arg); err != nil {
		app, rest, _ := strings.Cut(filepath.ToSlash(arg), "/")
		if store.CheckApp(app) == nil && (rest == "" || store.CheckPath(rest) == nil) {
			path = filepath.Join(DefaultAppsDir(), app, filepath.FromSlash(rest))
		}
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, ".wb.js"):
		name = strings.TrimSuffix(base, ".wb.js")
	case base == "app.js":
		name = filepath.Base(filepath.Dir(path))
	case strings.HasSuffix(base, ".js"):
		name = strings.TrimSuffix(base, ".js")
	default:
		name = base
	}
	return path, name
}

func openSummary(name string, info json.RawMessage, rec *timing.Recorder) string {
	var i struct {
		ID    string `json:"id"`
		Build struct {
			MS    float64 `json:"ms"`
			Nodes []struct {
				Stored bool `json:"stored"`
			} `json:"nodes"`
		} `json:"build"`
		Stats struct {
			Chunks  int      `json:"chunks"`
			Indexes []string `json:"indexes"`
			WarmMS  float64  `json:"warmMs"`
		} `json:"stats"`
	}
	_ = json.Unmarshal(info, &i)
	stored := 0
	for _, n := range i.Build.Nodes {
		if n.Stored {
			stored++
		}
	}
	var open float64
	for _, e := range rec.Entries() {
		if e.Kind == "phase" {
			open = e.MS
			break
		}
	}
	return fmt.Sprintf("%s: plan %s · %d chunks · indexes %s · build %.0f ms (%d/%d nodes from the store) · artifact load %.0f ms · open %.2f s",
		name, i.ID, i.Stats.Chunks, strings.Join(i.Stats.Indexes, ","), i.Build.MS, stored, len(i.Build.Nodes), i.Stats.WarmMS, open/1000)
}

// sourceTTL: without --watch the source snapshot taken at open stays valid for the
// life of the process; with it, a query re-walks the tree at most once per interval.
func sourceTTL(watch time.Duration) time.Duration {
	if watch <= 0 {
		return 100 * 365 * 24 * time.Hour
	}
	return watch
}
