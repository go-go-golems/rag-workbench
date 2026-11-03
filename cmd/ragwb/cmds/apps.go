package cmds

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-go-golems/glazed/pkg/cli"
	glazedcmds "github.com/go-go-golems/glazed/pkg/cmds"
	"github.com/go-go-golems/glazed/pkg/cmds/fields"
	"github.com/go-go-golems/glazed/pkg/cmds/schema"
	"github.com/go-go-golems/glazed/pkg/cmds/values"
	"github.com/go-go-golems/glazed/pkg/middlewares"
	"github.com/go-go-golems/glazed/pkg/types"
	"github.com/spf13/cobra"

	"github.com/go-go-golems/rag-workbench/pkg/store"
)

var appsDirFlag, appsDBFlag string

// DefaultAppsDir is where saved scripts live unless --apps says otherwise:
// $RAGWB_APPS, else ~/.ragwb/apps.
func DefaultAppsDir() string {
	if d := os.Getenv("RAGWB_APPS"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ragwb", "apps")
}

// DefaultDBPath is the SQLite file for history and the page's db capability.
func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ragwb", "ragwb.db")
}

func openApps() (*store.Apps, error) {
	dir, db := appsDirFlag, appsDBFlag
	if dir == "" {
		dir = DefaultAppsDir()
	}
	if db == "" {
		db = DefaultDBPath()
	}
	return store.OpenApps(dir, db)
}

// NewAppsCommand is `ragwb apps`: list saved apps, their files and history.
func NewAppsCommand() (*cobra.Command, error) {
	appsCmd := &cobra.Command{
		Use:   "apps",
		Short: "Saved apps: list apps, files and history (the workbench saves here)",
		Long: "Apps are directories under --apps (default $RAGWB_APPS or ~/.ragwb/apps). A file's role comes from its name:\n" +
			"  *.wb.js   workbench script   ragwb run <app>/<file>\n" +
			"  lib/*.js  library            require(\"./lib/...\")\n" +
			"  *.js      verbs              ragwb verbs --app <app> <file> <verb>",
	}
	appsCmd.PersistentFlags().StringVar(&appsDirFlag, "apps", "", "Apps directory (default $RAGWB_APPS or ~/.ragwb/apps)")
	appsCmd.PersistentFlags().StringVar(&appsDBFlag, "db", "", "SQLite file holding the history (default ~/.ragwb/ragwb.db)")

	appArg := fields.New("app", fields.TypeString, fields.WithIsArgument(true), fields.WithRequired(true), fields.WithHelp("App name"))
	pathArg := fields.New("path", fields.TypeString, fields.WithIsArgument(true), fields.WithHelp("File path inside the app (all files if omitted)"))
	all := []glazedcmds.Command{
		&appsList{glazedcmds.NewCommandDescription("list", glazedcmds.WithShort("List apps"))},
		&appsFiles{glazedcmds.NewCommandDescription("files", glazedcmds.WithShort("List an app's files with their role and how to run them"), glazedcmds.WithArguments(appArg))},
		&appsHistory{glazedcmds.NewCommandDescription("history", glazedcmds.WithShort("Saved versions of an app or one file, newest first"), glazedcmds.WithArguments(appArg, pathArg))},
	}
	if err := cli.AddCommandsToRootCommand(appsCmd, all, nil, cli.WithParserConfig(cli.CobraParserConfig{
		ShortHelpSections: []string{schema.DefaultSlug},
		MiddlewaresFunc:   cli.CobraCommandDefaultMiddlewares,
	})); err != nil {
		return nil, err
	}
	var version int
	show := &cobra.Command{
		Use:   "show <app> <path>",
		Short: "Print a file (or an older version with --version)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApps()
			if err != nil {
				return err
			}
			defer a.Close()
			var b []byte
			if version > 0 {
				b, err = a.ReadVersion(args[0], args[1], version)
			} else {
				b, err = a.Read(args[0], args[1])
			}
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
	show.Flags().IntVar(&version, "version", 0, "History version to print")
	appsCmd.AddCommand(show)
	return appsCmd, nil
}

type appsList struct{ *glazedcmds.CommandDescription }

func (c *appsList) RunIntoGlazeProcessor(ctx context.Context, _ *values.Values, gp middlewares.Processor) error {
	a, err := openApps()
	if err != nil {
		return err
	}
	defer a.Close()
	apps, err := a.ListApps()
	if err != nil {
		return err
	}
	for _, app := range apps {
		if err := gp.AddRow(ctx, types.NewRow(types.MRP("app", app.Name), types.MRP("files", app.Files), types.MRP("updated", app.Updated.Format("2006-01-02 15:04")),
			types.MRP("dir", filepath.Join(a.Dir, app.Name)))); err != nil {
			return err
		}
	}
	return nil
}

type appsFiles struct{ *glazedcmds.CommandDescription }

type appArgs struct {
	App  string `glazed:"app"`
	Path string `glazed:"path"`
}

func (c *appsFiles) RunIntoGlazeProcessor(ctx context.Context, vals *values.Values, gp middlewares.Processor) error {
	var in appArgs
	if err := vals.DecodeSectionInto(schema.DefaultSlug, &in); err != nil {
		return err
	}
	a, err := openApps()
	if err != nil {
		return err
	}
	defer a.Close()
	files, err := a.Files(in.App)
	if err != nil {
		return err
	}
	for _, f := range files {
		run := ""
		switch f.Role {
		case "workbench":
			run = fmt.Sprintf("ragwb run %s/%s", in.App, f.Path)
		case "verbs":
			run = fmt.Sprintf("ragwb verbs --app %s list", in.App)
		}
		if err := gp.AddRow(ctx, types.NewRow(types.MRP("path", f.Path), types.MRP("role", f.Role), types.MRP("size", f.Size), types.MRP("version", f.Version),
			types.MRP("updated", f.Updated.Format("2006-01-02 15:04")), types.MRP("run", run))); err != nil {
			return err
		}
	}
	return nil
}

type appsHistory struct{ *glazedcmds.CommandDescription }

func (c *appsHistory) RunIntoGlazeProcessor(ctx context.Context, vals *values.Values, gp middlewares.Processor) error {
	var in appArgs
	if err := vals.DecodeSectionInto(schema.DefaultSlug, &in); err != nil {
		return err
	}
	a, err := openApps()
	if err != nil {
		return err
	}
	defer a.Close()
	vs, err := a.History(in.App, in.Path)
	if err != nil {
		return err
	}
	for _, v := range vs {
		if err := gp.AddRow(ctx, types.NewRow(types.MRP("path", v.Path), types.MRP("version", v.Version), types.MRP("comment", v.Comment), types.MRP("deleted", v.Deleted),
			types.MRP("size", v.Size), types.MRP("created", v.Created.Format("2006-01-02 15:04:05")), types.MRP("sha256", v.SHA256[:12]))); err != nil {
			return err
		}
	}
	return nil
}
