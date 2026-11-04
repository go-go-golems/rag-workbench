package natives

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func r2doc(name string, n int, tag string) R2Doc {
	id := DocIDFor(name)
	d := R2Doc{ID: id, Name: name}
	for i := 0; i < n; i++ {
		rid := fmt.Sprintf("%s#s%d", id, i)
		d.Recs = append(d.Recs, R2Rec{ID: rid, Data: []byte(fmt.Sprintf(`{"id":%q,"tag":%q}`, rid, tag))})
	}
	return d
}

func r2all(t *testing.T, r *Records2) []string {
	t.Helper()
	out := []string{}
	if err := r.Slice(0, r.Len(), func(b []byte) error { out = append(out, string(b)); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

// A copy-on-write version equals a fresh write of the same documents: same
// records in the same canonical order, same index, and untouched shards are
// hard links of the base's.
func TestRecords2CowEqualsFresh(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	state := map[string]R2Doc{}
	for i := 0; i < 300; i++ {
		name := fmt.Sprintf("Notes/%03d note.md", i)
		state[name] = r2doc(name, 1+rng.Intn(6), "v0")
	}
	sorted := func() []R2Doc {
		names := make([]string, 0, len(state))
		for n := range state {
			names = append(names, n)
		}
		sort.Strings(names)
		out := make([]R2Doc, len(names))
		for i, n := range names {
			out[i] = state[n]
		}
		return out
	}
	tmp := t.TempDir()
	baseDir := filepath.Join(tmp, "v0")
	if err := WriteRecords2(baseDir, kindChunks, sorted()); err != nil {
		t.Fatal(err)
	}
	base, err := OpenRecords2(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	for step := 1; step <= 6; step++ {
		var remove []string
		var upsert []R2Doc
		names := []string{}
		for n := range state {
			names = append(names, n)
		}
		sort.Strings(names)
		for k := 0; k < 3; k++ { // edit, remove, add
			n := names[rng.Intn(len(names))]
			d := r2doc(n, rng.Intn(5), fmt.Sprintf("v%d", step)) // 0 records = the doc drops out
			state[n] = d
			upsert = append(upsert, d)
		}
		gone := names[rng.Intn(len(names))]
		if _, dup := state[gone]; dup {
			remove = append(remove, DocIDFor(gone))
			delete(state, gone)
			for i := range upsert {
				if upsert[i].Name == gone {
					upsert = append(upsert[:i], upsert[i+1:]...)
					break
				}
			}
		}
		added := fmt.Sprintf("Notes/%03d-new %d.md", rng.Intn(300), step)
		state[added] = r2doc(added, 2, "new")
		upsert = append(upsert, state[added])
		for n, d := range state {
			if len(d.Recs) == 0 {
				delete(state, n)
			}
		}

		dst := filepath.Join(tmp, fmt.Sprintf("v%d", step))
		if err := CowRecords2(base, dst, remove, upsert); err != nil {
			t.Fatal(err)
		}
		cow, err := OpenRecords2(dst)
		if err != nil {
			t.Fatal(err)
		}
		fresh := filepath.Join(tmp, fmt.Sprintf("fresh%d", step))
		if err := WriteRecords2(fresh, kindChunks, sorted()); err != nil {
			t.Fatal(err)
		}
		fr, _ := OpenRecords2(fresh)
		if got, want := r2all(t, cow), r2all(t, fr); !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d: cow has %d records, fresh %d (or order differs)", step, len(got), len(want))
		}
		if !reflect.DeepEqual(cow.Docs(), fr.Docs()) {
			t.Fatalf("step %d: index differs", step)
		}
		for _, d := range fr.Docs() {
			want, _ := fr.Order(d.ID)
			got, _ := cow.Order(d.ID)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("step %d: order(%s) = %v, want %v", step, d.ID, got, want)
			}
			b, ok, err := cow.Get(want[len(want)-1])
			if err != nil || !ok || len(b) == 0 {
				t.Fatalf("step %d: get %s: %v %v", step, want[len(want)-1], ok, err)
			}
		}
		for _, id := range remove {
			if cow.Has(id+"#s0") || len(must(cow.Order(id))) != 0 {
				t.Fatalf("step %d: removed doc %s still present", step, id)
			}
		}
		// slicing across document boundaries
		all := r2all(t, cow)
		var mid []string
		_ = cow.Slice(5, 40, func(b []byte) error { mid = append(mid, string(b)); return nil })
		if !reflect.DeepEqual(mid, all[5:40]) {
			t.Fatalf("step %d: slice(5,40) differs", step)
		}
		// untouched shards are hard links
		links := 0
		for s, f := range cow.ix.Files {
			if f != "" && f == base.ix.Files[s] {
				a, _ := os.Stat(filepath.Join(baseDir, f))
				b, _ := os.Stat(filepath.Join(dst, f))
				if !os.SameFile(a, b) {
					t.Fatalf("shard %s copied, not linked", f)
				}
				links++
			}
		}
		if links < 50 {
			t.Fatalf("step %d: only %d of 64 shards linked", step, links)
		}
		fr.Close()
		base.Close()
		base = cow
		baseDir = dst
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
