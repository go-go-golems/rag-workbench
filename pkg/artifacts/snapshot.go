package artifacts

import (
	"encoding/json"
	"github.com/pkg/errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Snapshot struct {
	ID      string    `json:"id"`
	Slot    string    `json:"slot"`
	Roots   []string  `json:"roots"`
	Created time.Time `json:"created"`
}

// Describe attaches ownership edges to an existing complete immutable payload.
func (s *Store) Describe(key, lineage string, dependencies []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.Has(key)
	if err != nil {
		return err
	}
	if meta == nil {
		return errors.Errorf("missing artifact %s", key)
	}
	meta.Lineage = lineage
	meta.Dependencies = dependencies
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	p, _ := s.path(key, ".json")
	return writeAtomic(p, b)
}
func (s *Store) Publish(snapshot Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return errors.New("store is closed")
	}
	if !keyRE.MatchString(snapshot.ID) || !keyRE.MatchString(snapshot.Slot) {
		return errors.New("invalid snapshot or slot key")
	}
	if _, err := s.reachable(snapshot.Roots); err != nil {
		return err
	}
	snapshot.Created = time.Now()
	dir := filepath.Join(s.Dir, s.NS, "snapshots")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, snapshot.ID+".json"), b); err != nil {
		return err
	}
	current := filepath.Join(s.Dir, s.NS, "current")
	if err := os.MkdirAll(current, 0755); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(current, snapshot.Slot+".json"), []byte(`{"snapshot":"`+snapshot.ID+`"}`))
}
func (s *Store) Pin(keys []string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil, errors.New("store is closed")
	}
	if _, err := s.reachable(keys); err != nil {
		return nil, err
	}
	for _, key := range keys {
		s.pins[key]++
	}
	released := false
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if released {
			return
		}
		released = true
		for _, key := range keys {
			s.pins[key]--
			if s.pins[key] == 0 {
				delete(s.pins, key)
			}
		}
	}, nil
}
func (s *Store) reachable(roots []string) (map[string]bool, error) {
	marked := map[string]bool{}
	var visit func(string) error
	visit = func(key string) error {
		if marked[key] {
			return nil
		}
		marked[key] = true
		meta, err := s.Has(key)
		if err != nil {
			return err
		}
		if meta == nil {
			return errors.Errorf("missing or corrupt authoritative artifact %s; collection aborted", key)
		}
		for _, dep := range meta.Dependencies {
			if err := visit(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, key := range roots {
		if err := visit(key); err != nil {
			return nil, err
		}
	}
	return marked, nil
}
func (s *Store) snapshots() ([]Snapshot, map[string]bool, error) {
	list := []Snapshot{}
	current := map[string]bool{}
	paths, err := filepath.Glob(filepath.Join(s.Dir, s.NS, "snapshots", "*.json"))
	if err != nil {
		return nil, nil, err
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		var item Snapshot
		if err = json.Unmarshal(b, &item); err != nil {
			return nil, nil, err
		}
		if !keyRE.MatchString(item.ID) || !keyRE.MatchString(item.Slot) || filepath.Base(p) != item.ID+".json" {
			return nil, nil, errors.New("invalid snapshot identity")
		}
		list = append(list, item)
	}
	pointers, err := filepath.Glob(filepath.Join(s.Dir, s.NS, "current", "*.json"))
	if err != nil {
		return nil, nil, err
	}
	for _, p := range pointers {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		var item struct {
			Snapshot string `json:"snapshot"`
		}
		if err = json.Unmarshal(b, &item); err != nil {
			return nil, nil, err
		}
		found := false
		for _, snap := range list {
			if snap.ID == item.Snapshot && filepath.Base(p) == snap.Slot+".json" {
				found = true
			}
		}
		if !found {
			return nil, nil, errors.New("current pointer refers to a missing snapshot")
		}
		current[item.Snapshot] = true
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created.After(list[j].Created) })
	return list, current, nil
}

// BeginBuild excludes collection while unpublished outputs are being assembled.
// The namespace owner is the only process allowed to build or collect.
func (s *Store) BeginBuild() (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil, errors.New("store is closed")
	}
	s.builds++
	released := false
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !released {
			released = true
			s.builds--
		}
	}, nil
}
