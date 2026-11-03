package cmds

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-go-golems/rag-workbench/pkg/server"
	"github.com/go-go-golems/rag-workbench/pkg/store"
)

// NewWorkbenchCommand serves the RAG workbench page with a local window.claude runtime.
func NewWorkbenchCommand() *cobra.Command {
	var addr, presets, corpus, dbPath, appsDir string
	cmd := &cobra.Command{
		Use:   "workbench",
		Short: "Serve the RAG workbench (editor, presets, inspector, agent) with a local window.claude runtime",
		Long: "Serves the ported claude.ai workbench. sample() runs on the --llm backend (tools still execute in the page),\n" +
			"db is a local SQLite document store, presets and the bundled corpus come from directories on disk.\n" +
			"Example: ragwb --llm lunaroute workbench --addr 127.0.0.1:8790",
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := LLM()
			if err != nil {
				return err
			}
			if dbPath == "" {
				dbPath = DefaultDBPath()
			}
			docs, err := server.OpenDocStore(dbPath)
			if err != nil {
				return err
			}
			defer docs.Close()
			if appsDir == "" {
				appsDir = DefaultAppsDir()
			}
			apps, err := store.OpenApps(appsDir, dbPath)
			if err != nil {
				return err
			}
			defer apps.Close()
			s := server.New(server.Config{LLM: backend, Docs: docs, PresetsDir: presets, CorpusDir: corpus, Apps: apps})
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
			llmName := "none (LLM calls disabled; pass --llm lunaroute)"
			if backend != nil {
				llmName = fmt.Sprintf("%T", backend)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ragwb workbench on http://%s  (llm: %s, db: %s, apps: %s)\n", ln.Addr(), llmName, dbPath, apps.Dir)
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
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8790", "Listen address (keep it on loopback: there is no auth)")
	cmd.Flags().StringVar(&presets, "presets", "examples/presets", "Directory with presets/index.json and preset scripts")
	cmd.Flags().StringVar(&corpus, "corpus", "examples/apps/reports/corpus", "Directory of .md files served as the bundled corpus")
	cmd.Flags().StringVar(&appsDir, "apps", "", "Apps directory for saved scripts (default $RAGWB_APPS or ~/.ragwb/apps); each app is a directory ragwb verbs/run can execute")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite document store for the page's db capability (default ~/.ragwb/ragwb.db)")
	return cmd
}
