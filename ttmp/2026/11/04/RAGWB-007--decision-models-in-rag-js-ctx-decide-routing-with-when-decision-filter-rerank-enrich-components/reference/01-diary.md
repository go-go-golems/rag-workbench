---
Title: Diary
Ticket: RAGWB-007
Status: active
Topics:
    - rag
    - decisions
    - llm
    - goja
    - search
DocType: reference
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: "Chronological implementation diary for RAGWB-007 (decision models in rag.js)."
LastUpdated: 2026-11-04T18:56:37.980167615-05:00
WhatFor: ""
WhenToUse: ""
---

# Diary

## Goal

Record how decision models were integrated into rag-workbench (ctx.decide, classify + when routing, filter.decide, rerank.decide, enrich.decide, hosts), with evidence, failures and review notes per step. The Geppetto side is GEPPETTO-DECISION-001 in the geppetto worktree of the same WSM workspace.

## Step 1: Study the engine, measure batch limits, write the intern guide

This step designed the integration from the engine's actual structure, which has no conditional nodes. Routing therefore became a node option (`when`) that reads a decision node through an explicit IR edge, rather than a new graph construct. A spike over the bundled report corpus measured grading latency and found an undocumented limit of 32 questions per djev request, which sets the batching of every hit-grading component.

### Prompt Context
**User prompt (verbatim):** see GEPPETTO-DECISION-001 diary Step 1 (the full prompt is recorded there): "...Then once implemented, create a ticket in rag-workbench and integrate decision models / classifiers like jev into the JS API and into the RAG pipelines, as a way to route queries or classify things or whatever. Create  a detailed analysis / design / implementation guide that is for a new intern ... Store in the ticket and the nupload to remarkable. ... Then implement. commit at appropriate intervals and keep a detailed diary as you work ... Diary MANDATORY."
**Assistant interpretation:** After Geppetto support landed, design and implement decision-model use in rag.js and its hosts, with a ticket, intern guide, reMarkable upload and diary.
**Inferred user intent:** Use fast calibrated classification for query routing, passage grading and chunk classification in RAG pipelines.
**Commit (code):** none yet (design step)

### What I did
- Branched `task/ragwb-007-decision-models` from `task/rag-workbench` (6ee9fc3) in the workspace worktree.
- Read `pkg/ragjs/assets/rag.js` (components `enrich.llm`, `rerank.llm`, `retrieve`, `answer`; `PlanSpec._addNode`, `splitOpts`, `finalize`, `costsCalls`, `Engine.sampler`, `buildCtx`, `Session.run`, `Session.ctx`, `eventSample`), `pkg/ragjs/{sample,module}.go`, `pkg/runner/runtime.go`, `cmd/ragwb/cmds/roots.go`, `pkg/server` (`/_rt/sample`), `web/workbench/{claude,app}.js`, `examples/apps/reports/lib/{plans,corpus}.js`.
- Wrote `scripts/01-relevance-batch-spike.py` and ran it over `examples/apps/reports/corpus` (184 sections of at most 1500 characters); output in `sources/01-relevance-batch-spike.txt`.
- Created ticket RAGWB-007 (new topic `decisions`), the guide and this diary.

### Why
- `when` as a node option: the live loop is a topological walk with a memo. A node-level predicate evaluated before the memo check, plus a `decision` IR edge for ordering, fits that loop without a new scheduler.
- A declarative `when` form is needed because lambdas are stored as source and compiled in a guarded scope (`lam`, `fnOf`), so they cannot capture variables.

### What worked
- Spike, `djev`, noul relevance per section: 5 → 1043 ms / 2257 input tokens; 10 → 1301 ms / 4580; 20 → 1830 ms / 9248.

### What didn't work
- 40 and 80 questions: `400 {"error":{"code":"systemone_invalid_request","message":"\"questions\" exceeds this model's max_questions of 32"}}`. Not in LunaRoute's docs. Batching defaults to 16 (max 32).

### What I learned
- Latency tracks input tokens more than question count, so two concurrent batches of 16 beat one batch of 32.
- The spike's first sections came from an unrelated report and all scored below 0.04: the model rejects off-topic passages decisively.

### What was tricky to build
- Nothing built yet. The ordering trap: without a `decision` edge, a node with `when` could be sorted before the `classify` node it depends on.

### What warrants a second pair of eyes
- Fail-open `when` semantics (a null decision runs the node) and the "skipped outputs" rule (pass-through for same-type ports, otherwise empty), guide §3.3.

### What should be done in the future
- Calibrate thresholds on the reports evals (phase 6).

### Code review instructions
- Read guide Parts 2 and 3; re-run `python3 -I scripts/01-relevance-batch-spike.py examples/apps/reports/corpus "<query>"`.

### Technical details
- djev limits observed so far: 32 questions per request, 32768-token context, 8 MiB body.
