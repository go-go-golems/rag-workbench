package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTest(t *testing.T) *Apps {
	t.Helper()
	d := t.TempDir()
	a, err := OpenApps(filepath.Join(d, "apps"), filepath.Join(d, "ragwb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestSaveReadHistory(t *testing.T) {
	a := openTest(t)
	r1, err := a.Save("notes", "lib/corpus.js", []byte("module.exports = 1;\n"), "first")
	if err != nil {
		t.Fatal(err)
	}
	if r1.Version != 1 || r1.Unchanged {
		t.Fatalf("first save = %+v", r1)
	}
	// identical bytes: no new version
	r2, err := a.Save("notes", "lib/corpus.js", []byte("module.exports = 1;\n"), "again")
	if err != nil || !r2.Unchanged || r2.Version != 1 {
		t.Fatalf("same-bytes save = %+v, %v", r2, err)
	}
	r3, err := a.Save("notes", "lib/corpus.js", []byte("module.exports = 2;\n"), "second")
	if err != nil || r3.Version != 2 {
		t.Fatalf("second save = %+v, %v", r3, err)
	}
	b, err := a.Read("notes", "lib/corpus.js")
	if err != nil || string(b) != "module.exports = 2;\n" {
		t.Fatalf("read = %q, %v", b, err)
	}
	// on disk where jsverbs will find it
	disk, _ := os.ReadFile(filepath.Join(a.Dir, "notes", "lib", "corpus.js"))
	if string(disk) != "module.exports = 2;\n" {
		t.Fatalf("disk = %q", disk)
	}
	vs, err := a.History("notes", "lib/corpus.js")
	if err != nil || len(vs) != 2 || vs[0].Version != 2 || vs[1].Comment != "first" {
		t.Fatalf("history = %+v, %v", vs, err)
	}
	old, err := a.ReadVersion("notes", "lib/corpus.js", 1)
	if err != nil || string(old) != "module.exports = 1;\n" {
		t.Fatalf("version 1 = %q, %v", old, err)
	}
	if _, err := a.ReadVersion("notes", "lib/corpus.js", 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing version err = %v", err)
	}
}

func TestExternalEditIsKeptBeforeOverwrite(t *testing.T) {
	a := openTest(t)
	if _, err := a.Save("x", "search.js", []byte("v1"), ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Dir, "x", "search.js"), []byte("edited in vim"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := a.Save("x", "search.js", []byte("v3 from the agent"), "agent")
	if err != nil || r.Version != 3 {
		t.Fatalf("save = %+v, %v", r, err)
	}
	vs, _ := a.History("x", "search.js")
	if len(vs) != 3 || vs[1].Comment != "changed on disk" {
		t.Fatalf("history = %+v", vs)
	}
	b, _ := a.ReadVersion("x", "search.js", 2)
	if string(b) != "edited in vim" {
		t.Fatalf("version 2 = %q", b)
	}
	// a file that existed before the store ever saw it is recorded too
	_ = os.WriteFile(filepath.Join(a.Dir, "x", "evals.js"), []byte("handwritten"), 0o644)
	r, _ = a.Save("x", "evals.js", []byte("replaced"), "")
	if r.Version != 2 {
		t.Fatalf("pre-existing file save = %+v", r)
	}
}

func TestDeleteKeepsHistory(t *testing.T) {
	a := openTest(t)
	_, _ = a.Save("x", "a.wb.js", []byte("return {}"), "")
	if err := a.Delete("x", "a.wb.js", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Read("x", "a.wb.js"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read after delete err = %v", err)
	}
	vs, _ := a.History("x", "a.wb.js")
	if len(vs) != 2 || !vs[0].Deleted || vs[0].Comment != "deleted" {
		t.Fatalf("history = %+v", vs)
	}
	// re-creating after a delete is a new version, not "unchanged"
	r, _ := a.Save("x", "a.wb.js", []byte("return {}"), "back")
	if r.Unchanged || r.Version != 3 {
		t.Fatalf("re-save = %+v", r)
	}
	if err := a.Delete("x", "nope.js", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing err = %v", err)
	}
}

func TestListAppsAndFiles(t *testing.T) {
	a := openTest(t)
	for _, p := range []string{"pipeline.wb.js", "search.js", "lib/corpus.js", "README.md", "goldens.json"} {
		if _, err := a.Save("notes", p, []byte("x"), ""); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(a.Dir, "notes", ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(a.Dir, "notes", ".git", "HEAD.js"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(a.Dir, "notes", "binary.exe"), []byte("x"), 0o644)
	_ = os.MkdirAll(filepath.Join(a.Dir, "not a valid name"), 0o755)
	fs, err := a.Files("notes")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		got = append(got, f.Path+":"+f.Role)
	}
	want := "README.md:doc goldens.json:data lib/corpus.js:library pipeline.wb.js:workbench search.js:verbs"
	if strings.Join(got, " ") != want {
		t.Fatalf("files = %v\nwant    %s", got, want)
	}
	apps, err := a.ListApps()
	if err != nil || len(apps) != 1 || apps[0].Name != "notes" || apps[0].Files != 5 {
		t.Fatalf("apps = %+v, %v", apps, err)
	}
	if _, err := a.Files("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("files of missing app err = %v", err)
	}
}

func TestPathSafety(t *testing.T) {
	a := openTest(t)
	for _, app := range []string{"", ".", "..", "../x", "a/b", "-x", strings.Repeat("a", 65)} {
		if _, err := a.Save(app, "x.js", []byte("x"), ""); err == nil {
			t.Errorf("app %q accepted", app)
		}
	}
	for _, p := range []string{"", "/etc/passwd.js", "../x.js", "a/../../x.js", "a//b.js", "./x.js", ".hidden.js", "lib/.x.js", "x.sh", "a\\b.js", "x"} {
		if _, err := a.Save("ok", p, []byte("x"), ""); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
	if _, err := a.Save("ok", "big.js", make([]byte, MaxFileBytes+1), ""); err == nil {
		t.Error("oversized file accepted")
	}
	// a symlink inside the app that points outside cannot be written through
	outside := t.TempDir()
	if _, err := a.Save("ok", "fine.js", []byte("x"), ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(a.Dir, "ok", "lib")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save("ok", "lib/escape.js", []byte("x"), ""); err == nil {
		t.Error("write through an escaping symlink accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "escape.js")); err == nil {
		t.Error("file was written outside the app directory")
	}
}
