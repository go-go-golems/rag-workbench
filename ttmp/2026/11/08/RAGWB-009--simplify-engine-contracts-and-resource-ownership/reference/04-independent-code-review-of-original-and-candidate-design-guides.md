---
Title: Independent code review of original and candidate design guides
Ticket: RAGWB-009
Status: active
Topics:
    - rag
    - search
DocType: reference
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-08T21:25:14.057424305-05:00
WhatFor: ""
WhenToUse: ""
---

# Independent source review: what the guides get right and what needs correction

## Scope and evidence

This review followed a fresh full reading and the separately saved [first reading notes](03-fresh-reading-notes-before-independent-code-inspection.md). I then inspected runtime, native, artifact, provider, server, graph and tooling code independently. The guides were treated as claims to check, not as evidence of their own correctness.

The inspected rag-workbench revision is `fe227fc` (documentation checkpoint; runtime code remains at the `c267f91` experiment base). The local workspace also uses go-go-goja; its inspected revision is `e9bc7891a7b4435ec659577dc346647daa548050`. Observations about that dependency describe this checkout, not every released version. No PR status or review priority was re-fetched; the twelve review labels remain historical input.

The reproducible [Node probe](../scripts/01-intern-guide-experiment/02-code-probes.mjs) uses local synthetic records and the committed bundle. Its [output](../scripts/01-intern-guide-experiment/code-probe-results.jsonl) records six observations. It makes no provider or network requests. Some probes isolate a mechanism with fake handles or overridden build work; they are not end-to-end regressions for all twelve findings. No real-socket, crash-recovery or native-descriptor test was run in this documentation experiment.

## Current implementation, checked against the claims

| Area | Independently observed code | Assessment of the guides |
|---|---|---|
| Source/tooling foundation | `package.json`, `tsconfig.json`, `scripts/build-runtime.mjs`, and `pkg/ragjs/src/` define strict TS with unchecked transitional JS and generate the committed ES2020 bundle | Candidate maps are current. Original §2 and §11 still describe the pre-extraction state; §16 corrects it too late. |
| Fluent example | `components/builtins.js:170` accepts index-name retrieval arguments; probe runs the displayed chain with `rag.files()` replacing native directory access | The retrieve/filter/limit example is valid. The memory probe does not validate `/data/reports` availability or native source authorization. |
| Duplicate chunks | `engine/engine.js:556` collects arrays into a Map (later assignment wins), stored views separately; `engine/records.js:30` gives Map entries precedence and chooses the first matching view | Confirmed mechanism. Probe returns `new` cold and `old` for two views. All drafts correctly motivate provenance. |
| Bake input/cache | `engine/engine.js:517` passes output values directly, calls `docs.map`, and hashes document IDs/section counts plus `chunks.length` | Correct concern, but fuller than “the user callback calls .map”: stored docs can fail inside the engine before callback invocation. Equal-size input changes can also reuse an inadequate bake key. |
| Native handle lifetime | `engine/engine.js:410` closes previously retired values absent from current digest liveness; no active-query accounting appears there | Fake-handle probe closes on second GC even with an external reference. This supports the design; it does not reproduce a provider-paused query with real readers. |
| Result construction | `engine/session.js:361` decorates results with methods closing over artifacts; `pkg/runner/serve.go:137` awaits the session, collects, then calls `art()` and packs text/neighbors | Correct: internal execution and later packing need one generation. The current browser lookup endpoints also resolve only current artifacts. |
| Reader registration | `pkg/natives/persist.go:47` permits no-op registration and registers loaded readers/indexes at 209/227/245; `markdown_native.go:103` and 136 return delta readers without equivalent closer registration | Correct. `pkg/ragjs/module.go:143` has another no-op default, and `runner/runtime.go` only asserts `EnvNative` for Bm25Native/Persist. The implementation inventory must include these integration points. |
| Durable storage | `pkg/artifacts/store.go:27` already has Meta descriptors and a List scanner; `lineage.go` bounds recorded history and GC iterates that history | Correct underlying issue. Describe the missing authority/dependencies, not an absence of all inventory metadata. The stored history bound is 32; actual incremental-base search is eight. |
| Dotted root | `store.go:274` visits the root itself; the directory test at 282 skips any basename containing a dot | Source confirms the reported mechanism. “Explicit layout traversal” is an appropriate local correction. |
| Shared build | `engine/engine.js:695` returns the existing promise; only the first opts reach `build`; `session.js:43` forwards the request signal | Probe confirms same promise and first signal. Later callers do not gain independent cancellation or full progress subscription there. |
| Async outcome | `pkg/natives/rerank.go:145` defers cancel, then posts settlement which checks `callCtx.Err()` later. Sample/decide classify before posting (`sample.go:147`, `decide.go:51`) | Correct specific rerank diagnosis. Do not imply all three have the same ordering bug. Their lifecycle duplication and uneven cleanup motivate reuse. |
| SSE/capacity | `pkg/server/sse.go` discards pump write errors, uses non-error-returning Flush and sets no per-write deadline; verb handler waits for pump before deferred release | Correct stall mechanism. Current admission cap applies to verbs (`verbs.go:239`), not direct query `handleRun`. Shared admission is a proposed behavior change. |
| Model fallback | `pkg/llm/gepp/backend.go:115` treats empty enabled list as unrestricted for the default, but fallback iterates only that empty list | Correct conditional failure: it matters when the configured default is absent/ineligible. Existing usable defaults still work. Deterministic unrestricted fallback remains a policy to specify. |
| Rename | `graph/graph.js:877` rewrites node inputs and plan outputs, not `when`; finalization validates decisions | Probe produces an error identifying the missing `route`. The stale reference prevents successful edit; it is not silently accepted. |
| Export | `export/html.js:6` removes submit-triggered non-answer live nodes before a second prune; calls `engine.files()` for data | Probe of a classified plan fails with a dangling decision. Explicit projection and portable data are justified. Export is publicly `rag.export`, while `exportHtml` is the internal function. |

## Documentation corrections and design gaps

### 1. Correct the description of failed rename

Candidate C §9 says, “The editor says rename succeeded but the compiled plan still points to the old node.” That is inaccurate for the inspected implementation. `applyOps` finalizes and rejects the dangling decision. The observed diagnostic is:

```text
after ops: 1 problem:
  text: when reads decision 'route', which does not exist
```

Use: “Rename updates ordinary inputs but misses the decision condition, so final validation rejects an otherwise intended rename.” This distinction matters: the defect is incomplete transformation, while the existing validator already provides a useful safety check. Retain that check in the refactor. A and B do not explicitly claim successful acceptance, but would also benefit from stating the actual diagnostic.

### 2. Define a graph-level reference API, not only a node visitor

All guides propose `references(node)` and mention pinned outputs in the same contract. Current pinned outputs are `raw.outputs`/`raw.pinned` at plan level (`graph.js:529`), not fields owned by an individual node. Signal dependencies also have distinct handling: input edges, `def.reads`, and lambda signal scans. `whenRefs` currently scans literal `ctx.decision(...)` uses in callback source. Simply implementing a node visitor cannot cover all these locations automatically.

Keep the common mechanism, but specify graph traversal that visits node references **and** plan output bindings, with reference kinds distinguishing node outputs from signals. Component schemas own component-specific references. Explicit callback bindings replace the relevant source inference; explain which automatic lambda scans are removed and migrate them together. Define decision question IDs separately from node IDs. The example `route.needs` contains both.

This is a design-completeness issue shared by the original and drafts; C's “another node's ... pinned output” is additionally misleading terminology.

### 3. Make resource handoff responsibility unambiguous on every return

All four sketches call `r.Close()` if `scope.Adopt` returns an error. Nearby prose also allows adoption to “reject it and close it.” If the registry closes on rejection and the caller follows the sketch, the handle is closed twice. This is not proof of a present registry defect—the registry is proposed—but the guide should not leave incompatible interpretations of its most important ownership boundary.

Choose one rule and test it. For example: on successful adoption the registry consumes the handle; on error ownership remains with the caller, which closes it. If duplicate adoption returns an existing entry successfully, the registry may consume and close the redundant newly opened handle. Show the three cases explicitly. Do not rely on every native Close being idempotent to make unclear ownership harmless.

Also clarify that snapshot retirement only releases resource uses **after** leases/other snapshot-retention reasons end. B's first retirement sentence is too immediate; its next sentence supplies the condition. C's detailed resource trace and A's diagram explain this better.

### 4. Separate terminal selection, callback delivery and cleanup completion

The proposed Go sketches execute synchronously on the page, then call `SettleOnOwner`; a comment promises actual asynchronous behavior. That hides the central implementation requirement. Reading `context.Cause` and separately classifying is not by itself an atomic race decision. Define the owner or synchronization primitive that arbitrates cancellation versus worker completion. Cleanup must occur whether posting fails, the callback is skipped, or the VM shuts down.

The current dependency's `Runtime.Close` cancels lifetime, waits for/interrupts the VM owner, calls closers and shuts down the owner/loop. That is not evidence that arbitrary provider goroutines have joined. Current bridge callers discard posting errors. A new operation owner must track those workers independently of successful JS delivery and integrate shutdown before native handles are released.

A short sequence diagram should show worker completion -> accepted outcome -> owner delivery, with independent cleanup on rejected delivery. Keep provider-specific classification: sample and decide already avoid rerank's deferred-cancel misclassification. Sharing their lifecycle should preserve that working behavior.

### 5. A bounded join needs an explicit unfinished-worker policy

All drafts require the handler to return and release capacity after a bounded join, while also requiring resources never close beneath workers. Both are sensible, but a timeout alone does not reconcile them. Cancellation is cooperative; a worker might not finish by the join deadline.

Specify which owner retains an unfinished operation and its resource leases after the response ends, how it is reported, and how worker admission remains bounded. Handler capacity and live-worker capacity need not be the same counter. Releasing the only cap while every timed-out worker continues would permit unbounded accumulation. Conversely, forcibly closing resources to meet a response deadline violates the registry contract.

This is a missing lifecycle policy, not a reason to add a durable scheduler. Keep a small operation owner and a defined cancellation contract. Restrict the claim to HTTP responses managed by the shared helper; “every network write” should not imply this helper also bounds every provider client's traffic.

The installed Go `net/http/responsecontroller.go:99` supports the drafts' deadline statement: setting an already-exceeded write deadline will not extend it. No external API lookup was needed to verify that local implementation/documentation.

### 6. Teach the storage objects and specify interrupted-publication cleanup

The metadata paragraphs remain the weakest teaching in every draft. Show one existing Meta record, then one proposed descriptor, snapshot manifest and current pointer with their relationships. Explain that a “root” is an artifact selected as a starting point for reachability, not a filesystem root directory. Distinguish the 32-entry stored lineage history from the eight-entry search window. The original and C's acceptance wording blur “32-entry lineage search.”

The publication sequence moves payloads before publishing descriptors. A crash in between can leave a payload without authoritative metadata. The guides correctly prohibit guessing that unknown files are safe to delete, but do not specify how owned interrupted staging/publication is recognized and recovered. Choose a recoverable staging/journal rule or an explicit conservative quarantine/manual disposition; test that boundary. “Crash before the pointer change leaves the old snapshot current” explains visible query state, not reclamation of every leftover file.

Existing `Meta` has key/type/codec/count/size/time/kind. It lacks the proposed dependency graph and snapshot roots. Extending that existing storage authority is a concrete simplification; presenting it as an entirely new inventory subsystem would overstate the work.

### 7. Clarify two identities and result lifetime limits

The guides distinguish dataset version from record ID well. They should also distinguish a stable build identity (which changing source versions publish under) from an immutable snapshot identity. Current digests incorporate source state, so `plan.buildKey` in the proposed sketch requires a definition. Otherwise `ensure` could wait for the latest source-derived identity instead of serving the last usable generation as the prose promises.

Detached ordinary results and later scoped lookups are a coherent proposed policy, not current behavior. State which records `result.chunk(hit)` can resolve after return, what trace/preview budgets include, and whether an immediately expired generation may cause later GUI lookup to return stale. Preserve the difference between already copied data and optional future inspection. No need to settle numeric defaults during this experiment, but the implementation guide should name who chooses them.

### 8. Add exact operational navigation without burying the reader

Give an actual reports command and plan path, with native prerequisites, rather than “run a reports example.” B's `go run ./... with the actual command package` is not an executable instruction as written. Move historical PR/commit stack detail out of the first conceptual page; retain a compact evidence table. Add the model fallback file and runtime shutdown integration points to the current map. Keep source maps versioned; do not claim branch/PR status stays current indefinitely.

## What remains sound

The main architecture survives source review. Uniform datasets plus exact provenance remove backend-dependent interpretation. Retained snapshots plus a single resource owner address both version consistency and premature closure. Durable ownership separate from bounded reuse history is appropriate. Independent shared-job interest and stable terminal outcomes resolve distinct cancellation problems. A bounded response helper and common graph reference machinery remove repeated incomplete implementations.

These are justified shared boundaries, not interchangeable layers of one universal framework. The small dotted-root and fallback fixes should remain local. The strongest missing work is precise transition and handoff specification, followed by better examples of storage metadata—not more abstractions.

## Verification limits and next action

The probes confirm selected mechanisms and two graph failures. They do not establish that all twelve findings reproduce end to end or that the proposed architecture is implemented. In particular, actual network stalls, provider cancellation races, native descriptor cleanup and crash durability still require the regression work already assigned to 009. No new implementation task is marked complete.

Before adopting a final v2, correct the demonstrated rename statement, resolve the adoption error contract, widen reference traversal to the graph, and explain unfinished-operation ownership and interrupted-publication recovery. Then revise the unclear supporting terms. Preserve these experimental drafts unchanged so the writing experiment remains inspectable.
