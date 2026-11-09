---
Title: Implementation contracts and migration examples
Ticket: RAGWB-009
Status: active
Topics:
    - rag
    - search
DocType: reference
Intent: long-term
Owners: []
RelatedFiles:
    - Path: repo://pkg/artifacts/snapshot.go
      Note: Publication and build leases
    - Path: repo://pkg/ragjs/src/engine/dataset.ts
      Note: Dataset and qualified identity contracts
    - Path: repo://pkg/rtenv/resources.go
      Note: Runtime worker and native resource ownership
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-09T00:19:00.672701848-05:00
WhatFor: ""
WhenToUse: ""
---

# Implementation contracts and migration examples

This is the implementation reference for RAGWB-009. The intern guides explain the reasoning; this document records the actual API cutover. Existing scripts and serialized plans must be updated. There are no old-shape adapters.

## Records identify their dataset

A passage may appear in both the original chunk output and an enriched output. Its local ID can be identical in both. Search hits now carry an opaque ID containing both the dataset digest and the local ID. Fusion therefore keeps these distinct records, and resolving a hit cannot accidentally choose whichever output loaded last.

The guide sketched an object `{dataset, id}`. The implementation encodes that pair as a canonical JSON string, branded `QualifiedRecordID` in TypeScript. This keeps one identifier usable as a map key, JSON field and model-tool argument. Applications should pass the ID through unchanged. Do not split on `#`, construct IDs from filenames, or decode them for ordinary lookups.

```js
const result = await rag.run(plan, {q: 'snapshot ownership'});
const hit = result.hits[0];
const chunk = result.chunk(hit.id);
// chunk.docId is also qualified. It is not a substring of hit.id.
```

Record catalogs expose `get`, `values`, `entries` and `size`. Records returned from datasets are immutable copies. Low-level index implementations operate on local IDs internally; the snapshot binds search results to their source dataset at the public boundary.

## Build callbacks receive datasets

Both cold and stored builds give callbacks the same interface. Use iteration when possible, and request an array explicitly when an algorithm needs one.

```js
rag.define('chunk.custom', {
  phase: 'build', in: ['docs'], out: 'chunks',
  all: ([documents]) => documents.materialize().map(document => ({
    id: document.id + '#body', docId: document.id,
    title: document.title, text: document.text,
    headingPath: [], blocks: [], meta: {},
  })),
});
```

The Dataset interface has `id`, `kind`, `size`, `get(localID)`, `values()` and `materialize()`. IDs inside these build-time records are local. Do not qualify them manually; the snapshot performs that conversion when exposing records to queries.

Bakes declare every dataset they consume. Their cache identity includes the callback and bound dataset identities; equal record counts do not imply equal content.

```js
rag.bake({datasets: {source: 'chunks'}}, ({datasets}) => {
  const terms = {};
  for (const chunk of datasets.source.values()) {
    // derive terms from this explicitly selected output
  }
  return terms;
});
```

## A query leases one complete snapshot

A build prepares outputs privately. Deferred provider work cannot become a queryable partial snapshot. The engine validates and retains a complete candidate, publishes the persistent current pointer, and selects the in-memory snapshot. Builds for the same graph slot serialize publication. Memoized output and current publication are separate: reverting source content can reuse old computations while still changing the selected snapshot.

A query acquires its snapshot before executing live nodes and releases it in `finally`. It can await a provider while refresh publishes newer snapshots. Reference-counted native handles and store pins preserve the old query's resources. Two recent generations per slot remain available for later HTTP lookups; a query lease can outlive that retention window.

Returned results contain copied records and plain output data. Their `chunk()` and `top()` methods use those copies. They continue working after refresh and collection. Cycles, native resource instances, excessive nesting and oversized detached data are rejected at the result boundary.

```text
build candidate -> validate + acquire -> publish -> current snapshot
query           -> acquire snapshot -> await live work -> copy result -> release
later lookup    -> generation ID -> acquire retained snapshot -> read -> release
```

HTTP chunk and document lookups require both identifiers:

```js
const query = new URLSearchParams({snapshotId: result.snapshotId, id: hit.id});
fetch('/api/rag/reports/chunk?' + query);
```

An evicted generation returns HTTP 410 with `stale_generation`. Repeat the query to get current references. The browser cache includes generation identity, rejects late responses from another generation, and keeps each result's record copies independent of the page cache.

## Shared work and shutdown have explicit owners

Identical build requests join one engine-owned job. Each waiter has its own cancellation listener and event subscription. Cancelling one waiter detaches it; cancelling the last waiter aborts the job. A successor waits for a cancelling job to exit before starting.

Native sampling, decisions and reranking share an operation arbiter. Accepting a terminal outcome, delivering it to the VM, and worker exit are distinct events. Worker cleanup cannot reclassify an already accepted provider failure as cancellation.

Native handles enter one runtime registry through adoption. Failed adoption leaves ownership with the opener; managed close is idempotent. Shutdown cancels and waits for tracked workers. If they do not exit within the shutdown budget, it reports incomplete shutdown and retains their resources until actual exit. `ServedApp.Close()` now returns that error.

## Store ownership and collection

One process owns a writable namespace through an OS lock at `.locks/<namespace>.lock`. A second writer or offline collector refuses ownership. Stop the serving owner before using a separate process to prune that namespace.

Completed artifact descriptors contain dependency edges. Snapshot manifests and active pins determine reachability; the bounded lineage list is only an incremental-build search aid. A build lease excludes collection while unpublished outputs are being assembled. After the lease ends, unreferenced outputs can be collected immediately. This replaces the intermediate implementation's one-hour grace period.

Collection inventories the explicit store layout, validates authoritative metadata before deletion, retains current/recent snapshots, follows dependencies, and marks active pins. Corrupt manifests or descriptors abort collection. Payload files and directory entries are synchronized before their descriptors; metadata and current-pointer replacement use atomic writes and directory synchronization. This is ordering discipline, not a claim of tested recovery from every filesystem or power-loss failure.

Call `Store.Close()` after runtimes and build leases have stopped. CLI query, serve and verb paths now close their store owner. Unsupported platforms refuse writable locking rather than silently operating without it.

## Graph references are declared data

Serialized plans use IR version 2. Compilation, rename and projection share one reference visitor. Built-in index references, bake datasets, decision conditions and callback decision bindings all participate. Custom component parameter references should provide a rewrite setter through `references(params)`. Opaque declarations can be validated but cannot be silently rewritten.

Live callback decision access uses named bindings:

```js
// parameters on a live node
{decisions: {route: 'classify-route'}}
// in its callback
ctx.decisions.route
```

Callback conditions use `{$fn, bindings: {localName: decisionNode}}` and read `ctx.decisions.localName`. Declarative conditions can continue to name a decision output, for example `{decision: 'route.needs', min: 0.5}`.

Ordinary removal rejects referenced nodes. When bypass is the intended operation, say so:

```js
{op: 'remove', node: 'rerank', bypass: true}
```

Bypass still rejects dependencies that cannot be safely rewired. Lexical export is a separate whole-graph transformation: remove model-dependent work and gates together, validate the resulting graph, embed materialized build outputs, and rebuild browser-local indexes. It does not use a sequence of potentially invalid intermediate edits.

## HTTP operation transport

Queries and verbs share app admission and the stream implementation. One pump owns SSE writes. Events, coalesced text and terminal responses have size limits; socket writes and final queue draining have deadlines. JSON operation responses use the same write timeout and terminal payload budget. Slow or disconnected clients cancel their operation; the handler joins its pump before releasing admission. Unsupported deadline implementations are rejected for streaming.

## Validation and practical limits

The consolidated Go suite covers the real reports corpus, the available vault parity fixtures, cold/stored/delta indexing, provider cancellation and HTTP APIs. Browser regressions cover duplicate local IDs, equal-sized bake changes, shared waiter cancellation, source reversion, a suspended query across three refreshes, graph rename and standalone lexical export. Focused tests exercise a real TCP client that stops reading, transitive artifact dependencies, 45 generations beyond lineage history, active pins, corruption refusal and delayed worker exit. Validation logs are under `sources/implementation/`.

The first consolidated run supplies an integration failure baseline. We did not rerun every newly shaped API regression against the pre-refactor branch, and should not describe that as completed baseline experimentation. Likewise, correctness/parity workloads are not a production load benchmark. The TypeScript foundation and boundary types remain intentionally incremental; the entire runtime has not been converted from JavaScript.

## Files to start with

- `pkg/ragjs/src/engine/dataset.ts`: public collection and qualified ID contracts.
- `pkg/ragjs/src/engine/engine.js`, `session.js`: publication, acquisition and result detachment.
- `pkg/ragjs/src/engine/jobs.ts`, `ownership.ts`: shared build and resource ownership primitives.
- `pkg/rtenv/operation.go`, `resources.go`: VM settlement and native shutdown ownership.
- `pkg/artifacts/snapshot.go`, `lineage.go`: store roots, build leases and collection.
- `pkg/ragjs/src/graph/references.ts`, `graph.js`: structural dependencies and edits.
- `pkg/server/response.go`, `sse.go`: response transport; `pkg/runner/serve.go`: generation-aware lookup.
