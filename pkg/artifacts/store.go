// Package artifacts persists build outputs of the rag engine by digest
// (guide P8, A5): a query verb that runs a plan over an unchanged corpus loads
// the stored docs, chunks and indexes instead of rebuilding them.
//
// Layout: <dir>/<ns>/<key[:2]>/<key>.bin plus <key>.json (metadata). ns names
// everything that can change an output without changing its digest (the rag.js
// source, the Go component version, JS-vs-Go components), so an upgrade starts
// a fresh namespace instead of reusing incompatible outputs.
package artifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Meta describes one stored output.
type Meta struct {
	Key     string    `json:"key"`
	Type    string    `json:"type"`  // component type, e.g. parse.markdown
	Codec   string    `json:"codec"` // json | bm25 | …
	Count   int       `json:"count"` // items, as the build report shows them
	Size    int64     `json:"size"`  // payload bytes
	Created time.Time `json:"created"`
	NS      string    `json:"ns,omitempty"`
}

// Store is one namespace of an artifact directory.
type Store struct {
	Dir string // root of all namespaces
	NS  string
}

var keyRE = regexp.MustCompile(`^[0-9a-z]{1,64}$`)

// Open prepares <dir>/<ns>.
func Open(dir, ns string) (*Store, error) {
	if !keyRE.MatchString(ns) {
		return nil, fmt.Errorf("artifacts: invalid namespace %q", ns)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, ns), 0o755); err != nil {
		return nil, err
	}
	return &Store{Dir: abs, NS: ns}, nil
}

func (s *Store) path(key, ext string) (string, error) {
	if !keyRE.MatchString(key) {
		return "", fmt.Errorf("artifacts: invalid key %q", key)
	}
	shard := key
	if len(shard) > 2 {
		shard = shard[:2]
	}
	return filepath.Join(s.Dir, s.NS, shard, key+ext), nil
}

// Has returns the metadata of a stored output, or nil.
func (s *Store) Has(key string) (*Meta, error) {
	p, err := s.path(key, ".json")
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
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, nil // a torn or foreign file: treat as missing, it will be rewritten
	}
	bin, _ := s.path(key, ".bin")
	if fi, err := os.Stat(bin); err != nil || fi.Size() != m.Size {
		return nil, nil
	}
	return &m, nil
}

// Save writes the payload, then the metadata (each atomically), so Has never
// reports an output whose payload is incomplete.
func (s *Store) Save(key, typ, codec string, count int, data []byte) error {
	bin, err := s.path(key, ".bin")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(bin, data); err != nil {
		return err
	}
	m := Meta{Key: key, Type: typ, Codec: codec, Count: count, Size: int64(len(data)), Created: time.Now()}
	mb, _ := json.Marshal(m)
	meta, _ := s.path(key, ".json")
	return writeAtomic(meta, mb)
}

func writeAtomic(p string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Load returns a stored output.
func (s *Store) Load(key string) (Meta, []byte, error) {
	m, err := s.Has(key)
	if err != nil {
		return Meta{}, nil, err
	}
	if m == nil {
		return Meta{}, nil, fmt.Errorf("artifacts: %s is not stored", key)
	}
	bin, _ := s.path(key, ".bin")
	b, err := os.ReadFile(bin)
	return *m, b, err
}

// List returns the outputs of every namespace under Dir, newest first.
func List(dir string) ([]Meta, error) {
	out := []Meta{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
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
			return nil
		}
		var m Meta
		if json.Unmarshal(b, &m) != nil || m.Key == "" {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		m.NS = filepath.Dir(filepath.Dir(rel))
		out = append(out, m)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, err
}

// Prune deletes every namespace under dir except keep ("" deletes all).
func Prune(dir, keep string) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() || e.Name() == keep || !keyRE.MatchString(e.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
