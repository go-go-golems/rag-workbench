package natives

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "github.com/mattn/go-sqlite3" // sqlite driver (also used by go-go-goja's db module)
)

// EmbedCache persists embeddings keyed by (model, dims, sha256(text)), so
// rebuilding a corpus only embeds new or changed chunks.
type EmbedCache struct {
	db *sql.DB
	mu sync.Mutex
}

// DefaultEmbedCachePath is ~/.ragwb/cache/embeddings.db (override with $RAGWB_CACHE_DIR).
func DefaultEmbedCachePath() string {
	dir := os.Getenv("RAGWB_CACHE_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".ragwb", "cache")
	}
	return filepath.Join(dir, "embeddings.db")
}

func OpenEmbedCache(path string) (*EmbedCache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS embeddings (
		model TEXT NOT NULL, dims INTEGER NOT NULL, hash TEXT NOT NULL, vec BLOB NOT NULL,
		PRIMARY KEY (model, dims, hash))`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &EmbedCache{db: db}, nil
}

func (c *EmbedCache) Close() error { return c.db.Close() }

func textHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

func encodeVec(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func decodeVec(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

// Get returns cached vectors for texts (nil entries are misses).
func (c *EmbedCache) Get(model string, dims int, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	if c == nil {
		return out, nil
	}
	idx := map[string][]int{}
	hashes := []string{}
	for i, t := range texts {
		h := textHash(t)
		if _, ok := idx[h]; !ok {
			hashes = append(hashes, h)
		}
		idx[h] = append(idx[h], i)
	}
	for start := 0; start < len(hashes); start += 500 {
		batch := hashes[start:min(len(hashes), start+500)]
		args := []any{model, dims}
		for _, h := range batch {
			args = append(args, h)
		}
		rows, err := c.db.Query(`SELECT hash, vec FROM embeddings WHERE model = ? AND dims = ? AND hash IN (?`+strings.Repeat(",?", len(batch)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var h string
			var b []byte
			if err := rows.Scan(&h, &b); err != nil {
				_ = rows.Close()
				return nil, err
			}
			v := decodeVec(b)
			for _, i := range idx[h] {
				out[i] = v
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Put stores vectors for texts.
func (c *EmbedCache) Put(model string, dims int, texts []string, vecs [][]float32) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	st, err := tx.Prepare(`INSERT OR REPLACE INTO embeddings (model, dims, hash, vec) VALUES (?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for i, t := range texts {
		if vecs[i] == nil {
			continue
		}
		if _, err := st.Exec(model, dims, textHash(t), encodeVec(vecs[i])); err != nil {
			_ = st.Close()
			_ = tx.Rollback()
			return fmt.Errorf("embedding cache: %w", err)
		}
	}
	_ = st.Close()
	return tx.Commit()
}
