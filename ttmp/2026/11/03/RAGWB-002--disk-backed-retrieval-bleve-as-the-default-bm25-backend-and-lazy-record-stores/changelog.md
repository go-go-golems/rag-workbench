# Changelog

## 2026-11-03

- Initial workspace created

## 2026-11-03

Ticket opened; intern design/implementation guide written: bleve as the default index.bm25 backend in Go runtimes (postings on disk, own BM25F scorer for parity), bbolt record stores for docs/chunks, artifact store file/dir payloads; phases 0–5

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/ttmp/2026/11/03/RAGWB-002--disk-backed-retrieval-bleve-as-the-default-bm25-backend-and-lazy-record-stores/design-doc/01-intern-guide-disk-backed-retrieval-with-bleve-and-lazy-record-stores.md — the guide

## 2026-11-03

Guide uploaded to reMarkable: /ai/2026/11/03/RAGWB-002/RAGWB-002 Disk-backed Retrieval Intern Guide.pdf

## 2026-11-03

Guide v2 (two browser pages section) uploaded: /ai/2026/11/03/RAGWB-002/RAGWB-002 Disk-backed Retrieval Intern Guide v2.pdf

## 2026-11-03

D2 changed per user: bleve's own BM25 scoring (rankings may change) behind a quality gate vs the memory backend; D3 now one pre-tokenized field per weighted field with boosts; guide §6.4, plan, risks, tests updated

## 2026-11-03

Implemented: bleve default index.bm25 (persisted, read-only), chunk records (bbolt), Go terms index (exact parity), file/dir artifact payloads. Vault warm CLI 2.2 s/1.4 GB → 0.16 s/90 MB; served app open 2.2 s → 77 ms, live heap 838 → 15 MB; median query 14.7 → 10.7 ms. Quality gate passed (diary step 4).

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/bleve_bm25.go — bleve backend
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/records.go — chunk records
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/terms.go — Go terms index
