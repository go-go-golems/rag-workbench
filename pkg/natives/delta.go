package natives

import (
	"fmt"
	"os"
)

// Delta builds of parse.markdown and chunk.sections (RAGWB-003 §6.5): the new
// output is a copy-on-write version of the base records; the delta tells the
// next node what changed. A non-empty reason means "can't, build fully".

// ParseDelta replaces the docs of the parsed (added or changed) files and
// drops those of removed files. Returns the new records, the removed doc ids.
func ParseDelta(base *Records2, docs []*Doc, removedNames []string, dir string) (*Records2, []string, string, error) {
	seen := map[string]string{}
	for _, d := range docs {
		if other, ok := seen[d.ID]; ok {
			return nil, nil, fmt.Sprintf("%s and %s have the same doc id %s", other, d.Name, d.ID), nil
		}
		seen[d.ID] = d.Name
		if info, ok := base.DocInfo(d.ID); ok && (info.Name != d.Name || info.N != 1) {
			return nil, nil, fmt.Sprintf("doc id %s is shared by %s and %s", d.ID, info.Name, d.Name), nil
		}
	}
	remove := []string{}
	for _, name := range removedNames {
		if info, ok := base.DocByName(name); ok {
			if info.N != 1 {
				return nil, nil, fmt.Sprintf("doc id %s is shared by several files", info.ID), nil
			}
			remove = append(remove, info.ID)
		}
	}
	up, err := DocsToR2(docs)
	if err != nil {
		return nil, nil, "", err
	}
	r, err := cowOpen(base, dir, remove, up)
	return r, remove, "", err
}

// ChunkDelta chunks the upserted docs and drops the removed ones. Returns the
// new records, the new chunks, the docs whose old chunks are gone (removed or
// upserted) and the ids of those old chunks.
func ChunkDelta(base *Records2, docs []*Doc, removeDocs []string, p ChunkParams, dir string) (*Records2, []Chunk, []string, []string, string, error) {
	var chunks []Chunk
	gone := append([]string{}, removeDocs...)
	upserts := make([]R2Doc, 0, len(docs))
	for _, d := range docs {
		if info, ok := base.DocInfo(d.ID); ok && info.Name != d.Name {
			return nil, nil, nil, nil, fmt.Sprintf("doc id %s is shared by %s and %s", d.ID, info.Name, d.Name), nil
		}
		cs := ChunkSections(d, p)
		for j := range cs {
			cs[j].DocName = d.Name
		}
		AssignIDs(cs) // ids and ord are per document (RAGWB-003 Phase 1): equal to a full build's
		r2, err := ChunksToR2(cs)
		if err != nil {
			return nil, nil, nil, nil, "", err
		}
		if len(r2) > 1 {
			return nil, nil, nil, nil, fmt.Sprintf("doc %s produced chunks of several docs", d.Name), nil
		}
		if len(r2) == 0 {
			r2 = []R2Doc{{ID: d.ID, Name: d.Name}} // no chunks: the doc drops out
		}
		upserts = append(upserts, r2[0])
		chunks = append(chunks, cs...)
		gone = append(gone, d.ID)
	}
	var removeIDs []string
	for _, id := range gone {
		ids, err := base.Order(id)
		if err != nil {
			return nil, nil, nil, nil, "", err
		}
		removeIDs = append(removeIDs, ids...)
	}
	r, err := cowOpen(base, dir, removeDocs, upserts)
	if chunks == nil {
		chunks = []Chunk{}
	}
	return r, chunks, gone, removeIDs, "", err
}

func cowOpen(base *Records2, dir string, remove []string, upsert []R2Doc) (*Records2, error) {
	if err := CowRecords2(base, dir, remove, upsert); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return OpenRecords2(dir)
}
