package natives

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/rangetable"
)

// jsLower is String.prototype.toLowerCase as goja implements it (goja ≥ 2026-10-10,
// string_unicode.go toLower): full Unicode lowercase mappings via
// x/text cases.Lower(language.Und) (so "İ" → "i̇", unlike strings.ToLower), and the
// ECMAScript Final_Sigma rule for "Σ" (ς at the end of a word, σ elsewhere), with
// case-ignorable runes tested before cased ones. Text is split at each Σ exactly as
// goja does, so the caser sees the same segments.
func jsLower(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	c := lowerCasers.Get().(cases.Caser)
	defer lowerCasers.Put(c)
	var b strings.Builder
	start := 0
	for {
		k := strings.IndexRune(s[start:], 'Σ')
		if k < 0 {
			b.WriteString(c.String(s[start:]))
			return b.String()
		}
		k += start
		b.WriteString(c.String(s[start:k]))
		if isFinalSigma(s, k) {
			b.WriteRune('ς')
		} else {
			b.WriteRune('σ')
		}
		start = k + len("Σ")
	}
}

var lowerCasers = sync.Pool{New: func() any { return cases.Lower(language.Und) }}

// The Unicode Case_Ignorable and Cased properties, built like goja builds them.
var (
	caseIgnorable = rangetable.Merge(
		unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk,
		rangetable.New( // Word_Break MidLetter/MidNumLet/Single_Quote (not exposed by package unicode)
			0x0027, 0x002E, 0x003A, 0x00B7, 0x0387, 0x055F, 0x05F4,
			0x2018, 0x2019, 0x2024, 0x2027, 0xFE13, 0xFE52, 0xFE55,
			0xFF07, 0xFF0E, 0xFF1A,
		),
	)
	cased = rangetable.Merge(unicode.Lu, unicode.Ll, unicode.Lt, unicode.Other_Lowercase, unicode.Other_Uppercase)
)

// isFinalSigma reports whether the Σ at byte offset i in s is final: preceded
// (skipping case-ignorables) by a cased rune and not followed (skipping
// case-ignorables) by one.
func isFinalSigma(s string, i int) bool {
	before := false
	for j := i; j > 0; {
		r, n := utf8.DecodeLastRuneInString(s[:j])
		j -= n
		if unicode.Is(caseIgnorable, r) {
			continue
		}
		before = unicode.Is(cased, r)
		break
	}
	if !before {
		return false
	}
	for j := i + len("Σ"); j < len(s); {
		r, n := utf8.DecodeRuneInString(s[j:])
		j += n
		if unicode.Is(caseIgnorable, r) {
			continue
		}
		return !unicode.Is(cased, r)
	}
	return true
}
