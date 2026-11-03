// ragwb is the RAG workbench CLI: run workbench scripts headlessly, run app
// directories as jsverbs commands, and (later phases) serve the workbench and apps.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/go-go-golems/rag-workbench/cmd/ragwb/cmds"
)

var version = "dev"

func main() {
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
	cmds.AddRootsFlag(root)
	root.AddCommand(cmds.NewRunCommand(), cmds.NewPresetsCommand())

	dir, _ := cmds.DiscoverVerbsDir(os.Args[1:])
	verbs, err := cmds.NewVerbsCommand(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ragwb:", err)
		os.Exit(1)
	}
	root.AddCommand(verbs)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ragwb:", err)
		os.Exit(1)
	}
}
