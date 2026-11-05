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

## Step 2: Phases 1–3 — ctx.decide, routing with classify and when, decision-graded hits

This step built the query-time half of the design. Phase 1 added the bridge (`env.decide` → a geppetto `decision.Provider`), the JS API (`ctx.decide`, its helpers, `rag.decide`), a deterministic `decide.Fake`, and `--decide`/`--decide-model`. Phase 2 added `classify`, the `when` node option and `answer({direct})`. Phase 3 added `filter.decide` and `rerank.decide`. A live run over the bundled reports with `--decide pi` put the section "5.9 The rebalance law" first at p = 1.00. `classify` took 812 ms and grading 20 hits took 1 008 ms.

### Prompt Context
**User prompt (verbatim):** see Step 1
**Assistant interpretation:** Implement guide phases 1–3.
**Inferred user intent:** Route and grade with decision models inside ordinary plans.
**Commit (code):** 8d97d45 (P1), dd417e1 (P2), d29f7a0 (P3)

### What I did
- `pkg/ragjs/decide.go`: `newDecide`, a promise bridge that runs the provider on a goroutine, settles on the event loop, cancels on the abort signal, and maps errors with `classifyDecideError`. `module.go`: `Options.Decide`; `configureHost` installs `env.decide` and `env.decideModel`.
- `pkg/decide/fake.go`: `Fake` (word overlap; `Calls`, `Requests`, `Delay`).
- `rag.js`:
  - helpers `wireOptions`, `wireDecision`, `fromWireDecision`, `answerValue`, `answerLine`, `makeDecide`, `eventDecide`;
  - `Engine.decider`;
  - `ctx.decide` in `buildCtx` and `Session.ctx`; `ctx.decision`; `rag.decide`;
  - `classify`;
  - `normWhen`, `whenRefs`, `whenHolds`, `whenSkipOutput`;
  - the `when` changes: `splitOpts`, `_addNode` and factories carry it, `finalize` adds decision edges and checks, `ir.digest` includes `when` only when present, and the live loop evaluates it before the memo;
  - `entry.decision` in the trace; `answer({direct})`; `gradeHits`, `filter.decide`, `rerank.decide`.
- `pkg/runner/runtime.go`: `Config.Decider`. `cmd/ragwb/cmds/roots.go`: `--decide`, `--decide-model`, `Decider(ctx)`, `DescribeDecider`, `piAgentDirOrEnv`. `run.go`, `serve.go`, `verbs.go` pass `Decider`, and `serve` prints `llm … · decide …`.
- Tests: `pkg/ragjs/decide_test.go`, `route_test.go`, `grade_test.go`.
- Live: `ragwb run --decide pi --files examples/apps/reports/corpus --trigger submit --no-store scripts/02-graded.wb.js`; output in `sources/02-graded-live-run.json`.

### Why
- `ir.digest` gains `when` only for nodes that have one, so existing plans' digests (and anything keyed on them) do not move.
- `classify`'s answers carry non-enumerable `$decision`/`$meta` markers, so `ctx.decision` and `when` accept only real decision outputs, and JSON views of the answers stay clean.
- `filter.decide` returns graded hits in their original order. Only `rerank.decide` reorders.

### What worked
- `go test ./pkg/... ./cmd/...` passes, as does `go test ./web/`.
- `TestClassifyAndWhenRoute`:
  - `route` is ordered first although it was added last;
  - `widgetix` is `skipped:when` with the note `when: route.strategy = law 1.00`;
  - a `change` run runs both branches;
  - exactly 1 decision call is made.
- `TestFilterAndRerankDecide`: 40 hits in batches of 16 → 3 calls and 3 progress events; filter keeps the 10 rebalance sections; `min: 2` keeps 2 under an unreachable threshold; `rerank` is the plan's hits output.

### What didn't work
- `TestRagDecideBridge` first failed on `Needs:0.66`. The fake's noul overlap was 2 of 3 query terms ("index" absent), so the expectation was wrong, not the code; lowered it to > 0.6.
- `TestRagDecideErrors`: `ReferenceError: AbortController is not defined` (goja has none). Used a minimal object with `addEventListener('abort')` and `aborted`, which is all the bridge reads.
- `cmd/ragwb/cmds/run.go:83:17: no new variables on left side of :=`: my scripted insert matched both indentation patterns and inserted the `Decider` block twice. Collapsed the duplicates.
- `route_test.go:14:6: newRuntime redeclared in this block` (one exists in module_test.go): renamed to `newOptsRuntime`. `SyntaxError: Identifier 'plan' has already been declared` (the shared corpus fixture declares `plan`): renamed to `dplan`.
- `TestFilterAndRerankDecide`: `progress events 83`. The corpus build also emits progress; the test now counts `unit === 'passages'` only.

### What I learned
- `refreshFactories` builds `rag.<family>` factories that copy only `id` and `trigger` into params. `when` had to be added there too, or `.then(rag.retrieve(..., {when}))` would drop it.
- Live grading with two concurrent batches of 10 took about 1 s, against 1.8 s for one batch of 20 in the Step 1 spike.

### What was tricky to build
- Memo versus `when`. The memo compares inputs and signal reads, not decisions. The `when` check therefore runs before the memo lookup and never writes the memo, so a node skipped in one run can still reuse its previous output in the next run if its inputs are unchanged.
- Skip outputs. A skipped `retrieve` must contribute `[]` to `fuse.rrf`, not its `queries` input. `whenSkipOutput` passes input through only when the in and out port types match.

### What warrants a second pair of eyes
- `whenHolds` for choices with `min`/`max` compares the probability mass of the listed options (`in`, or all but `not`), not the top choice. That is the useful semantics for "run the dense branch if P(semantic) ≥ 0.3", but it must be documented clearly.
- `when` lambdas receive `(s, {decision, signals})`, not the full ctx.

### What should be done in the future
- `applyOps` (plan patches) does not carry `when` on added nodes yet.

### Code review instructions
- `git diff 8841551..d29f7a0 -- pkg/ragjs/assets/rag.js` (search for `RAGWB-007`), `pkg/ragjs/decide.go`, `pkg/decide/fake.go`, `cmd/ragwb/cmds/roots.go`.
- `go test ./pkg/ragjs/ -run 'RagDecide|Classify|WhenCompile|AnswerDirect|FilterAndRerank' -v`.

### Technical details
- JS error codes: `decide_unavailable`, `invalid_request`, `invalid_response`, `not_granted`, `rate_limited`, `model_not_found`, `prompt_too_large`, `cancelled`, `upstream_error`. Extras: `status`, `providerCode`, `requestId`, `retryAfterMs`.
- Events: `{type: 'decision', node, call, model, ms, answers: {id: {type, value, p?}}}`, and node end `{status: 'skipped', skippedBy: 'when', notes}`.
