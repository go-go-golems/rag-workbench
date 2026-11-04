package artifacts

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Lineage (RAGWB-003 §6.4): for "this node of this pipeline" — a key that does
// not change when the corpus contents change — the most recent stored outputs,
// so a build can start from the previous version instead of from scratch.
// Stored per namespace as lineage/<l[:2]>/<l>.json, newest first.

// LineageEntry is one stored version of a lineage.
type LineageEntry struct {
	Lineage  string    `json:"lineage"`
	StoreKey string    `json:"storeKey"`
	Digest   string    `json:"digest"`
	Inputs   []string  `json:"inputs,omitempty"` // build digests of the inputs it was computed from
	Created  time.Time `json:"created"`
}

// LineageKeep is how many versions a lineage remembers (so `store gc` can
// find and delete the older ones); LineageSearch is how many of the newest a
// build considers as its base.
const (
	LineageKeep   = 32
	LineageSearch = 8
)

func (s *Store) lineagePath(lineage string) (string, error) {
	if _, err := s.path(lineage, ""); err != nil { // same key rules
		return "", err
	}
	shard := lineage
	if len(shard) > 2 {
		shard = shard[:2]
	}
	return filepath.Join(s.Dir, s.NS, "lineage", shard, lineage+".json"), nil
}

func (s *Store) lineageRead(lineage string) ([]LineageEntry, error) {
	p, err := s.lineagePath(lineage)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var es []LineageEntry
	if json.Unmarshal(b, &es) != nil {
		return nil, nil // torn or foreign: no lineage, a full build follows
	}
	return es, nil
}

// LineageLatest returns the newest version whose payload is still stored, or nil.
func (s *Store) LineageLatest(lineage string) (*LineageEntry, error) {
	es, err := s.lineageRead(lineage)
	if err != nil {
		return nil, err
	}
	for i := range es {
		if m, _ := s.Has(es[i].StoreKey); m != nil {
			return &es[i], nil
		}
	}
	return nil, nil
}

// LineageList returns the versions whose payloads are still stored, newest first.
func (s *Store) LineageList(lineage string) ([]LineageEntry, error) {
	es, err := s.lineageRead(lineage)
	if err != nil {
		return nil, err
	}
	out := es[:0]
	for _, e := range es {
		if m, _ := s.Has(e.StoreKey); m != nil {
			out = append(out, e)
			if len(out) == LineageSearch {
				break
			}
		}
	}
	return out, nil
}

// LineageRecord makes e the newest version of its lineage (keeping LineageKeep).
func (s *Store) LineageRecord(e LineageEntry) error {
	p, err := s.lineagePath(e.Lineage)
	if err != nil {
		return err
	}
	es, err := s.lineageRead(e.Lineage)
	if err != nil {
		return err
	}
	if e.Created.IsZero() {
		e.Created = time.Now()
	}
	out := []LineageEntry{e}
	for _, x := range es {
		if x.StoreKey != e.StoreKey && len(out) < LineageKeep {
			out = append(out, x)
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(out)
	return writeAtomic(p, b)
}

// Delete removes a stored output: its metadata first (so Has stops reporting
// it), then its payload. Hard-linked files shared with other outputs survive.
func (s *Store) Delete(key string) error {
	m, err := s.Has(key)
	if err != nil || m == nil {
		return err
	}
	meta, _ := s.path(key, ".json")
	if err := os.Remove(meta); err != nil {
		return err
	}
	ext := ".bin"
	if m.Kind != "" {
		ext = "." + m.Codec
	}
	p, err := s.path(key, ext)
	if err != nil {
		return err
	}
	return os.RemoveAll(p)
}

// GCResult reports what GC removed.
type GCResult struct {
	Lineages int   // lineage files looked at
	Removed  int   // outputs deleted
	Freed    int64 // bytes of files no other output links to
	Temps    int   // abandoned temp build directories deleted
}

// GC keeps the newest keep versions of every lineage and deletes the older
// ones' outputs (unless a kept version is the same output), then removes temp
// build directories older than an hour. Outputs no lineage mentions (nodes
// without a lineage key) are kept; Prune handles other namespaces. A process
// still serving an old version keeps its open files, but shards it has not
// opened yet would be gone: keep >= 2 covers one rebuild in flight.
func (s *Store) GC(keep int) (GCResult, error) {
	var r GCResult
	if keep < 1 {
		keep = 1
	}
	root := filepath.Join(s.Dir, s.NS, "lineage")
	type file struct {
		path string
		es   []LineageEntry
	}
	var files []file
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".json" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var es []LineageEntry
		if json.Unmarshal(b, &es) == nil {
			files = append(files, file{p, es})
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	kept := map[string]bool{}
	for _, f := range files {
		n := 0
		for _, e := range f.es {
			if m, _ := s.Has(e.StoreKey); m != nil && n < keep {
				kept[e.StoreKey] = true
				n++
			}
		}
	}
	for _, f := range files {
		r.Lineages++
		var rest []LineageEntry
		for _, e := range f.es {
			if kept[e.StoreKey] {
				rest = append(rest, e)
				continue
			}
			m, _ := s.Has(e.StoreKey)
			if m == nil {
				continue
			}
			r.Freed += unlinkedSize(s, e.StoreKey, m)
			if err := s.Delete(e.StoreKey); err != nil {
				return r, err
			}
			r.Removed++
		}
		b, _ := json.Marshal(rest)
		if err := writeAtomic(f.path, b); err != nil {
			return r, err
		}
	}
	entries, _ := os.ReadDir(filepath.Join(s.Dir, s.NS))
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > time.Hour {
			if os.RemoveAll(filepath.Join(s.Dir, s.NS, e.Name())) == nil {
				r.Temps++
			}
		}
	}
	return r, nil
}

// unlinkedSize is the size of an output's files that nothing else links to.
func unlinkedSize(s *Store, key string, m *Meta) int64 {
	ext := ".bin"
	if m.Kind != "" {
		ext = "." + m.Codec
	}
	p, err := s.path(key, ext)
	if err != nil {
		return 0
	}
	var n int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil && linkCount(fi) <= 1 {
			n += fi.Size()
		}
		return nil
	})
	return n
}
