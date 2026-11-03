package cmds

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-go-golems/rag-workbench/pkg/runner"
	"github.com/go-go-golems/rag-workbench/pkg/store"
)

type runFlags struct {
	files   string
	q       string
	k       int
	trigger string
	evals   bool
	verbose bool
	timeout time.Duration
}

func (f *runFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.files, "files", "", "Directory of .md/.markdown/.txt files served to rag.files(...) sources")
	cmd.Flags().StringVar(&f.q, "q", "", "Query signal (default: the plan's own q)")
	cmd.Flags().IntVar(&f.k, "k", 5, "Number of hits to report")
	cmd.Flags().StringVar(&f.trigger, "trigger", "change", "Run trigger: change (lexical nodes only) or submit (also LLM nodes)")
	cmd.Flags().BoolVar(&f.evals, "evals", false, "Also run the script's evals")
	cmd.Flags().BoolVarP(&f.verbose, "verbose", "v", false, "Print rag engine log lines to stderr")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 5*time.Minute, "Abort the run after this long")
}

func (f *runFlags) options() (runner.ScriptOptions, error) {
	opts := runner.ScriptOptions{Trigger: f.trigger, Evals: f.evals, TopK: f.k}
	if f.files != "" {
		files, err := runner.ReadFlatCorpus(f.files)
		if err != nil {
			return opts, err
		}
		opts.Files = files
	}
	if f.q != "" {
		opts.Signals = map[string]any{"q": f.q}
	}
	return opts, nil
}

// NewRunCommand runs one workbench-form script (a preset or pipeline.js) headlessly.
func NewRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "run <script.js | app/file.wb.js>",
		Short: "Run a workbench script (async body returning {plan, evals, app}) and print a JSON report",
		Long: "Runs a workbench-form script headlessly. The argument is a file path, or <app>/<file> for a script saved\n" +
			"from the workbench (resolved in $RAGWB_APPS or ~/.ragwb/apps). Relative source.dir roots resolve against the\n" +
			"script's directory, which is also allowlisted.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := resolveScript(args[0])
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			opts, err := f.options()
			if err != nil {
				return err
			}
			backend, err := LLM()
			if err != nil {
				return err
			}
			embedder, embedCache, err := Embedder()
			if err != nil {
				return err
			}
			store, err := Artifacts()
			if err != nil {
				return err
			}
			base := filepath.Dir(path)
			factory, err := runner.NewFactory(runner.Config{Verbose: f.verbose, Roots: Roots(base), Base: base, JSComponents: JSComponents(), LLM: backend, BuildCalls: BuildCalls(), Embedder: embedder, EmbedCache: embedCache, Artifacts: store})
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), f.timeout)
			defer cancel()
			rep, err := runner.RunWorkbenchScript(ctx, factory, path, string(src), opts)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(rep)
		},
	}
	f.bind(cmd)
	return cmd
}

// resolveScript maps <app>/<file> to the apps directory when it is not an existing path.
func resolveScript(arg string) string {
	if _, err := os.Stat(arg); err == nil {
		return arg
	}
	app, rest, ok := strings.Cut(filepath.ToSlash(arg), "/")
	if !ok || store.CheckApp(app) != nil || store.CheckPath(rest) != nil {
		return arg
	}
	return filepath.Join(DefaultAppsDir(), app, filepath.FromSlash(rest))
}

// NewPresetsCommand runs every preset in a directory and prints one summary line each:
// a regression sweep that the whole prototype grammar still works headlessly.
func NewPresetsCommand() *cobra.Command {
	f := &runFlags{}
	var dir string
	cmd := &cobra.Command{
		Use:   "presets",
		Short: "Run every preset (*.js) in a directory headlessly and summarize build, query and evals",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := f.options()
			if err != nil {
				return err
			}
			backend, err := LLM()
			if err != nil {
				return err
			}
			embedder, embedCache, err := Embedder()
			if err != nil {
				return err
			}
			store, err := Artifacts()
			if err != nil {
				return err
			}
			factory, err := runner.NewFactory(runner.Config{Verbose: f.verbose, Roots: Roots(), JSComponents: JSComponents(), LLM: backend, BuildCalls: BuildCalls(), Embedder: embedder, EmbedCache: embedCache, Artifacts: store})
			if err != nil {
				return err
			}
			paths, err := filepath.Glob(filepath.Join(dir, "*.js"))
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "PRESET\tBUILD\tNODES\tPENDING\tQUERY_MS\tTOP\tHIT@5\tMRR\tERRORS")
			failed := 0
			for _, p := range paths {
				src, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), f.timeout)
				rep, err := runner.RunWorkbenchScript(ctx, factory, p, string(src), opts)
				cancel()
				name := strings.TrimSuffix(filepath.Base(p), ".js")
				if err != nil {
					failed++
					fmt.Fprintf(tw, "%s\tFAIL\t\t\t\t\t\t\t%s\n", name, oneLine(err.Error(), 120))
					continue
				}
				fmt.Fprintln(tw, summarize(name, rep))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if failed > 0 {
				return fmt.Errorf("%d preset(s) failed", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "examples/presets", "Directory of preset scripts")
	f.bind(cmd)
	_ = cmd.Flags().Set("evals", "true")
	return cmd
}

func summarize(name string, rep *runner.ScriptReport) string {
	var b struct {
		OK     bool       `json:"ok"`
		Errors []string   `json:"errors"`
		Nodes  []struct{} `json:"nodes"`
	}
	var r struct {
		MS     float64  `json:"ms"`
		Errors []string `json:"errors"`
		Top    []struct {
			Doc string `json:"doc"`
		} `json:"top"`
	}
	var ev map[string]any
	var pending []struct{}
	_ = json.Unmarshal(rep.Build, &b)
	_ = json.Unmarshal(rep.Run, &r)
	_ = json.Unmarshal(rep.Evals, &ev)
	_ = json.Unmarshal(rep.Pending, &pending)
	top := ""
	if len(r.Top) > 0 {
		top = oneLine(r.Top[0].Doc, 28)
	}
	errs := append(append([]string{}, b.Errors...), r.Errors...)
	status := "ok"
	if !b.OK {
		status = "ERR"
	}
	return fmt.Sprintf("%s\t%s\t%d\t%d\t%.1f\t%s\t%v\t%v\t%s", name, status, len(b.Nodes), len(pending), r.MS, top,
		fmtMetric(ev["hit@5"]), fmtMetric(ev["mrr"]), oneLine(strings.Join(errs, "; "), 80))
}

func fmtMetric(v any) string {
	if v == nil {
		return "-"
	}
	if f, ok := v.(float64); ok {
		return fmt.Sprintf("%.3f", f)
	}
	return fmt.Sprint(v)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}
