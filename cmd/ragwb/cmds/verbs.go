package cmds

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-go-golems/glazed/pkg/cli"
	glazedcmds "github.com/go-go-golems/glazed/pkg/cmds"
	"github.com/go-go-golems/glazed/pkg/cmds/schema"
	"github.com/go-go-golems/glazed/pkg/cmds/values"
	"github.com/go-go-golems/glazed/pkg/middlewares"
	"github.com/go-go-golems/glazed/pkg/types"
	"github.com/go-go-golems/go-go-goja/pkg/engine"
	"github.com/go-go-golems/go-go-goja/pkg/jsverbs"
	"github.com/spf13/cobra"

	"github.com/go-go-golems/rag-workbench/pkg/runner"
)

// DiscoverVerbsDir finds the --dir value for the verbs command in raw argv.
// jsverbs commands are generated from the scanned directory, so it must be
// known before cobra parses flags (the same approach as jsverbs-example).
func DiscoverVerbsDir(args []string) (string, bool) {
	inVerbs := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "verbs" {
			inVerbs = true
			continue
		}
		if !inVerbs {
			continue
		}
		switch {
		case a == "--dir" || a == "-d":
			if i+1 < len(args) {
				return args[i+1], true
			}
		case strings.HasPrefix(a, "--dir="):
			return strings.TrimPrefix(a, "--dir="), true
		}
	}
	return "", inVerbs
}

// NewVerbsCommand scans dir for JS verbs and mounts them under "verbs".
// Every verb runs in a fresh runtime that has ragwb, ragwb/gui, ragwb/kit and the
// default go-go-goja modules (minus exec), plus jsverbs' scanned-source loader.
func NewVerbsCommand(dir string) (*cobra.Command, error) {
	verbsCmd := &cobra.Command{
		Use:   "verbs",
		Short: "Run JS functions from an app directory as commands (jsverbs)",
		Long: "Scan --dir for .js files; each top-level function (or __verb__) becomes a command whose result is printed as rows.\n" +
			"Example: ragwb verbs --dir examples/apps/reports reports search \"rebalance law\"",
	}
	verbsCmd.PersistentFlags().StringP("dir", "d", dir, "App directory to scan for verbs")
	if dir == "" {
		verbsCmd.RunE = func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("pass --dir <app directory>")
		}
		return verbsCmd, nil
	}
	registry, err := jsverbs.ScanDir(dir)
	if err != nil {
		return nil, err
	}
	invoker := func(ctx context.Context, reg *jsverbs.Registry, verb *jsverbs.VerbSpec, parsed *values.Values) (interface{}, error) {
		factory, err := runner.NewFactory(runner.Config{SourceLoader: reg.RequireLoader(), Roots: Roots(dir), Base: dir, JSComponents: JSComponents()})
		if err != nil {
			return nil, err
		}
		rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
		if err != nil {
			return nil, err
		}
		defer func() { _ = rt.Close(context.Background()) }()
		return reg.InvokeInRuntime(ctx, rt, verb, parsed)
	}
	commands, err := registry.CommandsWithInvoker(invoker)
	if err != nil {
		return nil, err
	}
	list := &listCommand{
		CommandDescription: glazedcmds.NewCommandDescription("list", glazedcmds.WithShort("List discovered verbs")),
		registry:           registry,
	}
	all := append([]glazedcmds.Command{list}, commands...)
	if err := cli.AddCommandsToRootCommand(verbsCmd, all, nil, cli.WithParserConfig(cli.CobraParserConfig{
		ShortHelpSections: []string{schema.DefaultSlug},
		MiddlewaresFunc:   cli.CobraCommandDefaultMiddlewares,
	})); err != nil {
		return nil, err
	}
	return verbsCmd, nil
}

type listCommand struct {
	*glazedcmds.CommandDescription
	registry *jsverbs.Registry
}

func (c *listCommand) RunIntoGlazeProcessor(ctx context.Context, _ *values.Values, gp middlewares.Processor) error {
	for _, verb := range c.registry.Verbs() {
		row := types.NewRow(
			types.MRP("path", verb.FullPath()),
			types.MRP("source", verb.SourceRef()),
			types.MRP("output_mode", verb.OutputMode),
		)
		if err := gp.AddRow(ctx, row); err != nil {
			return err
		}
	}
	return nil
}
