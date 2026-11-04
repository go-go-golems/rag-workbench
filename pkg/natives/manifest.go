package natives

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/dop251/goja"
)

// Manifest is what source.dir stores (RAGWB-003 §6.3): every matched file's
// size, mtime and content hash, in canonical (name) order, so the next build
// can tell exactly which files were added, changed or removed.
type Manifest struct {
	Version int             `json:"version"`
	Params  string          `json:"params"` // canonical resolved params: a manifest only diffs against the same source
	Entries []ManifestEntry `json:"entries"`
}

// ManifestEntry is one matched file. Skipped is the reason a matched file was
// not returned (maxBytes, HTML); such files are tracked so a change of status is a change.
type ManifestEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mtime   int64  `json:"mtime"` // UnixNano
	Hash    string `json:"hash"`  // hex sha256 of the bytes as read
	Skipped string `json:"skipped,omitempty"`
}

const manifestVersion = 1

// FilesDiff is source.dir's delta: what changed since a base manifest.
type FilesDiff struct {
	Full      bool // the diff is not usable: rebuild everything (Reason says why)
	Reason    string
	Added     []FileRecord // matched and returned now, absent (or skipped) before
	Changed   []FileRecord // returned before and now, different bytes
	Removed   []string     // returned before, now absent or skipped
	Unchanged int
}

// Changes is the number of files the diff touches.
func (d FilesDiff) Changes() int { return len(d.Added) + len(d.Changed) + len(d.Removed) }

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (dp dirParams) key() string {
	return fmt.Sprintf("%s\x00%v\x00%v\x00%d\x00%v", dp.root, dp.include, dp.exclude, dp.maxBytes, dp.skipHTML)
}

// readMatched reads one matched file and applies the skip rules, as Walk does.
func readMatched(dp dirParams, rel, path string, info fs.FileInfo) (*FileRecord, ManifestEntry, error) {
	e := ManifestEntry{Name: rel, Size: info.Size(), Mtime: info.ModTime().UnixNano()}
	if dp.maxBytes > 0 && info.Size() > dp.maxBytes {
		e.Skipped = fmt.Sprintf("larger than maxBytes (%d > %d)", info.Size(), dp.maxBytes)
		return nil, e, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, e, err
	}
	e.Hash = hashBytes(b)
	text := strings.ToValidUTF8(string(b), "�")
	if dp.skipHTML && looksLikeHTML(text) {
		e.Skipped = "HTML document with a text extension"
		return nil, e, nil
	}
	return &FileRecord{Name: rel, Path: path, Text: text, Size: info.Size(), Mtime: info.ModTime().UnixMilli()}, e, nil
}

// Diff compares the tree with a base manifest: a stat walk, then reading and
// hashing only files whose (size, mtime) changed, so a touched-but-identical
// file is not a change. More than maxFraction of the base changed (or a
// manifest of another source) means Full.
func (s *SourceDir) Diff(p map[string]any, base *Manifest, maxFraction float64) (FilesDiff, error) {
	dp, err := s.params(p)
	if err != nil {
		return FilesDiff{}, err
	}
	if base == nil || base.Version != manifestVersion || base.Params != dp.key() {
		return FilesDiff{Full: true, Reason: "no comparable base manifest"}, nil
	}
	old := make(map[string]ManifestEntry, len(base.Entries))
	for _, e := range base.Entries {
		old[e.Name] = e
	}
	var d FilesDiff
	entries := make([]ManifestEntry, 0, len(base.Entries))
	seen := map[string]bool{}
	err = matches(dp, func(rel, path string, info fs.FileInfo) error {
		seen[rel] = true
		prev, had := old[rel]
		if had && prev.Size == info.Size() && prev.Mtime == info.ModTime().UnixNano() {
			entries = append(entries, prev)
			d.Unchanged++
			return nil
		}
		f, e, err := readMatched(dp, rel, path, info)
		if err != nil {
			return err
		}
		entries = append(entries, e)
		was, is := had && prev.Skipped == "", f != nil
		switch {
		case had && prev.Hash == e.Hash && prev.Skipped == e.Skipped:
			d.Unchanged++ // touched, same bytes
		case was && is:
			d.Changed = append(d.Changed, *f)
		case is:
			d.Added = append(d.Added, *f)
		case was:
			d.Removed = append(d.Removed, rel)
		default:
			d.Unchanged++ // skipped before and after
		}
		return nil
	})
	if err != nil {
		return FilesDiff{}, err
	}
	for _, e := range base.Entries {
		if !seen[e.Name] && e.Skipped == "" {
			d.Removed = append(d.Removed, e.Name)
		}
	}
	sort.Slice(d.Added, func(i, j int) bool { return d.Added[i].Name < d.Added[j].Name })
	sort.Slice(d.Changed, func(i, j int) bool { return d.Changed[i].Name < d.Changed[j].Name })
	sort.Strings(d.Removed)
	if n := len(base.Entries); n > 0 && float64(d.Changes()) > maxFraction*float64(n) {
		return FilesDiff{Full: true, Reason: fmt.Sprintf("%d of %d files changed (> %.0f%%)", d.Changes(), n, maxFraction*100)}, nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	s.remember(dp, &Manifest{Version: manifestVersion, Params: dp.key(), Entries: entries})
	return d, nil
}

// remember keeps the manifest of the latest walk or diff per source, for persist.save.
func (s *SourceDir) remember(dp dirParams, m *Manifest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manifests == nil {
		s.manifests = map[string]*Manifest{}
	}
	s.manifests[dp.key()] = m
}

// LastManifest returns the manifest of the latest Walk or Diff of this source.
func (s *SourceDir) LastManifest(p map[string]any) (*Manifest, error) {
	dp, err := s.params(p)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.manifests[dp.key()]
	if m == nil {
		return nil, fmt.Errorf("source.dir: no walk of %s yet", dp.root)
	}
	return m, nil
}

// Lineage identifies this source independently of the files' contents (RAGWB-003 §6.4).
func (s *SourceDir) Lineage(p map[string]any) (string, error) {
	dp, err := s.params(p)
	if err != nil {
		return "", err
	}
	return dp.key(), nil
}

// manifestObject is the JS side of a manifest: an opaque object with the Go
// manifest as its handle (persist codec "manifest") and the entry count.
func manifestObject(vm *goja.Runtime, m *Manifest) *goja.Object {
	o := vm.NewObject()
	_ = o.Set("entries", len(m.Entries))
	_ = o.DefineDataProperty(goHandleKey, vm.ToValue(m), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	return o
}

// manifestHandle returns the Go manifest behind manifestObject, or nil.
func manifestHandle(v goja.Value) *Manifest {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	h := o.Get(goHandleKey)
	if h == nil {
		return nil
	}
	m, _ := h.Export().(*Manifest)
	return m
}

func EncodeManifest(m *Manifest) ([]byte, error) { return json.Marshal(m) }

func DecodeManifest(b []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest artifact: %w", err)
	}
	return &m, nil
}
