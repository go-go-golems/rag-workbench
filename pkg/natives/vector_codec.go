package natives

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"math"
)

// Artifact codec "vectors": a vecIndex (ids, docIds, normalized vectors). The ids
// travel as gob; the vectors as one block of little-endian float32s, which is
// both compact and fast for ~10^7–10^8 floats. Query embedding needs the
// runtime's embedder, so loading yields the raw index and index.vector's
// persist.load binds it (Vectors.restore).

type vecWire struct {
	IDs, DocIDs []string
	Model       string
	Dims        int
	ReqDims     int
	N           int // vectors
}

func EncodeVectors(ix *vecIndex) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(vecWire{IDs: ix.ids, DocIDs: ix.docIDs, Model: ix.model, Dims: ix.dims, ReqDims: ix.reqDims, N: len(ix.vecs)}); err != nil {
		return nil, err
	}
	head := buf.Len()
	out := make([]byte, 8+head+4*len(ix.vecs)*ix.dims)
	binary.LittleEndian.PutUint64(out, uint64(head))
	copy(out[8:], buf.Bytes())
	p := 8 + head
	for _, v := range ix.vecs {
		if len(v) != ix.dims {
			return nil, fmt.Errorf("vectors artifact: vector of %d dims in a %d-dim index", len(v), ix.dims)
		}
		for _, x := range v {
			binary.LittleEndian.PutUint32(out[p:], math.Float32bits(x))
			p += 4
		}
	}
	return out, nil
}

func DecodeVectors(b []byte) (*vecIndex, error) {
	if len(b) < 8 {
		return nil, fmt.Errorf("vectors artifact: too short")
	}
	head := int(binary.LittleEndian.Uint64(b))
	if head < 0 || 8+head > len(b) {
		return nil, fmt.Errorf("vectors artifact: bad header length")
	}
	var w vecWire
	if err := gob.NewDecoder(bytes.NewReader(b[8 : 8+head])).Decode(&w); err != nil {
		return nil, fmt.Errorf("vectors artifact: %w", err)
	}
	body := b[8+head:]
	if len(w.IDs) != w.N || len(w.DocIDs) != w.N || len(body) != 4*w.N*w.Dims {
		return nil, fmt.Errorf("vectors artifact: inconsistent sizes")
	}
	flat := make([]float32, w.N*w.Dims)
	for i := range flat {
		flat[i] = math.Float32frombits(binary.LittleEndian.Uint32(body[4*i:]))
	}
	vecs := make([][]float32, w.N)
	for i := range vecs {
		vecs[i] = flat[i*w.Dims : (i+1)*w.Dims : (i+1)*w.Dims]
	}
	return &vecIndex{ids: w.IDs, docIDs: w.DocIDs, vecs: vecs, model: w.Model, dims: w.Dims, reqDims: w.ReqDims}, nil
}
