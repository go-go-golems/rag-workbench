package natives

import (
	"bytes"
	"encoding/gob"
	"fmt"

	"github.com/dop251/goja"
)

// Artifact codecs "docs" and "chunks": the native parse.markdown and chunk.sections
// outputs carry their Go slices as a hidden handle on the JS array, so storing them
// is a gob of the Go structs and loading rebuilds the JS objects with the same
// docToJS/chunkToJS the components use (no JSON round trip through the VM).

func init() {
	gob.Register([]any{}) // Meta values: string | float64 | bool | []any
}

func EncodeDocs(docs []*Doc) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(docs); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecodeDocs(b []byte) ([]*Doc, error) {
	var docs []*Doc
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&docs); err != nil {
		return nil, fmt.Errorf("docs artifact: %w", err)
	}
	for _, d := range docs {
		if d.Meta.Values == nil {
			d.Meta.Values = map[string]any{}
		}
	}
	return docs, nil
}

func EncodeChunks(chunks []Chunk) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(chunks); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecodeChunks(b []byte) ([]Chunk, error) {
	var chunks []Chunk
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&chunks); err != nil {
		return nil, fmt.Errorf("chunks artifact: %w", err)
	}
	for i := range chunks {
		if chunks[i].Meta.Values == nil {
			chunks[i].Meta.Values = map[string]any{}
		}
	}
	return chunks, nil
}

// docsArray / chunksArray build the JS output of the native components (with the handle).
func docsArray(vm *goja.Runtime, docs []*Doc) *goja.Object {
	out := make([]any, len(docs))
	for i, d := range docs {
		out[i] = docToJS(vm, d)
	}
	arr := vm.NewArray(out...)
	_ = arr.DefineDataProperty(goHandleKey, vm.ToValue(docs), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	return arr
}

func chunksArray(vm *goja.Runtime, chunks []Chunk) *goja.Object {
	out := make([]any, len(chunks))
	for i := range chunks {
		out[i] = chunkToJS(vm, &chunks[i])
	}
	arr := vm.NewArray(out...)
	_ = arr.DefineDataProperty(goHandleKey, vm.ToValue(chunks), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	return arr
}
