package natives

import (
	"regexp"
	"strconv"
	"strings"
)

// A line-by-line Go port of rag.js parseFrontmatter/parseMarkdown (rag.js
// "markdown" section). Output must equal the JS output field for field; the
// only additions are byte/line ranges on blocks (API adjustment A2).

var (
	reFMStart   = regexp.MustCompile(`^---\r?\n`)
	reFMItem    = regexp.MustCompile(`^[` + jsWS + `]+-[` + jsWS + `]+(.*)$`)
	reFMKV      = regexp.MustCompile(`^([A-Za-z0-9_-]+):[` + jsWS + `]*(.*)$`)
	reBracket   = regexp.MustCompile(`^\[.*\]$`)
	reQuoted    = regexp.MustCompile(`^".*"$|^'.*'$`)
	reNumber    = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	reFence     = regexp.MustCompile("^[" + jsWS + "]*(`{3,}|~{3,})[" + jsWS + "]*([A-Za-z0-9_+-]*)")
	reHeading   = regexp.MustCompile(`^(#{1,6})[` + jsWS + `]+(.*?)[` + jsWS + `]*$`)
	reTable     = regexp.MustCompile(`^[` + jsWS + `]*\|`)
	reTableSep  = regexp.MustCompile(`^[` + jsWS + `]*\|[` + jsWS + `:|-]+\|[` + jsWS + `]*$`)
	reTablePipe = regexp.MustCompile(`^\||\|$`)
	reQuote     = regexp.MustCompile(`^[` + jsWS + `]*>`)
	reQuoteCut  = regexp.MustCompile(`^[` + jsWS + `]*>[` + jsWS + `]?`)
	reCallout   = regexp.MustCompile(`^\[!(\w+)\][` + jsWS + `]*(.*)$`)
	reListItem  = regexp.MustCompile(`^[` + jsWS + `]*([-*+]|\d+\.)[` + jsWS + `]+`)
	reListCont  = regexp.MustCompile(`^[` + jsWS + `]{2,}[^` + jsWS + `]`)
	reWikiLink  = regexp.MustCompile(`\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|[^\]]*)?\]\]`)
	reMdSuffix  = regexp.MustCompile(`(?i)\.md$`)

	rePlainCode  = regexp.MustCompile("`([^`]*)`")
	rePlainBold  = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	rePlainWiki  = regexp.MustCompile(`\[\[([^\]|]+)(\|[^\]]*)?\]\]`)
	rePlainLink  = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	rePlainHashT = regexp.MustCompile(`#+[` + jsWS + `]*$`)
)

// Meta is a frontmatter object with JS insertion order.
type Meta struct {
	Keys   []string
	Values map[string]any // string | float64 | bool | []any
}

func (m *Meta) set(k string, v any) {
	if _, ok := m.Values[k]; !ok {
		m.Keys = append(m.Keys, k)
	}
	m.Values[k] = v
}

func (m *Meta) del(k string) {
	delete(m.Values, k)
	for i, x := range m.Keys {
		if x == k {
			m.Keys = append(m.Keys[:i], m.Keys[i+1:]...)
			return
		}
	}
}

// Range locates text in the original file: half-open byte range, 1-based inclusive lines.
type Range struct {
	ByteStart, ByteEnd, LineStart, LineEnd int
}

func (r Range) union(o Range) Range {
	if r.ByteEnd == 0 && r.LineEnd == 0 {
		return o
	}
	return Range{min(r.ByteStart, o.ByteStart), max(r.ByteEnd, o.ByteEnd), min(r.LineStart, o.LineStart), max(r.LineEnd, o.LineEnd)}
}

type Block struct {
	Kind    string // paragraph | fence | table | callout | quote | list
	Lang    string // fence
	Variant string // callout
	Text    string
	Header  []string   // table
	Rows    [][]string // table
	Range   Range
}

type Section struct {
	Level  int
	Title  string
	Path   []string
	Blocks []Block
	ID     string
	Text   string
}

type Doc struct {
	ID       string
	Name     string
	Title    string
	Meta     Meta
	H1       *string
	Sections []Section
	Links    []string
}

func parseScalar(v string) any {
	v = jsTrim(v)
	if v == "" {
		return ""
	}
	if reBracket.MatchString(v) {
		out := []any{}
		for _, x := range strings.Split(v[1:len(v)-1], ",") {
			if s := parseScalar(x); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	if reQuoted.MatchString(v) {
		return v[1 : len(v)-1]
	}
	if v == "true" {
		return true
	}
	if v == "false" {
		return false
	}
	if reNumber.MatchString(v) && len16(v) < 12 {
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return f
		}
	}
	return v
}

// parseFrontmatter returns the meta and the byte offset where the body starts.
func parseFrontmatter(text string) (Meta, int) {
	meta := Meta{Values: map[string]any{}}
	if !reFMStart.MatchString(text) {
		return meta, 0
	}
	if len(text) < 4 {
		return meta, 0
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return meta, 0
	}
	end += 4
	key := ""
	for _, line := range splitLines(text[4:end]) {
		if m := reFMItem.FindStringSubmatch(line); m != nil && key != "" {
			arr, ok := meta.Values[key].([]any)
			if !ok {
				arr = []any{}
			}
			meta.set(key, append(arr, parseScalar(m[1])))
			continue
		}
		if m := reFMKV.FindStringSubmatch(line); m != nil {
			key = m[1]
			v := parseScalar(m[2])
			if v == "" {
				v = []any{}
			}
			meta.set(key, v)
		}
	}
	for _, k := range append([]string{}, meta.Keys...) {
		if arr, ok := meta.Values[k].([]any); ok && len(arr) == 0 {
			meta.del(k)
		}
	}
	nl := strings.Index(text[end+1:], "\n")
	if nl < 0 {
		return meta, 0 // JS: text.slice(-1 + 1) → whole text
	}
	return meta, end + 1 + nl + 1
}

// splitLines is s.split(/\r?\n/).
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}

func plain(s string) string {
	s = rePlainCode.ReplaceAllString(s, "${1}")
	s = rePlainBold.ReplaceAllString(s, "${1}")
	s = rePlainWiki.ReplaceAllString(s, "${1}")
	s = rePlainLink.ReplaceAllString(s, "${1}")
	if loc := rePlainHashT.FindStringIndex(s); loc != nil {
		s = s[:loc[0]] + s[loc[1]:]
	}
	return jsTrim(s)
}

// ParseMarkdown ports rag.js parseMarkdown(file, p). frontmatter=false skips frontmatter.
func ParseMarkdown(name, text string, frontmatter bool) *Doc {
	meta, bodyStart := Meta{Values: map[string]any{}}, 0
	if frontmatter {
		meta, bodyStart = parseFrontmatter(text)
	}
	body := text[bodyStart:]
	raw := strings.Split(body, "\n")
	lines := make([]string, len(raw))
	offs := make([]int, len(raw)) // byte offset of each body line in text
	o := bodyStart
	for i, r := range raw {
		lines[i] = strings.TrimSuffix(r, "\r")
		offs[i] = o
		o += len(r) + 1
	}
	firstLine := strings.Count(text[:bodyStart], "\n") + 1
	lineRange := func(a, b int) Range { // lines a..b inclusive
		end := offs[b] + len(lines[b])
		return Range{ByteStart: offs[a], ByteEnd: end, LineStart: firstLine + a, LineEnd: firstLine + b}
	}

	sections := []*Section{{Path: []string{}}}
	sec := sections[0]
	var stack []*Section
	var para []string
	paraStart := -1
	var h1 *string
	flush := func(lastLine int) {
		if len(para) > 0 {
			sec.Blocks = append(sec.Blocks, Block{Kind: "paragraph", Text: strings.Join(para, "\n"), Range: lineRange(paraStart, lastLine)})
			para = nil
			paraStart = -1
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if m := reFence.FindStringSubmatch(line); m != nil {
			flush(i - 1)
			marker := m[1]
			start := i
			body2 := []string{}
			i++
			for i < len(lines) && !strings.HasPrefix(jsTrim(lines[i]), marker) {
				body2 = append(body2, lines[i])
				i++
			}
			last := min(i, len(lines)-1)
			sec.Blocks = append(sec.Blocks, Block{Kind: "fence", Lang: m[2], Text: strings.Join(body2, "\n"), Range: lineRange(start, last)})
			continue
		}
		if m := reHeading.FindStringSubmatch(line); m != nil {
			flush(i - 1)
			level, title := len(m[1]), plain(m[2])
			if level == 1 && h1 == nil {
				t := title
				h1 = &t
				continue
			}
			for len(stack) > 0 && stack[len(stack)-1].Level >= level {
				stack = stack[:len(stack)-1]
			}
			path := make([]string, 0, len(stack)+1)
			for _, x := range stack {
				path = append(path, x.Title)
			}
			sec = &Section{Level: level, Title: title, Path: append(path, title)}
			stack = append(stack, sec)
			sections = append(sections, sec)
			continue
		}
		if reTable.MatchString(line) {
			flush(i - 1)
			start := i
			rows := []string{}
			for i < len(lines) && reTable.MatchString(lines[i]) {
				rows = append(rows, lines[i])
				i++
			}
			i--
			data := [][]string{}
			for _, r := range rows {
				if reTableSep.MatchString(r) {
					continue
				}
				cells := strings.Split(reTablePipe.ReplaceAllString(jsTrim(r), ""), "|")
				for j, c := range cells {
					cells[j] = plain(jsTrim(c))
				}
				data = append(data, cells)
			}
			b := Block{Kind: "table", Header: []string{}, Rows: [][]string{}, Text: strings.Join(rows, "\n"), Range: lineRange(start, i)}
			if len(data) > 0 {
				b.Header, b.Rows = data[0], data[1:]
			}
			sec.Blocks = append(sec.Blocks, b)
			continue
		}
		if reQuote.MatchString(line) {
			flush(i - 1)
			start := i
			q := []string{}
			for i < len(lines) && reQuote.MatchString(lines[i]) {
				q = append(q, reQuoteCut.ReplaceAllString(lines[i], ""))
				i++
			}
			i--
			first := ""
			if len(q) > 0 {
				first = q[0]
			}
			if c := reCallout.FindStringSubmatch(first); c != nil {
				parts := []string{}
				for _, x := range append([]string{c[2]}, q[1:]...) {
					if x != "" {
						parts = append(parts, x)
					}
				}
				sec.Blocks = append(sec.Blocks, Block{Kind: "callout", Variant: jsLower(c[1]), Text: strings.Join(parts, "\n"), Range: lineRange(start, i)})
			} else {
				sec.Blocks = append(sec.Blocks, Block{Kind: "quote", Text: strings.Join(q, "\n"), Range: lineRange(start, i)})
			}
			continue
		}
		if reListItem.MatchString(line) {
			flush(i - 1)
			start := i
			items := []string{}
			for i < len(lines) && (reListItem.MatchString(lines[i]) || (reListCont.MatchString(lines[i]) && len(items) > 0)) {
				items = append(items, lines[i])
				i++
			}
			i--
			sec.Blocks = append(sec.Blocks, Block{Kind: "list", Text: strings.Join(items, "\n"), Range: lineRange(start, i)})
			continue
		}
		if jsTrim(line) == "" {
			flush(i - 1)
			continue
		}
		if paraStart < 0 {
			paraStart = i
		}
		para = append(para, line)
	}
	flush(len(lines) - 1)

	id := DocIDFor(name)
	var title string
	if t, ok := meta.Values["title"]; ok && jsTruthy(t) {
		title = jsString(t)
	} else if h1 != nil && *h1 != "" {
		title = *h1
	} else {
		title = reMdSuffix.ReplaceAllString(name, "")
	}
	title = strings.TrimSuffix(strings.TrimPrefix(title, `"`), `"`)

	links := []string{}
	seen := map[string]bool{}
	for _, m := range reWikiLink.FindAllStringSubmatch(text, -1) {
		l := jsTrim(m[1])
		if !seen[l] {
			seen[l] = true
			links = append(links, l)
		}
	}
	doc := &Doc{ID: id, Name: name, Title: title, Meta: meta, H1: h1, Links: links}
	for _, s := range sections {
		last := "intro"
		if len(s.Path) > 0 {
			last = s.Path[len(s.Path)-1]
		}
		s.ID = slug(last)
		texts := make([]string, len(s.Blocks))
		for i, b := range s.Blocks {
			texts[i] = b.Text
		}
		s.Text = strings.Join(texts, "\n\n")
		if jsTrim(s.Text) != "" || s.Level > 0 {
			doc.Sections = append(doc.Sections, *s)
		}
	}
	if doc.Sections == nil {
		doc.Sections = []Section{}
	}
	return doc
}
