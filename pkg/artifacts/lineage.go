package artifacts

import (
	"encoding/json"
	"errors"
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

// LineageKeep is how many versions a lineage remembers.
const LineageKeep = 3

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
