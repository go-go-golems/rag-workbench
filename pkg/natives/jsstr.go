package natives

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Helpers that reproduce JavaScript string semantics, so Go ports of rag.js
// functions produce byte-identical results (lengths are UTF-16 code units,
// \s and trim() use the ECMAScript whitespace set).

// jsWS is the body of a regexp character class equal to JS \s.
const jsWS = `\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// len16 is String.prototype.length.
func len16(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// tokens is rag.js's estimate: ceil(length / 4).
func tokens(s string) int { return (len16(s) + 3) / 4 }

// slice16 is String.prototype.slice(start, end) with UTF-16 indices (negative
// indices count from the end). Lone surrogates produced by cutting a pair
// become U+FFFD, which is what a JS string would serialize to anyway.
func slice16(s string, start, end int) string {
	u := utf16.Encode([]rune(s))
	n := len(u)
	norm := func(i int) int {
		if i < 0 {
			i += n
		}
		return max(0, min(i, n))
	}
	a, b := norm(start), norm(end)
	if a >= b {
		return ""
	}
	return string(utf16.Decode(u[a:b]))
}

// clip is rag.js clip(s, n).
func clip(s string, n int) string {
	if len16(s) > n {
		return slice16(s, 0, n-1) + "…"
	}
	return s
}

// slug is rag.js slug(): lowercase, drop quotes, non-alnum runs → '-', trim '-', 64 chars, 'x' if empty.
func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r == '`' || r == '\'' || r == '"' || r == '’':
			continue
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	out := b.String()
	if len(out) > 64 { // ASCII only at this point; JS slices after trimming dashes, so a trailing '-' may remain
		out = out[:64]
	}
	if out == "" {
		return "x"
	}
	return out
}

// jsString is String(v) for the scalar/array values parseScalar produces.
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e21 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return ""
}

// jsTruthy is Boolean(v) for parseScalar values.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true // arrays and objects
}

// splitSentences is text.split(/(?<=[.!?])\s+/).
func splitSentences(text string) []string {
	out := []string{}
	start := 0
	prev := rune(-1)
	for i := 0; i < len(text); {
		r, w := utf8.DecodeRuneInString(text[i:])
		if isJSSpace(r) && (prev == '.' || prev == '!' || prev == '?') {
			out = append(out, text[start:i])
			j := i
			for j < len(text) {
				r2, w2 := utf8.DecodeRuneInString(text[j:])
				if !isJSSpace(r2) {
					break
				}
				j += w2
			}
			start = j
			i = j
			prev = -1
			continue
		}
		prev = r
		i += w
	}
	return append(out, text[start:])
}
