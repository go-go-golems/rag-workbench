// ragwb is the RAG workbench CLI: run workbench scripts headlessly, run app
// directories as jsverbs commands, and (later phases) serve the workbench and apps.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-go-golems/rag-workbench/cmd/ragwb/cmds"
	"github.com/go-go-golems/rag-workbench/pkg/timing"
)

var version = "dev"

// processStart is as early as Go code can observe; --timings measures from here.
var processStart = time.Now()

func main() {
	for _, a := range os.Args[1:] {
		if a == "--timings" {
			timing.Default = timing.New(processStart, "startup (Go init, CLI, jsverbs scan)")
		}
	}
	root := &cobra.Command{
		Use:           "ragwb",
		Short:         "RAG workbench: JS pipelines, queries and apps on a Go runtime",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run:   func(cmd *cobra.Command, args []string) { fmt.Fprintln(cmd.OutOrStdout(), version) },
	})
	cmds.AddGlobalFlags(root)
	root.AddCommand(cmds.NewRunCommand(), cmds.NewPresetsCommand(), cmds.NewWorkbenchCommand())
	apps, err := cmds.NewAppsCommand()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	root.AddCommand(apps)
	store, err := cmds.NewStoreCommand()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	root.AddCommand(store)

	dir, _ := cmds.DiscoverVerbsDir(os.Args[1:])
	verbs, err := cmds.NewVerbsCommand(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ragwb:", err)
		os.Exit(1)
	}
	root.AddCommand(verbs)

	err = root.Execute()
	timing.Default.Print(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ragwb:", err)
		os.Exit(1)
	}
}
