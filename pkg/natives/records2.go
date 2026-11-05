package natives

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)

// Records2 (RAGWB-003 §6.6, codec "records2", a directory payload): a stored
// docs or chunks output split into 64 bbolt shards by document, so a delta
// build rewrites only the shards of the documents it touched and hard-links
// the rest. Layout:
//
//	index.json   {version, kind, count, shards, files[64], docs:[{id,name,n}], ext}
//	NN-<rand>.bolt
//	  r  docID \x00 pos(uint32 BE) → record (JSON)
//	  i  record id → its r key
//	  o  docID → ids of its records (JSON), in order
//
// index.json's docs list is the canonical order (the full build's array
// order: documents by source name, records by position), so iteration and
// ordinal slicing never depend on which shards were rewritten. A record's
// shard is found from its id: the part before the first '#' is its document
// (the chunk id scheme, docIdFor + "#…"); ids that don't follow it are listed in ext.
const (
	records2Version   = 2
	records2Shards    = 64
	records2IndexFile = "index.json"
)

var (
	bucketR2Recs  = []byte("r")
	bucketR2IDs   = []byte("i")
	bucketR2Order = []byte("o")
)

// R2Rec is one record; R2Doc is one document's records, in order.
type R2Rec struct {
	ID   string
	Data []byte
}

type R2Doc struct {
	ID, Name string
	Recs     []R2Rec
}

// R2DocInfo is a document's entry in the index.
type R2DocInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	N    int    `json:"n"`
}

type records2Index struct {
	Version int               `json:"version"`
	Kind    string            `json:"kind"`
	Count   int               `json:"count"`
	Shards  int               `json:"shards"`
	Files   []string          `json:"files"` // per shard, "" = no records
	Docs    []R2DocInfo       `json:"docs"`
	Ext     map[string]string `json:"ext,omitempty"` // record id → doc id, when the id's prefix isn't its doc
}

func shardOf(docID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(docID))
	return int(h.Sum32() % records2Shards)
}

func r2Key(docID string, pos int) []byte {
	k := make([]byte, 0, len(docID)+5)
	k = append(k, docID...)
	k = append(k, 0)
	return binary.BigEndian.AppendUint32(k, uint32(pos))
}

func idPrefix(id string) string {
	if i := strings.IndexByte(id, '#'); i >= 0 {
		return id[:i]
	}
	return id
}

func randName(shard int) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%02d-%s.bolt", shard, hex.EncodeToString(b[:]))
}

// putDoc writes one document's records into an open shard transaction.
func putDoc(tx *bbolt.Tx, d R2Doc) error {
	br, bi, bo := tx.Bucket(bucketR2Recs), tx.Bucket(bucketR2IDs), tx.Bucket(bucketR2Order)
	ids := make([]string, len(d.Recs))
	for pos, r := range d.Recs {
		k := r2Key(d.ID, pos)
		if err := br.Put(k, r.Data); err != nil {
			return err
		}
		if err := bi.Put([]byte(r.ID), k); err != nil {
			return err
		}
		ids[pos] = r.ID
	}
	b, _ := json.Marshal(ids)
	return bo.Put([]byte(d.ID), b)
}

// delDoc removes one document's records from an open shard transaction.
func delDoc(tx *bbolt.Tx, docID string) error {
	br, bi, bo := tx.Bucket(bucketR2Recs), tx.Bucket(bucketR2IDs), tx.Bucket(bucketR2Order)
	if b := bo.Get([]byte(docID)); b != nil {
		var ids []string
		if err := json.Unmarshal(b, &ids); err != nil {
			return err
		}
		for _, id := range ids {
			if err := bi.Delete([]byte(id)); err != nil {
				return err
			}
		}
		if err := bo.Delete([]byte(docID)); err != nil {
			return err
		}
	}
	prefix := append([]byte(docID), 0)
	c := br.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Seek(prefix) {
		if err := c.Delete(); err != nil {
			return err
		}
	}
	return nil
}

func openShardRW(path string) (*bbolt.DB, error) {
	db, err := bbolt.Open(path, 0o644, &bbolt.Options{Timeout: time.Second, NoSync: true})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		for _, b := range [][]byte{bucketR2Recs, bucketR2IDs, bucketR2Order} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func closeShard(db *bbolt.DB) error {
	if err := db.Sync(); err != nil {
		_ = db.Close()
		return err
	}
	return db.Close()
}

// writeShard writes documents (sorted by id, so keys go in ascending order) to a new shard file.
func writeShard(path string, docs []R2Doc, base string) error {
	if base != "" {
		if err := copyFile(base, path); err != nil {
			return err
		}
	}
	db, err := openShardRW(path)
	if err != nil {
		return err
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		if base == "" {
			tx.Bucket(bucketR2Recs).FillPercent = 0.9
		}
		for _, d := range docs {
			if err := putDoc(tx, d); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return err
	}
	return closeShard(db)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func writeIndex(dir string, ix *records2Index) error {
	b, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, records2IndexFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, records2IndexFile))
}

func extOf(ix *records2Index, d R2Doc) {
	for _, r := range d.Recs {
		if idPrefix(r.ID) != d.ID {
			if ix.Ext == nil {
				ix.Ext = map[string]string{}
			}
			ix.Ext[r.ID] = d.ID
		}
	}
}

// WriteRecords2 writes documents, in canonical order, to a new records2 directory.
// A document id may appear only once; documents without records are left out.
func WriteRecords2(dir, kind string, docs []R2Doc) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ix := &records2Index{Version: records2Version, Kind: kind, Shards: records2Shards, Files: make([]string, records2Shards), Docs: []R2DocInfo{}}
	byShard := make([][]R2Doc, records2Shards)
	seen := map[string]bool{}
	for _, d := range docs {
		if len(d.Recs) == 0 {
			continue
		}
		if seen[d.ID] {
			return fmt.Errorf("records2: document %q appears twice", d.ID)
		}
		seen[d.ID] = true
		ix.Docs = append(ix.Docs, R2DocInfo{ID: d.ID, Name: d.Name, N: len(d.Recs)})
		ix.Count += len(d.Recs)
		extOf(ix, d)
		s := shardOf(d.ID)
		byShard[s] = append(byShard[s], d)
	}
	var wg sync.WaitGroup
	errs := make([]error, records2Shards)
	for s, ds := range byShard {
		if len(ds) == 0 {
			continue
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i].ID < ds[j].ID })
		ix.Files[s] = randName(s)
		wg.Add(1)
		go func(s int, ds []R2Doc) {
			defer wg.Done()
			errs[s] = writeShard(filepath.Join(dir, ix.Files[s]), ds, "")
		}(s, ds)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return writeIndex(dir, ix)
}

// CowRecords2 writes a new version of base into dst: documents in remove are
// dropped, documents in upsert replace (or join) base's, placed by name among
// the rest. Shards that no touched document maps to are hard-linked.
func CowRecords2(base *Records2, dst string, remove []string, upsert []R2Doc) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	touched := map[string]bool{}
	for _, id := range remove {
		touched[id] = true
	}
	up := map[string]bool{}
	for _, d := range upsert {
		if up[d.ID] {
			return fmt.Errorf("records2: document %q upserted twice", d.ID)
		}
		up[d.ID] = true
		touched[d.ID] = true
	}
	ix := &records2Index{Version: records2Version, Kind: base.ix.Kind, Shards: records2Shards, Files: make([]string, records2Shards), Docs: []R2DocInfo{}}
	// canonical order: the kept base docs and the upserts, merged by name
	kept := make([]R2DocInfo, 0, len(base.ix.Docs))
	for _, d := range base.ix.Docs {
		if !touched[d.ID] {
			kept = append(kept, d)
		}
	}
	ups := make([]R2Doc, 0, len(upsert))
	for _, d := range upsert {
		if len(d.Recs) > 0 {
			ups = append(ups, d)
		}
	}
	sort.SliceStable(ups, func(i, j int) bool { return ups[i].Name < ups[j].Name })
	for i, j := 0, 0; i < len(kept) || j < len(ups); {
		if j == len(ups) || (i < len(kept) && kept[i].Name <= ups[j].Name) {
			ix.Docs = append(ix.Docs, kept[i])
			ix.Count += kept[i].N
			i++
			continue
		}
		d := ups[j]
		ix.Docs = append(ix.Docs, R2DocInfo{ID: d.ID, Name: d.Name, N: len(d.Recs)})
		ix.Count += len(d.Recs)
		j++
	}
	for id, doc := range base.ix.Ext {
		if !touched[doc] {
			if ix.Ext == nil {
				ix.Ext = map[string]string{}
			}
			ix.Ext[id] = doc
		}
	}
	for _, d := range ups {
		extOf(ix, d)
	}
	// shards
	dirty := map[int][]string{}
	for id := range touched {
		s := shardOf(id)
		dirty[s] = append(dirty[s], id)
	}
	upsBy := map[int][]R2Doc{}
	for _, d := range ups {
		upsBy[shardOf(d.ID)] = append(upsBy[shardOf(d.ID)], d)
	}
	for s := 0; s < records2Shards; s++ {
		bf := base.ix.Files[s]
		if _, ok := dirty[s]; !ok {
			if bf != "" {
				if err := linkOrCopy(filepath.Join(base.dir, bf), filepath.Join(dst, bf)); err != nil {
					return err
				}
				ix.Files[s] = bf
			}
			continue
		}
		has := false
		for _, d := range ix.Docs {
			if shardOf(d.ID) == s {
				has = true
				break
			}
		}
		if !has {
			continue // every doc of this shard was removed
		}
		ix.Files[s] = randName(s)
		path := filepath.Join(dst, ix.Files[s])
		if bf == "" {
			ds := upsBy[s]
			sort.Slice(ds, func(i, j int) bool { return ds[i].ID < ds[j].ID })
			if err := writeShard(path, ds, ""); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(filepath.Join(base.dir, bf), path); err != nil {
			return err
		}
		db, err := openShardRW(path)
		if err != nil {
			return err
		}
		if err := db.Update(func(tx *bbolt.Tx) error {
			for _, id := range dirty[s] {
				if err := delDoc(tx, id); err != nil {
					return err
				}
			}
			for _, d := range upsBy[s] {
				if err := putDoc(tx, d); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			_ = db.Close()
			return err
		}
		if err := closeShard(db); err != nil {
			return err
		}
	}
	return writeIndex(dst, ix)
}

func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	return copyFile(src, dst)
}

// Records2 is an opened (read-only) records2 directory. Shards open on first use.
type Records2 struct {
	mu     sync.Mutex
	dir    string
	ix     *records2Index
	starts []int // ordinal of each doc's first record
	pos    map[string]int
	byName map[string]int
	shards []*bbolt.DB
	closed bool
}

// OpenRecords2 reads the index of a records2 directory.
func OpenRecords2(dir string) (*Records2, error) {
	b, err := os.ReadFile(filepath.Join(dir, records2IndexFile))
	if err != nil {
		return nil, fmt.Errorf("records2 %s: %w", dir, err)
	}
	var ix records2Index
	if err := json.Unmarshal(b, &ix); err != nil {
		return nil, fmt.Errorf("records2 %s: %w", dir, err)
	}
	if ix.Version != records2Version || ix.Shards != records2Shards || len(ix.Files) != records2Shards {
		return nil, fmt.Errorf("records2 %s: format %d/%d shards, want %d/%d", dir, ix.Version, ix.Shards, records2Version, records2Shards)
	}
	r := &Records2{dir: dir, ix: &ix, starts: make([]int, len(ix.Docs)), pos: make(map[string]int, len(ix.Docs)), byName: make(map[string]int, len(ix.Docs)), shards: make([]*bbolt.DB, records2Shards)}
	n := 0
	for i, d := range ix.Docs {
		r.starts[i] = n
		r.pos[d.ID] = i
		r.byName[d.Name] = i
		n += d.N
	}
	if n != ix.Count {
		return nil, fmt.Errorf("records2 %s: index counts %d records, says %d", dir, n, ix.Count)
	}
	return r, nil
}

func (r *Records2) Len() int     { return r.ix.Count }
func (r *Records2) Kind() string { return r.ix.Kind }
func (r *Records2) Dir() string  { return r.dir }

// Docs is the canonical document list.
func (r *Records2) Docs() []R2DocInfo { return r.ix.Docs }

// DocByName returns the document stored for a source name.
func (r *Records2) DocByName(name string) (R2DocInfo, bool) {
	i, ok := r.byName[name]
	if !ok {
		return R2DocInfo{}, false
	}
	return r.ix.Docs[i], true
}

// DocInfo returns a document's index entry.
func (r *Records2) DocInfo(id string) (R2DocInfo, bool) {
	i, ok := r.pos[id]
	if !ok {
		return R2DocInfo{}, false
	}
	return r.ix.Docs[i], true
}

// Relocate reopens the reader at the directory's new place (after the store
// moved it there, or found an equal output already stored there).
func (r *Records2) Relocate(dir string) error {
	n, err := OpenRecords2(dir)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, db := range r.shards {
		if db != nil {
			_ = db.Close()
			r.shards[i] = nil
		}
	}
	r.dir, r.ix, r.starts, r.pos, r.byName = n.dir, n.ix, n.starts, n.pos, n.byName
	return nil
}

func (r *Records2) shard(s int) (*bbolt.DB, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, fmt.Errorf("records2 %s: closed", r.dir)
	}
	if r.shards[s] != nil {
		return r.shards[s], nil
	}
	if r.ix.Files[s] == "" {
		return nil, nil
	}
	db, err := bbolt.Open(filepath.Join(r.dir, r.ix.Files[s]), 0o444, &bbolt.Options{ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("records2 shard %s: %w", r.ix.Files[s], err)
	}
	r.shards[s] = db
	return db, nil
}

func (r *Records2) docOf(id string) string {
	if d, ok := r.ix.Ext[id]; ok {
		return d
	}
	return idPrefix(id)
}

// Get returns a copy of the record with this id.
func (r *Records2) Get(id string) ([]byte, bool, error) {
	doc := r.docOf(id)
	if _, ok := r.pos[doc]; !ok {
		return nil, false, nil
	}
	db, err := r.shard(shardOf(doc))
	if err != nil || db == nil {
		return nil, false, err
	}
	var out []byte
	err = db.View(func(tx *bbolt.Tx) error {
		k := tx.Bucket(bucketR2IDs).Get([]byte(id))
		if k == nil {
			return nil
		}
		v := tx.Bucket(bucketR2Recs).Get(k)
		if v == nil {
			return fmt.Errorf("records2: id %q without record", id)
		}
		out = append([]byte(nil), v...)
		return nil
	})
	return out, out != nil, err
}

// Has reports whether a record with this id exists.
func (r *Records2) Has(id string) bool {
	doc := r.docOf(id)
	if _, ok := r.pos[doc]; !ok {
		return false
	}
	db, err := r.shard(shardOf(doc))
	if err != nil || db == nil {
		return false
	}
	found := false
	_ = db.View(func(tx *bbolt.Tx) error {
		found = tx.Bucket(bucketR2IDs).Get([]byte(id)) != nil
		return nil
	})
	return found
}

// Slice calls f with records [from, to) in canonical order (data is valid only during the call).
func (r *Records2) Slice(from, to int, f func(data []byte) error) error {
	from, to = max(0, from), min(r.ix.Count, to)
	if from >= to {
		return nil
	}
	di := sort.Search(len(r.starts), func(i int) bool { return r.starts[i] > from }) - 1
	at := from
	for ; di < len(r.ix.Docs) && at < to; di++ {
		d := r.ix.Docs[di]
		first := at - r.starts[di]
		last := min(d.N, to-r.starts[di])
		db, err := r.shard(shardOf(d.ID))
		if err != nil {
			return err
		}
		if db == nil {
			return fmt.Errorf("records2: doc %q in a missing shard", d.ID)
		}
		if err := db.View(func(tx *bbolt.Tx) error {
			c := tx.Bucket(bucketR2Recs).Cursor()
			prefix := append([]byte(d.ID), 0)
			n := first
			for k, v := c.Seek(r2Key(d.ID, first)); k != nil && bytes.HasPrefix(k, prefix) && n < last; k, v = c.Next() {
				if err := f(v); err != nil {
					return err
				}
				n++
			}
			if n != last {
				return fmt.Errorf("records2: doc %q has %d records, index says %d", d.ID, n, d.N)
			}
			return nil
		}); err != nil {
			return err
		}
		at = r.starts[di] + last
	}
	return nil
}

// DocRecords calls f with one document's records, in order.
func (r *Records2) DocRecords(docID string, f func(data []byte) error) error {
	i, ok := r.pos[docID]
	if !ok {
		return nil
	}
	return r.Slice(r.starts[i], r.starts[i]+r.ix.Docs[i].N, f)
}

// Order returns the ids of a document's records, in order.
func (r *Records2) Order(docID string) ([]string, error) {
	if _, ok := r.pos[docID]; !ok {
		return nil, nil
	}
	db, err := r.shard(shardOf(docID))
	if err != nil || db == nil {
		return nil, err
	}
	var ids []string
	err = db.View(func(tx *bbolt.Tx) error {
		if b := tx.Bucket(bucketR2Order).Get([]byte(docID)); b != nil {
			return json.Unmarshal(b, &ids)
		}
		return nil
	})
	return ids, err
}

// Close releases the open shards.
func (r *Records2) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	var first error
	for i, db := range r.shards {
		if db != nil {
			if err := db.Close(); err != nil && first == nil {
				first = err
			}
			r.shards[i] = nil
		}
	}
	return first
}
