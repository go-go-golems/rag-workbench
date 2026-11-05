package natives

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"sort"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// Terms ports rag.js's TermsIndex (index.terms, rag.js "class TermsIndex"):
// metadata values (type, tags, status, …) → key → chunks, searched by checking
// which whole values occur as word sequences in the query. Values arrive
// already extracted and lowercased by JS (String(v).toLowerCase() of
// arr(getPath(meta, key))), so only postings, search and storage are Go.
// Postings hold chunk ordinals instead of id strings: the JS Maps of id arrays
// were ~50 MB on the vault (RAGWB-001 step 25).
type Terms struct {
	Keys []string
	IDs  []string // chunk ids by ordinal
	Vals []termVal
	N    int
	// the build input, kept for delta builds (RAGWB-003 D6): each chunk's doc id
	// and extracted values, in chunk order; a delta rebuilds from these rows
	DocIDs []string
	Rows   [][][]string
}

type termVal struct {
	Val   string
	Words string // " " + trim(val with non-word runs → " ") + " ", precomputed
	Keys  []termPost
}

type termPost struct {
	Key string
	Ord []int32
}

// TermHit is one search result; Why is key → matched value, in insertion order.
type TermHit struct {
	ID    string
	Score float64
	Why   [][2]string
}

// NewTerms builds the index. vals[i][k] holds chunk i's lowercased values for keys[k].
func NewTerms(ids []string, keys []string, vals [][][]string) *Terms {
	t := &Terms{Keys: keys, IDs: ids, N: len(ids)}
	at := map[string]int{}
	for i := range ids {
		for k, key := range keys {
			if k >= len(vals[i]) {
				continue
			}
			for _, v := range vals[i][k] {
				vi, ok := at[v]
				if !ok {
					vi = len(t.Vals)
					at[v] = vi
					t.Vals = append(t.Vals, termVal{Val: v, Words: " " + jsTrim(nonWordToSpace(v)) + " "})
				}
				tv := &t.Vals[vi]
				pi := -1
				for j := range tv.Keys {
					if tv.Keys[j].Key == key {
						pi = j
						break
					}
				}
				if pi < 0 {
					tv.Keys = append(tv.Keys, termPost{Key: key})
					pi = len(tv.Keys) - 1
				}
				tv.Keys[pi].Ord = append(tv.Keys[pi].Ord, int32(i))
			}
		}
	}
	return t
}

// NewTermsRows builds the index and keeps its rows for delta builds.
func NewTermsRows(ids, docIDs []string, keys []string, vals [][][]string) *Terms {
	t := NewTerms(ids, keys, vals)
	t.DocIDs, t.Rows = docIDs, vals
	return t
}

// Delta rebuilds the index for a new chunk set: docOrder is the new canonical
// document order; each document's rows come from the upserts if it has any,
// else from this index, unless it is in gone (removed or replaced). The rows
// are in the order a full build would see them, so the result equals one
// (D6). A non-empty reason means "can't, build fully".
func (t *Terms) Delta(docOrder []string, upIDs, upDocIDs []string, upVals [][][]string, gone []string) (*Terms, string) {
	if len(t.DocIDs) != len(t.IDs) || len(t.Rows) != len(t.IDs) {
		return nil, "base terms index has no rows"
	}
	type span struct{ from, to int }
	baseAt := map[string]span{}
	for i := 0; i < len(t.DocIDs); {
		j := i
		for j < len(t.DocIDs) && t.DocIDs[j] == t.DocIDs[i] {
			j++
		}
		if _, dup := baseAt[t.DocIDs[i]]; dup {
			return nil, "base rows are not grouped by document"
		}
		baseAt[t.DocIDs[i]] = span{i, j}
		i = j
	}
	upAt := map[string][]int{}
	for i, d := range upDocIDs {
		upAt[d] = append(upAt[d], i)
	}
	isGone := make(map[string]bool, len(gone))
	for _, d := range gone {
		isGone[d] = true
	}
	var ids, docIDs []string
	var vals [][][]string
	for _, d := range docOrder {
		if rows, ok := upAt[d]; ok {
			for _, i := range rows {
				ids, docIDs, vals = append(ids, upIDs[i]), append(docIDs, d), append(vals, upVals[i])
			}
			continue
		}
		sp, ok := baseAt[d]
		if !ok || isGone[d] {
			return nil, fmt.Sprintf("no rows for document %s", d)
		}
		ids, docIDs, vals = append(ids, t.IDs[sp.from:sp.to]...), append(docIDs, t.DocIDs[sp.from:sp.to]...), append(vals, t.Rows[sp.from:sp.to]...)
	}
	return NewTermsRows(ids, docIDs, t.Keys, vals), ""
}

// Search ports TermsIndex#search, including its order: score descending, then
// id by goja's String.prototype.localeCompare (root collation over NFD), stable.
func (t *Terms) Search(q string, k int) []TermHit {
	lq := " " + nonWordToSpace(jsLower(q)) + " "
	type acc struct {
		score float64
		why   [][2]string
	}
	scores := map[int32]*acc{}
	order := []int32{}
	for _, v := range t.Vals {
		if len16(jsTrim(v.Words)) < 2 || !strings.Contains(lq, v.Words) {
			continue
		}
		for _, p := range v.Keys {
			w := 1 / math.Log(2+float64(len(p.Ord))/10)
			for _, o := range p.Ord {
				a := scores[o]
				if a == nil {
					a = &acc{}
					scores[o] = a
					order = append(order, o)
				}
				a.score += w
				a.why = setWhy(a.why, p.Key, v.Val)
			}
		}
	}
	col := collate.New(language.Und)
	nfd := make(map[int32]string, len(order))
	for _, o := range order {
		nfd[o] = norm.NFD.String(t.IDs[o])
	}
	sort.SliceStable(order, func(x, y int) bool {
		a, b := scores[order[x]].score, scores[order[y]].score
		if a != b {
			return b < a
		}
		return col.CompareString(nfd[order[x]], nfd[order[y]]) < 0
	})
	if k < len(order) {
		order = order[:max(0, k)]
	}
	out := make([]TermHit, len(order))
	for i, o := range order {
		out[i] = TermHit{ID: t.IDs[o], Score: jsToFixed(scores[o].score, 4), Why: scores[o].why}
	}
	return out
}

// setWhy is {...why, [key]: val}: a new key is appended, an existing one keeps its place.
func setWhy(why [][2]string, key, val string) [][2]string {
	for i := range why {
		if why[i][0] == key {
			why[i][1] = val
			return why
		}
	}
	return append(why, [2]string{key, val})
}

// Values ports TermsIndex#values: value → number of chunks for key, in insertion order.
func (t *Terms) Values(key string) [][2]any {
	out := [][2]any{}
	for _, v := range t.Vals {
		for _, p := range v.Keys {
			if p.Key == key {
				out = append(out, [2]any{v.Val, len(p.Ord)})
			}
		}
	}
	return out
}

// EncodeTerms / DecodeTerms are the artifact codec "terms".
func EncodeTerms(t *Terms) ([]byte, error) {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(t)
	return buf.Bytes(), err
}

func DecodeTerms(b []byte) (*Terms, error) {
	var t Terms
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&t); err != nil {
		return nil, fmt.Errorf("terms artifact: %w", err)
	}
	for _, v := range t.Vals {
		for _, p := range v.Keys {
			for _, o := range p.Ord {
				if int(o) >= len(t.IDs) {
					return nil, fmt.Errorf("terms artifact: ordinal %d out of range", o)
				}
			}
		}
	}
	return &t, nil
}
