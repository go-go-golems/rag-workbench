---
Title: Design investigation diary
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
LastUpdated: 2026-11-08T18:12:11.742746384-05:00
WhatFor: ""
WhenToUse: ""
---

# Design investigation diary

## Step 1: Define consistent contracts and an implementation plan

Inspected the current engine and native boundaries to turn the review findings into a coherent simplification plan. The guide explains the existing architecture before proposing contracts, deletion targets, migration steps and focused acceptance tests. This step changes documentation only.

The central decision is to share semantic contracts, rather than generalize unrelated algorithms: datasets and explicit provenance, snapshot/resource ownership, operation lifecycles, and canonical graph references. Provider protocols, retrieval implementations and trigger scheduling remain specialized.

### Prompt Context

**User prompt (verbatim):**

> Ok, create a new docmgr ticket then to do this simplification while making the codebase more robust. Create  a detailed analysis / design / implementation guide that is for a new intern, explaining all the parts of the system needed to understand what it is, with prose paragraphs and bullet point sand pseudocode and diagrams and api references and file references. It should be very clear and technical. Store in the ticket and the nupload to remarkable.
>
> The document is detailed, but the cleanup is geared towards quality, simplicity, elegance.

**Follow-up (verbatim):**

> consistency and composability / reuse as well, when appropriate

**Assistant interpretation:** Create and deliver a researched design ticket; do not implement the refactor during this documentation step.

**Inferred user intent:** Resolve recurring classes of defects with fewer concepts, consistent interfaces and explicit ownership. Backwards compatibility is not a requirement.

**Baseline:** `0854c6a` on `task/ragwb-008-vault-ui`.

### What I did

- Created RAGWB-009 with docmgr, a design document, this diary and a phased implementation task list.
- Inspected `pkg/ragjs/assets/rag.js`, especially dataset consumption, shared builds, session execution, GC and graph export/editing.
- Traced persistence and delta reader cleanup through `pkg/natives/persist.go` and `pkg/natives/markdown_native.go`.
- Studied lineage retention in `pkg/artifacts/lineage.go`, result packaging in `pkg/runner/serve.go`, SSE writes in `pkg/server/sse.go`, and provider bridges in `pkg/ragjs/sample.go` and adjacent modules.
- Incorporated the twelve previously collected review findings, retaining their P1/P2 classification and distinguishing reported evidence from newly reproduced tests.
- Wrote Go and JavaScript pseudocode, architecture/lifetime diagrams, code/API references, deletion targets, direct migration guidance and regression acceptance criteria.
- Related the primary source files to the design via docmgr.

### Why

Fixing each array/view branch or cancellation symptom separately preserves the conditions that create the next defect. Shared contracts should establish identity, lifetime and ownership once, at the boundary that can enforce them. A design must also specify what disappears, otherwise abstraction work merely adds another layer.

### What worked

- Local code inspection exposed the additional result-packaging lifetime boundary: the server can consult current artifacts after query execution. The design therefore requires detached results before releasing a query lease.
- The review inventory maps cleanly to four major contract families, with two small corrections that do not warrant new abstractions.
- The existing high-level corpus and plan DSL can remain recognizable while low-level dataset access and result references change directly.

### What didn't work

`go doc net/http.ResponseController.SetWriteDeadline` and `go doc net/http.ResponseController.Flush` returned the requested documentation but also emitted module-proxy lookup warnings under restricted networking. The exact warning bytes were not retained; no dependency change or successful dependency download is claimed. These commands were documentation inspection, not test execution.

### What I learned

- Logical chunk IDs are insufficient for provenance when multiple enriched versions coexist. Dataset identity belongs in record references and index bindings.
- A bounded lineage history cannot be the authoritative inventory for garbage collection.
- Pinning only execution is insufficient if deferred result access still depends on native resources.
- Shared build cancellation and progress must belong to the job, with each caller holding an independent waiter.

### What was tricky to build

The design must preserve useful laziness without making consumers responsible for hidden native lifetimes. It separates read-only datasets, snapshot leases and detached public results. It also separates authoritative disk ownership from searchable lineage and chooses a single writable process per namespace, avoiding an unnecessary distributed coordination protocol.

### What warrants a second pair of eyes

- Validate snapshot publication/acquisition ordering and resource adoption across fresh, warm and delta builds.
- Review provenance and fusion rules for enriched datasets before migrating callbacks.
- Confirm manifest publication and sweep locking against crashes and interrupted builds.
- Check cancellation, loop disposal and blocked SSE writes using deterministic lifecycle tests.
- Measure materialization costs; the design does not claim performance parity without measurements.

### What should be done in the future

Implement the phased tasks in the guide. Start with regression characterization and contract conformance, then migrate consumers and delete superseded mechanisms at each boundary. Production authentication, distributed indexing and multiple writer processes remain separate work.

### Code review instructions

Read the guide's findings inventory and current-code map first, then the invariants and deletion list. Review the API migration and acceptance table against existing examples. Documentation validation uses `docmgr doctor --ticket RAGWB-009 --stale-after 30` and `git diff --check`; runtime suites are intentionally deferred because no runtime code changed.

### Technical details

Proposed modules cover datasets, snapshots, resource registry, artifact store, shared jobs, operation transport and graph references. These are design sketches, not existing exported packages. Query results retain explicit generation and dataset provenance but require no caller-owned close operation. Refresh failure leaves the last good snapshot usable. One canonical reference traversal must cover inputs, uses, conditions and outputs.

## Step 2: Validate and deliver the guide

Delivered the completed design guide to reMarkable and marked only the design/delivery task complete. The implementation tasks remain unchecked.

- **Validation:** `docmgr doctor --ticket RAGWB-009 --stale-after 30` passed; source-path inspection checked 13 referenced paths with none missing. No runtime tests were run for this documentation-only change.
- **Delivery command:** `remarquee upload bundle <guide> --name 'RAGWB 009 Engine Simplification Intern Guide' --remote-dir '/ai/2026/11/08/RAGWB-009' --toc-depth 2 --non-interactive`.
- **Receipt:** `OK: uploaded RAGWB 009 Engine Simplification Intern Guide.pdf -> /ai/2026/11/08/RAGWB-009`.
- **Limits:** The receipt confirms cloud delivery, not physical device synchronization. The proposed APIs and tests are not implemented by this ticket documentation commit.
- **Checkpoint:** This entry and the guide are committed together; use the ticket's git history to identify the documentation revision.
