---
Title: 'Intern guide: disk-backed retrieval with bleve and lazy record stores'
Ticket: RAGWB-002
Status: active
Topics:
    - rag
    - backend
    - goja
    - search
DocType: design-doc
Intent: long-term
Owners: []
RelatedFiles:
    - Path: repo://pkg/artifacts/store.go
      Note: artifact store; gains file and directory payloads
    - Path: repo://pkg/natives/bm25.go
      Note: Go BM25F whose scores the bleve backend reproduces
    - Path: repo://pkg/natives/bm25_native.go
      Note: pattern for replacing index.bm25 from Go
    - Path: repo://pkg/natives/persist.go
      Note: artifact codecs; gains records and bleve
    - Path: repo://pkg/ragjs/assets/rag.js
      Note: engine; artifacts(), Session.ctx, registry and persist hook change
    - Path: repo://pkg/runner/runtime.go
      Note: NewFactory and ArtifactNamespace; index backend setting
ExternalSources:
    - https://github.com/blevesearch/bleve (v2.6.0)
    - ~/code/wesen/go-go-golems/goja-bleve
Summary: How rag-workbench executes retrieval today (everything decoded into memory), and a phased design that makes a disk-backed bleve index the default BM25 backend in every Go runtime, with chunks and docs served from an on-disk record store on demand.
LastUpdated: 2026-11-03T17:00:00-05:00
WhatFor: Onboarding an engineer who will implement RAGWB-002; reviewing the design decisions.
WhenToUse: Before touching index components, the artifact store, artifacts() or the serve harness.
---

# Intern guide: disk-backed retrieval with bleve and lazy record stores

## 1. Executive summary

rag-workbench is a retrieval-augmented-generation (RAG) toolkit. You describe a search pipeline in JavaScript: which files to read, how to split them into chunks, which indexes to build, and how to retrieve, fuse, filter and answer. One engine, `rag.js`, then runs that description in three places:
- a browser workbench page;
- the `ragwb` command line;
- `ragwb serve`, which hosts small search apps.

In the Go-hosted places, the engine runs inside an embedded JavaScript interpreter (goja). The expensive parts — markdown parsing, chunking and BM25 — are Go code plugged into the engine.

Today a query in a Go runtime first loads **everything** into memory: every chunk's text, every chunk as a JavaScript object, and the whole BM25 index. On the author's Obsidian vault (85,882 chunks), that is:

| Measure | Value | Evidence |
|---|---|---|
| Startup cost per CLI query | 2.1 s of artifact loading | RAGWB-001 diary step 23 |
| Live heap of one served app | 838 MB (~1.4 GB RSS) | step 25 |
| Query time itself | 2–40 ms | steps 23, 24 |

A probe that put the same chunks into a persistent **bleve** index (step 26) opened in **0.9 ms**, used **91 MB RSS** for the whole query process, and answered in 4–32 ms.

This guide designs the change that makes that the normal path. There are three parts:
1. **bleve becomes the default backend of `index.bm25` in every Go runtime.** Chunks are indexed with *our* tokenizer, and queries are built from *our* query terms, but bleve does the BM25 scoring. Rankings may differ somewhat from the in-memory engine (and the browser's). That is accepted as long as they are not wrong, and a quality gate against the in-memory backend checks it (D2).
2. **Docs and chunks are stored in an on-disk record store** (a `bbolt` file, which bleve already depends on). The engine reads one record when it needs it, instead of decoding all of them at load.
3. **The artifact store learns file and directory payloads,** so indexes and record stores are kept by digest, as everything else already is.

The **workbench page** (`ragwb workbench`) runs its queries in the browser and is unchanged: a browser tab has no filesystem, so it keeps the in-memory engine. The **app shell** that `ragwb serve` opens runs every query on the server, so it gets the new backend automatically (§3.2).

**Expected result:** a served vault app opens in tens of milliseconds and needs on the order of 100 MB instead of 1.4 GB. Queries stay in the same 2–40 ms range. These are targets to be measured in Phase 5, not results yet.

## 2. Problem statement and scope

### 2.1 The problem

The engine was born in a browser tab (`sources/prototype/` in RAGWB-001), where every structure lives in memory. RAGWB-001 ported it to Go and added an artifact store (phase P8), so build outputs survive between processes. But the store saves each output as one blob, and loading means decoding the whole blob:

- **Chunks:** 136 MB of gob, decoded into 85,882 Go structs *and* 85,882 goja objects.
- **BM25 index:** 52 MB of gob, decoded into a Go map of postings.
- **Terms index:** 41 MB of JSON, parsed into JS `Map`s.

A query then reads about 50 chunks. The cost is proportional to the corpus, not to the query.

### 2.2 In scope

- A bleve-backed implementation of `index.bm25` that is the default in Go runtimes, with the same JS contract and rankings that pass a quality gate (not necessarily identical).
- An on-disk record store for `docs` and `chunks` outputs, and a lazy `Map`-like view of it in JS.
- Engine changes so nothing on the query path iterates all chunks.
- File and directory payloads in the artifact store; read-only opening; closing handles with the runtime.
- A compact Go terms index (Phase 4), so facet and meta retrieval stop costing 50 MB.
- Flags to select the old in-memory backend.
- Parity, memory and latency tests.

### 2.3 Out of scope (deferred, see §11)

- bleve's own query features (prefix-as-you-type, phrase, fuzzy, native facets).
- Exposing goja-bleve's `require("bleve")` to user scripts (needs a path policy first).
- Incremental index updates for `ragwb serve --watch`.
- Vectors in bleve (needs FAISS/cgo).
- Any change to the workbench page (the app shell needs none either, see §3.2).

## 3. Orientation: what you need to know first

### 3.1 The system in one picture

```
                         ┌──────────────────────────── one engine: pkg/ragjs/assets/rag.js ───────────────────────────┐
  browser workbench ───► │ plan builders → compile → IR → build (digest-memoized) → artifacts() → Session.run(live) │
  (web/workbench)        └───────────────▲───────────────────────────────▲───────────────────────────────▲──────────────┘
                                         │ in the browser: pure JS        │ in Go: goja runtime             │
  ragwb CLI ─────────────┐               │                                │                                 │
  (cmd/ragwb: run, verbs,├─► runner.NewFactory ─► goja runtime + modules "ragwb", "ragwb/gui", "ragwb/kit"   │
   presets)              │      (pkg/runner)        │                                                         │
  ragwb serve ───────────┘                          ├─ natives (pkg/natives): Go components installed into rag.js
  (pkg/server/rag.go,                               │    source.dir · parse.markdown · chunk.sections · index.bm25 · vectors
   web/app-shell)                                   └─ Persist: env.artifacts → artifacts.Store (pkg/artifacts) on disk
```

The **same `rag.js`** is served to the browser (`GET /rag.js`) and loaded into every goja runtime (`pkg/ragjs/module.go`). That is why "parity" — byte-identical results between the JS and Go implementations — matters throughout this codebase. A plan tuned in the workbench page must rank the same way when run from the CLI or a served app. RAGWB-002 deliberately relaxes this for one component: the bleve BM25 backend may rank somewhat differently, within a quality gate (D2). The in-memory backend keeps exact parity.

### 3.2 Two browser pages, two execution models

The repository ships two pages, and they run queries in different places. The rest of the guide assumes you know which is which.

| | Workbench page | App shell |
|---|---|---|
| Started by | `ragwb workbench` | `ragwb serve <app>` |
| Files | `web/workbench/{index.html, app.js, agent.js}` | `web/app-shell/{index.html, shell.js}` |
| Purpose | editor/IDE: script editor, inspector, DAG, agent | the app's search UI, nothing else |
| Where `rag.js` builds and queries | **in the browser tab** | **on the server** (goja runtime + Go natives) |
| Corpus | files loaded into the page (`rag.files(...)`: bundled `/corpus.json` or uploads) | whatever the server-side plan reads, e.g. `source.dir` over the vault |
| What the Go server does | LLM calls (`/_rt/sample`), db, saved apps, presets | `POST /api/rag/<app>/run`, `GET …/chunk`, `…/doc` (`pkg/server/rag.go`) |
| Affected by RAGWB-002 | no (a tab cannot open bleve or bbolt) | yes, transparently: its queries already run on the backend |

In the app shell, `host.rag.session().run(...)` is a fetch to `/api/rag/<app>/run` (`web/app-shell/shell.js`, `run`). The response carries the result and the bodies of the chunks it mentions. `rag.js` is loaded in that page only for helpers over returned data (`rag.ir()`, `rag.explain`); it never builds or searches. The measurements in §4.5 for `ragwb serve` are server-side numbers, and they are what this design improves.

### 3.3 Terms

**Corpus spec and plan.** A *corpus spec* chains build components: `rag.corpus("parc").from(rag.source.dir(...)).parse(rag.markdown()).chunk(rag.sections(...)).index("text", rag.bm25(...))`. A *plan* adds live components on top: `rag.plan("q").over(corpus).signals({q:"",k:10}).retrieve("text",{k:30}).filter.where().limit()`. See `examples/apps/parc/lib/corpus.js`.

**IR (intermediate representation).** `rag.compile(plan)` turns the builders into JSON: nodes `{id, t: type, p: params, in: [...], phase}`, edges, signals and outputs. Lambdas are stored as source text.

**Component and registry.** Each node type (`index.bm25`, `retrieve`, …) is declared in a registry (`REG`, `rag.js:489`). A declaration has:
- a phase: `build` (runs once per corpus) or `live` (runs per query);
- ports, from the vocabulary `files docs chunks index value | queries hits context answer`;
- `all(inputs, ctx, p)` for build components, or `run(signals, ctx, input, p)` for live ones;
- optionally `persist: {save, load}`.

`rag.define(type, def)` (`rag.js:1995`) adds or replaces a component. That is how Go code replaces JS implementations.

**Natives.** Go types implementing `ragjs.Native { Name(); Install(vm, rag) }` (`pkg/ragjs/module.go:99`). `Install` typically defines a component whose heavy work calls Go. For example, `natives.Bm25Native` redefines `index.bm25` (`pkg/natives/bm25_native.go:19-49`).

**Digest.** Every build node has a digest: a hash of its type, params, input digests and an optional `def.digest` hook. `source.dir`'s hook covers the path, size and mtime of every file. An unchanged digest means "reuse the memoized output" (`Engine.digests`, `rag.js:1296`).

**Artifact store (P8).** Build outputs saved on disk by `storeKey = hash([digest, def.src])` (`rag.js:1274`), under a namespace that changes whenever rag.js or Go component behavior changes (`runner.ArtifactNamespace`, `pkg/runner/runtime.go:58`). Default location `~/.ragwb/store/<ns>/<key[:2]>/<key>.bin` plus `.json` metadata (`pkg/artifacts/store.go`).

**Codec.** How a stored output is encoded (`pkg/natives/persist.go`):
- `json` — plain JS values;
- `docs`/`chunks` — gob of Go structs;
- `bm25` — gob of the Go index;
- `vectors` — raw float32.

**Lazy.** A stored output is not decoded when the build reuses it. It becomes a `Lazy` memo entry, decoded on first read (`rag.js:1252`, used at `rag.js:1347-1352`).

**artifacts(ir).** The engine's assembly of everything a query needs (`rag.js:1478`):
- `chunks`: a `Map` id → chunk, from every chunk-producing node;
- `order`: docId → chunk ids, in reading order;
- `indexes`: name → index object;
- `synonyms`;
- lazy `docs` and `links`.

`Session.run` calls it at the start of every query (`rag.js:1560`).

**Session, served app.** `Session.run(signals, {trigger})` executes the live nodes and returns a result: hits, outputs, trace and answer. `ragwb serve` keeps one runtime and one session per app (`pkg/runner/serve.go`, RAGWB-001 P7).

**Hit.** `{id, score, why}`. `why` maps the top contributing query terms to their partial scores. The inspector and `rag.explain` use it.

### 3.4 A query, end to end (today)

```
ragwb serve examples/apps/parc               (one long-lived goja runtime)
 open:   require(app.js) → plan → rag.compile → eng.build(ir)
           build: for each build node: digest → stored? → Lazy(load) memo entry   (no decoding yet)
         eng.artifacts(ir)                                                       ← 2.1 s, 838 MB
           chunks node: Lazy.force() → decode 136 MB gob → []Chunk → 85,882 goja objects → Map
           ix-text:     Lazy.force() → decode 52 MB gob → Go Bm25 (postings in a Go map)
           ix-meta:     Lazy.force() → JSON.parse 41 MB → TermsIndex (JS Maps)
 query:  POST /api/rag/parc/run {q}
         Session.run → artifacts() (cached) → retrieve: ix.search(q,k) → Go Bm25.Search → [{id,score,why}]
                     → filter.where: ctx.chunk(id).meta → limit → result
         pack: bodies of ~50 chunks → JSON → browser                             ← 7–40 ms total
```

### 3.5 Where the memory goes (measured)

From `ttmp/2026/11/02/RAGWB-001-…/scripts/06-serve-heap` (diary step 25), one opened vault session:

| What | Live MB | Why it exists |
|---|---|---|
| chunk JS objects (`chunkToJS`) | 411 | `artifacts()` builds `Map` id → chunk for all 85,882 chunks; 156 MB of it is each chunk's own copy of its document's frontmatter |
| Go `[]Chunk` (gob decode) | 204 | the decoded array is kept as the JS array's hidden `__go` handle |
| BM25 index (Go) | 132 | postings `{int, float64}`, 16 B each, plus the term map |
| terms index (`ix-meta`) | ~53 | JSON-parsed JS Maps of value → key → chunk ids |
| everything else | ~20 | |

A query reads about 50 chunks and the postings of 2–5 terms.

## 4. Current state, with evidence

### 4.1 How Go replaces a JS component: `index.bm25`

`pkg/natives/bm25_native.go` installs a JS snippet (`bm25JS`) that calls `rag.define('index.bm25', {...})`:
- `all([chunks], ctx, p)` extracts each chunk's field texts in JS, then calls Go `h.build(ids, docIds, texts, fields, k1, b, stem)` (`bm25_native.go:51`).
- The result is wrapped as the **index contract** every retriever relies on:

```js
{ kind: 'bm25', N, fields,
  search(q, k = 20, { synonyms, only }) → [{ id, score, why }],    // only: optional subset (unused by built-ins)
  searchIn(docId, q, k = 3, { synonyms }) → [...],                // used by expand.links (rag.js:707)
  toJSON() → { kind, chunks, terms, avgLen, fields },
  __ix: <Go *Bm25, non-enumerable> }
```

- `persist: { save: v => v.__ix, load: (ix, p) => wrap(ix, ...) }`. The Go handle travels to `Persist`, which encodes it with the `bm25` gob codec (`persist.go:96-125`).

`retrieve` (`rag.js:564-581`) only calls `ctx.index(name).search(q, k, {synonyms})` per query string, then merges by best score. **Any backend that honors this contract can replace the in-memory index without touching retrieve.**

### 4.2 Our BM25 is BM25F-style

`natives.NewBm25` (`pkg/natives/bm25.go:225-260`) does not score fields separately. For each chunk it adds `weight` to a term's frequency every time the term occurs in a weighted field, and it adds `len(tokens) × weight` to the chunk's length. A chunk is one weighted virtual document. Scoring (`bm25.go:317-345`) uses, for each query term `t` with query weight `w`:

```
idf(t)   = ln(1 + (N − df + 0.5) / (df + 0.5))
score   += w · idf · tf·(k1+1) / (tf + k1·(1 − b + b·len/avgLen))      // k1 = 1.2, b = 0.75 by default
```

Query terms come from `queryTerms` (`bm25.go:273-299`). Each distinct token of `q` has weight 1. When a synonyms entry's key appears in the query, each new token of its expansions is added with weight 0.5.

Tokenization is `natives.Tokenize` (`bm25.go:109`) with our stemmer (`bm25.go:69`). Both are byte-identical ports of rag.js; RAGWB-001 spent real effort on Unicode classes, UTF-16 lengths and final sigma to get there.

### 4.3 Build, persist and lazy load

In `Engine.build` (`rag.js:1340-1362`), for each build node:
- If a store exists and `store.has(storeKey)`, the node becomes a `Lazy` memo entry. Its `load` is `codec.load(store.load(key), p)`.
- Otherwise it computes, then `store.save(key, type, codec.save(value), count)` (`rag.js:1415-1418`).

`Persist` (`pkg/natives/persist.go:35-94`) bridges `env.artifacts.{has,save,load}` to `artifacts.Store`. Save chooses a codec from the value: a Go handle → its codec; anything else → `JSON.stringify`. Load decodes the whole payload (`os.ReadFile` + decode).

### 4.4 Everything that reads chunks

**Query path** (must stay cheap):
- `ctx.chunk(id)` — `filter.where` (`rag.js:637`), `rerank.llm` (`657`), `expand.links` (`699-707`), `pack` (`721`), `answer` (`731-732`), `agentic` tools (`766-786`), `viewHit` (`559`);
- `ctx.neighbors(id)` — `expand.neighbors` (`684`), via `art.order`;
- `result.chunk(id)` and `result.top()` — `decorateResult` (`rag.js:1673`);
- the serve harness `pack` (`pkg/runner/serve.go`, `serveHarness`): `a.chunks.get`, `a.order`.

**Whole-corpus iteration** (the dangerous ones):
- `Session.ctx()` builds `const chunkList = [...art.chunks.values()]` **for every live node of every run** (`rag.js:1649`). That is an 85k-element array per node per query, even when unused.
- `ctx.chunks(f)` — the `agentic` component's "sections of a document" tool filters all chunks by docId (`rag.js:788`).
- Evals: for every eval case, `R = [...art.chunks.values()].filter(relevant)` (`rag.js:1756`). With a lazy store, that would parse all 85k records per case.
- Synthetic eval sampling: a pool of all section chunks (`rag.js:1813`), once per call.
- Build components that take `[chunks]` as input (indexes, `enrich`, custom chunkers) receive the whole array. That is fine: build runs once per corpus.

### 4.5 Measurements that motivate the design

| Measurement | Value | Evidence |
|---|---|---|
| CLI warm `parc search`, total | 2.2 s; `artifacts()` 2.06 s (chunks 1.2 s: decode 1.1 s of 136 MB; terms 0.43 s; bm25 0.24 s); query 1.7 ms | step 23 |
| `ragwb serve` vault open | 2.15 s, 1.4 GB RSS | step 24 |
| `ragwb serve` query round trip | median 14.7 ms, max 45 ms | step 24 |
| Live heap breakdown | table §3.5 | step 25 |
| bleve probe: build | 21.0 s (3 analyzed stored fields, `en` analyzer) | step 26 |
| bleve probe: disk | 315 MB with stored text | step 26 |
| bleve probe: open | 0.9 ms; query process 91 MB RSS | step 26 |
| bleve probe: search (with a tags facet) | 4–32 ms | step 26 |
| bleve probe: fetch 10 stored docs | 0.1–0.35 ms | step 26 |

## 5. Gap analysis

| Need | Today | Gap |
|---|---|---|
| Open cost independent of corpus size | Decode everything (2.1 s vault) | Indexes and records must be opened, not decoded |
| Memory proportional to the query | 838 MB live for one session | Postings and records must be read from disk on demand |
| Rankings comparable to the browser | Exact (Go and JS BM25 parity tests) | bleve's scoring differs (per-field BM25, coordination factor); a quality gate replaces exact parity for this backend (§7, D2) |
| Several processes using one stored corpus | Each loads its own copy | Indexes must open read-only, concurrently |
| Query path never touches all chunks | `chunkList` per node, evals `R` per case | Engine fixes (§6.5) |
| Stored outputs larger than a blob | The store holds one `.bin` per output | File and directory payloads (§6.2) |
| Handles closed with the runtime | Nothing to close today | Natives need the runtime's closer hook |

## 6. Proposed architecture

### 6.1 The picture after the change

```
artifacts.Store  ~/.ragwb/store/<ns>/
  ab/abc123….bin         gob/json blobs (unchanged codecs)
  cd/cd45…​.rec           bbolt record store: docs or chunks       (codec "records", file payload)
  ef/ef67…​.bleve/        bleve index directory                    (codec "bleve",   dir payload)

goja runtime (CLI or served app)
  rag.js Engine
    build ─ stored? ─► Lazy(load) ─► load = OPEN (not decode)
    artifacts(ir)
      chunks ─► RecordView over chunks.rec  (Map-like; get(id) reads one record)
      order  ─► RecordView.order(docId)     (stored with the records)
      indexes.text ─► BleveBm25 wrapper     (index contract; search reads postings from disk)
      docs   ─► RecordView over docs.rec
  natives (Go)
    RecordStore  (bbolt, read-only, shared lock)
    BleveBm25    (bleve scorch index, read-only; our BM25F scorer over its postings)
    closers registered with the runtime → closed on rt.Close()
```

### 6.2 Artifact store: file and directory payloads

`artifacts.Store` today writes `<key>.bin` (bytes) and `<key>.json` (metadata) atomically (`store.go:94-131`). We add two payload kinds:
- **File payloads.** `<key>.rec` is a file produced elsewhere (a bbolt database) and moved into place with `os.Rename`.
- **Directory payloads.** `<key>.bleve/` is a directory produced elsewhere and renamed into place.

Metadata gains `Kind: "blob" | "file" | "dir"`, so `Has`, `List` and `Prune` know what to check, size and delete.

```go
// pkg/artifacts/store.go (new API)
func (s *Store) TempDir() (string, error)                                   // <dir>/<ns>/.tmp-*, same filesystem → rename is atomic
func (s *Store) SaveFile(key, typ, codec string, count int, src string) error // rename src → <key>.rec, then write meta
func (s *Store) SaveDir(key, typ, codec string, count int, src string) error  // rename src → <key>.<codec>/, then write meta
func (s *Store) Path(key string) (Meta, string, error)                       // path of a file/dir payload, for opening in place
```

**Invariants:**
- Metadata is written **after** the payload is in place (as today), so `Has` never reports a half-written output.
- Stored payloads are **immutable**. A key names a digest; a changed input is a different key. That is what makes read-only opening safe.
- `Prune` deletes whole namespaces (unchanged); `ragwb store list` reports directory sizes by walking them.

### 6.3 Record store: docs and chunks on disk

**What it stores.** For an output that is an array of objects with an `id` (docs from `parse.*`, chunks from `chunk.*` and `enrich*`), a bbolt file with these buckets:
- `rec`: id → record bytes. JSON of the JS object, which preserves arbitrary fields from custom components. A Go-produced chunk is encoded from the Go struct with the same field names `chunkToJS` uses.
- `ord`: ordinal (big-endian uint32) → id, preserving array order for iteration and for `order`.
- `doc`: docId → JSON array of chunk ids in reading order. Chunk stores only; this replaces `art.order`.
- `meta`: count, kind (`docs`|`chunks`), format version.

**Why bbolt.**
- It is already in bleve's dependency tree (`go.etcd.io/bbolt v1.4.0`, verified in the probe's `go.sum`).
- It is a memory-mapped B+tree. A read-only open uses a shared file lock, so several processes can read one store.
- A `Get` is a page lookup.

The alternatives are in D5.

**Go API:**

```go
// pkg/natives/records.go
type RecordWriter struct{ db *bbolt.DB; n int }
func CreateRecords(path, kind string) (*RecordWriter, error)
func (w *RecordWriter) Put(id string, rec []byte, docID string) error  // appends ord, rec, and doc order (chunks)
func (w *RecordWriter) Close() error

type Records struct{ db *bbolt.DB; kind string; n int }
func OpenRecords(path string) (*Records, error)          // bbolt.Open(path, 0o444, &bbolt.Options{ReadOnly: true})
func (r *Records) Get(id string) ([]byte, bool)
func (r *Records) Len() int
func (r *Records) Order(docID string) []string            // chunk ids of a doc, in reading order
func (r *Records) Each(fn func(id string, rec []byte) bool) // in ordinal order; for whole-corpus consumers
func (r *Records) Close() error
```

**JS view.** Installed by the native (not in rag.js, so the browser engine stays as it is). It is `Map`-compatible for the methods the engine uses:

```js
// returned by the chunks/docs persist.load, and by build when a store exists
class RecordView {                       // h = Go *Records handle
  constructor(h) { this.__records = h; this.size = h.len(); this.cache = new LRU(4096); }
  get(id)   { let v = this.cache.get(id); if (v === undefined) { const s = h.get(String(id)); v = s == null ? undefined : JSON.parse(s); this.cache.set(id, v); } return v; }
  has(id)   { return h.has(String(id)); }
  order(docId) { return h.order(String(docId)); }        // chunk stores only
  *values() { for (const [, v] of this.entries()) yield v; }   // streams; costs what today's load costs
  *keys()   { ... }  *entries() { ... }  forEach(f) { ... }  [Symbol.iterator]() { return this.entries(); }
  toArray() { return [...this.values()]; }               // for build components that need the array
}
```

`get` returns the same object for repeated reads while it is cached, so identity holds within a query. Records are treated as **immutable**. The engine never mutates a chunk in place (`enrich` returns `{...chunk, …}`).

### 6.4 bleve as the default `index.bm25` backend

#### 6.4.1 Shape

A new native, `natives.BleveBm25`, redefines `index.bm25` exactly like `Bm25Native` does today. It honors the same JS contract (§4.1), with `kind: 'bm25'`. `retrieve`, `expand.links`, the inspector and evals don't change.

#### 6.4.2 Indexing: our tokens, one bleve field per weighted field

Each chunk becomes one bleve document with:
- one indexed (not stored) field per weighted plan field, named `f.<field>` (e.g. `f.title`, `f.headingPath`, `f.text`). Its value is the space-joined tokens of that field, produced by `natives.Tokenize(text, stem)`, the same tokenizer and stemmer as the in-memory engine;
- `docId` (keyword, indexed, not stored), for `searchIn`;
- the document id = the chunk id.

The `f.*` fields use a whitespace-only analyzer, because the tokens are already normalized.

**Why pre-tokenize, if rankings may change anyway?**
- Index and query use exactly the terms the rest of the engine uses: Unicode letter classes, our stemmer, and synonym expansion through `queryTerms`. Differences from the in-memory engine then come only from *scoring*, which is what the quality gate measures.
- bleve's `en` analyzer (porter stemmer, stop words) would change *which* chunks match at all, and would make synonyms and `why` terms inconsistent with the rest of the engine.

```go
// pkg/natives/bleve_bm25.go (sketch)
func BuildBleveBm25(dir string, n int, chunk func(i int) (id, docID string, texts []string), fields []Field, stem bool) (*BleveBm25, error) {
    m := bleve.NewIndexMapping()
    m.ScoringModel = "bm25"                                       // bleve's BM25 (k1 1.2, b 0.75), not TF-IDF
    doc := bleve.NewDocumentMapping()
    for _, f := range fields {
        fm := bleve.NewTextFieldMapping(); fm.Analyzer = "ragwb-pretokenized"; fm.Store = false
        doc.AddFieldMappingsAt("f."+f.Name, fm)
    }
    kw := bleve.NewKeywordFieldMapping(); kw.Store = false
    doc.AddFieldMappingsAt("docId", kw)
    m.DefaultMapping = doc
    idx, err := bleve.New(dir, m)
    if err != nil { return nil, err }
    batch := idx.NewBatch()
    for i := 0; i < n; i++ {
        id, docID, texts := chunk(i)
        d := map[string]any{"docId": docID}
        for fi, f := range fields {
            if texts[fi] != "" { d["f."+f.Name] = strings.Join(Tokenize(texts[fi], stem), " ") }
        }
        batch.Index(id, d)
        if batch.Size() >= 2000 { if err := idx.Batch(batch); err != nil { return nil, err }; batch = idx.NewBatch() }
    }
    if err := idx.Batch(batch); err != nil { return nil, err }
    idx.SetInternal([]byte("ragwb.meta"), mustJSON(meta{N: n, Stem: stem, Fields: fields}))
    return &BleveBm25{idx: idx, meta: ...}, nil
}
```

`ragwb-pretokenized` is bleve's whitespace tokenizer with no token filters, registered once with `registry.RegisterAnalyzer` (`bleve/v2@v2.6.0/registry/analyzer.go:23`).

#### 6.4.3 Searching: a bleve query built from our query terms

```go
func (b *BleveBm25) Search(q string, k int, syn []Synonym, only []string, explain bool) []Hit {
    terms := queryTerms(q, syn, b.meta.Stem)               // refactor of bm25.go:273: (term, weight) pairs, synonyms at 0.5
    if len(terms) == 0 { return nil }
    dq := bleve.NewDisjunctionQuery()
    for _, qt := range terms {
        for _, f := range b.meta.Fields {
            tq := bleve.NewTermQuery(qt.t)
            tq.SetField("f." + f.Name)
            tq.SetBoost(f.Weight * qt.w)                     // field weight × query-term weight; fractions are fine
            dq.AddQuery(tq)
        }
    }
    var query query.Query = dq
    if only != nil { query = bleve.NewConjunctionQuery(bleve.NewDocIDQuery(only), dq) }
    req := bleve.NewSearchRequestOptions(query, k, 0, explain)
    if !explain { req.IncludeLocations = true }               // cheap: which query terms matched each hit
    res, err := b.idx.Search(req)
    ...
    return hitsFrom(res, explain)                            // {id, score (4 dp), why}
}

func (b *BleveBm25) SearchIn(docID, q string, k int, syn []Synonym) []Hit {
    // same disjunction, AND a term query docId:<docID>
}
```

**`why`.** The in-memory index reports each matched term's partial score (top 5). Computing that for every candidate needs bleve's `Explain`, which costs time on every scored document. So:
- by default, `why` maps each matched query term to the sum of its clause scores, derived from term locations: matched terms are exact, values are approximate;
- with `explain: true` (the inspector, `rag.explain`, evals' explain), `Explain` is turned on and `why` holds the exact per-term contributions summed across fields.

The trace and `rag.explain` work in both cases.

**How the scores differ from the in-memory engine.** These are expected and accepted (D2):
- bleve scores each field separately and sums the field scores. Ours is BM25F: weighted term frequencies are summed into one virtual document before saturation.
- bleve multiplies a disjunction's score by `matched clauses / total clauses` (`scorer_disjunction.go:65-66`), which favors chunks that match more query terms in more fields.
- The numerator constant differs (`k1` vs `k1+1`), a uniform factor that doesn't change rankings.

The quality gate (§10, Phase 3) checks that the result is still sensible.

#### 6.4.4 Lifecycle: build, persist, open

```
build (cold):  BuildBleveBm25(store.TempDir()/x.bleve, …) → idx (read-write)
persist.save:  handle → Persist.encodeArtifact sees *BleveBm25 →
                 idx.Close(); store.SaveDir(key, "index.bm25", "bleve", n, tmpDir); idx = OpenReadOnly(store.Path(key))
               (the JS wrapper keeps working: the handle reopens itself at the final path)
load (warm):   persist.load → OpenReadOnly(store.Path(key))    ← milliseconds, no decoding
no store:      (--no-store) index lives in a per-runtime temp dir, removed by the runtime closer
close:         every opened index/records handle is registered with the runtime's AddCloser
               (go-go-goja engine/runtime_modules.go:28; Runtime.AddCloser runtime.go:68)
```

**Read-only opening:** `bleve.OpenUsing(path, map[string]any{"read_only": true})` (`bleve/v2@v2.6.0/index.go:330`; scorch reads `read_only` at `index/scorch/scorch.go:219`). Concurrent read-only opens across processes must be tested (Phase 3 acceptance). A served app and a CLI query will open the same stored index.

#### 6.4.5 Making it the default

- `runner.Config.IndexBackend string` (`"bleve"` default, `"memory"`), and a global CLI flag `--index-backend bleve|memory`.
  - `bleve` installs `BleveBm25` instead of `Bm25Native`.
  - `--js-components` implies `memory` (it keeps rag.js's own components).
- **Per-plan override:** `rag.bm25({ backend: 'memory' })`. The Go component reads `p.backend`; rag.js's own `index.bm25` ignores it, and the browser always runs in memory. Because it is a param, it enters the digest, so differently configured outputs never collide.
- **Stored-output separation:** `ArtifactNamespace` already hashes the component mode (`"go"`/`"js"`). It becomes `"go+bleve"`/`"go"`/`"js"`, and `natives.Version` is bumped. Old stores are untouched and can be pruned (`ragwb store prune`).
- **Defaults are gated on quality, not parity** (D2). The exact parity tests (`pkg/runner/bm25_parity_test.go`) stay, pinned to the memory backend. The bleve backend gets quality tests against it (§12).

### 6.5 Engine changes in rag.js

These are small and must keep the browser behavior identical. A view is detected by duck typing (`v && v.__records`), so the browser code path, which never sees views, is unchanged.

```js
// artifacts(ir), rag.js:1478 — chunks/order from views instead of a Map built by iteration
for (const n of ir.nodes) {
  if (n.phase !== 'build') continue;
  if (n.out === 'chunks') chunkSources.push(byId.get(n.id));   // array or RecordView
  ...
}
const chunks = chunkSources.length === 1 && isView(chunkSources[0]) ? chunkSources[0]
             : chunkSources.some(isView) ? new UnionView(chunkSources)   // later streams win, as Map.set did
             : buildMapAsToday(chunkSources);
const order = isView(primary) ? { get: docId => primary.order(docId) } : buildOrderAsToday(primary);

// Session.ctx, rag.js:1649 — no per-node copy of all chunks
let chunkList = null;
const all = () => chunkList || (chunkList = [...art.chunks.values()]);
chunks: f => (f ? all().filter(f) : all()),

// agentic "sections of a document" tool, rag.js:788 — use the order instead of filtering all chunks
return (art.order.get(docId) || []).map(id => ctx.chunk(id)).map(x => ({ id: x.id, heading: x.headingPath.join(' › ') }));

// evals, rag.js:1756 — count relevant chunks once per run, not once per case
const R = relevantCounts(cases, art);   // one pass over art.chunks.values(), all cases' predicates per chunk

// build inputs — components that need arrays get arrays
const inputs = n.in.map(id => { const v = outputs.get(id); return isView(v) ? v.toArray() : v; });
```

The last item preserves the contract for custom build components. Built-in Go components skip the materialization: `BleveBm25` and the terms index read records from the Go handle directly (§6.6).

### 6.6 Terms index (`index.terms`): compact and Go-native (Phase 4)

`TermsIndex` (`rag.js:446-484`) maps `value → key → [chunk ids]`. Its `search` checks which whole values occur as word sequences in the query. Its `values(key)` feeds facet counts.

The Go replacement:
- stores chunk ordinals (`[]uint32`) instead of id strings;
- persists as gob (blob codec — it is a few MB);
- resolves ids through the chunk record store.

Semantics are ported exactly (`RE_NONWORD`, the `1/ln(2 + n/10)` weight, the tie order). This removes the ~53 MB of JS Maps without involving bleve. bleve keyword fields would also work, but the value-in-query matching is not a bleve query (D6).

### 6.7 What does not change

- **Vectors (`index.vector`):** they keep their raw float32 artifact. Moving to memory-mapped vectors or bleve KNN is deferred: bleve KNN needs FAISS and cgo (goja-bleve `docs/faiss-xgoja-playbook.md`).
- **The workbench page and its in-browser engine.** The app shell needs no change either: its queries already run on the server.
- **The live components, the result shape, the serve API and the app shell.** The serve harness `pack` already uses `a.chunks.get` and `a.order`, which the views provide.

## 7. Decision records

### Decision D1: bleve is the default BM25 backend in Go runtimes

- **Context:** Opening a stored corpus decodes it entirely (2.1 s, 838 MB on the vault). The user asked to make a disk-backed bleve index the default.
- **Options considered:**
  1. Keep in-memory as default; bleve opt-in.
  2. bleve default in Go; memory as fallback.
  3. A custom memory-mapped postings format, with no bleve.
- **Decision:** (2).
- **Rationale:**
  - The probe shows a 1000× faster open and a 10× smaller process.
  - bleve brings a maintained segment format, read-only multi-process access, and later incremental updates (`--watch`), prefix/phrase queries (typing "obsid" currently finds nothing until the word is complete) and facets.
  - Option (3) would give the same memory profile for BM25 alone, but we would own a file format and gain none of those.
- **Consequences:**
  - A new dependency tree (bleve v2.6, zapx segment versions).
  - The `go` directive rises to 1.26.4.
  - Build time for the BM25 node rises: the probe took 21 s with three analyzed, stored fields, against 8.45 s for the current Go BM25. It must be measured with the single pre-tokenized field.
- **Status:** accepted (user directive), pending Phase 5 numbers.

### Decision D2: bleve scores; rankings may change within a quality gate

- **Context:** The memory backend reproduces rag.js exactly, so the browser, CLI and served apps rank identically. bleve's BM25 differs in three ways:
  - per-field scoring;
  - a coordination factor on disjunctions;
  - a constant numerator factor.

  The user decided that rankings may change with bleve, as long as they are not wrong.
- **Options considered:**
  - (a) bleve's own scoring, with a quality gate;
  - (b) our BM25F scorer over bleve's low-level postings (`TermFieldReader`), for exact parity;
  - (c) a custom memory-mapped format (see D1).
- **Decision:** (a).
- **Rationale:**
  - It uses only bleve's public query API: no low-level reader, no external-ID mapping, no field-length precision issues.
  - Fractional field weights work as query boosts.
  - It leaves bleve's native query features within reach (prefix, phrase, facets: §11).
  - Ranking quality is protected by measurement instead of by construction.
- **Consequences:**
  - A plan tuned in the browser page may rank somewhat differently when served. The memory backend (`--index-backend memory`, `rag.bm25({ backend: 'memory' })`) gives exact parity where it matters.
  - Eval baselines recorded under the memory backend are compared, not reused.
  - Scores are bleve's scale: don't compare absolute scores across backends.
- **Status:** accepted (user decision, 2026-11-03).

### Decision D3: index our tokens, one bleve field per weighted field

- **Context:** Our tokenizer, stemmer and synonym expansion are ports of rag.js and the source of truth for *which* terms exist. bleve's `en` analyzer (porter stemmer plus stop words) produces different terms.
- **Options considered:**
  - (a) a bleve custom analyzer wrapping `natives.Tokenize`;
  - (b) tokenize in Go before indexing and index space-joined tokens with a whitespace analyzer;
  - (c) bleve's `en` analyzer.
- **Decision:** (b), with one field per weighted plan field and weights applied as query boosts.
- **Rationale:**
  - It is the smallest surface: no tokenizer plugin.
  - Matching stays identical to the in-memory engine, so only scoring differs and the quality gate measures exactly that.
- **Consequences:** The index is only meaningful when queried with our tokens. That is acceptable, since only `BleveBm25` queries it. Exposing it to `require("bleve")` scripts later would need the same pre-tokenization on their side.
- **Status:** proposed.

### Decision D4: the backend is a param plus a runtime default, not a new component type

- **Context:** Making it "the default" means existing plans (`rag.bm25(...)`) get it without edits, while authors can opt out.
- **Options considered:**
  - (a) a new type `index.bleve`;
  - (b) the same type `index.bm25`, with the backend chosen by `p.backend` or else the runtime default.
- **Decision:** (b).
- **Rationale:** Existing plans, presets, apps and the agent's prompt all say `rag.bm25`. A param keeps explicit choices in the digest. The runtime default plus the namespace keeps stored outputs separate.
- **Consequences:** The browser ignores `backend`, which is documented in the component help. Note that the default is not in the IR, which is why the namespace must include the backend.
- **Status:** proposed.

### Decision D5: record store on bbolt

- **Context:** Chunk and doc lookups must not require decoding the whole output.
- **Options considered:**
  1. bleve stored fields;
  2. SQLite;
  3. bbolt;
  4. a custom offset-table file.
- **Decision:** bbolt.
- **Rationale:**
  - **vs bleve stored fields:** chunks are an output of the chunk node and exist without any BM25 index; tying them to the index couples unrelated nodes.
  - **vs SQLite:** it is already linked (cgo, `mattn/go-sqlite3`), but a second engine for key→value lookups is more than needed, and its locking is heavier.
  - **vs a custom file:** a custom file would be simplest to read, but we would own its format.
  - **For bbolt:** it is already a transitive dependency via bleve, memory-mapped, and read-only opens share a lock.
- **Consequences:**
  - One more file per stored docs/chunks output.
  - JSON per record means a `JSON.parse` per `get`, about 10–50 µs for a vault chunk; that needs measuring. A small LRU in the view bounds the repeats.
- **Status:** proposed.

### Decision D6: terms index stays out of bleve

- **Context:** `index.terms` matches whole metadata values contained in the query and counts facets.
- **Options considered:**
  - (a) bleve keyword fields plus facets;
  - (b) a compact Go port with ordinal postings.
- **Decision:** (b).
- **Rationale:** The semantics are not a bleve query, and the data is small once ids are ordinals. A port keeps parity with rag.js.
- **Consequences:** It becomes a Go native with a gob codec and a parity test like BM25's.
- **Status:** proposed.

## 8. Key flows

### 8.1 Cold build with a store (first run of a vault app)

```
eng.build(ir)
  files    (source.dir)       compute → marker persisted (unchanged)
  parse    (parse.markdown)   compute → []Doc → persist: CreateRecords(tmp) → Put each → SaveFile(key,"docs","records")
                                         value in this runtime: RecordView(OpenRecords(final))
  chunks   (chunk.sections)   compute (needs docs: view.toArray() or Go handle) → persist like docs, with doc order
  ix-text  (index.bm25)       BleveBm25 build from the chunks Go handle (no JS objects) → SaveDir(key,"index.bm25","bleve")
  ix-meta  (index.terms)      Go terms index → gob blob
artifacts(ir) → views + opened index (milliseconds)
```

### 8.2 Warm open (every later process)

```
eng.build(ir)    every node: store.has(key) → Lazy(load = open)                  ~ms
artifacts(ir)    forces: chunks → OpenRecords; ix-text → OpenReadOnly; ix-meta → gob decode (~MBs)
                 docs stay unopened until something reads art.docs
query            retrieve → BleveBm25.Search (postings from mmap) → hits
                 filter.where → view.get(id) per hit (JSON.parse, cached)
                 pack → view.get for ~50 ids + order lookups
```

### 8.3 A served app's request (unchanged API)

```
POST /api/rag/parc/run {signals:{q}}
  → ServedApp.Run → __serve.run → Session.run → [same as 8.2 query] → JSON {result, chunks, neighbors, timing}
```

## 9. API reference

### 9.1 JS: index contract (unchanged; both backends)

| Member | Signature | Notes |
|---|---|---|
| `kind` | `'bm25'` | |
| `N` | number | chunks indexed |
| `fields` | `{name: weight}` | |
| `search` | `(q, k=20, {synonyms, only, explain}) → Hit[]` | `Hit = {id, score (4 dp), why: {term: score (2 dp)}}`, top-5 why. bleve backend: `why` values are approximate unless `explain: true` (§6.4.3); scores are on bleve's scale |
| `searchIn` | `(docId, q, k=3, {synonyms}) → Hit[]` | |
| `toJSON` | `() → {kind, chunks, terms, avgLen, fields, backend}` | `backend` is new; shown in the inspector |
| `__ix` | Go handle | non-enumerable; the persist codec uses it |

### 9.2 JS: `RecordView` (new; Go runtimes only)

| Member | Notes |
|---|---|
| `get(id)`, `has(id)`, `size` | `Map`-compatible |
| `keys()`, `values()`, `entries()`, `forEach(f)`, `[Symbol.iterator]` | stream all records in stored order; cost proportional to the corpus |
| `order(docId)` | chunk stores: ids in reading order |
| `toArray()` | materialize for build components that need arrays |
| `__records` | Go handle; detection by `isView(v)` |

### 9.3 Go

| Package | API | New/changed |
|---|---|---|
| `pkg/artifacts` | `Store.TempDir`, `SaveFile`, `SaveDir`, `Path`; `Meta.Kind` | new |
| `pkg/natives` | `CreateRecords`, `OpenRecords`, `Records.{Get,Len,Order,Each,Close}` | new (`records.go`) |
| `pkg/natives` | `BuildBleveBm25`, `OpenBleveBm25`, `BleveBm25.{Search,SearchIn,Close}` | new (`bleve_bm25.go`) |
| `pkg/natives` | `queryTerms` as a free function (shared by both backends) | refactor of `bm25.go:273` |
| `pkg/natives` | `encodeArtifact`/`decodeArtifact`: codecs `records`, `bleve` | changed (`persist.go`) |
| `pkg/natives` | `Version` bump | changed |
| `pkg/ragjs` | `Native` gets an optional closer hook (or `Options.AddCloser`) | changed (`module.go:99`) |
| `pkg/runner` | `Config.IndexBackend`; `ArtifactNamespace(mode)` includes the backend | changed (`runtime.go`) |
| `cmd/ragwb/cmds` | global `--index-backend bleve\|memory` | new (`roots.go`) |

### 9.4 Timing events (for `--timings` and the serve API)

The new events report open times separately from decode times:
- `store <type>` with detail `records · open 0.4ms` or `bleve · open 0.9ms`;
- `build ix-text` with detail `index.bm25 · computed · bleve`.

## 10. Implementation plan

Each phase ends with a commit, a diary step (investigation format) and the listed acceptance checks.

**Phase 0: dependencies and scaffolding.**
- Add `github.com/blevesearch/bleve/v2 v2.6.0` and raise `go` to 1.26.4.
- Add the `--index-backend` flag (no-op until Phase 3).
- Acceptance: `go test ./...` and `make lint` pass; the binary size delta is noted.

**Phase 1: artifact store payloads** (`pkg/artifacts`).
- `TempDir`, `SaveFile`, `SaveDir`, `Path`, `Meta.Kind`; `List` sizes for dirs; `Prune` unchanged.
- Tests: atomicity (meta written last), a crash between rename and meta (`Has` false), list sizes.

**Phase 2: record store and views** (`pkg/natives/records.go`, `persist.go`, rag.js §6.5).
- Codec `records` for `docs` and `chunks` outputs.
- `RecordView` installed by a native; rag.js changes from §6.5, with browser behavior unchanged.
- Tests:
  - record round trip (Go and JS);
  - `pkg/runner/artifacts_test.go` extended: warm results equal cold results (same dump hash, as in P8);
  - whole-vault equality check (P8's chunk/doc/terms hashes);
  - the browser engine's tests unchanged.
- Measure with `scripts/06-serve-heap` (RAGWB-001): live heap after open should drop by about 600 MB.

**Phase 3: bleve BM25 backend** (`pkg/natives/bleve_bm25.go`).
- Step 1, quality spike: build the reports and vault indexes and compare `Search` against the memory backend. Use the reports goldens (hit@5, MRR) and about 200 vault queries (top-10 overlap, plus a manual look at the worst overlaps). This sets the thresholds of the quality gate.
- Step 2: build, persist, read-only open, closers, `searchIn`, synonyms, `only`, `why`.
- Step 3: make it the default; namespace and `Version` bump.
- Tests:
  - the quality gate: on the reports goldens, hit@5 and MRR no more than 0.05 below the memory backend; on the presets sweep, no preset's HIT@5 drops by more than 0.1; on the vault sample, median top-10 overlap ≥ 0.5 with every query below 0.2 inspected by hand. The thresholds are revisited after the Step 1 spike;
  - `bm25_parity_test.go` stays exact, pinned to the memory backend;
  - two processes opening one stored index read-only;
  - `--no-store` temp dir cleanup.

**Phase 4: Go terms index.**
- Port `TermsIndex` with ordinal postings, plus a parity test against rag.js on reports and the vault.

**Phase 5: measurement and docs.**
- `ragwb --timings` (CLI), the `serve` open breakdown, `scripts/05-serve-latency-bench.py`, the heap script.
- `ragwb presets` sweep: metrics unchanged.
- Update the RAGWB-001 guide's §17 status, help text and the agent prompt's component notes.
- Targets: vault open under 100 ms excluding first-touch page faults; RSS under 200 MB; median query unchanged (≤ 15 ms round trip).

## 11. Risks, alternatives and open questions

- **Build time.** BM25 indexing in bleve may be 2× slower than the in-memory build. Mitigations: larger batches, `unsafe_batch` (scorch), building in a temp dir with persister settings tuned. Measure in Phase 3.
- **Ranking drift.** bleve's per-field scoring and coordination factor can promote chunks that match many query terms weakly. If the gate fails, the first levers are: field boosts (weights currently tuned for BM25F); a `MatchQuery` with operator `or` per field instead of per-term clauses; and, as a last resort, D2 option (b), our scorer over bleve's postings.
- **Per-query allocation for common terms.** The accumulator for 30k matching docs allocates per query. A pooled slice keyed by a dense ordinal (stored as a doc value) avoids map growth. Measure first.
- **`JSON.parse` per record.** If `filter.where` over many hits shows up in timings, add a `chunkMeta(id)` fast path or a larger LRU.
- **Read-only concurrency.** Must be verified with two processes. If scorch takes an exclusive lock even read-only, the fallback is to copy-on-open (hardlink the segment files into a per-process dir).
- **Deferred, worth a ticket each:**
  - bleve-native queries (prefix-as-you-type, phrase) as `index.bleve`;
  - `require("bleve")` via goja-bleve, after adding a root-restricted path policy, BM25 scoring, facets and `get(id)` upstream (gaps listed in RAGWB-001 diary step 26);
  - incremental updates for `serve --watch`;
  - memory-mapped vectors.
- **Open question:** should `ragwb workbench` (the browser page's server) offer a server-side session for corpora too big for the page? That is RAGWB-001's "connect the agent to served apps" follow-up, which this design makes cheap.

## 12. Test strategy summary

| Level | What | Where |
|---|---|---|
| Unit | store payloads, record store, scorer formula, query terms | `pkg/artifacts`, `pkg/natives` |
| Parity | in-memory BM25 vs rag.js stays exact (memory backend) | `pkg/runner/bm25_parity_test.go` |
| Quality | bleve vs memory backend: goldens hit@5/MRR, presets HIT@5, vault top-10 overlap when `RAGWB_QUALITY_VAULT` is set | `pkg/runner/bm25_quality_test.go` (new) |
| Engine | warm = cold dumps; views vs Maps; evals R counts unchanged | `pkg/runner/artifacts_test.go`, `pkg/ragjs` |
| Integration | `ragwb serve` routes on the reports app (existing `rag_test.go`) with the bleve default | `pkg/server` |
| Concurrency | two processes, one stored index, read-only | new test with `exec.Command` of a helper |
| Performance | open time, RSS, live heap, query latency on the vault | RAGWB-001 scripts 05 and 06; numbers into the diary |

## 13. File reference

| File | Role | Change |
|---|---|---|
| `pkg/ragjs/assets/rag.js` | engine: registry (`489`), index.bm25 (`536`), retrieve (`564`), Lazy (`1252`), build persist (`1340-1418`), artifacts (`1478`), Session (`1549`), ctx (`1648`), evals (`1744-1760`) | views in artifacts, lazy chunkList, evals R, agentic tool, build inputs |
| `pkg/natives/bm25.go` | Go BM25F, tokenizer, stemmer, query terms, scoring | `queryTerms` shared |
| `pkg/natives/bm25_native.go` | current `index.bm25` replacement | stays as the memory backend |
| `pkg/natives/bleve_bm25.go` | new bleve backend | new |
| `pkg/natives/records.go` | new bbolt record store and `RecordView` installer | new |
| `pkg/natives/persist.go` | codecs and `Version` | records/bleve codecs, bump |
| `pkg/natives/markdown_codec.go` | `chunksArray`/`chunkToJS` (the 411 MB) | used only by the memory path |
| `pkg/artifacts/store.go` | artifact store | file/dir payloads |
| `pkg/runner/runtime.go` | `NewFactory`, `ArtifactNamespace` | backend selection, namespace |
| `pkg/runner/serve.go` | served sessions | none expected (uses `get`/`order`) |
| `cmd/ragwb/cmds/roots.go` | global flags | `--index-backend` |
| `ttmp/2026/11/02/RAGWB-001-…/scripts/06-serve-heap` | heap profile of a served session | reuse for acceptance |
| `ttmp/2026/11/02/RAGWB-001-…/scripts/07-bleve-vault-probe` | bleve probe on vault chunks | baseline numbers |
| `~/code/wesen/go-go-golems/goja-bleve` | JS module for bleve | later: path policy, scoring, facets, get(id) |

## 14. References

- **RAGWB-001 design guide:** `ttmp/2026/11/02/RAGWB-001-…/design-doc/01-intern-guide-…md` (§9 architecture, §17 phase status).
- **RAGWB-001 diary steps:**
  - 21 (P8 artifact store);
  - 23 (`--timings`);
  - 24 (P7 `ragwb serve`);
  - 25 (heap profile);
  - 26 (goja-bleve check and vault probe).
- **bleve v2.6.0:**
  - `index.go:330` (`OpenUsing`);
  - `index/scorch/scorch.go:219` (`read_only`);
  - `search/scorer/scorer_term.go:141-157` (BM25 term score, norms);
  - `search/scorer/scorer_disjunction.go:46-73` (coordination factor);
  - `search/util.go:266-273` (`BM25_k1`, `BM25_b`).
- **go-go-goja v0.10.7:** `pkg/engine/runtime.go:66-87` (`AddCloser`, `Close`); `pkg/engine/runtime_modules.go:28`.
