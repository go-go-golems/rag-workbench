package cmds

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	extraRoots   []string
	jsComponents bool
)

// AddGlobalFlags registers persistent flags shared by run, presets and verbs.
func AddGlobalFlags(root *cobra.Command) {
	AddRootsFlag(root)
	root.PersistentFlags().BoolVar(&jsComponents, "js-components", false,
		"Use rag.js's own parse.markdown, chunk.sections and index.bm25 instead of the Go ports (parity debugging)")
}

// JSComponents reports the --js-components flag.
func JSComponents() bool { return jsComponents }

// AddRootsFlag registers the persistent --root flag (repeatable): directories
// scripts may read through rag.source.dir.
func AddRootsFlag(root *cobra.Command) {
	root.PersistentFlags().StringArrayVar(&extraRoots, "root", nil,
		"Directory that rag.source.dir may read (repeatable; the working directory and the verbs --dir are always allowed)")
}

// Roots returns the allowlist: the working directory, the given dirs, and every --root.
func Roots(dirs ...string) []string {
	out := []string{}
	if wd, err := os.Getwd(); err == nil {
		out = append(out, wd)
	}
	for _, d := range dirs {
		if d != "" {
			out = append(out, d)
		}
	}
	return append(out, extraRoots...)
}
