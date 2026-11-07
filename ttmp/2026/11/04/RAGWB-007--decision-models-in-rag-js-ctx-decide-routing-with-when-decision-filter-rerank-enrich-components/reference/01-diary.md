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
RelatedFiles:
    - Path: repo://examples/apps/reports/lib/plans.js
      Note: Example decision plans
    - Path: repo://pkg/decide/fake.go
      Note: Deterministic fake provider
    - Path: repo://pkg/ragjs/decide.go
      Note: Go bridge for env.decide
    - Path: repo://pkg/server/decide.go
      Note: Workbench endpoint
ExternalSources: []
Summary: Chronological implementation diary for RAGWB-007 (decision models in rag.js).
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

## Step 3: Phases 4–6 — enrich.decide, hosts and GUI, example plans, measurement

This step finished the feature and measured it. `enrich.decide` classifies chunks at build time into `chunk.meta`, where `index.terms` and `filter.where` read it. The workbench gets `POST /_rt/decide` and a `decide` capability. The GUI shows decision rows and when-skips. The reports app gains the `graded`, `filtered` and `adaptive` plans and `ask --plan adaptive`.

On the bundled goldens, decisions change little, because the eval is saturated: hit@5 = 1 for every plan. The measurement that matters is rejection. Off-topic queries peak at p ≤ 0.006, while answerable ones reach 0.965–1.0.

### Prompt Context
**User prompt (verbatim):** see Step 1
**Assistant interpretation:** Implement guide phases 4–6.
**Inferred user intent:** Decisions available everywhere plans run, with evidence of what they do on real data.
**Commit (code):** 6306dd7 (P4 enrich.decide), b5d1b9d (P5 hosts, GUI, plans); the phase 6 commit follows this entry

### What I did
- `rag.js`: `build('enrich.decide')` (`llm: true`; per-chunk state `{title, section, text, corpus?}`; compact values `id`, `id_p`).
- `pkg/server/decide.go`: `handleDecide`; `server.Config.Decider`; `/healthz` reports `decide`.
- `pkg/ragjs/decide.go`: exported `DecideErrorCode`.
- `cmd/ragwb/cmds/workbench.go`: passes `Decider` and prints `decide: …`.
- `web/workbench/claude.js`: `caps.decide(reqJSON, signal)`. `app.js`: `get('decide')`, `rag.configure({decide})`.
- `gui.js`:
  - `foldEvent` keeps `decision` events and `skippedBy`;
  - `decisionTable`/`decisionRows`/`eventDecisionRows` render one row per question: answer, a probability bar, the top alternatives;
  - step status shows `skipped (when)`.
- `examples/apps/reports/lib/plans.js`: `graded`, `filtered`, `adaptive`. `search.js` plan choices. `app.js`: `ask` takes `--plan` and returns `route` and `direct`.
- Tests: `enrich_decide_test.go`, `pkg/server/decide_test.go`, `TestTraceFoldsDecisions`.
- Measurements:
  - `reports goldens --names lexical,fused,graded,filtered --trigger submit` → `sources/04-goldens-submit.txt`;
  - the off-topic probe app `scripts/03-offtopic-probe/` → `sources/05-offtopic-probe.txt`;
  - adaptive live runs → `sources/03-adaptive-*.json`.

### Why
- `enrich.decide` writes to `meta` as well as to `chunk[name]`, because `TermsIndex` and `filter.where` read `getPath(c.meta, key)`.
- The `filtered` example drops `min: 1`. The probe showed that a question the corpus cannot answer gets all-low p, and keeping one irrelevant hit defeats the purpose.

### What worked
- `adaptive`, live (`--decide pi --llm pi`):
  - "hi there, how are you today?" → `needs` 0.00009, retrieval and rerank skipped, a direct answer, `direct: true`;
  - "how does the rebalance law preserve placements?" → `needs` 0.914; rerank.decide top is "5.9 The rebalance law" p = 1.00; the answer cites [E1]; 4.5 s wall clock including the LLM answer.
- Goldens (12 questions, submit):

  | plan | hit@5 | MRR | nDCG@10 |
  |---|---|---|---|
  | lexical | 1 | 1 | 0.884 |
  | fused | 1 | 0.958 | 0.835 |
  | graded | 1 | 0.958 | 0.855 |
  | filtered | 1 | 1 | 0.837 |

  The run took 16.3 s for 48 runs. `filtered` without `min` gives the same result.
- Off-topic probe (max p over 20 graded hits):
  - off-topic: boiling point 0.003, carbonara 0 (2 hits), world cup 0.006, passport (no BM25 hits);
  - on-topic: rebalance law 1.0 (4 ≥ 0.5), delta builds 0.965 (2), PBUI headless engine 0.999 (8);
  - 330–910 ms per query.

### What didn't work
- `TestEnrichDecide` first failed with `topics {Topics:map[Law notes:law More widgets:widgets Rebalance law:law Widgets:widgets] ...}`. `headingPath` excludes the document title, so the test's keys were wrong; fixed the keys.
- My probe labelled "what is the bleve index used for" on-topic. It scored max p 0.058, and `grep -il bleve examples/apps/reports/corpus/*.md | wc -l` → 0: the corpus never mentions bleve. The model was right and the label was wrong.

### What I learned
- The goldens are too easy to separate plans (hit@5 saturates). Decision grading lifted fused's nDCG@10 by 0.02 and filtering lifted its MRR to 1. Neither beats lexical on this set. A harder eval with distractors and unanswerable questions is needed to measure ranking gains.
- Rejection is the strong signal. Thresholds near 0.5 separate off-topic from on-topic with a wide margin on this corpus.

### What was tricky to build
- Browser parity. `env.decide` in the browser must have the Go host's signature (`requestJSON, signal` → `responseJSON`), so `rag.js` works unchanged in both. `claude.js` posts the already-normalised JSON as-is and unwraps the server's `{error: {code, message}}` envelope into the same `{code, message}` rejection.

### What warrants a second pair of eyes
- `handleDecide` uses status 499 for cancellation (nginx convention) when the request context is cancelled. The client has gone by then, so the status is cosmetic.
- `adaptive` gates `rerank.decide` with the same `when` as the retrieves. Without the gate, a skipped retrieve's `[]` would reach rerank and grade nothing, which is harmless but still records a step.

### What should be done in the future
- A harder eval set (unanswerable and distractor questions) to calibrate `threshold`.
- `applyOps` support for `when`.
- Workbench presets that use decisions (a "routing" preset).

### Code review instructions
- `git diff d29f7a0..HEAD` for `pkg/server/decide.go`, `web/workbench/{claude,app}.js`, `pkg/ragjs/assets/gui.js`, `examples/apps/reports/lib/plans.js`.
- Live:
  - `ragwb --decide pi --llm pi --no-store verbs --dir examples/apps/reports reports ask "…" --plan adaptive`;
  - `ragwb --decide pi --no-store verbs --dir examples/apps/reports reports goldens --names fused,graded,filtered --trigger submit`.

### Technical details
- `/_rt/decide`: request = `decision.Request` JSON (options as `[{name, description}]`), response = `decision.Response` JSON; errors `{error: {code, message}}` with 503 `decide_unavailable`, 400 `invalid_request`/`prompt_too_large`/`model_not_found`, 403 `not_granted`, 429 `rate_limited`, 502 otherwise.

## Step 4: Resume after the Geppetto integration and constructor cleanup

Resume RAGWB-007 from the completed decision pipeline implementation. The immediate goal is a reproducible dependency on released Geppetto, followed by harder decision evaluations and the small routing follow-ups already recorded in Part 6. Geppetto PRs #447 and #449 have merged; the latest published release is still v0.13.13.

The original rag-workbench checkout has its common Git directory outside the writable workspace. Created isolated sibling `rag-workbench-resume` on branch `task/ragwb-007-resume`, based on `7a00da6`, and copied only the newly added continuation tasks. Restored the original checkout's task file after copying, preserving its initial clean state.

### Prompt Context
**User prompt (verbatim):** "ok, continue with that, bump geppetto and such as well. (make release). commit at appropriate intervals and keep a detailed diary as you work (using the diary format from the skill)"
**User question (verbatim):** "what's that ? - Three reports-app plans: graded, filtered, and adaptive."
**User steering (verbatim):** "you can run `make release` in geppetto I think" and "after rebasing on main".
**Assistant interpretation:** Release the merged Geppetto work, update the actual project dependency/workspace, then improve evaluation and routing support while maintaining the full diary format.
**Inferred user intent:** Return to measurable RAG behavior after the supporting credential/API work, with a release-backed dependency instead of relying on old workspace overrides.

### What I did
- Checked rag-workbench Git state, RAGWB-007 guide/results and task list. The feature implementation is complete at `7a00da6`, but go.mod still pins Geppetto v0.13.13 and parent go.work selects the old local `geppetto` branch.
- Confirmed Geppetto #449 merged at `c9e989e8` and latest published release is v0.13.13. The constructor API break warrants v0.14.0.
- Inspected the actual release targets: `tag-minor` creates a tag using svu; `release` pushes origin tags and requests the current module version through proxy.golang.org. Tag pushes trigger release automation.
- Added explicit continuation tasks using docmgr and created the isolated writable checkout.

### Why
- `make release` alone does not create a new tag, and the isolated Geppetto checkout's origin originally pointed at the old local worktree. Correct source revision and upstream destination must be established before publication.
- A real module release is necessary to validate rag-workbench without go.work supplying unreleased APIs.

### What worked
- Read-only GitHub connector checks confirm merged main and the release baseline.
- The isolated rag-workbench clone/branch and copied tasks are ready for commits.

### What didn't work
- `gh release list --repo go-go-golems/geppetto --limit 5` failed: `error connecting to api.github.com`.
- The GitHub connector exposes release reads but no tag/release creation or workflow-dispatch mutation. A standalone browser reached GitHub's sign-in page; the supported browser runtime reports `No browser is available` and its browser list is empty. No credentials or browser storage were inspected.

### What I learned
- Release automation already exists; use the repository's tag/release targets once upstream Git networking is available rather than inventing another release mechanism.

### What was tricky to build
- Separating the merged remote history from the isolated local source commits, which have equivalent trees but different IDs because publication used the connector.

### What warrants a second pair of eyes
- Release from merged main, not the old source branch, and verify that the module version resolves without workspace substitution.

### What should be done in the future
- Publish v0.14.0, bump the module, verify integration, then add harder evaluations plus when-preserving plan edits and a routing preset.

### Code review instructions
- Start with the new tasks and this continuation entry, then inspect go.mod/go.sum and the evaluation additions as they are committed.

### Technical details
- Working clone: /home/manuel/workspaces/2026-11-02/rag-workbench/rag-workbench-resume.
- Original source branch: task/ragwb-007-decision-models; source head: 7a00da6.
- Proposed version: v0.14.0 (not yet tagged or published at this checkpoint).

## Step 5: Release Geppetto and update the dependency

The user enabled an escalation-capable session. Fetched upstream main and tags successfully. The isolated cleanup branch tree matched upstream main exactly, so created `release/geppetto-v0.14.0` directly from `upstream/main` instead of replaying already merged commits. `make tag-minor` created v0.14.0 and `make release` pushed the tag; the Go proxy resolved it successfully.

### Prompt Context
**User prompt:** See Step 4. **Follow-up:** "ok try again".
**Assistant interpretation:** Retry the authorized network operations and continue the dependency integration.
**Inferred user intent:** Use a published release rather than an unreleased workspace dependency.

### What I did
- `git fetch upstream main --tags`; verified identical source trees.
- Created the release branch from upstream main, configured the release origin, ran `make tag-minor` and `make release`.
- Ran `GOWORK=off go get github.com/go-go-golems/geppetto@v0.14.0` in the isolated rag-workbench checkout.

### Why
- The minor release includes intentionally breaking constructor changes; the dependency must resolve independently of go.work.

### What worked
- Tag v0.14.0 was pushed; `go list -m` resolved `github.com/go-go-golems/geppetto v0.14.0`.
- `go get` upgraded the module from v0.13.13 to v0.14.0.

### What didn't work
- No new failures observed in this step. Earlier DNS restrictions are recorded in Step 4.

### What I learned
- The published main and local implementation had identical trees, making a new branch from main sufficient without replaying duplicated connector-created commits.

### What was tricky to build
- The release target pushes all local tags, so fetched upstream tags first and set origin to the real upstream repository.

### What warrants a second pair of eyes
- Validate the updated module without workspace substitution and check the release workflow result.

### What should be done in the future
- Complete dependency verification and the harder evaluation/routing work recorded in the ticket.

### Code review instructions
- Review go.mod/go.sum and run `GOWORK=off go test ./...` in a suitable environment.

### Technical details
- Release tag: v0.14.0. Workspace source checkout is unchanged; tests must explicitly disable workspace substitution to validate the released module.

## Step 6: Preserve routing conditions in plan edits

Resumed after Geppetto v0.14.0 was published and rag-workbench built successfully with `GOWORK=off`. The user noted that normal workspace builds are also acceptable. Because this isolated clone is not listed in the parent workspace, `/tmp/ragwb-resume.work` selects the clone and the release checkout without changing the parent workspace.

### Prompt Context
**User prompt (verbatim):** "ok,  continue. you can build without GOORK=off i think. continue"
**Assistant interpretation:** Continue the RAGWB-007 evaluation and routing follow-ups using an appropriate workspace.
**Inferred user intent:** Return to product behavior and avoid unnecessary workspace restrictions.
**Prior commit:** `11790da` bumps Geppetto to v0.14.0.

### What I did
- Carry `when` through component and raw node specifications in `specOf`, and normalize it on inserted or replaced nodes in `applyOp`.
- Add `TestWhenSurvivesInsertedPlanOperation`, which executes a patched plan and checks the decision actually skips the inserted node.

### Why
- Conditional routing must remain effective when users edit a plan; retaining only a visible field without enforcing it would be insufficient.

### What worked
- `GOWORK=/tmp/ragwb-resume.work GOCACHE=/tmp/geppetto-bound-go-cache GOFLAGS=-buildvcs=false GOPROXY=off go test ./pkg/ragjs -run '^TestWhenSurvivesInsertedPlanOperation$' -count=1` passed.
- Existing `TestClassifyAndWhenRoute` and `TestWhenCompile` passed in the same workspace.
- Release-backed `GOWORK=off go build ./...` passed before this change.

### What didn't work
- No new failures observed.

### What I learned
- The component wrapper stores routing in `opts.when`; raw edit specifications expose it as `when`. Both must reach the same normalization step.

### What was tricky to build
- Keep an existing node's condition on replacement unless a new one is explicitly provided, while routing all new conditions through `normWhen` and graph finalization.

### What warrants a second pair of eyes
- Verify insertion and replacement behavior, particularly decision edges and lambda conditions.

### What should be done in the future
- Complete harder evaluation cases and the workbench routing preset, then validate and document the results.

### Code review instructions
- Read `specOf` and `applyOp` in `pkg/ragjs/assets/rag.js`; run the tests listed above.

### Technical details
- `/tmp/ragwb-resume.work` points to the isolated rag-workbench clone and the Geppetto release checkout. It is not committed.

## Step 7: Exercise conditional edits and ship a routing preset

The first continuation commit retained `when` on inserted and replaced nodes. Expanded verification to exercise raw specifications, operation-level options, component values, replacement with a new condition, replacement retaining the existing condition, lambda conditions, missing references, and preservation of the original IR. Each executable case checks the decision edge and actual run/skip outcome.

Added the requested workbench preset. It classifies a question on submit, runs retrieval and decision reranking only when the reports are needed, and uses a direct answer otherwise. The shipped script is exercised in the Goja runtime using the bundled corpus and deterministic decision/LLM fakes; the test checks both a greeting and a report question and compiles the GUI.

### Prompt Context
**User prompt (verbatim):** "do 2, 3, 4. commit at appropriate intervals and keep a detailed diary as you work (using the diary format from the skill)"
**Assistant interpretation:** Complete the harder evaluation set, conditional plan-edit support, and routing preset with validated checkpoints.
**Inferred user intent:** Resume the actual RAG work without leaving partial routing behavior or undocumented changes.
**Prior commit:** `75c9920` — preserve `when` in plan edits.

### What I did
- Added `TestWhenInPlanEdits` with six executable edit forms, decision-edge checks, a rejected missing reference, and an unchanged-original check.
- Added `examples/presets/decision-routing.js` and registered it in `examples/presets/index.json`; its description states the required decision and LLM backends.
- Added `TestDecisionRoutingPreset`, which reads the actual preset file, runs it over the bundled reports using fakes, checks the two paths and compiles its GUI.

### Why
- Routing is an execution property, not only a serialized node property. The checks exercise ordering and branch outcomes.
- A discoverable preset makes the decision feature usable in the workbench, with the trace explaining skips.

### What worked
- `GOWORK=/tmp/ragwb-resume.work GOCACHE=/tmp/geppetto-bound-go-cache GOFLAGS=-buildvcs=false GOPROXY=off go test ./pkg/ragjs -run '^TestWhen(InPlanEdits|SurvivesInsertedPlanOperation)$' -count=1` passed.
- The same environment with `go test ./pkg/ragjs -run '^TestDecisionRoutingPreset$' -count=1` passed, exercising the actual file and both routing outcomes.

### What didn't work
- No new failures observed in these checks.

### What I learned
- A component wrapper and a raw node take different paths through normalization. Both preserve the explicit decision dependency.

### What was tricky to build
- Test the actual preset without network calls while retaining the same host modules, GUI compiler, and corpus used by the workbench.

### What warrants a second pair of eyes
- Conditions on replacement intentionally remain unchanged when the replacement omits `when`; explicit new conditions replace them. Review whether a future edit operation should explicitly clear a condition.
- The fake is deterministic and checks wiring; it does not establish decision-model quality or calibration.

### What should be done in the future
- Build and measure the harder answerable/unanswerable evaluation set, distinguishing ranking metrics from rejection metrics and model errors.

### Code review instructions
- Read `route_test.go`, `examples/presets/decision-routing.js` and its index entry. Run the two focused test commands above.

### Technical details
- Preset requires `--decide pi` (or fake) and an LLM backend. Both gated retrieval nodes and reranking share `route.needs`; missing decisions follow the engine's existing fail-open policy.

## Step 8: Build a harder reports challenge and measure the offline baseline

The original goldens saturated hit@5, so I added a corpus-relative challenge that separates retrieval ranking from rejection. Twelve answerable questions point to an expected document and section; eight near-topic questions ask for details absent from the bundled reports. The new command can compare plans, vary the filtering threshold and inspect individual cases.

### Prompt Context
**User prompt (verbatim):** "do 2, 3, 4. commit at appropriate intervals and keep a detailed diary as you work (using the diary format from the skill)"
**Assistant interpretation:** Complete a more discriminating evaluation, including rejection and ranking measurements, and record how to reproduce it.
**Prior commits:** `75c9920` and `37acafc` implement conditional plan edits and the routing preset.

### What I did
- Added `examples/apps/reports/lib/challenge.js` with answerable section labels and corpus-unanswerable cases. Checked the distinctive absent terms against every bundled corpus file.
- Added `reports challenge`, which scores lexical, fused, graded and filtered plans, accepts the filter threshold in percent and can output case-level results.
- Counted false rejections of answerable queries and false acceptances of unanswerable queries separately. Provider errors, unsupported triggers and labels that match no section abort evaluation.
- Added focused runtime tests for all labels, metrics, invalid triggers and missing-provider errors; documented usage and limits in the reports README.
- Recorded the complete fake-provider comparison in `sources/06-challenge-fake.txt`.

### Why
- A false acceptance is not visible in ordinary answerable-only retrieval metrics. An evaluation that interprets provider failure as an empty successful result would overstate rejection quality.
- Empty output for a question with a labeled relevant section is tracked as a false rejection, so a higher rejection rate cannot be assessed in isolation.

### What worked
- Offline command: `GOWORK=/tmp/ragwb-resume.work GOCACHE=/tmp/geppetto-bound-go-cache GOFLAGS=-buildvcs=false GOPROXY=off go run ./cmd/ragwb --decide fake --no-store verbs --dir examples/apps/reports reports challenge --names lexical,fused,graded,filtered --threshold 50`.
- Fake lexical: hit@5 0.833, MRR 0.611, 0/8 rejected, 0/12 false rejections. Fake filtered: hit@5 0.417, MRR 0.333, 2/8 rejected, 1/12 false rejections, 40 decision calls. The fake is deterministic and these numbers only validate the test setup.
- `GOWORK=/tmp/ragwb-resume.work GOCACHE=/tmp/geppetto-bound-go-cache GOFLAGS=-buildvcs=false GOPROXY=off go test ./pkg/ragjs -run '^TestReportsChallenge' -count=1` passed.

### What didn't work
- A first test run failed because the Go test result struct lacked the JSON tag for `false_accepts`; corrected the decoding and reran successfully.
- A run with `--decide pi` returned `systemone transport failed: decision provider unavailable`. A network-enabled retry was rejected by automatic approval review because it would send report excerpts to the configured external provider without explicit approval for that data transfer. No live calibration result was obtained.

### What I learned
- This set is harder than the earlier hit@5-saturated goldens even for lexical retrieval. The fake filter reduces both accepted distractors and answerable retrieval; its behavior should not be generalized to a real model.
- Unanswerable means unanswerable from the provided corpus. The negative label requires review if the corpus changes.

### What was tricky to build
- Existing evaluation infrastructure scores ranking but does not measure rejection. The challenge runs negatives separately and treats any execution error as an error, not a successful rejection.

### What warrants a second pair of eyes
- Check the hand-labeled sections and the corpus-relative negative assumptions before treating the sample as a model benchmark. It is small and is not a statistically robust evaluation.

### What should be done in the future
- With explicit approval for external transfer of the questions and excerpts, repeat with a real decision provider and compare several thresholds before selecting one.

### Code review instructions
- Read `examples/apps/reports/lib/challenge.js`, `examples/apps/reports/evals.js`, and `pkg/ragjs/challenge_test.go`. Follow `examples/apps/reports/README.md` for reproduction and case-level inspection.

### Technical details
- `--threshold` changes only the filtered plan and is scaled from integer percent to probability. The evaluator runs with the submit trigger, so decision nodes are executed. The source receipt contains deterministic fake results, not model calibration.

## Step 9: Measure the challenge with the configured decision provider

The user explicitly approved sending the evaluation questions and bundled report excerpts to the configured Pi decision provider. Repeated the challenge with the provider at three filter thresholds and recorded the outputs in `sources/07-challenge-live.txt`.

### Prompt Context
**User prompt (verbatim):** "yes you may"
**Assistant interpretation:** The user approved the previously described external transfer of challenge questions and report excerpts for live calibration.
**Prior commit:** `786a733` adds the challenge and its offline baseline.

### What I did
- Ran the lexical and filtered plans at threshold 0.5, then filtered in separate runs at 0.3 and 0.7. Each filtered run used 40 decision calls.
- Saved the command outputs and checked off the harder evaluation task in the ticket.

### Why
- Fake-provider results establish wiring but do not measure the real model. Rejection, false rejection and ranking must be considered together when choosing a threshold.

### What worked
- With `GOWORK=/tmp/ragwb-resume.work GOCACHE=/tmp/geppetto-bound-go-cache GOFLAGS=-buildvcs=false GOPROXY=off`, the command `go run ./cmd/ragwb --decide pi --no-store verbs --dir examples/apps/reports reports challenge --names lexical,filtered --threshold 50` succeeded with network access.
- On 12 answerable and 8 corpus-unanswerable cases, filtered at 0.3 and 0.5 rejected 3/8 negatives; 0.7 rejected 4/8. Each returned some passages for all 12 positives and had hit@5 0.833.
- Filtered MRR was 0.607, 0.653 and 0.639 at 0.3, 0.5 and 0.7 respectively. The lexical baseline had MRR 0.611 and rejected 0/8 negatives.

### What didn't work
- Nothing failed in the approved live runs. The previous restricted-network failure and automatic review rejection are recorded in Step 8.

### What I learned
- This small challenge exposes a remaining false-acceptance problem: even at 0.7, four unanswerable cases returned passages. No threshold is justified as a general production setting by these samples alone.

### What was tricky to build
- The three thresholds were evaluated in separate live executions, not against one fixed set of saved probabilities. Observed differences can reflect model variability as well as the threshold.

### What warrants a second pair of eyes
- Assess the eight negative labels and the consequences of returning partially relevant passages for questions whose requested detail is absent; passage relevance is not the same as answerability.

### What should be done in the future
- Expand and independently label the dataset, record model identity and score distributions, and evaluate thresholds on fixed model outputs before making calibration claims.

### Code review instructions
- Read the live output in `sources/07-challenge-live.txt` and compare the question labels in `examples/apps/reports/lib/challenge.js`. Reproduction sends questions and excerpts to the configured provider and requires its credentials and network access.

### Technical details
- Runs were independent; each filtered invocation made 40 decision calls. No generated answers were scored. Rejection means the retrieval plan returned zero passages, not that an answer model abstained.

## Step 10: Synchronize the shared workspace and inspect vault-search readiness

Updated the parent `go.work` to use the existing Geppetto release checkout at v0.14.0 and this resumed rag-workbench checkout. Investigated the existing parc example and tested a local-only server on the actual vault to ground the production advice.

### Prompt Context
**User prompt (verbatim):** "yes, update geppetto and stuff.\n\nTHen, analyze what it would take for us to build a UI for searching the vaultl, and how to index the vault regularly on triggers, and how to put this into production. Study and then tell me what you advise"
**Assistant interpretation:** Finish the previously proposed workspace integration and review preparation, and study existing vault functionality, refresh, deployment and gaps.
**Inferred user intent:** A practical recommendation based on the actual vault and implementation.

### What I did
- Changed the shared, untracked parent `go.work` from `./geppetto` and `./rag-workbench` to `./geppetto-bound-connections` and `./rag-workbench-resume`, leaving other workspace modules unchanged. The former checkout remains on its prior task branch; the latter Geppetto checkout is at tag v0.14.0.
- Verified the selected module path with `go list -m` and built the workbench and its focused routing/challenge tests under the ordinary shared workspace.
- Examined `examples/apps/parc`, serve, watch, runtime refresh, source manifests, artifacts, GUI and the vault's ignore file. Counted vault Markdown and surveyed syntax without printing note content.
- Started a localhost-only `ragwb serve --watch 30s` in tmux against the real vault, with a temporary local artifact store and no model providers. Queried health, app shell and search, then stopped it using `lsof-who`.

### Why
- A workspace can silently override the released dependency in `go.mod`; validating the actual workspace selection ensures the integration is real.
- Existing code and measured behavior provide a better production starting point than designing a replacement from assumptions.

### What worked
- `go env GOWORK` points to the parent workspace; `go list -m -f '{{.Path}} {{.Dir}}' github.com/go-go-golems/geppetto` points to `geppetto-bound-connections`, whose HEAD is v0.14.0.
- `GOCACHE=/tmp/geppetto-bound-go-cache GOPROXY=off GOFLAGS=-buildvcs=false go build ./...` passed; focused `go test ./pkg/ragjs -run '^(TestReportsChallenge.*|TestWhenInPlanEdits|TestDecisionRoutingPreset)$' -count=1` passed.
- Full suite passed with local test sockets permitted and a temporary store: `GOCACHE=/tmp/geppetto-bound-go-cache RAGWB_STORE=/tmp/ragwb-test-suite-store GOPROXY=off GOFLAGS=-buildvcs=false go test ./...`.
- Cold vault build: 86,395 chunks, 24.831 s build, 25.09 s open, temporary artifact store 688 MiB. Health and shell returned HTTP 200; one search returned HTTP 200 in 32 ms. This is a single local sample, not a load test.

### What didn't work
- With the default cache, `go build ./...` and the focused test could not write `/home/manuel/.cache/go-build/...: read-only file system`; using the writable temporary cache fixed them.
- Both default and custom tmux sockets failed inside the sandbox with `Operation not permitted`; escalated execution allowed the localhost-only test.
- An initial sandboxed `go test ./...` could not create the default `/home/manuel/.ragwb/store` and tests using httptest failed with `socket: operation not permitted`. These are sandbox restrictions, not evidence the full suite passes.

### What I learned
- The parc app already supplies a lexical search UI, facets and a section viewer; `serve --watch` polls for changes and requests incremental refresh. The implementation defaults to loopback and explicitly has no authentication.
- Source change detection hashes paths, sizes and mtimes rather than contents on each poll; a same-size edit preserving mtime needs reconciliation. The example hardcodes its vault path, does not use the vault's `.vault-ignore` file, excludes all `ttmp`, and covers Markdown rather than attachment contents.
- The server logs query text unless `--quiet` is set. The indexes and cached chunks contain vault content and require the same protection as the source.

### What was tricky to build
- Keep the existing worktree branches intact while changing which checkouts the shared workspace resolves. The parent workspace file is outside either repository and therefore is not part of the PR.

### What warrants a second pair of eyes
- Deployment access control, exclusion policy and Obsidian-specific rendering and links need explicit design. A full-vault search result must be checked after edits, deletes and renames under concurrent query load.

### What should be done in the future
- Evaluate metadata/content freshness failures, build and query latency under concurrent edits, index retention and recovery, and a representative set of vault queries before production.

### Code review instructions
- Check `examples/apps/parc/lib/corpus.js`, `pkg/natives/sourcedir.go`, `cmd/ragwb/cmds/serve.go` and `pkg/runner/serve_watch_test.go`; see the recorded commands above.

### Technical details
- A filesystem survey excluding hidden/tooling/attachment directories found 2,299 Markdown files, about 80.7 MB of text, 1,895 with frontmatter, 1,417 with wiki links and 53 with embedded wiki links. Survey inclusion is approximate and differs from the app's exact globs.
