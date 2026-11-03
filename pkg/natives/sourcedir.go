// Package natives implements Go-backed rag components and installs them into
// the ragwb module with rag.define, so the IR, inspector, ops and agent treat
// them like any other component.
package natives

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dop251/goja"
)

// SourceDir is the `source.dir` build component: a recursive directory source.
//
//	rag.source.dir({ root, include?: ['**/*.md'], exclude?: [...], maxBytes?: 4<<20, skipHtml?: true })
//
// It produces the prototype's file records ({name, text}) plus path, size and
// mtime. name is the slash-separated path relative to root, so chunk ids stay
// unique across folders. Its node digest folds in (relpath, size, mtime) of
// every matched file, so edits invalidate the build without hashing contents.
type SourceDir struct {
	// Roots are the directories scripts may read. A root outside all of them is
	// rejected. Empty means "no filesystem access".
	Roots []string
	// Base resolves relative roots (the app directory for jsverbs, else the
	// working directory). Module __dirname is virtual under jsverbs' overlay
	// loader, so scripts should write root: "corpus", not __dirname + "/corpus".
	Base string
}

// DefaultExclude is always applied in addition to the caller's excludes.
var DefaultExclude = []string{".git/**", ".obsidian/**", "node_modules/**", ".trash/**"}

const defaultMaxBytes = 4 << 20

func (s *SourceDir) Name() string { return "source.dir" }

type dirParams struct {
	root     string
	include  []string
	exclude  []string
	maxBytes int64
	skipHTML bool
}

// FileRecord is one matched file.
type FileRecord struct {
	Name  string
	Path  string
	Text  string
	Size  int64
	Mtime int64 // unix milliseconds
}

// Skipped describes a matched file that was not returned.
type Skipped struct {
	Name   string
	Reason string
}

func (s *SourceDir) params(p map[string]any) (dirParams, error) {
	dp := dirParams{maxBytes: defaultMaxBytes, skipHTML: true}
	root, _ := p["root"].(string)
	if strings.TrimSpace(root) == "" {
		return dp, fmt.Errorf("source.dir: root is required")
	}
	abs, err := s.resolveRoot(root)
	if err != nil {
		return dp, err
	}
	dp.root = abs
	dp.include = stringList(p["include"])
	if len(dp.include) == 0 {
		dp.include = []string{"**/*.md"}
	}
	dp.exclude = append(append([]string{}, DefaultExclude...), stringList(p["exclude"])...)
	for _, pat := range append(append([]string{}, dp.include...), dp.exclude...) {
		if !doublestar.ValidatePattern(pat) {
			return dp, fmt.Errorf("source.dir: invalid glob %q", pat)
		}
	}
	switch v := p["maxBytes"].(type) {
	case int64:
		dp.maxBytes = v
	case float64:
		dp.maxBytes = int64(v)
	}
	if v, ok := p["skipHtml"].(bool); ok {
		dp.skipHTML = v
	}
	return dp, nil
}

func (s *SourceDir) resolveRoot(root string) (string, error) {
	if strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, root[2:])
	}
	if !filepath.IsAbs(root) && s.Base != "" {
		root = filepath.Join(s.Base, root)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("source.dir: %w", err)
	}
	for _, allowed := range s.Roots {
		// Allowlist entries may be relative (e.g. a verbs --dir); compare absolute, symlink-free paths.
		a, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}
		a, err = filepath.EvalSymlinks(a)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(a, real); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return real, nil
		}
	}
	return "", fmt.Errorf("source.dir: root %q is outside the allowed roots %v (pass --root)", real, s.Roots)
}

// matches walks the tree and calls fn for every included, non-excluded regular file.
func matches(dp dirParams, fn func(rel, path string, info fs.FileInfo) error) error {
	return filepath.WalkDir(dp.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dp.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			for _, pat := range dp.exclude {
				if ok, _ := doublestar.Match(pat, rel+"/"); ok {
					return filepath.SkipDir
				}
				if ok, _ := doublestar.Match(strings.TrimSuffix(pat, "/**"), rel); ok && strings.HasSuffix(pat, "/**") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // symlinks and devices are not followed
		}
		for _, pat := range dp.exclude {
			if ok, _ := doublestar.Match(pat, rel); ok {
				return nil
			}
		}
		included := false
		for _, pat := range dp.include {
			if ok, _ := doublestar.Match(pat, rel); ok {
				included = true
				break
			}
		}
		if !included {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(rel, path, info)
	})
}

// Walk returns matched files (sorted by name) and the matched-but-skipped ones.
func (s *SourceDir) Walk(p map[string]any) ([]FileRecord, []Skipped, error) {
	dp, err := s.params(p)
	if err != nil {
		return nil, nil, err
	}
	files := []FileRecord{}
	skipped := []Skipped{}
	err = matches(dp, func(rel, path string, info fs.FileInfo) error {
		if dp.maxBytes > 0 && info.Size() > dp.maxBytes {
			skipped = append(skipped, Skipped{rel, fmt.Sprintf("larger than maxBytes (%d > %d)", info.Size(), dp.maxBytes)})
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := strings.ToValidUTF8(string(b), "�")
		if dp.skipHTML && looksLikeHTML(text) {
			skipped = append(skipped, Skipped{rel, "HTML document with a text extension"})
			return nil
		}
		files = append(files, FileRecord{Name: rel, Path: path, Text: text, Size: info.Size(), Mtime: info.ModTime().UnixMilli()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, skipped, err
}

// Digest hashes (relpath, size, mtime) of every matched file plus the resolved params.
func (s *SourceDir) Digest(p map[string]any) (string, error) {
	dp, err := s.params(p)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%v\x00%v\x00%d\x00%v\n", dp.root, dp.include, dp.exclude, dp.maxBytes, dp.skipHTML)
	lines := []string{}
	err = matches(dp, func(rel, _ string, info fs.FileInfo) error {
		lines = append(lines, fmt.Sprintf("%s\x00%d\x00%d", rel, info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:24], nil
}

func looksLikeHTML(text string) bool {
	head := strings.ToLower(strings.TrimSpace(text))
	if len(head) > 64 {
		head = head[:64]
	}
	return strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html")
}

func stringList(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return x
	}
	return nil
}

// sourceDirJS registers the component. Params are exported to Go maps by goja;
// the component's digest hook (see PROVENANCE.md change 2) keeps cached builds
// honest when files change.
const sourceDirJS = `(function (rag, h) {
  rag.define('source.dir', {
    phase: 'build', in: [], out: 'files',
    args: o => (typeof o === 'string' ? { root: o } : { ...(o || {}) }),
    describe: p => p.root + ' · ' + [].concat(p.include || ['**/*.md']).join(', '),
    digest: p => h.digest(p),
    all: (inputs, ctx, p) => {
      const r = h.walk(p);
      if (r.skipped.length) ctx.log('skipped ' + r.skipped.length + ' file(s): ' + r.skipped.slice(0, 5).map(s => s.name + ' (' + s.reason + ')').join('; '));
      return r.files;
    },
    // artifact store: nothing is written; a stored marker makes the files a lazy
    // re-read, valid because the digest covers every file's path, size and mtime.
    // A run whose downstream nodes are all stored never reads the source.
    persist: { save: () => 0, load: (_, p) => h.walk(p).files },
  });
})`

// Install implements ragjs.Native.
func (s *SourceDir) Install(vm *goja.Runtime, rag *goja.Object) error {
	h := vm.NewObject()
	_ = h.Set("walk", func(p map[string]any) (map[string]any, error) {
		files, skipped, err := s.Walk(p)
		if err != nil {
			return nil, err
		}
		fs := make([]any, len(files))
		for i, f := range files {
			fs[i] = map[string]any{"name": f.Name, "path": f.Path, "text": f.Text, "size": f.Size, "mtime": f.Mtime}
		}
		sk := make([]any, len(skipped))
		for i, x := range skipped {
			sk[i] = map[string]any{"name": x.Name, "reason": x.Reason}
		}
		return map[string]any{"files": fs, "skipped": sk}, nil
	})
	_ = h.Set("digest", s.Digest)
	v, err := vm.RunString(sourceDirJS)
	if err != nil {
		return err
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		return fmt.Errorf("source.dir installer is not a function")
	}
	_, err = fn(goja.Undefined(), rag, h)
	return err
}
