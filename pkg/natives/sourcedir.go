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
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dop251/goja"
	"github.com/go-go-golems/rag-workbench/pkg/vaultignore"
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
	// DigestTTL reuses a computed digest for this long instead of re-walking the
	// tree on every artifacts() lookup (each query asks). 0 = always re-walk (CLI
	// runs); `ragwb serve` sets it so a keystroke does not stat the whole vault.
	DigestTTL time.Duration

	mu        sync.Mutex
	digests   map[string]cachedDigest
	manifests map[string]*Manifest // latest Walk/Diff per source, for persist.save (manifest.go)
}

type cachedDigest struct {
	at     time.Time
	digest string
}

// DefaultExclude is always applied in addition to the caller's excludes.
var DefaultExclude = []string{".git/**", ".obsidian/**", "node_modules/**", ".trash/**"}

const defaultMaxBytes = 4 << 20

func (s *SourceDir) Name() string { return "source.dir" }

type dirParams struct {
	root        string
	include     []string
	exclude     []string
	maxBytes    int64
	skipHTML    bool
	vaultIgnore bool
	ignore      *vaultignore.Ignore
	ignoreBytes []byte
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
	if enabled, _ := p["vaultIgnore"].(bool); enabled {
		dp.vaultIgnore = true
		ignorePath := filepath.Join(dp.root, ".vault-ignore")
		if info, err := os.Lstat(ignorePath); err == nil && !info.Mode().IsRegular() {
			return dp, fmt.Errorf("source.dir: .vault-ignore must be a regular file")
		} else if err != nil && !os.IsNotExist(err) {
			return dp, err
		}
		b, err := os.ReadFile(ignorePath)
		if err != nil && !os.IsNotExist(err) {
			return dp, err
		}
		dp.ignoreBytes = b
		dp.ignore, err = vaultignore.Parse(strings.NewReader(string(b)))
		if err != nil {
			return dp, fmt.Errorf("source.dir: parse .vault-ignore: %w", err)
		}
	}
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
		if dp.ignore != nil && dp.ignore.Match(rel, false) {
			return nil
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
	entries := []ManifestEntry{}
	err = matches(dp, func(rel, path string, info fs.FileInfo) error {
		f, e, err := readMatched(dp, rel, path, info)
		if err != nil {
			return err
		}
		entries = append(entries, e)
		if f == nil {
			skipped = append(skipped, Skipped{rel, e.Skipped})
			return nil
		}
		files = append(files, *f)
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	if err == nil {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		s.remember(dp, &Manifest{Version: manifestVersion, Params: dp.key(), Entries: entries})
	}
	return files, skipped, err
}

// Digest hashes (relpath, size, mtime) of every matched file plus the resolved params.
func (s *SourceDir) Digest(p map[string]any) (string, error) {
	dp, err := s.params(p)
	if err != nil {
		return "", err
	}
	if s.DigestTTL <= 0 {
		return s.digest(dp)
	}
	key := fmt.Sprintf("%s\x00%v\x00%v\x00%d\x00%v\x00%v\x00%x", dp.root, dp.include, dp.exclude, dp.maxBytes, dp.skipHTML, dp.vaultIgnore, sha256.Sum256(dp.ignoreBytes))
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.digests[key]; ok && time.Since(c.at) < s.DigestTTL {
		return c.digest, nil
	}
	d, err := s.digest(dp)
	if err != nil {
		return "", err
	}
	if s.digests == nil {
		s.digests = map[string]cachedDigest{}
	}
	s.digests[key] = cachedDigest{time.Now(), d}
	return d, nil
}

func (s *SourceDir) digest(dp dirParams) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%v\x00%v\x00%d\x00%v\n", dp.root, dp.include, dp.exclude, dp.maxBytes, dp.skipHTML)
	if dp.vaultIgnore {
		h.Write(dp.ignoreBytes)
	}
	lines := []string{}
	err := matches(dp, func(rel, _ string, info fs.FileInfo) error {
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
    lineage: p => h.lineage(p), // identity without file stats: delta builds find the previous version
    all: (inputs, ctx, p) => {
      const r = h.walk(p);
      if (r.skipped.length) ctx.log('skipped ' + r.skipped.length + ' file(s): ' + r.skipped.slice(0, 5).map(s => s.name + ' (' + s.reason + ')').join('; '));
      return r.files;
    },
    // artifact store: the manifest (every file's size, mtime and hash) is stored, not the files: loading re-reads them lazily, valid because
    // the digest covers every file's path, size and mtime; a run whose downstream
    // nodes are all stored never reads the source. The next build diffs against it.
    persist: { save: (_, p) => h.manifest(p), load: (_, p) => h.walk(p).files },
    // delta build (RAGWB-003 §6.5): diff the tree against the stored manifest;
    // the files are re-read only if a downstream node needs all of them
    delta: {
      apply(base, inputs, ctx, p) {
        const d = h.diff(p, base.raw(), ctx.deltaMaxFraction);
        if (d.full) { ctx.log('full build: ' + d.reason); return null; }
        return {
          lazy: () => h.walk(p).files, count: d.count,
          delta: { full: false, added: d.added, changed: d.changed, removed: d.removed },
          summary: { upserts: d.added.length + d.changed.length, removes: d.removed.length, unchanged: d.unchanged },
        };
      },
    },
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
	_ = h.Set("lineage", s.Lineage)
	// the manifest stays in Go (codec "manifest"): nanosecond mtimes don't survive JS numbers
	_ = h.Set("manifest", func(p map[string]any) (goja.Value, error) {
		m, err := s.LastManifest(p)
		if err != nil {
			return nil, err
		}
		return manifestObject(vm, m), nil
	})
	fileJS := func(f FileRecord) map[string]any {
		return map[string]any{"name": f.Name, "path": f.Path, "text": f.Text, "size": f.Size, "mtime": f.Mtime}
	}
	// diff(p, storedManifest, maxFraction) → {full, reason} | {full: false, added, changed, removed, unchanged, count}
	_ = h.Set("diff", func(p map[string]any, base goja.Value, maxFraction float64) (map[string]any, error) {
		d, err := s.Diff(p, manifestHandle(base), maxFraction)
		if err != nil {
			return nil, err
		}
		if d.Full {
			return map[string]any{"full": true, "reason": d.Reason}, nil
		}
		added, changed := make([]any, len(d.Added)), make([]any, len(d.Changed))
		for i, f := range d.Added {
			added[i] = fileJS(f)
		}
		for i, f := range d.Changed {
			changed[i] = fileJS(f)
		}
		removed := make([]any, len(d.Removed))
		for i, n := range d.Removed {
			removed[i] = n
		}
		count := 0
		if m, err := s.LastManifest(p); err == nil {
			for _, e := range m.Entries {
				if e.Skipped == "" {
					count++
				}
			}
		}
		return map[string]any{"full": false, "added": added, "changed": changed, "removed": removed, "unchanged": d.Unchanged, "count": count}, nil
	})
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
