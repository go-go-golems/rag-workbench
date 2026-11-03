package natives

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"go.etcd.io/bbolt"
)

// Chunk records (RAGWB-002 Phase 2, codec "records"): a stored chunks output as
// a bbolt file read one chunk at a time, instead of a gob blob decoded whole.
// Buckets:
//
//	c  ordinal (uint32 BE) → chunk (JSON of Chunk; decoded and turned into the
//	   same JS object chunkToJS makes, so stored and computed chunks are equal)
//	i  chunk id → ordinal
//	d  doc id → ids of its chunks (JSON), in array order (artifacts().order)
//	m  "version", "count"
const recordsVersion = "1"

var (
	bucketChunks = []byte("c")
	bucketIDs    = []byte("i")
	bucketDocs   = []byte("d")
	bucketMeta   = []byte("m")
)

func ordKey(i int) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(i))
	return b[:]
}

// WriteChunkRecords writes chunks to a new record file at path.
func WriteChunkRecords(path string, chunks []Chunk) error {
	db, err := bbolt.Open(path, 0o644, &bbolt.Options{Timeout: time.Second, NoSync: true})
	if err != nil {
		return err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		bc, err := tx.CreateBucket(bucketChunks)
		if err != nil {
			return err
		}
		bc.FillPercent = 1 // keys are appended in order
		bi, err := tx.CreateBucket(bucketIDs)
		if err != nil {
			return err
		}
		bd, err := tx.CreateBucket(bucketDocs)
		if err != nil {
			return err
		}
		bm, err := tx.CreateBucket(bucketMeta)
		if err != nil {
			return err
		}
		byDoc := map[string][]string{}
		docOrder := []string{}
		for i := range chunks {
			c := &chunks[i]
			rec, err := json.Marshal(c)
			if err != nil {
				return err
			}
			if err := bc.Put(ordKey(i), rec); err != nil {
				return err
			}
			if err := bi.Put([]byte(c.ID), ordKey(i)); err != nil {
				return err
			}
			if _, ok := byDoc[c.DocID]; !ok {
				docOrder = append(docOrder, c.DocID)
			}
			byDoc[c.DocID] = append(byDoc[c.DocID], c.ID)
		}
		for _, d := range docOrder {
			b, _ := json.Marshal(byDoc[d])
			if err := bd.Put([]byte(d), b); err != nil {
				return err
			}
		}
		if err := bm.Put([]byte("version"), []byte(recordsVersion)); err != nil {
			return err
		}
		return bm.Put([]byte("count"), []byte(fmt.Sprint(len(chunks))))
	})
	if err != nil {
		_ = db.Close()
		return err
	}
	if err := db.Sync(); err != nil {
		_ = db.Close()
		return err
	}
	return db.Close()
}

// ChunkRecords is an opened (read-only) record file.
type ChunkRecords struct {
	db   *bbolt.DB
	n    int
	path string
}

// OpenChunkRecords opens a record file read-only (a shared lock: several
// processes may read one stored output).
func OpenChunkRecords(path string) (*ChunkRecords, error) {
	db, err := bbolt.Open(path, 0o444, &bbolt.Options{ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("chunk records %s: %w", path, err)
	}
	r := &ChunkRecords{db: db, path: path}
	err = db.View(func(tx *bbolt.Tx) error {
		bm := tx.Bucket(bucketMeta)
		if bm == nil || tx.Bucket(bucketChunks) == nil || tx.Bucket(bucketIDs) == nil || tx.Bucket(bucketDocs) == nil {
			return fmt.Errorf("not a chunk record file")
		}
		if v := string(bm.Get([]byte("version"))); v != recordsVersion {
			return fmt.Errorf("record format %q, want %q", v, recordsVersion)
		}
		_, err := fmt.Sscan(string(bm.Get([]byte("count"))), &r.n)
		return err
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("chunk records %s: %w", path, err)
	}
	return r, nil
}

// Len is the number of chunks.
func (r *ChunkRecords) Len() int { return r.n }

// Close releases the file.
func (r *ChunkRecords) Close() error {
	if r.db == nil {
		return nil
	}
	err := r.db.Close()
	r.db = nil
	return err
}

func decodeChunk(b []byte) (Chunk, error) {
	var c Chunk
	err := json.Unmarshal(b, &c) // b is only valid inside the transaction: decode there
	return c, err
}

// Get returns the chunk with this id.
func (r *ChunkRecords) Get(id string) (Chunk, bool, error) {
	var c Chunk
	var ok bool
	err := r.db.View(func(tx *bbolt.Tx) error {
		ord := tx.Bucket(bucketIDs).Get([]byte(id))
		if ord == nil {
			return nil
		}
		b := tx.Bucket(bucketChunks).Get(ord)
		if b == nil {
			return fmt.Errorf("chunk %q: id without record", id)
		}
		var err error
		c, err = decodeChunk(b)
		ok = err == nil
		return err
	})
	return c, ok, err
}

// Has reports whether a chunk with this id exists.
func (r *ChunkRecords) Has(id string) bool {
	found := false
	_ = r.db.View(func(tx *bbolt.Tx) error {
		found = tx.Bucket(bucketIDs).Get([]byte(id)) != nil
		return nil
	})
	return found
}

// Slice returns chunks [from, to) in stored (array) order.
func (r *ChunkRecords) Slice(from, to int) ([]Chunk, error) {
	from, to = max(0, from), min(r.n, to)
	if from >= to {
		return nil, nil
	}
	out := make([]Chunk, 0, to-from)
	err := r.db.View(func(tx *bbolt.Tx) error {
		cur := tx.Bucket(bucketChunks).Cursor()
		for k, v := cur.Seek(ordKey(from)); k != nil && int(binary.BigEndian.Uint32(k)) < to; k, v = cur.Next() {
			c, err := decodeChunk(v)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return nil
	})
	return out, err
}

// Order returns the ids of a document's chunks in array order.
func (r *ChunkRecords) Order(docID string) ([]string, error) {
	var ids []string
	err := r.db.View(func(tx *bbolt.Tx) error {
		if b := tx.Bucket(bucketDocs).Get([]byte(docID)); b != nil {
			return json.Unmarshal(b, &ids)
		}
		return nil
	})
	return ids, err
}

// fileSize is used by tests and timing details.
func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}
