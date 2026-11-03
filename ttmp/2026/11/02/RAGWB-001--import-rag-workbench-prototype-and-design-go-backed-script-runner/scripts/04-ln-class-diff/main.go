// 04-ln-class-diff: count runes where the natives isLN fallback class and \p{L}|\p{N} disagree.
// Usage: go run . <vault-dir>
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func isLN(r rune) bool { // copy of pkg/natives/bm25.go isLN
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == 0xAA, r == 0xB5, r == 0xBA:
		return true
	case r >= 0xC0 && r <= 0xD6, r >= 0xD8 && r <= 0xF6, r >= 0xF8 && r <= 0x24F:
		return true
	case r >= 0x370 && r <= 0x3FF, r >= 0x400 && r <= 0x4FF, r >= 0x590 && r <= 0x5FF, r >= 0x600 && r <= 0x6FF:
		return true
	case r >= 0x3040 && r <= 0x30FF, r >= 0x3400 && r <= 0x4DBF, r >= 0x4E00 && r <= 0x9FFF, r >= 0xAC00 && r <= 0xD7AF:
		return true
	}
	return false
}

func main() {
	counts := map[rune]int{}
	files := map[rune]map[string]bool{}
	nfiles := 0
	_ = filepath.WalkDir(os.Args[1], func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() && strings.HasPrefix(d.Name(), ".") && p != os.Args[1] {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		b, _ := os.ReadFile(p)
		nfiles++
		for _, r := range string(b) {
			if isLN(r) != (unicode.IsLetter(r) || unicode.IsNumber(r)) {
				counts[r]++
				if files[r] == nil {
					files[r] = map[string]bool{}
				}
				files[r][p] = true
			}
		}
		return nil
	})
	type kv struct {
		r rune
		n int
	}
	var all []kv
	affected := map[string]bool{}
	for r, n := range counts {
		all = append(all, kv{r, n})
		for f := range files[r] {
			affected[f] = true
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n })
	fmt.Printf("%d .md files, %d distinct differing runes, %d files affected\n", nfiles, len(all), len(affected))
	for i, e := range all {
		if i == 15 {
			break
		}
		dir := "\\p only"
		if isLN(e.r) {
			dir = "fallback only"
		}
		fmt.Printf("  U+%04X %q  %6d occurrences in %3d files  (%s)\n", e.r, e.r, e.n, len(files[e.r]), dir)
	}
}
