package natives

import (
	"fmt"
	"os"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
)

// VerifyStore checks that every output stored in s's namespace can be opened
// and holds what its metadata says (RAGWB-003 `ragwb store verify`): record
// stores open and count right, every records2 shard exists, bleve indexes open
// with the expected document count, blobs decode. It returns one line per problem.
func VerifyStore(s *artifacts.Store) (checked int, problems []string, err error) {
	ms, err := artifacts.List(s.Dir)
	if err != nil {
		return 0, nil, err
	}
	for _, m := range ms {
		if m.NS != s.NS {
			continue
		}
		checked++
		if p := verifyOne(s, m); p != "" {
			problems = append(problems, fmt.Sprintf("%s %s (%s): %s", m.Key, m.Type, m.Codec, p))
		}
	}
	return checked, problems, nil
}

func verifyOne(s *artifacts.Store, m artifacts.Meta) string {
	if m.Kind == "" {
		_, b, err := s.Load(m.Key)
		if err != nil {
			return err.Error()
		}
		var derr error
		switch m.Codec {
		case "manifest":
			_, derr = DecodeManifest(b)
		case "terms":
			_, derr = DecodeTerms(b)
		case "docs":
			_, derr = DecodeDocs(b)
		case "chunks":
			_, derr = DecodeChunks(b)
		case "bm25":
			_, derr = DecodeBm25(b)
		case "vectors":
			_, derr = DecodeVectors(b)
		}
		if derr != nil {
			return derr.Error()
		}
		return ""
	}
	_, path, err := s.Path(m.Key)
	if err != nil {
		return err.Error()
	}
	if _, err := os.Stat(path); err != nil {
		return "payload missing: " + err.Error()
	}
	switch m.Codec {
	case "records2":
		r, err := OpenRecords2(path)
		if err != nil {
			return err.Error()
		}
		defer r.Close()
		if r.Len() != m.Count {
			return fmt.Sprintf("%d records, metadata says %d", r.Len(), m.Count)
		}
		n := 0
		if err := r.Slice(0, r.Len(), func([]byte) error { n++; return nil }); err != nil {
			return err.Error()
		}
		if n != m.Count {
			return fmt.Sprintf("read %d records, want %d", n, m.Count)
		}
	case "records":
		r, err := OpenChunkRecords(path)
		if err != nil {
			return err.Error()
		}
		defer r.Close()
		if r.Len() != m.Count {
			return fmt.Sprintf("%d records, metadata says %d", r.Len(), m.Count)
		}
	case "bleve":
		ix, err := OpenBleveBm25(path)
		if err != nil {
			return err.Error()
		}
		defer ix.Close()
		n, err := ix.idx.DocCount()
		if err != nil {
			return err.Error()
		}
		if int(n) != ix.Meta.N || ix.Meta.N != m.Count {
			return fmt.Sprintf("%d documents, index meta says %d, metadata %d", n, ix.Meta.N, m.Count)
		}
	}
	return ""
}
