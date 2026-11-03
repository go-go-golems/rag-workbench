package natives

import (
	"math"
	"math/big"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A Go port of rag.js tokenize/stem/Bm25Index ("text" and "indexes"
// sections). Scores, tie order and rounding equal the goja-hosted JS
// implementation (see bm25_parity_test.go); the word class is rag.util.LN,
// which is [\p{L}\p{N}] on goja ≥ e1fc788 (go-go-goja ≥ v0.10.7) and in browsers.

var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields("a an and are as at be but by can could did do does for from had has have how i if in into is it its " +
		"me more most my no not of on or our out so such than that the their them then there these they this those through to " +
		"too up us was we were what when where which while who why will with would you your about after again all also any " +
		"because been before being between both each few further here just only other own same should some very via over under") {
		m[w] = true
	}
	return m
}()

// isLN is [\p{L}\p{N}]: goja hands \p under /u to regexp2/v2, which builds
// the class from unicode.Categories, the same tables IsLetter/IsNumber use.
func isLN(r rune) bool {
	if r < utf8.RuneSelf {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	}
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func hasASCIIDigit(s string) bool { return strings.ContainsAny(s, "0123456789") }

// splitIdent is r.split(/_+|(?<=[a-z0-9])(?=[A-Z])/).filter(Boolean).
func splitIdent(s string) []string {
	out := []string{}
	cur := strings.Builder{}
	prev := rune(-1)
	for _, r := range s {
		if r == '_' {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			prev = r
			continue
		}
		if r >= 'A' && r <= 'Z' && ((prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')) && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
		prev = r
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// stem ports rag.js stem(w). Lengths are JS lengths (UTF-16 units): with the
// [\p{L}\p{N}] class, words can hold astral letters such as 𝐾 (two units).
// Every cut removes ASCII or BMP runes (one unit each), so cutting runes is safe.
func stem(w string) string {
	n := func() int { return len16(w) }
	if n() <= 3 || hasASCIIDigit(w) {
		return w
	}
	cut := func(k int) { r := []rune(w); w = string(r[:len(r)-k]) }
	if strings.HasSuffix(w, "'s") {
		cut(2)
	}
	switch {
	case strings.HasSuffix(w, "ies") && n() > 4:
		cut(3)
		w += "y"
	case strings.HasSuffix(w, "sses"):
		cut(2)
	case strings.HasSuffix(w, "xes") || strings.HasSuffix(w, "ches") || strings.HasSuffix(w, "shes") || strings.HasSuffix(w, "zes"):
		cut(2)
	case strings.HasSuffix(w, "s") && !(strings.HasSuffix(w, "ss") || strings.HasSuffix(w, "us") || strings.HasSuffix(w, "is") || strings.HasSuffix(w, "ys")) && n() > 4:
		cut(1)
	}
	switch {
	case strings.HasSuffix(w, "ing") && n() > 6:
		cut(3)
	case strings.HasSuffix(w, "ed") && n() > 5:
		cut(2)
	case strings.HasSuffix(w, "ly") && n() > 5:
		cut(2)
	}
	// /([^aeiouls])\1$/ without the u flag compares the last two UTF-16 units; for a
	// trailing astral rune those are its two surrogate halves, which never match.
	if r := []rune(w); len(r) >= 2 && r[len(r)-1] < 0x10000 && r[len(r)-1] == r[len(r)-2] && !strings.ContainsRune("aeiouls", r[len(r)-1]) {
		cut(1)
	}
	if strings.HasSuffix(w, "e") && n() > 4 {
		cut(1)
	}
	return w
}

// Tokenize ports tokenize(text, {stem}).
func Tokenize(text string, doStem bool) []string {
	out := []string{}
	start := -1
	emit := func(raw string) {
		parts := splitIdent(raw)
		words := parts
		if len(parts) > 1 {
			words = append(append([]string{}, parts...), strings.Join(parts, ""))
		}
		for _, w := range words {
			w = jsLower(w)
			if len16(w) < 2 && !hasASCIIDigit(w) { // JS w.length: an astral letter alone counts 2
				continue
			}
			if stopWords[w] {
				continue
			}
			if doStem {
				w = stem(w)
			}
			out = append(out, w)
		}
	}
	for i, r := range text {
		if isLN(r) || r == '_' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			emit(text[start:i])
			start = -1
		}
	}
	if start >= 0 {
		emit(text[start:])
	}
	return out
}

// nonWordToSpace is s.replace(RE_NONWORD, ' ').
func nonWordToSpace(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if isLN(r) {
			b.WriteRune(r)
			in = false
		} else if !in {
			b.WriteByte(' ')
			in = true
		}
	}
	return b.String()
}

// jsToFixed is +x.toFixed(digits): exact decimal value, ties rounded away from zero.
func jsToFixed(x float64, digits int) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) >= 1e21 {
		return x
	}
	exact := new(big.Float).SetPrec(2000).SetFloat64(math.Abs(x)).Text('f', 1100)
	dot := strings.IndexByte(exact, '.')
	intPart, frac := exact[:dot], exact[dot+1:]
	keep := []byte(intPart + frac[:digits])
	if frac[digits] >= '5' { // half-up on the exact expansion (JS picks the larger n on ties)
		i := len(keep) - 1
		for ; i >= 0; i-- {
			if keep[i] == '9' {
				keep[i] = '0'
				continue
			}
			keep[i]++
			break
		}
		if i < 0 {
			keep = append([]byte{'1'}, keep...)
		}
	}
	s := string(keep)
	s = s[:len(s)-digits] + "." + s[len(s)-digits:]
	f := new(big.Float).SetPrec(200)
	f.SetString(s)
	v, _ := f.Float64()
	if x < 0 {
		v = -v
	}
	return v
}

// Field is one weighted field of a BM25 index, in declaration order.
type Field struct {
	Path   string
	Weight float64
}

// Bm25 is the in-memory index (same layout as the JS class).
type Bm25 struct {
	Fields []Field
	K1, B  float64
	Stem   bool
	IDs    []string
	DocOf  []string
	Len    []float64
	Post   map[string][]posting
	terms  int
	N      int
	Avg    float64
}

type posting struct {
	i  int
	tf float64
}

// NewBm25 builds the index; text(i, f) returns chunk i's text for fields[f] (false = skip, like null/”).
func NewBm25(n int, id, docID func(i int) string, text func(i, f int) (string, bool), fields []Field, k1, b float64, doStem bool) *Bm25 {
	ix := &Bm25{Fields: fields, K1: k1, B: b, Stem: doStem, Post: map[string][]posting{}}
	for i := 0; i < n; i++ {
		ix.IDs = append(ix.IDs, id(i))
		ix.DocOf = append(ix.DocOf, docID(i))
		tf := map[string]float64{}
		order := []string{}
		var length float64
		for fi, f := range fields {
			v, ok := text(i, fi)
			if !ok {
				continue
			}
			ts := Tokenize(v, doStem)
			length += float64(len(ts)) * f.Weight
			for _, t := range ts {
				if _, seen := tf[t]; !seen {
					order = append(order, t)
				}
				tf[t] += f.Weight
			}
		}
		ix.Len = append(ix.Len, length)
		for _, t := range order {
			ix.Post[t] = append(ix.Post[t], posting{i, tf[t]})
		}
	}
	ix.N = len(ix.IDs)
	var sum float64
	for _, l := range ix.Len {
		sum += l
	}
	ix.Avg = sum / float64(max(1, ix.N))
	ix.terms = len(ix.Post)
	return ix
}

type queryTerm struct {
	t string
	w float64
}

// Synonym is one entry of a synonyms table (key → expansions), in table order.
type Synonym struct {
	Key  string
	Vals []string
}

func (ix *Bm25) queryTerms(q string, syn []Synonym) []queryTerm {
	return queryTermsFor(q, syn, ix.Stem)
}

// queryTermsFor is the query side shared by both BM25 backends: q's distinct
// tokens (weight 1), then the tokens of synonym expansions whose key occurs in
// q (weight 0.5), in first-seen order.
func queryTermsFor(q string, syn []Synonym, stem bool) []queryTerm {
	out := []queryTerm{}
	idx := map[string]int{}
	for _, t := range Tokenize(q, stem) {
		if _, ok := idx[t]; !ok {
			idx[t] = len(out)
			out = append(out, queryTerm{t, 1})
		}
	}
	if len(syn) > 0 {
		lq := " " + nonWordToSpace(jsLower(q)) + " "
		for _, s := range syn {
			kk := " " + jsTrim(nonWordToSpace(jsLower(s.Key))) + " "
			if jsTrim(kk) == "" || !strings.Contains(lq, kk) {
				continue
			}
			for _, v := range s.Vals {
				for _, t := range Tokenize(v, stem) {
					if _, ok := idx[t]; !ok {
						idx[t] = len(out)
						out = append(out, queryTerm{t, 0.5})
					}
				}
			}
		}
	}
	return out
}

// Why is one term's contribution, rounded like rag.js (+s.toFixed(2)).
type Why struct {
	Term  string
	Score float64
}

// Hit is one search result.
type Hit struct {
	ID    string
	Score float64 // rounded to 4 decimals like rag.js
	Why   []Why   // top 5, descending
}

// Search ports Bm25Index#search; only (optional) restricts to chunk indexes.
func (ix *Bm25) Search(q string, k int, syn []Synonym, only map[int]bool) []Hit {
	type acc struct {
		score float64
		why   []Why // raw (unrounded) contributions in term order
	}
	scores := map[int]*acc{}
	order := []int{}
	for _, qt := range ix.queryTerms(q, syn) {
		pl := ix.Post[qt.t]
		if len(pl) == 0 {
			continue
		}
		df := float64(len(pl))
		idf := math.Log(1 + (float64(ix.N)-df+0.5)/(df+0.5))
		for _, p := range pl {
			if only != nil && !only[p.i] {
				continue
			}
			// float64() conversions force IEEE rounding of each product (no FMA fusion), as in JS.
			num := float64(float64(qt.w*idf) * float64(p.tf*(ix.K1+1)))
			den := p.tf + float64(ix.K1*float64(1-ix.B+float64(ix.B*ix.Len[p.i])/ix.Avg))
			s := num / den
			a := scores[p.i]
			if a == nil {
				a = &acc{}
				scores[p.i] = a
				order = append(order, p.i)
			}
			a.score += s
			a.why = append(a.why, Why{qt.t, s})
		}
	}
	sort.SliceStable(order, func(x, y int) bool { return scores[order[x]].score > scores[order[y]].score })
	if k < len(order) {
		order = order[:max(0, k)]
	}
	out := make([]Hit, len(order))
	for n, i := range order {
		a := scores[i]
		why := make([]Why, len(a.why))
		for j, w := range a.why {
			why[j] = Why{w.Term, jsToFixed(w.Score, 2)}
		}
		// rag.js keeps `why` in a plain object, whose enumeration puts array-index keys
		// (e.g. the term "2026") first in ascending order; the stable sort sees that order.
		sort.SliceStable(why, func(x, y int) bool {
			ix, xok := arrayIndex(why[x].Term)
			iy, yok := arrayIndex(why[y].Term)
			if xok && yok {
				return ix < iy
			}
			return xok && !yok
		})
		sort.SliceStable(why, func(x, y int) bool { return why[x].Score > why[y].Score })
		if len(why) > 5 {
			why = why[:5]
		}
		out[n] = Hit{ID: ix.IDs[i], Score: jsToFixed(a.score, 4), Why: why}
	}
	return out
}

// SearchIn ports Bm25Index#searchIn.
func (ix *Bm25) SearchIn(docID, q string, k int, syn []Synonym) []Hit {
	only := map[int]bool{}
	for i, d := range ix.DocOf {
		if d == docID {
			only[i] = true
		}
	}
	return ix.Search(q, k, syn, only)
}

// arrayIndex reports whether s is a canonical array index ("0", "17"; not "017"), as JS orders those keys first.
func arrayIndex(s string) (uint64, bool) {
	if s == "" || len(s) > 10 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + uint64(s[i]-'0')
	}
	return n, n < 4294967295
}
