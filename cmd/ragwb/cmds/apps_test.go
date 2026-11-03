package cmds

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverVerbsDirApp(t *testing.T) {
	t.Setenv("RAGWB_APPS", "/srv/apps")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"verbs", "--dir", "examples/apps/reports", "list"}, "examples/apps/reports"},
		{[]string{"verbs", "--app", "notes", "list"}, "/srv/apps/notes"},
		{[]string{"--llm", "fake", "verbs", "--app=notes", "notes", "search", "q"}, "/srv/apps/notes"},
		{[]string{"verbs", "-a", "notes"}, "/srv/apps/notes"},
		{[]string{"run", "--app", "notes"}, ""}, // only after "verbs"
	} {
		if got, _ := DiscoverVerbsDir(tc.args); got != tc.want {
			t.Errorf("DiscoverVerbsDir(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestResolveScript(t *testing.T) {
	apps := t.TempDir()
	t.Setenv("RAGWB_APPS", apps)
	if err := os.MkdirAll(filepath.Join(apps, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "x.js")
	_ = os.WriteFile(local, []byte("return {}"), 0o644)
	for _, tc := range []struct{ arg, want string }{
		{local, local}, // an existing path wins
		{"notes/pipeline.wb.js", filepath.Join(apps, "notes", "pipeline.wb.js")},
		{"notes/sub/x.wb.js", filepath.Join(apps, "notes", "sub", "x.wb.js")},
		{"../etc/x.js", "../etc/x.js"},       // not an app path: left alone
		{"notes/../x.js", "notes/../x.js"},   // unclean: left alone
		{"pipeline.wb.js", "pipeline.wb.js"}, // no app part
	} {
		if got := resolveScript(tc.arg); got != tc.want {
			t.Errorf("resolveScript(%q) = %q, want %q", tc.arg, got, tc.want)
		}
	}
}
