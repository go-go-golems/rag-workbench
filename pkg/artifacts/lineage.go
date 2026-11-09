package artifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// GC derives liveness from snapshot roots and active leases, never bounded
// incremental lineage history. Incomplete authoritative metadata aborts sweeping.
func (s *Store) GC(keep int) (GCResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result GCResult
	if s.lock == nil {
		return result, fmt.Errorf("store is closed")
	}
	if s.builds > 0 {
		return result, fmt.Errorf("collection deferred while builds are active")
	}
	if keep < 1 {
		keep = 1
	}
	snapshots, current, err := s.snapshots()
	if err != nil {
		return result, err
	}
	roots := []string{}
	retained := map[string]bool{}
	counts := map[string]int{}
	for _, snapshot := range snapshots {
		if current[snapshot.ID] || counts[snapshot.Slot] < keep {
			retained[snapshot.ID] = true
			counts[snapshot.Slot]++
			roots = append(roots, snapshot.Roots...)
		}
	}
	for key := range s.pins {
		roots = append(roots, key)
	}
	marked, err := s.reachable(roots)
	if err != nil {
		return result, err
	}
	inventory, err := List(s.Dir)
	if err != nil {
		return result, err
	}
	// Validate the complete sweep before deleting anything. Active build leases
	// exclude collection, so unreferenced completed outputs can be removed immediately.
	for _, meta := range inventory {
		if meta.NS != s.NS {
			continue
		}
		if _, err := s.reachable([]string{meta.Key}); err != nil {
			return result, err
		}
	}
	for _, snapshot := range snapshots {
		if !retained[snapshot.ID] {
			if err := os.Remove(filepath.Join(s.Dir, s.NS, "snapshots", snapshot.ID+".json")); err != nil {
				return result, err
			}
		}
	}
	for _, meta := range inventory {
		if meta.NS != s.NS || marked[meta.Key] {
			continue
		}
		result.Freed += unlinkedSize(s, meta.Key, &meta)
		if err := s.Delete(meta.Key); err != nil {
			return result, err
		}
		result.Removed++
	}
	return result, nil
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
