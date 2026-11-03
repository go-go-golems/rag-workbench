package natives

import (
	"strconv"
	"strings"
)

// A Go port of rag.js chunkSections / splitBlock / blocksText / makeChunk /
// pickMeta / assignIds ("chunking" section). Chunk ranges (A2) are the union
// of the source ranges of the blocks a chunk covers.

// ChunkParams mirrors rag.sections({...}).
type ChunkParams struct {
	Depth         int  // default 3
	MaxTokens     int  // default 450
	MinTokens     int  // default 30
	DropMermaid   bool // default true
	PrefixHeading bool
	Overlap       int // tokens of the previous part prepended (0 = off)
}

func DefaultChunkParams() ChunkParams {
	return ChunkParams{Depth: 3, MaxTokens: 450, MinTokens: 30, DropMermaid: true}
}

// Chunk is one section chunk (field order follows rag.js makeChunk).
type Chunk struct {
	ID          string
	DocID       string
	Title       string
	HeadingPath []string
	Kind        string
	Text        string
	Meta        Meta
	Callout     string
	HasCode     bool
	HasTable    bool
	Tokens      int
	Part, Parts int
	Ord         int
	Range       Range
}

var metaSkip = map[string]bool{"title": true, "created": true, "updated": true, "source_ticket_path": true,
	"source_design_doc": true, "related_vault_notes": true, "derived-from": true}

func pickMeta(m Meta) Meta {
	out := Meta{Values: map[string]any{}}
	for _, k := range m.Keys {
		v := m.Values[k]
		if metaSkip[k] {
			continue
		}
		if s, ok := v.(string); ok && len16(s) > 120 {
			continue
		}
		if arr, ok := v.([]any); ok {
			long := false
			for _, x := range arr {
				if s, ok := x.(string); ok && len16(s) > 120 {
					long = true
					break
				}
			}
			if long {
				continue
			}
		}
		out.set(k, v)
	}
	return out
}

func blocksTokens(bs []Block) int {
	texts := make([]string, len(bs))
	for i, b := range bs {
		texts[i] = b.Text
	}
	return tokens(strings.Join(texts, "\n"))
}

func splitBlock(b Block, max int) []Block {
	if tokens(b.Text) <= max {
		return []Block{b}
	}
	atomic := b.Kind == "fence" || b.Kind == "table"
	if atomic && tokens(b.Text) <= max*3 {
		return []Block{b}
	}
	sep := " "
	var units []string
	if atomic || b.Kind == "list" {
		units = strings.Split(b.Text, "\n")
		sep = "\n"
	} else {
		units = splitSentences(b.Text)
	}
	out := []Block{}
	cur := []string{}
	for _, u := range units {
		// rag.js: tokens(cur.join(' ') + u) — joined with a space and no separator before u
		if len(cur) > 0 && tokens(strings.Join(cur, " ")+u) > max {
			nb := b
			nb.Text = strings.Join(cur, sep)
			out = append(out, nb)
			cur = nil
		}
		cur = append(cur, u)
	}
	if len(cur) > 0 {
		nb := b
		nb.Text = strings.Join(cur, sep)
		out = append(out, nb)
	}
	return out
}

func blocksText(bs []Block) string {
	parts := make([]string, len(bs))
	for i, b := range bs {
		switch b.Kind {
		case "fence":
			parts[i] = "```" + b.Lang + "\n" + b.Text + "\n```"
		case "callout":
			parts[i] = "[" + b.Variant + "] " + b.Text
		default:
			parts[i] = b.Text
		}
	}
	return strings.Join(parts, "\n\n")
}

func makeChunk(doc *Doc, path []string, blocks []Block, text string, part, parts int) Chunk {
	callouts := []string{}
	c := Chunk{DocID: doc.ID, Title: doc.Title, HeadingPath: path, Kind: "section", Text: text, Meta: pickMeta(doc.Meta),
		Tokens: tokens(text), Part: part, Parts: parts}
	for _, b := range blocks {
		switch b.Kind {
		case "callout":
			callouts = append(callouts, b.Text)
		case "fence":
			c.HasCode = true
		case "table":
			c.HasTable = true
		}
		c.Range = c.Range.union(b.Range)
	}
	c.Callout = strings.Join(callouts, "\n")
	return c
}

// ChunkSections ports chunkSections(doc, p) for one document.
func ChunkSections(doc *Doc, p ChunkParams) []Chunk {
	type group struct {
		path   []string
		blocks []Block
	}
	groups := []*group{}
	for _, s := range doc.Sections {
		blocks := make([]Block, 0, len(s.Blocks))
		for _, b := range s.Blocks {
			if p.DropMermaid && b.Kind == "fence" && b.Lang == "mermaid" {
				continue
			}
			blocks = append(blocks, b)
		}
		if s.Level == 0 || s.Level <= p.Depth || len(groups) == 0 {
			groups = append(groups, &group{path: s.Path, blocks: blocks})
		} else {
			g := groups[len(groups)-1]
			g.blocks = append(g.blocks, Block{Kind: "paragraph", Text: s.Title})
			g.blocks = append(g.blocks, blocks...)
		}
	}
	out := []Chunk{}
	for _, g := range groups {
		if len(g.blocks) == 0 {
			continue
		}
		parts := [][]Block{}
		cur := []Block{}
		for _, ob := range g.blocks {
			for _, b := range splitBlock(ob, p.MaxTokens) {
				if len(cur) > 0 && blocksTokens(cur)+tokens(b.Text) > p.MaxTokens {
					parts = append(parts, cur)
					cur = []Block{}
				}
				cur = append(cur, b)
			}
		}
		if len(cur) > 0 {
			parts = append(parts, cur)
		}
		if len(parts) > 1 && blocksTokens(parts[len(parts)-1]) < p.MinTokens {
			last := parts[len(parts)-1]
			parts = parts[:len(parts)-1]
			parts[len(parts)-1] = append(parts[len(parts)-1], last...)
		}
		for i, blocks := range parts {
			text := blocksText(blocks)
			if p.PrefixHeading && len(g.path) > 0 {
				text = strings.Join(g.path, " › ") + "\n" + text
			}
			if p.Overlap > 0 && i > 0 {
				text = clip(slice16(blocksText(parts[i-1]), -p.Overlap*4, len16(blocksText(parts[i-1]))), p.Overlap*4) + "\n" + text
			}
			out = append(out, makeChunk(doc, g.path, blocks, text, i+1, len(parts)))
		}
	}
	return out
}

// AssignIDs ports assignIds over the node's whole chunk list.
func AssignIDs(chunks []Chunk) {
	seen := map[string]int{}
	for i := range chunks {
		c := &chunks[i]
		base := c.ID
		if base == "" {
			last := "intro"
			if len(c.HeadingPath) > 0 && c.HeadingPath[len(c.HeadingPath)-1] != "" {
				last = c.HeadingPath[len(c.HeadingPath)-1]
			}
			prefix := ""
			if c.Kind != "section" {
				prefix = slug(c.Kind) + "-"
			}
			base = c.DocID + "#" + prefix + slug(last)
		}
		n := seen[base]
		seen[base] = n + 1
		if n > 0 {
			c.ID = base + "-" + strconv.Itoa(n+1)
		} else {
			c.ID = base
		}
		c.Ord = i
	}
}
