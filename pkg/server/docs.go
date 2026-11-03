package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DocStore backs the page's db capability: JSON documents addressed by
// slash-separated paths (doc/collection/doc/...), Firestore-style.
type DocStore struct{ db *sql.DB }

func OpenDocStore(path string) (*DocStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS docs (path TEXT PRIMARY KEY, json TEXT NOT NULL, updated INTEGER NOT NULL)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &DocStore{db: db}, nil
}

func (s *DocStore) Close() error { return s.db.Close() }

func cleanPath(p string) (string, error) {
	p = strings.Trim(p, "/")
	if p == "" || strings.Contains(p, "//") || len(p) > 1024 {
		return "", fmt.Errorf("invalid document path %q", p)
	}
	return p, nil
}

// Get returns the document JSON, or nil when it doesn't exist.
func (s *DocStore) Get(path string) (json.RawMessage, error) {
	p, err := cleanPath(path)
	if err != nil {
		return nil, err
	}
	var j string
	err = s.db.QueryRow(`SELECT json FROM docs WHERE path = ?`, p).Scan(&j)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return json.RawMessage(j), err
}

// MaxDocBytes mirrors the platform's per-document cap.
const MaxDocBytes = 256 << 10

func (s *DocStore) Set(path string, data json.RawMessage) error {
	p, err := cleanPath(path)
	if err != nil {
		return err
	}
	if len(data) > MaxDocBytes {
		return fmt.Errorf("document %s is %d bytes, over the %d byte limit", p, len(data), MaxDocBytes)
	}
	_, err = s.db.Exec(`INSERT INTO docs (path, json, updated) VALUES (?, ?, ?) ON CONFLICT(path) DO UPDATE SET json = excluded.json, updated = excluded.updated`,
		p, string(data), time.Now().UnixMilli())
	return err
}

func (s *DocStore) Delete(path string) error {
	p, err := cleanPath(path)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM docs WHERE path = ?`, p)
	return err
}

// DocEntry is one document of a collection listing.
type DocEntry struct {
	Path string          `json:"path"`
	Data json.RawMessage `json:"data"`
}

// List returns the direct children of a collection path (collection/doc, not deeper).
func (s *DocStore) List(path string) ([]DocEntry, error) {
	p, err := cleanPath(path)
	if err != nil {
		return nil, err
	}
	prefix := p + "/"
	rows, err := s.db.Query(`SELECT path, json FROM docs WHERE substr(path, 1, ?) = ? ORDER BY path`, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DocEntry{}
	for rows.Next() {
		var path, j string
		if err := rows.Scan(&path, &j); err != nil {
			return nil, err
		}
		if strings.Contains(path[len(prefix):], "/") {
			continue // a deeper subcollection document
		}
		out = append(out, DocEntry{Path: path, Data: json.RawMessage(j)})
	}
	return out, rows.Err()
}
