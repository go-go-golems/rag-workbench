---
Title: 'Intern guide: incremental re-indexing with delta builds'
Ticket: RAGWB-003
Status: active
Topics:
    - rag
    - backend
    - search
    - goja
DocType: design-doc
Intent: long-term
Owners: []
RelatedFiles:
    - Path: repo://pkg/natives/bleve_bm25.go
      Note: bleve backend; gains clone + apply delta
    - Path: repo://pkg/natives/chunk.go
      Note: global AssignIDs (ids/ord) to become per-doc
    - Path: repo://pkg/natives/markdown.go
      Note: doc id from slug(name), to become DocIDFor
    - Path: repo://pkg/natives/records.go
      Note: chunk records v1 (ordinal keys); v2 sharded COW
    - Path: repo://pkg/natives/sourcedir.go
      Note: source digest and walk; gains manifest and lineage
    - Path: repo://pkg/ragjs/assets/rag.js
      Note: Engine.build/digests/artifacts; delta protocol and lineage
ExternalSources:
    - https://github.com/blevesearch/bleve (v2.6.0, scorch)
    - https://github.com/etcd-io/bbolt (v1.4.0)
Summary: 'Why editing one note currently rebuilds the whole corpus (~28 s on the vault), and a phased design for delta builds: stable per-file identity, source manifests, lineage lookup of the previous build, a delta protocol through the build DAG, and copy-on-write artifacts (sharded records, cloned bleve segments) — so an edit costs about a second.'
LastUpdated: 2026-11-04T10:00:00-05:00
WhatFor: Onboarding an engineer who will implement RAGWB-003; reviewing its design decisions.
WhenToUse: Before changing source.dir, the markdown parser/chunker ids, the artifact store, the record store, the bleve backend or Engine.build.
---

# Intern guide: incremental re-indexing with delta builds

> **Implementation status (2026-11-03):** Phases 0–5 are implemented. The commits are `efe927d`, `29feef3`, `1bd2495`, `4235a13`, `0e1321e` and `780c4ab`; the details are in diary steps 2–7.
>
> On a copy of the vault, a one-note edit runs in 0.45 s wall for the whole CLI process. Under `serve --watch`, the rebuild takes 161–202 ms, and the edit is visible to queries about 1.2 s after it is saved.
>
> Where the code differs from this design:
> 1. **BM25 statistics.** Searches pass exact BM25 statistics to bleve through `PreSearchData`: N, plus the token count per field. bleve's own statistic sums each segment's dictionary size, so a delta and a full build scored differently (diary step 6). This changed rankings: presets went from hit@5 0.942 to 0.944 and MRR 0.893 to 0.913; vault self-recall went from 0.755 to 0.730.
> 2. **records2 order.** The canonical order lives in `index.json`, a document list with record counts. Shard key order is not used, so iteration is not a 64-way merge.
> 3. **Lineage matching.** Lineage entries record the digests of their inputs. A node continues only from a version built from exactly the versions its input deltas start from.
> 4. **Manifest codec.** The manifest is a Go-only codec (`manifest`), because nanosecond mtimes don't survive JS numbers.
> 5. **No-delta components.** `index.vector`, `map` components (`enrich`) and the memory BM25 backend have no delta. They build fully, using their caches.
> 6. **gc and verify.** `ragwb store gc --keep N` and `ragwb store verify` are implemented. `verify` checks integrity; equivalence with a full build is checked by `TestDeltaBuildEqualsFullBuild`.

## 1. Executive summary

rag-workbench builds a search index from a directory of markdown notes, here the author's Obsidian vault: 2,282 notes, 85,882 chunks. The build has several steps:
- read the files;
- parse them into documents;
- split the documents into chunks;
- build a full-text index (bleve), a metadata index, and optionally vectors.

Since RAGWB-002, every build output is stored on disk and *opened* rather than decoded. A search over an unchanged vault therefore takes 0.16 s.

The problem is what happens when one note changes. Each build step's output is stored under a fingerprint (a *digest*). The source step's fingerprint covers every file's path, size and modification time, so editing one note changes it, and with it the fingerprint of every step downstream. Nothing stored matches any more, and the next search rebuilds the whole corpus: about 28 s on the vault (cold-build breakdown in §4.2). In daily use the vault changes all the time, so this cost is paid constantly.

This guide designs **delta builds**: when a step's input changed only a little, compute its new output from its *previous* output plus the *change*, instead of from scratch. The design has five parts:

1. **Stable, per-file identity.** Today a document's id is `slug(file name)`, truncated to 64 characters. On the vault, 2,282 files map to only 2,063 ids, because 219 files collide with another file's id. Chunk-id suffixes and the chunk `ord` field are also assigned over the whole corpus. Ids must depend only on the file itself, or one edit can rename chunks in unrelated files.
2. **A source manifest.** `source.dir` records `(path, size, mtime, content hash)` for every file, so the next build can compute exactly which files were added, changed or removed.
3. **Lineage.** A lookup from "this step of this pipeline" to its most recent stored output, independent of the corpus contents, so a build can find the output to start from.
4. **A delta protocol through the build DAG.** Steps that can work incrementally (`source.dir`, `parse.markdown`, `chunk.sections`, `index.bm25`, `index.terms`, `index.vector`, `map`-style components) receive their previous output plus an input delta, and emit a new output plus an output delta. Any other step falls back to a full rebuild.
5. **Copy-on-write artifacts.** Stored outputs stay immutable, but a new version can be made cheaply from an old one:
   - record stores are split into shards, and untouched shards are hard-linked;
   - a bleve index is cloned by hard-linking its immutable segment files, then the delta is applied to the clone.

**Goal:** editing one note and searching again costs **under 2 s on the vault** (estimate in §8.1, to be measured), with results **equivalent to a full rebuild**. The design defines "equivalent" precisely (§6.9) and tests it with random edit sequences.

## 2. Problem statement and scope

### 2.1 The problem

| Situation | What happens today | Cost on the vault |
|---|---|---|
| nothing changed | every step's output is found in the store and opened | 0.16 s per CLI search |
| one note edited | the source digest changes; every downstream digest changes; everything recomputes | ~28 s |
| `ragwb serve --watch 5s` | the next query after a change waits for the full rebuild, inline | ~28 s for one unlucky query |

Each full rebuild also writes about 400 MB of new artifacts (chunk records 260 MB, bleve 140 MB, plus terms and docs). That fills the store quickly.

### 2.2 In scope

- Stable document and chunk identity in both the Go components and rag.js, keeping browser parity.
- Source manifests and file-level diffs.
- A lineage index in the artifact store.
- The delta protocol in `Engine.build` and delta implementations for the built-in components listed above.
- Copy-on-write record stores (chunks and docs) and bleve index clones.
- Releasing superseded artifacts (open handles) in long-running processes.
- `ragwb serve --watch` that applies deltas, plus CLI controls: force a full build, verify, garbage-collect.
- Tests: equivalence with full builds, crash safety, and the performance target.

### 2.3 Out of scope

- Filesystem notifications (fsnotify). Polling the stat walk (16 ms on the vault) is enough for phase one.
- Incremental *LLM* steps beyond what the per-item caches already give.
- Incremental builds in the browser workbench page, which runs its own in-memory engine.
- A delta for the in-memory BM25 backend (`backend: 'memory'`). It rebuilds fully, as today.

## 3. Orientation: what you need to know first

Read RAGWB-002's guide §3 if any of these terms are new; here is the short version.

### 3.1 Pipeline, build, artifacts

```
plan (JS, e.g. examples/apps/parc/lib/corpus.js)
  rag.corpus("parc").from(rag.source.dir({root, include, exclude}))
     .parse(rag.markdown())
     .chunk(rag.sections({depth: 3, maxTokens: 450}))
     .index("text", rag.bm25({fields: {title: 3, headingPath: 2, text: 1}}))
     .index("meta", rag.terms({keys: ["type","tags","status"]}))

compile → IR (nodes + edges)                         build phase (once per corpus version)
  files ──► parse ──► chunks ──► ix-text (bleve)
                         └─────► ix-meta (terms)
                                                       live phase (per query)
  q ──► retrieve(text) ──► filter.where ──► limit ──► hits
```

- **Engine.build** (`pkg/ragjs/assets/rag.js:1354`) walks the build nodes in order. For each node it computes a **digest** (`Engine.digests`, `rag.js:1313`): a hash of the node type, params, input digests, the component's source (since PR #1) and an optional `def.digest(p)` hook.
- **Memo:** if the digest's output is in the in-memory memo or in the artifact store (`storeKey = hash([digest, def.src])`, `rag.js:1291`), the node is *reused*. Otherwise it is *computed* from its inputs and then *saved*.
- **Artifact store** (`pkg/artifacts/store.go`): content-addressed and immutable. A key always names the same bytes. Payloads are a blob (`<key>.bin`), a file (`<key>.<codec>`, e.g. a bbolt record file) or a directory (`<key>.<codec>/`, e.g. a bleve index).
- **`source.dir`'s digest hook** (`pkg/natives/sourcedir.go:232-274`) stats every matched file and hashes `(relpath, size, mtime)`. This is why one edit invalidates everything: the digest is a single hash over all files.

### 3.2 What each build component does today

| Component | Kind | Input → output | Per-unit? | Stored as | Source |
|---|---|---|---|---|---|
| `source.dir` | `all` | – → files `{name, path, text, size, mtime}` | per file (independent) | a marker; files are re-read lazily | `pkg/natives/sourcedir.go` |
| `parse.markdown` (Go) | `all` | files → docs | per file: `ParseMarkdown(name, text, frontmatter)` is pure | gob blob (`docs` codec) | `pkg/natives/markdown_native.go:34-50`, `markdown.go` |
| `chunk.sections` (Go) | `all` | docs → chunks | per doc (`ChunkSections`), **then a global `AssignIDs`** | bbolt record file (`records`) | `markdown_native.go:51-62`, `chunk.go:219-243` |
| `index.bm25` (bleve, default) | `all` | chunks → index | per chunk (one bleve document each) | directory (`bleve`) | `pkg/natives/bleve_bm25.go` |
| `index.terms` (Go) | `all` | chunks → index | per chunk (values extracted per chunk) | gob blob (`terms`) | `pkg/natives/terms.go` |
| `index.vector` | `all` | chunks → index | per chunk (embedding cached by text hash) | raw float32 (`vectors`) | `pkg/natives/vector.go`, `embcache.go` |
| `enrich`, `enrich.llm`, custom `map` | `map` | items → items | per item, with an item cache (`Engine.items`, `env.cache`) | JSON blob | `rag.js` build loop |
| custom `all`, `bake`, `synonyms` | `all` | anything → anything | unknown (arbitrary code) | JSON blob | `rag.js` |

Most of the pipeline is *naturally* per-file or per-chunk. The exceptions, which this design must handle, are:
- the global `AssignIDs`;
- arbitrary `all` components.

### 3.3 Terms used in this guide

- **Delta:** the difference between two versions of a step's output, expressed per unit:
  - files: added, changed, removed names;
  - docs: upserted docs and removed doc ids;
  - chunks: per doc, the full new chunk list, plus removed docs.
- **Lineage key:** an identity for "this node of this pipeline" that does *not* change when file contents change (§6.4). Two builds of the same plan over different vault versions share lineage keys but have different digests.
- **Base:** the stored output a delta build starts from, found through the lineage index.
- **Copy-on-write (COW):** making a new immutable artifact from a base by sharing unchanged parts (hard links) and rewriting only the changed parts.
- **Canonical order:** the order a *full* build produces. Files are sorted by name (`sourcedir.go:203-230` sorts), docs follow the files, and chunks follow the docs, by position. Incremental outputs must reproduce it.

## 4. Current state, with evidence

### 4.1 Change detection

`SourceDir.Digest` walks the tree with `matches(dp, …)`, collects `relpath\0size\0mtimeNanos` lines, sorts them and hashes them (`sourcedir.go:257-274`). It takes about 16 ms on the vault (RAGWB-001 diary step 24). `ragwb serve` caches it for the life of the process unless `--watch` is set (`DigestTTL`, RAGWB-001 step 24).

The digest says *whether* something changed, not *what* changed. Answering "what" needs a per-file manifest of the previous build.

### 4.2 What a full rebuild costs (vault, diary RAGWB-002 steps 5–6)

| Step | Time | Note |
|---|---|---|
| walk + read files | ~0.4 s | 2,282 files |
| parse.markdown | ~2.5–3 s | |
| chunk.sections + write records | 5.4–6.8 s | the records write is ~2–3 s of it |
| index.bm25 (bleve) | 15.5–17 s | |
| index.terms (Go) | 2.25 s | includes JS value extraction over 85,882 chunks |
| **total** | **~28 s** | max RSS 3.8–4.0 GB during the build |

### 4.3 Identity is not per-file

**Document ids.** `ParseMarkdown` sets `id := slug(name without .md)` (`pkg/natives/markdown.go:348`). rag.js does the same (`rag.js:284`). `slug` lowercases, maps non-`[a-z0-9]` runs to `-` and cuts to 64 characters (`rag.js:62`). `name` is the path relative to the root, so folders are included, but:
- **long paths truncate to the same 64-character prefix** (`projects-2026-05-06-article-protobuf-payload-contracts-and-sessi…`);
- **different paths can slug to the same id** (case, punctuation).

Measured on the stored vault chunks: **2,282 files, 2,063 distinct doc ids, and 49 doc ids whose chunks carry more than one title**. So at least 49 ids are shared by distinct notes, and 219 files lose their own identity. A re-measurement over the current file list (2,287 files) found 271 files sharing an id, and **no** pair of paths whose *uncapped* slugs are equal: every collision comes from the 64-character cut (§6.2). Where `art.docs` is built (`rag.js:1504`, `docs.set(d.id, d)`), the later file **overwrites** the earlier one. This is a live bug, independent of this ticket.

**Chunk ids and `ord`.** Each chunk's base id is `docId#slug(last heading)`. `AssignIDs` (`pkg/natives/chunk.go:219-243`; rag.js `assignIds`, `rag.js:332-342`) then deduplicates bases with a counter that spans **the whole corpus**, and sets `c.ord = i`, a **global** position. Consequences:
- inserting a file early in sort order shifts the `ord` of every later chunk;
- with colliding doc ids, a chunk's `-N` suffix depends on how many same-base chunks came before it *in other files*.

On the vault, 18,627 chunk ids carry a `-N` suffix. Most are legitimate repeats within one doc (e.g. several "Overview" headings).

`ord` is read in one place: `pack({order: 'doc'})` sorts evidence by `docId + ord` (`rag.js:731`). Only its order *within a document* matters there.

### 4.4 Storage layout relevant to deltas

- **Chunk records** (`pkg/natives/records.go`): one bbolt file. Bucket `c` uses ordinal keys (`ordKey(i)`, a 4-byte big-endian position), with buckets `i` (id → ordinal) and `d` (docId → ids). Ordinal keys make insertion in the middle impossible without renumbering, and a 260 MB file per version makes whole-file copies expensive.
- **bleve index** (`bleve_bm25.go`): a scorch directory. On the vault that is 16 immutable `*.zap` segment files (2–39 MB each) plus `root.bolt` (128 KB: the snapshot, segment list and deletion bitmaps) and `index_meta.json`. Segments are never modified in place; updates write new segments and record deletions in `root.bolt`.
- **Docs:** a gob blob, decoded in full when used.
- **Terms:** a gob blob (9.1 MB on the vault) holding ordinal postings (`terms.go`).
- **Vectors:** raw float32 rows (`vector_codec.go`). Embeddings are cached by `(model, dims, text hash)` in `~/.ragwb/cache/embeddings.db` (`embcache.go`), so re-embedding unchanged chunks costs a cache hit.

### 4.5 bleve facts that matter for deltas (bleve v2.6.0 source)

- **Document frequency excludes deleted docs.** `zapx PostingsList.Count()` subtracts the `except` (deleted) bitmap (`zapx/v16 posting.go:235-249`). The term-searcher `Count()` sums those (`index/scorch/snapshot_index_tfr.go:217-223`).
- **Doc count:** `IndexSnapshot.DocCount()` sums `segment.CountRoot()` (`snapshot_index.go:478-484`).
- **BM25 average length:** comes from `FieldCardinality(field) / docCount` (`search/searcher/search_term.go:83-130`; `snapshot_index.go:215`). Whether field cardinality drops deleted docs before a segment merge **must be verified** (Phase 0 spike). If it doesn't, scores after a delta drift slightly from a fresh build until segments merge, and a forced merge, or accepting the drift within the quality gate, is the fallback.
- **Read-only open:** `bleve.OpenUsing(dir, {"read_only": true})` (`index.go:330`; `scorch.go:219`), already used for stored indexes.

## 5. Gap analysis

| Need | Today | Gap |
|---|---|---|
| Know *which* files changed | one digest over all files | per-file manifest, stored with the build |
| Find the previous output of a step | outputs are looked up by exact digest only | a lineage index (§6.4) |
| Compute an output from its previous version | every component is `all`-from-scratch | a delta protocol and delta implementations (§6.5) |
| Ids stable under unrelated edits | global suffixes, global `ord`, colliding doc ids | per-file identity (§6.2) |
| Cheap new artifact versions | records: one 260 MB file with ordinal keys; bleve: a full rebuild | sharded records with canonical keys; bleve clone plus delta (§6.6) |
| Old handles released | Lazy loads register closers only at runtime close | evict superseded memo entries and close their handles (§6.7) |
| Trust | none needed (full builds) | an equivalence contract plus a verifier (§6.9) |

## 6. Proposed architecture

### 6.1 The picture

```
edit Research/foo.md
        │
ragwb parc search … / serve --watch tick
        │
Engine.build(ir)
  ├─ files   digest changed ─► lineage base found? ─► source.dir.delta: manifest diff (stat, hash suspects)
  │                                                    Δfiles = {changed: [Research/foo.md]}
  ├─ parse   digest changed ─► base + Δfiles ─► parse.delta: ParseMarkdown(foo.md) → Δdocs = {upsert: [doc foo]}
  │                                             new docs artifact = COW(base docs shards, Δdocs)
  ├─ chunks  digest changed ─► base + Δdocs  ─► chunk.delta: ChunkSections(foo) → Δchunks = {foo: [c1..c7], removed: []}
  │                                             new chunk records = COW(base shards, Δchunks)
  ├─ ix-text digest changed ─► base + Δchunks ─► clone bleve dir (hard-link *.zap, copy root.bolt) →
  │                                             batch: delete old ids of foo, index c1..c7 → close → SaveDir
  └─ ix-meta digest changed ─► base + Δchunks ─► rebuild Terms from stored per-chunk rows ± foo's rows
        │
lineage index updated (each node: lineage key → new store key)          every artifact: new key, immutable
        │
artifacts(ir) ─► views over the new records, the new bleve dir         superseded handles closed (§6.7)
```

Every arrow on the delta path has a **fallback**. The node then runs its ordinary full computation, and its output delta to downstream nodes is "everything changed" (`Δ = full`), which forces downstream nodes to rebuild fully too, unless they are `map`-style (§6.5).

### 6.2 Phase 1: stable per-file identity

**Rules** (identical in Go and rag.js, so browser parity holds):

```js
// document id: computed from the file's own relative path only (no global state)
function docIdFor(name) {
  const stem = name.replace(/\.md$/i, '');
  const full = slugUncapped(stem);                          // slug()'s rules without the 64-char cap
  if (full.length <= 160) return full;                      // ids ≤ 64 chars are exactly today's ids
  return full.slice(0, 151) + '-' + sha256hex(name).slice(0, 8);  // very long paths: prefix + hash of the path
}
```

A file cannot know whether *another* file collides with it, and anything that needs that knowledge brings back global state. So the rule is purely local:
- ids are the **full** path slug up to 160 characters;
- longer paths get a 151-character prefix plus an 8-hex hash of the path.

**Measured on the vault** (2,287 files, Python re-implementation of `slug`, parc's include and exclude rules):
- Today, 271 files share an id with another file.
- **None of them are true collisions.** No two paths have the same *uncapped* slug; every collision comes from the 64-character cut.
- Uncapped slug lengths: median 88, p90 118, p99 146, max 183. A 160-character cap keeps 2,278 ids fully readable; 9 get the hash suffix (at cap 128 it would be 110, at the old cap 64 it would be 1,911).

With this rule, every vault file gets its own id. True slug collisions (two paths whose full slugs are equal, e.g. `Foo Bar.md` and `foo-bar.md` in one folder) remain theoretically possible. The corpus assembly **reports** them in the build report (`report.warnings`) rather than silently overwriting in `art.docs`.

**Chunk ids:** `AssignIDs` runs **per document**. The `seen` map is reset for each doc, so suffixes depend only on the doc's own chunks. Uniqueness across docs follows from unique doc ids, because every chunk id is prefixed with `docId#`.

**`ord`:** the position **within the document** (0, 1, 2, …). `pack({order: 'doc'})` only needs within-doc order. Global positions are no longer stored anywhere.

**Compatibility:**
- Browser corpora are flat file lists with short names, so their ids don't change.
- On the vault, ids change for long paths and for the cross-doc suffix cases.
- Bump `natives.Version`, plus the rag.js source hash through the namespace, so old stores are not reused.
- `TestMarkdownParity` (whole vault, Go vs rag.js) must still pass, since both sides change together.

### 6.3 Phase 2: the source manifest

`source.dir` persists a **manifest** instead of the marker `0`:

```go
// pkg/natives/manifest.go
type ManifestEntry struct {
    Name  string // relpath, slash-separated (FileRecord.Name)
    Size  int64
    Mtime int64  // UnixNano
    Hash  string // hex sha256 of the bytes as read (after the maxBytes/skipHtml filters)
}
type Manifest struct {
    Params  string          // canonical root/include/exclude/maxBytes/skipHtml (the lineage part)
    Entries []ManifestEntry // sorted by Name (canonical order)
}

// DiffManifests: cheap first (stat), exact second (hash), so `touch` is not a change.
func DiffManifests(old Manifest, now []StatEntry, readHash func(name string) (string, error)) (FilesDelta, Manifest, error)

type FilesDelta struct {
    Added, Changed []string // names (new manifest has their hashes)
    Removed        []string
    Full           bool     // true when the diff is not usable (no base, params differ, > threshold)
}
```

- The walk already reads every file's text during a full build (`Walk`). Hashing those bytes costs about 0.1 s for the vault's ~20 MB, a cheap addition.
- In a delta build, only files whose `(size, mtime)` changed are read and hashed. A file whose hash is unchanged (touched, or saved without edits) is **not** a change; its new mtime goes into the new manifest.
- The manifest is stored as the `source.dir` node's artifact (a JSON blob, ~250 KB for the vault).
- **Renames** appear as remove + add. Doc ids derive from paths, so a renamed note gets a new id. That is correct, because its links and identity changed too.

### 6.4 Phase 2: lineage

**Lineage key:** the node's digest computed with every source node's `def.digest` contribution replaced by its `def.lineage(p)` (resolved parameters only, no file stats):

```js
// rag.js Engine
lineages(ir) {                                  // like digests(), but stable across corpus edits
  const out = new Map();
  for (const n of ir.nodes) {
    if (n.phase !== 'build') continue;
    const def = REG.get(n.t);
    const extra = def && typeof def.lineage === 'function' ? String(def.lineage(n.p, this))
                : def && typeof def.digest === 'function' ? null       // a digest hook without a lineage hook: not lineage-capable
                : '';
    out.set(n.id, extra === null ? null
      : hash(['lineage', n.t, n.p, n.in.map(i => out.get(i)), extra, ...(def && def.src ? [def.src] : [])]));
  }
  return out;                                   // null = no lineage (always full)
}
```

**Lineage index:** a small table in the artifact store, per namespace:

```go
// pkg/artifacts/lineage.go
type LineageEntry struct {
    Lineage  string    `json:"lineage"`
    StoreKey string    `json:"storeKey"`   // the stored output of this version
    Digest   string    `json:"digest"`
    Source   string    `json:"source"`     // store key of the source manifest this version was built from
    Created  time.Time `json:"created"`
}
func (s *Store) LineageLatest(lineage string) (*LineageEntry, error) // most recent with a present payload
func (s *Store) LineageRecord(e LineageEntry) error                  // append; keeps the last N (default 3)
```

The index is stored as `<dir>/<ns>/lineage/<lineage[:2]>/<lineage>.json`: a small JSON list, written atomically like metadata. Keeping a few versions gives a fallback when the latest payload was pruned or damaged.

### 6.5 Phase 3: the delta protocol

**Delta shapes**, as passed between nodes in JS. Large payloads stay in Go and travel as handles.

```js
// files → docs → chunks
FilesDelta  = { full: false, added: [file], changed: [file], removed: [name] }          // file = {name, path, text, size, mtime}
DocsDelta   = { full: false, upsert: [doc], remove: [docId] }
ChunksDelta = { full: false, byDoc: Map<docId, chunk[]>, removeDocs: [docId] }           // a doc's chunk list replaces its old one
FULL        = { full: true }                                                             // "everything changed": recompute
```

**Component hook** (opt-in, next to `all`/`map`):

```js
rag.define('chunk.sections', {
  ..., all: ...,
  delta: {
    // prev: the base output (array or record view); inputs: per input {value, delta}; returns the new output and its delta
    apply(prev, [docsIn], ctx, p) {
      if (docsIn.delta.full) return null;                       // null = "can't": the engine runs all()
      const byDoc = new Map();
      for (const d of docsIn.delta.upsert) byDoc.set(d.id, chunkDoc(d, p)); // per-doc AssignIDs (Phase 1)
      return { value: cow(prev, byDoc, docsIn.delta.remove), delta: { full: false, byDoc, removeDocs: docsIn.delta.remove } };
    },
  },
});
```

**Engine.build with deltas** (sketch of the change to `rag.js:1354-1440`):

```js
for (const n of buildOrder) {
  const d = digest(n), lin = lineage(n);
  if (memoOrStored(d)) { outputs.set(n.id, reuse(d)); deltas.set(n.id, NONE); continue; }  // unchanged: Δ = none
  const def = REG.get(n.t);
  const ins = n.in.map(i => ({ value: outputs.get(i), delta: deltas.get(i) }));
  let res = null;
  if (def.delta && lin && !opts.full && ins.every(x => x.delta !== FULL || def.delta.acceptsFull)) {
    const base = store.lineageLatest(lin);                       // the previous output of this node
    if (base) res = await tryDelta(() => def.delta.apply(loadBase(base), ins, ctx, n.p));  // errors → null
  }
  if (res) { st.incremental = true; st.delta = summarize(res.delta); }
  else     { res = { value: await computeFull(n, ins), delta: FULL }; }
  outputs.set(n.id, res.value); deltas.set(n.id, res.delta);
  save(storeKey(d, def), res.value);  store.lineageRecord({ lineage: lin, storeKey: storeKey(d, def), digest: d, source: sourceKey });
}
```

**Capabilities:**

| Component | `delta.apply` | Notes |
|---|---|---|
| `source.dir` | manifest diff → FilesDelta | full when there is no base manifest, params changed, or the diff exceeds the threshold (§6.8) |
| `parse.markdown` | parse added + changed files; remove docs of removed files | output: COW docs records |
| `chunk.sections` | chunk upserted docs (per-doc ids); drop removed docs | output: COW chunk records |
| `index.bm25` (bleve) | clone the base dir; delete the old chunk ids of changed/removed docs; index new chunks | old ids come from the base chunk records' `doc` bucket |
| `index.bm25` (memory) | — | full rebuild (8 s), as today |
| `index.terms` | rebuild from stored per-chunk value rows, minus removed docs, plus new rows | identical to a full build by construction (D6) |
| `index.vector` | copy the base rows, drop removed chunks, embed new ones (cache hits for unchanged text) | |
| `map` components (`enrich`, custom) | map only upserted items; carry the base items of unchanged docs | the generic engine path; item caches already cover the per-item work |
| custom `all`, `bake`, `synonyms` | — | full; downstream gets `FULL` |

### 6.6 Phase 3: copy-on-write artifacts

**Sharded record stores (codec `records2`, a directory payload).** Used for both chunks and docs:

```
<key>.records2/
  manifest.json          {"version":2, "kind":"chunks", "count":85882, "shards":64, "shardFiles":{"07": "07-<hash>.bolt", …}}
  07-3fa2c1d0.bolt       bucket r: (docKey, pos) → record JSON; bucket i: id → (docKey,pos); bucket d: docId → [ids]
  …
```

- **Shard:** `shard(docId) = fnv32(docId) % 64`. All chunks of a doc live in one shard.
- **Canonical key:** `docKey + "\x00" + pos(uint32 BE)`, where `docKey` is the doc's source name (path), so a key's byte order *is* the full build's order: files sorted by name, then chunk position. Iteration (`values()`) does a k-way merge of 64 cursors by key. That reproduces canonical order regardless of which shards were rewritten.
- **COW:** a new version hard-links every shard file that has no upserted or removed doc. Each touched shard is copied, modified in one write transaction and renamed under a new content name. For a one-note edit, that is one shard copied (about 4 MB for vault chunks) and 63 hard links.
- **Readers** open shards lazily: a `get(id)` opens one shard, and `values()` opens all of them.

**bleve clone:**

```go
func CloneBleve(src, dst string) error {
    // scorch layout: index_meta.json, store/root.bolt, store/*.zap (immutable)
    mkdir(dst/store); copy(src/index_meta.json); copy(src/store/root.bolt)
    for each src/store/*.zap: os.Link(src, dst)   // hard link: no bytes copied
}
func (b *BleveBm25) ApplyDelta(dst string, removeIDs []string, add []BleveDoc) error {
    CloneBleve(b.path, dst); idx := bleve.Open(dst)           // writable clone
    batch: for id in removeIDs: Delete(id); for d in add: Index(d.id, d.fields)
    idx.Batch(batch); idx.Close()                             // scorch writes new segments + a new root.bolt
}
```

Scorch only adds files and replaces `root.bolt`. A later merge deletes obsolete segment files in the *clone's* directory, which removes the clone's hard link, not the base's file. Store directories are on one filesystem (the namespace), so hard links work. A cross-device store falls back to copying, which is slow but correct.

**Docs** move from the gob blob to `records2`, so `parse.markdown`'s delta doesn't have to decode the base docs. As a side benefit, `art.docs` becomes a view and stops loading all docs when first touched.

### 6.7 Releasing superseded artifacts

In a long-running `ragwb serve --watch`, every delta build opens new artifacts, while the old record views and bleve handles stay open until the runtime closes. Over a day of edits that leaks file descriptors and mmaps. The fix has three parts:
- **`Engine.gc(ir)`:** after a successful build, drop memo entries whose digest is not in the current `digests(ir)` and call `close()` on their handles. Persist-loaded handles get a `dispose` hook, and the runtime-level closer stays as the backstop.
- **`artifacts()` cache:** keyed by the digest list (`_art.key`). It is replaced naturally, and the session memo already clears on an `art.key` change (PR #1 review fix).
- **In-flight queries keep their artifacts.** A query that started before the swap holds references to the old view and index. Closing must wait until no query is running. In serve, queries and builds share the runtime's owner goroutine, so a build never interleaves with a query's synchronous part. LLM awaits are the exception: close handles **after the next query completes**, using a generation counter.

### 6.8 serve, CLI and thresholds

- **`ragwb serve --watch 2s`:** a Go ticker computes the source digest (16 ms). On a change it schedules `eng.build(ir)` on the runtime owner, then `Engine.gc`. Queries arriving during a delta build (about a second) wait on the owner.
- **`--full`** (global): ignore lineage and rebuild everything. Used to debug and to verify.
- **Threshold:** if more than 20% of files changed, or the base is missing, `source.dir` returns `FULL`. A large change rebuilds faster in one pass than as deltas against a mostly stale base.
- **`ragwb store verify <plan>`:** build fully into a temporary namespace and compare against the current incremental artifacts with the equivalence check (§6.9).
- **`ragwb store gc`:** keep the newest *N* lineage versions plus their sources, and delete unreferenced keys in the current namespace. `prune` keeps deleting other namespaces.

### 6.9 Correctness: what "equivalent to a full build" means

For a corpus state S, an incremental build reached by any sequence of edits must produce outputs **equivalent** to a full build of S:
- **files/manifest:** the same entries (name, size, mtime, hash), in canonical order;
- **docs and chunks:** the same set of ids, byte-identical records per id (JSON), and the same canonical iteration order;
- **terms:** identical search results (same JSON), guaranteed by rebuilding from canonical rows (D6);
- **bleve:**
  - the same hit ids for a fixed query set;
  - scores equal within 1e-6 relative, or within the documented drift if Phase 0 shows that field cardinality lags deletions; the remedy is a forced merge;
  - tie order may differ only among equal scores;
- **vectors:** identical rows per chunk id; row order may differ, because search is exhaustive cosine.

`ragwb store verify` and the property tests (§11) check exactly this.

## 7. Decision records

### Decision D1: immutable, copy-on-write artifacts; no in-place mutation

- **Context:** The store is content-addressed (RAGWB-001 P8, RAGWB-002). Readers in several processes open artifacts read-only.
- **Options:**
  - (a) mutate the stored index and records in place;
  - (b) derive a new immutable artifact from the base by sharing unchanged parts.
- **Decision:** (b).
- **Rationale:**
  - Concurrent readers, such as a served app and a CLI query, keep a consistent snapshot.
  - Crash safety is inherited: a half-written new version is just an unreferenced temp dir.
  - Keys still mean "these exact bytes".
  - Hard links make sharing cheap.
- **Consequences:**
  - Each version costs the bytes of its changed parts: one record shard plus the new bleve segments and `root.bolt`.
  - Garbage collection is needed (`store gc`).
- **Status:** proposed.

### Decision D2: a lineage index, not a store scan

- **Context:** A delta build must find "the previous output of this node".
- **Options:**
  - (a) scan the store's metadata for the same node type and params;
  - (b) a lineage key plus a small index of recent versions.
- **Decision:** (b).
- **Rationale:** Lookup is O(1). The key is defined by the same inputs as the digest, minus file contents, so "same pipeline" is exact rather than heuristic.
- **Consequences:**
  - Components with a `digest` hook must also provide a `lineage` hook to be delta-capable (only `source.dir` has one).
  - The index is advisory: if its entry points to a missing payload, the next entry is tried, and failing that, a full build.
- **Status:** proposed.

### Decision D3: identity from the file path, computed locally

- **Context:** Doc ids collide (219 files on the vault). Chunk suffixes and `ord` are global.
- **Options:**
  - (a) keep the ids, and make deltas recompute every doc that shares an id or suffix base;
  - (b) full path slugs (cap 160, a hash suffix only beyond it), per-doc suffixes, per-doc `ord`;
  - (c) opaque ids (hashes only).
- **Decision:** (b).
- **Rationale:**
  - Locality is what makes deltas correct.
  - Readable ids matter: chunk ids show up in the inspector, in citations and in the agent's tools.
  - A 160-character cap keeps 2,278 of the vault's 2,287 ids fully readable (9 get a hash suffix); ids that fit in 64 characters, including all browser corpora, are unchanged.
- **Consequences:**
  - On the vault, ids longer than 64 characters change once: they become the full slug (a version bump; stores rebuild once). Chunk ids and citations get longer.
  - True slug collisions remain possible and are now *reported*, not silently overwritten.
  - rag.js changes too (shared with the browser), keeping parity.
- **Status:** proposed.

### Decision D4: sharded record stores with canonical keys

- **Context:** The chunk record file is 260 MB with ordinal keys. A COW copy per edit would cost about 0.3–0.5 s and 260 MB each.
- **Options:**
  - (a) copy the whole file and renumber;
  - (b) one bbolt file with canonical keys, copied whole;
  - (c) 64 shards by doc, canonical keys, hard-link untouched shards;
  - (d) store chunks as bleve stored fields.
- **Decision:** (c).
- **Rationale:**
  - A one-note edit rewrites about 4 MB.
  - Canonical keys make the full and incremental orders equal by construction.
  - Lazy shard opening keeps `get` cheap.
  - (d) would couple chunks to one index type (RAGWB-002 D5).
- **Consequences:**
  - `values()` becomes a 64-way merge.
  - A records format version 2 is needed, readable alongside v1 until old stores are pruned.
- **Status:** proposed.

### Decision D5: the delta protocol lives in Engine.build, opt-in per component

- **Context:** Deltas must flow through the DAG, and users can define components.
- **Options:**
  - (a) a Go-only side path that understands the built-in pipeline shape;
  - (b) an engine-level protocol with `def.delta.apply`, falling back to `all`.
- **Decision:** (b).
- **Rationale:**
  - It works for any plan shape, including enrich steps and custom `map` components.
  - Unknown components are safe by default (they rebuild fully).
  - The inspector can show `incremental` per node.
- **Consequences:**
  - rag.js gains the protocol. In the browser, nothing persists, so `delta` is never used; the code path is shared but inert.
- **Status:** proposed.

### Decision D6: terms are rebuilt from canonical per-chunk rows

- **Context:** `Terms` must equal rag.js's `TermsIndex` over the full chunk array (RAGWB-002 parity test). Its search sums contributions in value-insertion order and keeps insertion order for keys.
- **Options:**
  - (a) patch the postings in place, accepting float-order drift;
  - (b) store per-chunk extracted value rows in canonical order and rebuild `Terms` from rows on every delta.
- **Decision:** (b).
- **Rationale:**
  - It is identical to a full build by construction.
  - Rebuilding `NewTerms` from rows is fast. The JS extraction, about 2 s of today's 2.25 s, runs only for the changed chunks.
- **Consequences:**
  - The terms artifact also stores its rows (about 10 MB on the vault).
  - Measure the rebuild; the target is under 200 ms.
- **Status:** proposed.

### Decision D7: thresholds and fallbacks

- **Decision:** Fall back to full when there is no base, more than 20% of files changed, params changed (a different lineage anyway), any `delta.apply` throws, or `--full` is set.
- **Rationale:** Deltas are an optimization. Correctness must never depend on them.
- **Status:** proposed; tune the threshold after Phase 5 measurements.

## 8. Key flows

### 8.1 Edit one note (the target case), with cost estimates

| Step | Work | Estimate |
|---|---|---|
| digest walk | stat 2,282 files | 16 ms (measured) |
| manifest diff | 1 file changed by stat; read + hash it | < 5 ms |
| parse | `ParseMarkdown` on 1 file; COW docs: 1 shard | ~10 ms + one shard copy |
| chunk | `ChunkSections` on 1 doc; COW chunk records: 1 shard (~4 MB) | ~30 ms |
| bleve | clone (16 hard links + 128 KB copy); delete ~40 ids, index ~40 docs; close (persist) | 100–500 ms (spike) |
| terms | rebuild from ~85k rows, about 50 of them new | < 200 ms (spike) |
| lineage + metadata writes | a few small files | < 10 ms |
| **total** | | **~0.3–1 s**, target < 2 s |

### 8.2 Delete a note

FilesDelta `removed: [name]` → DocsDelta `remove: [docId]` → ChunksDelta `removeDocs: [docId]` → bleve deletes that doc's old chunk ids → terms rows dropped. Links *to* the deleted note resolve to nothing at query time (`art.links` is computed lazily from docs).

### 8.3 Rename a note

A rename is remove(old path) plus add(new path), giving a new doc id and new chunk ids. Equivalent to a full build by construction.

### 8.4 Bulk change (e.g. `git pull` touching 30% of files)

The `source.dir` threshold triggers `FULL`, and a full rebuild follows (~28 s). Correct and simple.

## 9. API reference

### 9.1 JS component hooks (rag.js)

| Hook | Signature | Meaning |
|---|---|---|
| `def.lineage` | `(p, engine) → string` | stable identity part for source components (required if `def.digest` exists, to be delta-capable) |
| `def.delta.apply` | `(prev, inputs: [{value, delta}], ctx, p) → {value, delta} \| null` | compute the new output from the base; `null` → full |
| `def.delta.acceptsFull` | `boolean` | can handle a `FULL` input delta (e.g. `map` components re-map everything, using their caches) |
| report fields | `report.nodes[id].incremental`, `.delta` (`{upserts, removes}`), `.base` (store key) | shown by `--timings` and the inspector |

### 9.2 Go

| Package | API |
|---|---|
| `pkg/natives` | `Manifest`, `ManifestEntry`, `DiffManifests`, `FilesDelta` (`manifest.go`) |
| `pkg/natives` | `ParseMarkdown` unchanged; `DocIDFor(name)`; `AssignIDsPerDoc` (`markdown.go`, `chunk.go`) |
| `pkg/natives` | `Records2` writer/reader, `CowRecords(base, upserts map[docID][]Record, removes []docID) (dir, error)` (`records2.go`) |
| `pkg/natives` | `CloneBleve(src, dst)`, `(*BleveBm25).ApplyDelta(dst, removeIDs, add)` (`bleve_bm25.go`) |
| `pkg/natives` | `TermsRows` (per-chunk values) + `NewTermsFromRows` (`terms.go`) |
| `pkg/artifacts` | `LineageLatest`, `LineageRecord`, `GC(keep int)` (`lineage.go`) |
| `pkg/runner` | `Config.Full` (ignore lineage) |
| `cmd/ragwb/cmds` | global `--full`; `serve --watch` delta builds; `store verify`, `store gc` |

## 10. Implementation phases

**Phase 0: spikes** (no product code).
- **bleve:** clone a stored vault index, apply a 40-doc delta, and compare `DocCount`, `FieldCardinality` and the top 10 for 50 queries against a fresh build of the same chunk set. Measure the clone and apply times.
- **bbolt:** COW of one 4 MB shard: time and size.
- **Exit:** the numbers recorded in the diary, and the bleve equivalence decision (exact, merge, or tolerance).

**Phase 1: stable identity.**
- `DocIDFor` (full slug up to 160 characters, hash suffix beyond), per-doc `AssignIDs`, per-doc `ord`, in Go and rag.js.
- Collision reporting in the build report.
- Version bump.
- Tests: Go/JS markdown parity on the reports corpus and the vault; on the vault, unique doc ids for every file except true slug collisions (counted, expected ≤ a handful); `pack({order:'doc'})` unchanged.

**Phase 2: manifest and lineage.**
- `source.dir` persists its manifest.
- `DiffManifests`; `Engine.lineages`; the lineage index in `pkg/artifacts`.
- Report fields, still without deltas.
- Tests: diffs for add/change/remove/rename/touch; lineage stable across edits and different across params.

**Phase 3: records v2, the delta protocol, and the core deltas.**
- `records2`, sharded with canonical keys and COW, for chunks and docs.
- The engine protocol.
- Deltas for `source.dir`, `parse.markdown` and `chunk.sections`.
- Tests: the equivalence property test for docs and chunks (§11).

**Phase 4: index deltas.**
- bleve clone + apply.
- Terms rows + rebuild.
- Vector rows.
- `map` components (generic).
- Tests: equivalence for the indexes; crash safety (kill during apply → no corruption, next build succeeds).

**Phase 5: serve, CLI and measurements.**
- `--watch` delta builds plus `Engine.gc` and deferred close.
- `--full`, `store verify`, `store gc`.
- Vault measurements (edit one note: time, bytes written, RSS); thresholds tuned; docs.

## 11. Test strategy

| Test | What | Where |
|---|---|---|
| **Equivalence property test** | from a seed corpus, apply random sequences of edits (add, change text, change frontmatter, delete, rename, touch); after each step, incremental outputs equal a fresh full build per §6.9 | `pkg/runner/incremental_test.go` |
| Identity | doc ids unique per file, except reported slug collisions; chunk ids stable when *other* files change | `pkg/natives`, `pkg/runner` |
| Parity | Go vs rag.js markdown/chunks on reports + vault (existing tests, updated ids) | `markdown_parity_test.go` |
| Manifest | diff cases, including `touch` (mtime only) → no change | `pkg/natives/manifest_test.go` |
| Lineage | same plan → same lineage across edits; changed params → different lineage | `pkg/ragjs`, `pkg/artifacts` |
| COW | untouched shards are hard links (same inode); touched shards differ; the base is unchanged after a delta | `pkg/natives/records2_test.go` |
| bleve | a delta-applied clone equals a fresh index (Phase 0 criterion); the base dir is unchanged | `pkg/natives/bleve_bm25_test.go` |
| Crash safety | interrupt during COW/apply (temp dir left) → the next build ignores it, and `store gc` removes it | `pkg/runner` |
| Performance | vault: edit one note → search < 2 s; bytes written < 50 MB | `scripts/` + diary |
| Serve | `--watch`: edit, then the next query sees the change; no fd growth over 50 edits | `pkg/server` / manual |

## 12. Risks, alternatives and open questions

- **bleve field-cardinality drift** after deletions (§4.5). Mitigation: a forced merge after apply, or drift accepted within the RAGWB-002 quality gate. Phase 0 decides.
- **Hard links** need the store on one filesystem. The temp dirs are inside the namespace, as already done in RAGWB-002. On filesystems without hard links, copy, which is slow but correct.
- **Store growth:** each version adds its changed parts. Mitigation: `store gc`, keeping N lineage versions.
- **Plans with custom `all` components** in the middle of the DAG lose incrementality downstream of them. That is by design (D5); the inspector shows it.
- **Alternative considered:** a single mutable bleve index plus a mutable record DB, updated in place. It is simpler, but it breaks the immutable-artifact model, multi-reader consistency and crash safety (D1).
- **Open:**
  - whether docs records should store parsed `blocks` (large) or re-parse on demand;
  - whether fsnotify is worth it over a 16 ms poll;
  - whether `--watch` should build on a second runtime and swap, to keep queries flowing during a delta build. A delta build is about a second, so phase one blocks.

## 13. File reference

| File | Role today | Change |
|---|---|---|
| `pkg/natives/sourcedir.go` | walk (`:203`), digest (`:232-274`), persist marker | manifest persist, `lineage` hook, delta |
| `pkg/natives/markdown.go` | `ParseMarkdown`; doc id (`:348`) | `DocIDFor` |
| `pkg/natives/chunk.go` | `ChunkSections`, global `AssignIDs` (`:219-243`) | per-doc ids and ord |
| `pkg/natives/markdown_native.go` | `parse.markdown`/`chunk.sections` defines (`:21-28`, `:34-62`) | `delta` hooks |
| `pkg/natives/records.go` | chunk records v1 (ordinal keys) | v2 sharded COW in `records2.go` |
| `pkg/natives/bleve_bm25.go` | build/open/relocate/search | `CloneBleve`, `ApplyDelta` |
| `pkg/natives/terms.go` | Go TermsIndex | rows + rebuild |
| `pkg/natives/vector.go`, `embcache.go` | vector index, embedding cache | row deltas |
| `pkg/natives/persist.go` | codecs, save/load | records2, lineage writes, dispose |
| `pkg/artifacts/store.go` | payloads, list, prune | `lineage.go`, `GC` |
| `pkg/ragjs/assets/rag.js` | `slug` (`:62`), `parseMarkdown` id (`:284`), `assignIds` (`:332`), digests (`:1313`), build (`:1354`), artifacts (`:1499`) | identity, `lineages`, delta protocol, `gc` |
| `pkg/runner/runtime.go`, `serve.go`, `cmd/ragwb/cmds/serve.go` | factory, served apps, `--watch` (TTL) | watcher, `Full` |

## 14. References

- **RAGWB-001:** design guide §9 (architecture); diary steps 21 (P8 store), 23 (`--timings`), 24 (serve, `--watch`).
- **RAGWB-002:** design guide (disk-backed retrieval, §10.1 status); diary steps 4–6 (quality gate, records, bleve persistence, measurements).
- **bleve v2.6.0:**
  - `index.go:330`;
  - `index/scorch/scorch.go:219`;
  - `index/scorch/snapshot_index.go:215` (`FieldCardinality`), `:478` (`DocCount`);
  - `index/scorch/snapshot_index_tfr.go:217`;
  - `search/searcher/search_term.go:83-130`;
  - `zapx/v16 posting.go:235-249`.
- **bbolt v1.4.0:** read-only opens use a shared lock, so many processes can read concurrently.
