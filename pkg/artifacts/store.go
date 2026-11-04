// Package artifacts persists build outputs of the rag engine by digest
// (guide P8, A5): a query verb that runs a plan over an unchanged corpus loads
// the stored docs, chunks and indexes instead of rebuilding them.
//
// Layout: <dir>/<ns>/<key[:2]>/<key>.bin plus <key>.json (metadata). Outputs
// that are opened in place rather than decoded (a bleve index, a record store)
// are a file <key>.<codec> or a directory <key>.<codec>/ instead of .bin
// (Meta.Kind "file" / "dir"). ns names
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
	"strings"
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
	// Kind is "" (a blob, <key>.bin, read with Load) or "file"/"dir" (a payload
	// opened in place at Path: <key>.<codec>).
	Kind string `json:"kind,omitempty"`
}

// Payload kinds.
const (
	KindFile = "file"
	KindDir  = "dir"
)

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
	switch m.Kind {
	case "":
		bin, _ := s.path(key, ".bin")
		if fi, err := os.Stat(bin); err != nil || fi.Size() != m.Size {
			return nil, nil
		}
	case KindFile, KindDir:
		p, err := s.path(key, "."+m.Codec)
		if err != nil {
			return nil, nil
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() != (m.Kind == KindDir) || (m.Kind == KindFile && fi.Size() != m.Size) {
			return nil, nil
		}
	default:
		return nil, nil
	}
	return &m, nil
}

// TempDir returns a new directory inside the namespace for building a file or
// directory payload; SaveFile/SaveDir then rename from it (same filesystem).
// Callers remove it if they do not save.
func (s *Store) TempDir() (string, error) {
	return os.MkdirTemp(filepath.Join(s.Dir, s.NS), ".tmp-")
}

// SaveFile moves the file src into the store as key's payload (<key>.<codec>)
// and writes the metadata after it, so Has never reports a half-moved payload.
func (s *Store) SaveFile(key, typ, codec string, count int, src string) error {
	return s.savePath(key, typ, codec, count, src, KindFile)
}

// SaveDir moves the directory src into the store as key's payload (<key>.<codec>/).
func (s *Store) SaveDir(key, typ, codec string, count int, src string) error {
	return s.savePath(key, typ, codec, count, src, KindDir)
}

func (s *Store) savePath(key, typ, codec string, count int, src, kind string) error {
	if !keyRE.MatchString(codec) {
		return fmt.Errorf("artifacts: invalid codec %q", codec)
	}
	dst, err := s.path(key, "."+codec)
	if err != nil {
		return err
	}
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() != (kind == KindDir) {
		return fmt.Errorf("artifacts: %s payload %s is not a %s", typ, src, kind)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		// stored meanwhile (another process, same digest → same content): keep it
		_ = os.RemoveAll(src)
	} else if err := os.Rename(src, dst); err != nil {
		// a different filesystem (e.g. a build under /tmp): copy, then remove the source
		if cerr := copyTree(src, dst+".partial"); cerr != nil {
			_ = os.RemoveAll(dst + ".partial")
			return fmt.Errorf("artifacts: move %s: %v (copy: %w)", src, err, cerr)
		}
		if err := os.Rename(dst+".partial", dst); err != nil {
			return err
		}
		_ = os.RemoveAll(src)
	}
	size, err := pathSize(dst)
	if err != nil {
		return err
	}
	m := Meta{Key: key, Type: typ, Codec: codec, Count: count, Size: size, Created: time.Now(), Kind: kind}
	mb, _ := json.Marshal(m)
	meta, _ := s.path(key, ".json")
	return writeAtomic(meta, mb)
}

// Path returns the location of a file or directory payload, for opening in place.
func (s *Store) Path(key string) (Meta, string, error) {
	m, err := s.Has(key)
	if err != nil {
		return Meta{}, "", err
	}
	if m == nil {
		return Meta{}, "", fmt.Errorf("artifacts: %s is not stored", key)
	}
	if m.Kind != KindFile && m.Kind != KindDir {
		return *m, "", fmt.Errorf("artifacts: %s is a blob; use Load", key)
	}
	p, err := s.path(key, "."+m.Codec)
	return *m, p, err
}

func pathSize(p string) (int64, error) {
	var n int64
	err := filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			n += fi.Size()
		}
		return nil
	})
	return n, err
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
	if m.Kind != "" {
		return *m, nil, fmt.Errorf("artifacts: %s is a %s payload; use Path", key, m.Kind)
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
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".tmp-") || strings.Contains(d.Name(), ".") {
				return filepath.SkipDir // temp builds and directory payloads (<key>.<codec>/) hold no metadata
			}
			return nil
		}
		if filepath.Ext(p) != ".json" {
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

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
}
