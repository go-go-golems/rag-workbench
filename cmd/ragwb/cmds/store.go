package cmds

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-go-golems/glazed/pkg/cli"
	glazedcmds "github.com/go-go-golems/glazed/pkg/cmds"
	"github.com/go-go-golems/glazed/pkg/cmds/schema"
	"github.com/go-go-golems/glazed/pkg/cmds/values"
	"github.com/go-go-golems/glazed/pkg/middlewares"
	"github.com/go-go-golems/glazed/pkg/types"
	"github.com/spf13/cobra"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
	"github.com/go-go-golems/rag-workbench/pkg/natives"
	"github.com/go-go-golems/rag-workbench/pkg/runner"
)

var (
	storeDir string
	noStore  bool
)

func addStoreFlags(f interface {
	StringVar(*string, string, string, string)
	BoolVar(*bool, string, bool, string)
}) {
	f.StringVar(&storeDir, "store", "", "Artifact store for persisted build outputs (default $RAGWB_STORE or ~/.ragwb/store)")
	f.BoolVar(&noStore, "no-store", false, "Build everything in memory; do not load or save persisted build outputs")
}

// DefaultStoreDir is $RAGWB_STORE, else ~/.ragwb/store.
func DefaultStoreDir() string {
	if d := os.Getenv("RAGWB_STORE"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ragwb", "store")
}

func storeRoot() string {
	if storeDir != "" {
		return storeDir
	}
	return DefaultStoreDir()
}

// Artifacts opens this binary's namespace of the artifact store (nil with --no-store).
func Artifacts() (*artifacts.Store, error) {
	if noStore {
		return nil, nil
	}
	return artifacts.Open(storeRoot(), runner.ArtifactNamespace(jsComponents, indexBackend))
}

// NewStoreCommand is `ragwb store`: inspect and prune persisted build outputs.
func NewStoreCommand() (*cobra.Command, error) {
	storeCmd := &cobra.Command{
		Use:   "store",
		Short: "Persisted build outputs (docs, chunks, BM25 indexes) keyed by digest",
		Long: "Builds save their docs, chunks and BM25 indexes under --store, keyed by the node digest (component type, params,\n" +
			"inputs, source-file stats and the component's code). A later run over an unchanged corpus loads them instead of\n" +
			"rebuilding. Outputs live in a namespace per ragwb version; `prune` removes older namespaces.",
	}
	list := &storeList{glazedcmds.NewCommandDescription("list", glazedcmds.WithShort("List stored outputs (all namespaces); the current one is marked"))}
	if err := cli.AddCommandsToRootCommand(storeCmd, []glazedcmds.Command{list}, nil, cli.WithParserConfig(cli.CobraParserConfig{
		ShortHelpSections: []string{schema.DefaultSlug},
		MiddlewaresFunc:   cli.CobraCommandDefaultMiddlewares,
	})); err != nil {
		return nil, err
	}
	var all bool
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Delete namespaces of other ragwb versions (--all: everything)",
		RunE: func(cmd *cobra.Command, args []string) error {
			keep := runner.ArtifactNamespace(jsComponents, indexBackend)
			if all {
				keep = ""
			}
			n, err := artifacts.Prune(storeRoot(), keep)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %d namespace(s) from %s\n", n, storeRoot())
			return nil
		},
	}
	prune.Flags().BoolVar(&all, "all", false, "Also delete the current namespace")
	storeCmd.AddCommand(prune)
	var keep int
	gc := &cobra.Command{
		Use:   "gc",
		Short: "Delete superseded versions in the current namespace (keep the newest --keep per pipeline node)",
		Long: "Delta builds (RAGWB-003) store a new version of every changed node and remember the versions per node\n" +
			"(its lineage). gc keeps the newest --keep of each and deletes the rest; files a kept version shares through\n" +
			"hard links stay. Run it while no ragwb serve uses an old version, or keep at least 2.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := artifacts.Open(storeRoot(), runner.ArtifactNamespace(jsComponents, indexBackend))
			if err != nil {
				return err
			}
			r, err := s.GC(keep)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "gc %s: %d lineage(s), removed %d output(s), freed %.1f MB, %d abandoned temp dir(s)\n",
				s.NS, r.Lineages, r.Removed, float64(r.Freed)/(1<<20), r.Temps)
			return nil
		},
	}
	gc.Flags().IntVar(&keep, "keep", 3, "Versions to keep per pipeline node")
	storeCmd.AddCommand(gc)
	verify := &cobra.Command{
		Use:   "verify",
		Short: "Open every stored output of the current namespace and check it against its metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := artifacts.Open(storeRoot(), runner.ArtifactNamespace(jsComponents, indexBackend))
			if err != nil {
				return err
			}
			n, problems, err := natives.VerifyStore(s)
			if err != nil {
				return err
			}
			for _, p := range problems {
				fmt.Fprintln(cmd.OutOrStdout(), p)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "verify %s: %d output(s), %d problem(s)\n", s.NS, n, len(problems))
			if len(problems) > 0 {
				return fmt.Errorf("%d stored output(s) failed verification; delete them with `ragwb store prune --all` or rebuild with --full", len(problems))
			}
			return nil
		},
	}
	storeCmd.AddCommand(verify)
	return storeCmd, nil
}

type storeList struct{ *glazedcmds.CommandDescription }

func (c *storeList) RunIntoGlazeProcessor(ctx context.Context, _ *values.Values, gp middlewares.Processor) error {
	ms, err := artifacts.List(storeRoot())
	if err != nil {
		return err
	}
	cur := runner.ArtifactNamespace(jsComponents, indexBackend)
	for _, m := range ms {
		if err := gp.AddRow(ctx, types.NewRow(types.MRP("ns", m.NS), types.MRP("current", m.NS == cur), types.MRP("type", m.Type), types.MRP("codec", m.Codec),
			types.MRP("count", m.Count), types.MRP("mb", float64(m.Size*10/(1<<20))/10), types.MRP("created", m.Created.Format("2006-01-02 15:04:05")), types.MRP("key", m.Key[:12]))); err != nil {
			return err
		}
	}
	return nil
}
