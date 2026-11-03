# 07 — bleve on the vault chunks (RAGWB-001 step 26)

A nested module (own go.mod) so the root module does not depend on bleve.

- `compat/`: rag-workbench (go-go-goja v0.10.7, goja master) and goja-bleve compile together.
- `probe/`: index the stored vault chunks (P8 store, codec `chunks`) into a persistent bleve
  index (en analyzer, BM25 scoring, stored fields), then open it in a fresh process and query.

```
cd ttmp/2026/11/02/RAGWB-001-…/scripts/07-bleve-vault-probe
GOWORK=off go build -o /tmp/probe ./probe
/tmp/probe ~/.ragwb/store/<ns>/ed/<chunks-key>.bin /tmp/bleve-vault build
/tmp/probe x /tmp/bleve-vault query
```
