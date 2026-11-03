---
title: "PBUI Workbench Stabilization: Safe Publication, a Proven Headless Boundary, and Binding Semantics Shared with Go"
aliases:
  - PBUI-WORKBENCH-CORE-1 stabilization report
  - workbench stabilization S0–S7
  - design doc 04 implementation
  - reentrant_execution
  - onObserverError
  - pbui link-kernel entry
  - WorkbenchBindingRule
  - workbench launch policy
  - document source ownership
tags:
  - project-report
  - pbui
  - workbench
  - typescript
  - go
  - architecture
  - refactoring
  - testing
  - sync
  - protobuf
  - release
status: complete
type: project-report
created: 2026-09-03
updated: 2026-09-03
repo: /home/manuel/workspaces/2026-09-01/add-plot-editor/pbui
branch: task/consolidate-pbui-kernel
source_ticket: PBUI-WORKBENCH-CORE-1
source_ticket_path: /home/manuel/workspaces/2026-09-01/add-plot-editor/pbui/ttmp/2026/09/03/PBUI-WORKBENCH-CORE-1--hard-cutover-consolidation-of-the-workbench-into-a-reusable-composable-core
source_design_doc: design-doc/04-workbench-stabilization-transaction-safety-headless-boundary-and-typescript-go-parity.md
related_vault_notes:
  - "[[PROJECT REPORT - PBUI Workbench Core - A Headless Engine, a Pure Planner, and the Hard Cutover of the React Shell]]"
  - "[[PROJ - PBUI - Presentation-Based UIs in TypeScript and React]]"
  - "[[PROJ - PBUI Workbench Tiles - A Reusable Server-less Shell and the Chat Agent on Tiles]]"
  - "[[PROJECT REPORT - PBUI Workbench Control Plane - Revisioned Authoring Across React, Go, and Agents]]"
  - "[[PROJECT REPORT - PBUI Application Views - Logical Views, Linked Placements, and the Launcher Foundation]]"
  - "[[PROJECT REPORT - PBUI Linked Tiles - Landing the Binding Algebra in the pbui Workbench]]"
  - "[[PROJECT REPORT - PBUI Kernel - One Compiled Presentation, Named Fragments, and the Clean Cutover of Every Consumer]]"
---

# PBUI Workbench Stabilization: Safe Publication, a Proven Headless Boundary, and Binding Semantics Shared with Go

This report describes the stabilization program run on the pbui workbench on 2026-09-03 under ticket PBUI-WORKBENCH-CORE-1, specified by the ticket's fourth design document and executed as phases S0 through S7. The program followed the hard cutover described in [[PROJECT REPORT - PBUI Workbench Core - A Headless Engine, a Pure Planner, and the Hard Cutover of the React Shell]], whose post-implementation review had found seven correctness defects at the new transaction boundary, one false claim about the core's dependency graph, and one under-specified contract between the TypeScript manifests and the Go catalog. The program fixed all three groups without changing the protocol → core → shell architecture. It covers the evidence the program started from, the seven invariants it was required to establish, the three tracks of work (transaction safety and owned state, the headless package boundary, binding and source semantics shared with Go), the phase-by-phase implementation with excerpts from the code that landed, the decisions taken where the design left a choice, the verification across the repository and four external products, and what remains. The purpose is to let an engineer who has not read the ticket understand why each boundary now holds, which test proves it, and where the remaining edges are.

The reader is assumed to know the sibling report on the cutover, in particular its description of `createWorkbenchCore`, the pure planner, the links collaborator, document sources, and the batch-preserving sync outbox. This report does not repeat those descriptions; it describes what changed in each and why. The design document is `design-doc/04-workbench-stabilization-transaction-safety-headless-boundary-and-typescript-go-parity.md` in the ticket directory, cited below by section as "§n"; the diary steps are 20 through 28 of `reference/01-investigation-diary.md`.

> [!summary]
> - Publication is now safe past the point of no return. A transaction is prepared as pure values (document, session, index, and the link runtime's next state), installed without notification, and published under one primitive, `attemptAll`, that attempts every observer exactly once and reports failures as `{ stage, revision, error }` through `onObserverError`. Any mutation method called while the core is preparing or publishing is refused with the code `reentrant_execution`. The recorded defects SUBSCRIBER_ESCAPE, POST_COMMIT_ESCAPE, and REENTRANT_RECEIPTS are inverted.
> - The core owns its state. Every document is cloned at ingress; outside production, what `getState()` returns is deep-frozen, including the index's maps; `core.snapshot()` returns a clone. Preview draws ids from a lookahead pool and only a committed execution consumes them, so `execute` after `preview` mints the ids the preview reported. A transition that reproduces the current document installs nothing.
> - Sync waits for an answer. Bootstrap creates the server row from a clone of the local document and acknowledges the outbox entries the clone already covers; adoption advances `revision` only after the target accepted the candidate, and a refusal is the new phase `incompatible`; rebased entries are validated through the target's catalog; the 422 isolation loop keeps every pending batch overlaid.
> - The headless claim is proven by a build. PBUI gained a pure `./link-kernel` entry and declared React and react-dom as optional peers; the core imports only that entry; `pnpm boundary` packs the three packages, installs the core alone into an empty project, asserts React is absent, imports the core, plans a command, and scans the built output's import specifiers.
> - Bindings and sources have a cross-language contract. A manifest declares `bindings: Record<name, { required, formats?, role }>`, a `launch` policy, and optionally `additionalBindings: { formats? }`; `openBindings` is gone. Go's `BindingRule` gained `Formats` and `ApplicationDescriptor` gained `AdditionalBindings`, validated in the same order with the same codes. Fifteen shared fixtures under `contracts/workbench/v1` are asserted by both validators. Document sources carry an `id` recorded in each stub's `$source` field, and `readWorkbenchSnapshot` hydrates sources before the catalog validates.
> - Phases S0–S7 are committed (`8dd9302` through `306a3c6`, 87 files, +3,421/−295 lines). The whole-workspace audit is green (13 typechecks, 1,508 tests, 12 builds, 5 Storybooks, protocol parity, the boundary check, Go); the four external products were re-verified in parallel; the versions are pbui 0.12.0, protocol 0.5.0, core 0.2.0, shell 0.6.0. All seventeen completion gates of §16 hold in the repository. The publish remains with the user.

## 1. Project status

The work lives on the pbui branch `task/consolidate-pbui-kernel`, on top of commit `03fde84`, which is the baseline that committed design doc 04 and the review bookkeeping. Each phase has one code commit and one docs commit that adds the diary step, the changelog entry, and the task tick.

| Phase | Deliverable | Code commit | Docs commit |
|---|---|---|---|
| S0 | The seven review probes as `it.fails` package tests; public-surface golden; stabilization inventory; dependency graph | `8dd9302` | `156052b` |
| S1 | `attemptAll`; `onObserverError` replacing `onPostCommitError`; execution phase and `reentrant_execution`; the source's try-then-defer protocol | `740ef57` | `87916b3` |
| S2 | Pure link-runtime reducers; `stage`/`stageReplace`/`install`/`publish` on the collaborator; publication order receipt → link observers → core observers | `740ef57` (shared with S1) | `87916b3` |
| S3 | Source scheduling pinned by tests; bootstrap with covered entries; acknowledged adoption and the `incompatible` phase; `validateDocument`; isolation overlay; 409 requeue-before-rebase | `929c9e1` | `8485c76` |
| S4 | Ingress clone; development deep freeze; `snapshot()`; id pool; no-op detection; replacement title, description index capture, expansion refusal index, shell completeness, focus scoping; all seven probes green | `2833785` | `2470cbf` |
| S5 | `@hyperslop-systems/pbui/link-kernel`; optional React peers; nine core imports repointed; `pnpm boundary`; `packageGraph.test.ts` | `c3befc7` | `51155c8` |
| S6 | Binding rules, launch policy, `additionalBindings`; Go `Formats` and `AdditionalBindings`; shared fixtures; source ownership; persistence hydration; in-repo consumers | `7d76033` | `0e49450` |
| S7 | Whole-workspace audit; browser smoke; versions; consumer re-verification; migration note on the launch-policy default | `eabf8e1`, `306a3c6` | `de6dcb8` |

The kickoff analysis (diary Step 20) is `b1eea22`. The range `03fde84..de6dcb8` changes 87 files with 3,421 insertions and 295 deletions; the largest code commit is S6 (46 files, +1,373), because it adds the fixture catalog and the Go parity test alongside the TypeScript changes.

The test picture, comparing the end of Phase 9 of the cutover (`83074c5`) with the end of S7:

| Package | At `83074c5` | At `eabf8e1` |
|---|---|---|
| `workbench-protocol` | 40 | 40 |
| `pbui` (root) | 554 | 554 |
| `workbench-core` | 189 | 241 |
| `pbui-workbench` | 114 | 116 |
| ecommerce, editor, sandbox, chat, plotscript, datalab | 35, 12, 224, 241, 32, 13 | 35, 12, 224, 241, 32, 13 |
| Total | 1,454 | 1,508 |

The 52 new core tests include the seven probes, the public-surface golden, three publication cases, three source-scheduling cases and four ownership cases in `sources.test.ts`, five sync cases, eight ownership cases, three package-graph cases, the fifteen fixture cases plus one existence check, and the describe and hydration cases. The two new shell tests cover construction completeness and focus scoping.

## 2. The evidence

The program did not begin from an argument about the code but from seven executable probes written during the post-implementation review (`scripts/04-implementation-review-probes.test.ts`, output in `04-implementation-review-probes.output.txt`). Each probe constructs a core, performs a realistic sequence, and prints what happened. The recorded values are the acceptance criteria of the program: §4 of the design states that each must be inverted in a package test before the pass is complete.

| Probe | Recorded behaviour | Consequence |
|---|---|---|
| EXPOSED_STATE_MUTATION | `getState().document.name` assigned from outside; the document changed under revision 0 | The single gateway is not enforced; a caller can bypass validation, notification, and the revision |
| PREVIEW_ID_DRIFT | `preview` reported placement `n-00000009-0000`; the immediate `execute` of the same command created `n-00000012-0000` | An agent that previews and then executes cannot refer to what it previewed |
| SUBSCRIBER_ESCAPE | A throwing core subscriber: revision after the throw 1, commit receipts delivered 0 | The change is visible, the caller sees an exception, and persistence and sync miss the commit |
| POST_COMMIT_ESCAPE | A throwing links post-commit callback: revision after the throw 1 | Same failure through the link path |
| REENTRANT_RECEIPTS | A document source removing a bound resource, then the view closing: receipts in revision order `[4, 3]`, mutations `[documentDelete]` before `[placementClose, viewDelete]` | An outbox sends a delete before the mutation that made it legal; a Go host refuses the request |
| DROPPED_REPLACE_TITLE | A same-app replacement with an explicit title returned `{ ok: true, changed: false }` | The requested title is lost silently |
| CREATE_BOOTSTRAP_DROP | Sync against a missing server row with one queued batch: the row was created, `onDropped` was called once, phase `synced` | A batch that the created document already contains is reported as dropped |

The review's executive summary adds the two findings that are not probes: the core's source contains no React import but its package graph reaches React through `@hyperslop-systems/pbui`'s root entry, so the "No React, no DOM" claim is proven only for the source text; and `openBindings` lets TypeScript accept bindings the Go validator refuses, `DocumentSource` uses a format as an implicit ownership token, and persisted layouts are validated before sources can repair them.

Phase S0 moved the seven probes into `packages/workbench-core/src/stabilization.probes.test.ts`. Each is written as the behaviour the program requires and marked `it.fails`: the suite is green while the defect stands and turns red when a later phase fixes the case, which is the signal to remove the marker. Two probes were rewritten for the package: POST_COMMIT_ESCAPE throws from a link runtime subscriber rather than from a replaced `afterCommit`, which is the realistic failure and the stage the design names; SUBSCRIBER_ESCAPE names the not-yet-existing option `onObserverError` through a cast so the file typechecks before S1. A second S0 test, `publicSurface.test.ts`, snapshots the sorted export names of the `index`, `sync`, `persistence`, and `rebalance` entries so that every surface change in S1–S6 is a deliberate snapshot update. The inventory (`reference/03-stabilization-inventory-consumers-surface-dependency-graph.md`) records the consumers of each surface the program changes, in the repository and in the four external products, and the core's dependency graph at 0.1.0: runtime dependencies `@bufbuild/protobuf`, pbui, and protocol; no peers; react and react-dom as devDependencies used by nothing but the fence test's wording; nine production modules importing the pbui root entry.

## 3. The invariants

§5 of the design states seven invariants. They are listed here because the rest of the report is organized by which one each change establishes.

- Point of no return (§5.1). Once internal state is current, no error may make `execute`, `apply`, or `replace` throw or return a refusal. Observer failures are reported separately.
- Monotonic publication (§5.2). For every receipt observer, `receipt[i].revision < receipt[i+1].revision`. No nested transaction publishes before the outer one finishes publishing.
- Complete observers (§5.3). Every registered observer is attempted exactly once per matching publication; one observer cannot suppress another; failures are collected and reported after all attempts.
- Snapshot ownership (§5.4). The same revision always means the same semantic document and a matching index. No public reference can mutate internal state.
- Preview purity (§5.5). `execute(c)` after `preview(c)` is equivalent to `execute(c)` alone, apart from concurrent state changes.
- Cross-language binding (§5.6). For a catalog `A` and a document `D`, the first diagnostic's code and path from the TypeScript validator equal those from the Go validator, for the fixtures the shared contract covers.
- Package boundary (§5.7). A clean consumer can import `@hyperslop-systems/workbench-core` without React in its dependency graph or in the built runtime imports.

The design groups the work into three tracks: Track A (transaction safety, owned state, sources, sync; invariants 1–5), Track B (the headless boundary; invariant 7), and Track C (binding and source semantics; invariant 6). §18 fixes the order A → B → C, because every source and sync consumer depends on safe transactions, the package boundary must be complete before Datalab adopts the core, and binding semantics must be shared with Go before Datalab adds a large catalog and a remote persistence path.

## 4. Track A: transaction safety and owned state

### 4.1 Prepare, install, publish

The cutover's `install` set `state`, ran a `for … of` loop over the listeners with no guard, called `onCommit` under a guard, and then called `links.afterCommit(effects)` outside it. The link runtime notified its own subscribers from inside its `commit`. Three consequences followed: a throwing listener stopped the loop and escaped through the mutation method after the state had changed; a listener could synchronously call `core.apply` and publish an inner receipt before the outer one; and a link subscriber could observe a new durable link program while the runtime still held the previous values.

The stabilized `install` in `createWorkbenchCore.ts` separates the three stages the design names in §6.1:

```text
prepare (pure)
  plan the command or take the raw batch
  append links maintenance
  apply the mutations structurally (applyMutations clones the document first)
  validate against the catalog
  build the index; repair the session
  stage the link runtime's next value: links.stage(effects) or links.stageReplace(doc)

install (no notification)
  state = owned({ document, session, index, revision: revision + 1 })
  links.install(stagedLinks)

publish (exception-isolated, in this order)
  phase = "publishing"
  attemptAll([receipt], onCommit, "commit-receipt")     -- only when mutations exist
  links.publish(revision, failures)                       -- only when the link state changed
  attemptAll(listeners, listener => listener(), "core-subscriber")
  phase = "idle"                                          -- in finally
  reportFailures(failures, onObserverError)
```

The code that implements the publish stage:

```ts
const stagedLinks = linkState ?? (effects && effects.length > 0 && links ? links.stage(effects) : null);
state = owned({ document: next, session: repaired, index, revision });
if (stagedLinks && links) links.install(stagedLinks);
phase = "publishing";
const failures: WorkbenchObserverError[] = [];
try {
  if (mutations && mutations.length > 0) {
    const receipt: CommitReceipt = { mutations, document: next, revision };
    attemptAll([receipt], (item) => options.onCommit?.(item), "commit-receipt", revision, failures);
  }
  if (stagedLinks && links) links.publish(revision, failures);
  attemptAll(listeners, (listener) => listener(), "core-subscriber", revision, failures);
} finally {
  phase = "idle";
}
reportFailures(failures, options.onObserverError);
```

Two properties of this ordering are worth stating. The link state is staged before the assignment of `state`, so a reducer that throws leaves nothing installed; a throw from `stage` is the one place in the transaction where an exception is still the correct answer, because it occurs before the point of no return. The receipt is published first because persistence and sync consume receipts and the design's §6.2 order places them before UI subscribers; the diary flags that order for a second reader but no consumer depended on the alternative.

### 4.2 Observer errors as data

`packages/workbench-core/src/publication.ts` is the safe observer primitive of §6.2. Its whole surface is two functions and two types:

```ts
export type ObserverStage = "commit-receipt" | "core-subscriber" | "link-subscriber" | "replacement-effects";

export interface WorkbenchObserverError {
  readonly stage: ObserverStage;
  readonly revision: number;
  readonly error: unknown;
}

export function attemptAll<T>(observers: Iterable<T>, call: (observer: T) => void, stage: ObserverStage, revision: number, failures: WorkbenchObserverError[]): void {
  for (const observer of [...observers]) {
    try {
      call(observer);
    } catch (error) {
      failures.push({ stage, revision, error });
    }
  }
}

export function reportFailures(failures: readonly WorkbenchObserverError[], sink: ObserverErrorSink | undefined): void {
  for (const finding of failures) {
    try {
      if (sink) sink(finding);
      else console.error(`workbench-core: ${finding.stage} observer failed at revision ${finding.revision}`, finding.error);
    } catch (reportingError) {
      console.error("workbench-core: observer error handler failed", reportingError);
    }
  }
}
```

`attemptAll` iterates over a snapshot of the observer set, so an observer that unsubscribes another (or itself) during publication does not change who is attempted in that round. `reportFailures` is itself guarded: a throwing `onObserverError` is logged and the remaining findings are still delivered, which is what makes the sink unable to break the publication. The option `onPostCommitError(error, receipt)` of the cutover, which covered only the receipt hook, is replaced by `onObserverError(finding)` with no alias.

The link runtime uses the same primitive with two sinks. A runtime-only write (a tile emitting a value, `setContext`, `setClass`) has no core transaction around it, so `createLinkRuntime({ onObserverError })` installs and publishes at once under the runtime's own sink. A core transaction publishes the runtime through `links.publish(revision, failures)` into the core's failure list, so a throwing link subscriber during a commit is reported with stage `link-subscriber` and the core's revision.

The test `createWorkbenchCore.test.ts › every observer is attempted and every failure is reported after all attempts, in publication order` asserts the order and the report; the probes SUBSCRIBER_ESCAPE and POST_COMMIT_ESCAPE assert that the result is `ok`, that the receipt was delivered, that the other subscriber ran, and that the finding carries the expected stage.

### 4.3 The execution phase and the reentrancy rule

§6.3 lists three policies for a mutation requested from inside a publication: allow immediate nesting, reject synchronously, or enqueue until publication finishes. The design chooses rejection (Decision B), and the diary records the reasoning the kickoff adopted: queuing a synchronous `execute()` makes its return value dishonest, because the caller receives a result for a transaction that has not run; immediate nesting caused the observed receipt inversion; rejection catches accidental subscriber mutation at the moment it happens; and intended reactive maintenance can schedule a microtask.

The core keeps one variable and one constant:

```ts
let phase: "idle" | "preparing" | "publishing" = "idle";
const REENTRANT = { code: "reentrant_execution", because: "the workbench is publishing a transaction; a mutation from an observer must be scheduled for after it" } as const;
```

Every mutation method checks the phase first. `execute` returns `{ ok: false, code: "reentrant_execution", because }`; `apply` adds `diagnostics: []`; `replace` (and through it `restore`, `reset`, and sync adoption) returns `{ ok: false, diagnostics: [reentrant_execution] }`. None of these refusals is reported through `onRejected`, because nothing was wrong with the batch. `preview` is not guarded: it is read-only, and a preview from a subscriber plans against the newly installed state, which is the state the subscriber observes.

The phase is set and restored under `try/finally`, and the `finally` clause is conditional:

```ts
execute(input, executeOptions = {}) {
  if (phase !== "idle") return { ok: false, ...REENTRANT };
  phase = "preparing";
  try {
    ...
    commitIds();
    install({ ... });
    return { ok: true, changed: true, ...ids_of(transition) };
  } finally {
    if (phase === "preparing") phase = "idle";
  }
}
```

The diary names this as the tricky part of S1: the phase must be restored on every early return before `install` (a refusal, an unexpected exception during planning), but must not be reset by the `finally` after `install` has already set it to `idle` on its own, since `install` sets `publishing` and restores `idle` in its own `finally`. The condition `phase === "preparing"` distinguishes the two cases.

Two shell-side stores (the shell-local state and the placement controller) still notify with a bare loop; §11 S1 states that they "may follow", and the diary records them as a follow-up.

### 4.4 The link runtime staged as a value

S2 made the link runtime's post-commit effects a pure function of the previous state, so the core can compute the next link state before the point of no return. `links/runtime.ts` exports two reducers:

```ts
export function reduceRuntimeEffects(state: LinkRuntimeState, effects: readonly RuntimeEffect[]): LinkRuntimeState {
  if (effects.length === 0) return state;
  const emitted = new Map(state.emitted);
  const classes = new Map(state.classes);
  for (const effect of effects) {
    if (effect.kind === "seed-class") classes.set(effect.classId, effect.reference);
    else if (effect.kind === "forget-class") classes.delete(effect.classId);
    else if (effect.kind === "set-emitted") {
      if (effect.reference) emitted.set(effect.port, effect.reference);
      else emitted.delete(effect.port);
    }
  }
  return { ...state, emitted, classes };
}

export function forgetViewValues(state: LinkRuntimeState, viewId: string): LinkRuntimeState {
  const prefix = `${viewId}/`;
  const emitted = new Map([...state.emitted].filter(([port]) => !port.startsWith(prefix)));
  const attended = new Map([...state.attended].filter(([port]) => !port.startsWith(prefix)));
  if (emitted.size === state.emitted.size && attended.size === state.attended.size) return state;
  return { ...state, emitted, attended };
}
```

Both return the same object when nothing changes. The collaborator's `stage(effects)` folds a transition's `link-runtime` and `forget-view-values` effects through the two reducers and returns `null` when the result is the previous state; `stageReplace(doc)` forgets the values of every view the replacement document no longer has. The `LinkRuntime` interface gained `install(next)` (assign without notifying) and `publish(revision, failures)`; `apply`, `forgetView`, `afterCommit`, and `afterReplace` were removed. Because a `null` stage installs and publishes nothing, a close of a view that held no runtime values produces no link notification, which keeps `useSyncExternalStore` consumers quiet.

S1 and S2 share the commit `740ef57` for a reason the diary states: the POST_COMMIT_ESCAPE probe cannot pass with S1 alone. While `afterCommit` notified from inside the runtime, a throwing link subscriber was reported through the runtime's own sink, not the core's, and the design's publication order is only achievable once the runtime's notification belongs to the core's publish step. The exit gate of S2 (a mixed core/link selector cannot observe a new durable link program with old runtime values) follows from installing both values before either is published.

### 4.5 Document sources: try, then defer

`connectDocumentSource` was the one integration that mutated the core from a subscriber; the REENTRANT_RECEIPTS probe is its lifecycle. The design's §6.4 specifies pure microtask scheduling: every signal enqueues a reconcile that runs after the current publication. The implementation chose a variant, recorded in the kickoff as a decision and flagged for review:

```ts
const sync = () => {
  if (disposed) return;
  const { mutations, collisions } = documentSourceMutations(core.getState().document, source);
  ... report collisions once per id ...
  if (mutations.length === 0) return;
  const applied = core.apply(mutations);
  if (!applied.ok && applied.code === "reentrant_execution" && !deferred) {
    deferred = true;
    queueMicrotask(() => {
      deferred = false;
      sync();
    });
  }
};
sync();
const unsubscribeSource = source.subscribe?.(sync) ?? (() => undefined);
const unsubscribeCore = core.subscribe(sync);
```

Reconciliation is attempted synchronously; only when the core refuses it as reentrant, because the signal arrived from inside a publication, is it retried once in a microtask. The reason is a call pattern the sandbox tools use: `library.putProgram()` followed by `commands.open("script", …)` in the same tick. With pure microtask scheduling the stub would not exist when the open command validated its binding and the command would be refused with `unknown_document`. With try-then-defer, a resource added and bound in one tick finds its stub synchronously, while a delete triggered by a close lands as the next transaction, after the receipt that made it legal. A `disposed` flag ensures a disconnected source applies nothing from a stale microtask.

S3 pinned the three behaviours with tests in `sources.test.ts`: a burst of signals outside a publication reconciles once and applies once; a signal from inside a publication reconciles after it, in one transaction; a disconnected source applies nothing from a deferred reconcile. The REENTRANT_RECEIPTS probe flipped in S1 with the guard and the retry alone: the delete is refused inside the close's publication and lands one microtask later as revision 4 after revision 3.

### 4.6 Sync: bootstrap, acknowledged adoption, and the isolation overlay

S3 rewrote the four sync paths named in §7 of the design.

Bootstrap against a missing row (§7.1). The cutover's `bootstrap` created the row from the optimistic local document and then rebased the outbox over it; since the local document already contained the outbox's effects, the rebase found nothing to apply and reported the entries as dropped. The corrected algorithm sets the covered entries aside, creates from a clone, and lets the creation acknowledge them:

```ts
async function bootstrap(): Promise<boolean> {
  setPhase("probing");
  const existing = await client.get();
  if (existing) return adopt(existing).ok;
  const covered = outbox;
  outbox = [];
  const snapshot = clone(WorkbenchDocumentSchema, target!.getState().document);
  let created: SyncResult;
  try {
    created = await client.create(snapshot);
  } catch (error) {
    outbox = [...covered, ...outbox];
    throw error;
  }
  return adopt(created).ok;
}
```

Entries queued while the create request is out are in `outbox` when `adopt(created)` runs and are overlaid on the created document; a failed creation puts the covered entries back ahead of them. The diary records the lesson from a mis-staged test: "covered" is decided by when the snapshot is cloned, not by when the row is found missing. The first version of the test "work queued during create is overlaid and sent once" staged its change while `client.get()` was pending, before the snapshot, so the creation legitimately covered it and nothing was sent; the corrected test waits for `create` to start. A second correction in the same test counted mutations (two for a duplicate) where batches (one) had been written.

Acknowledged adoption (§7.2). `SyncTarget.replaceDocument` must now return `{ ok: true } | { ok: false; diagnostics }`; a `WorkbenchCore` already did, so the in-repo consumers did not change. `adopt` advances `revision`, the outbox, and the phase only after the target accepted:

```ts
const adopt = (result, extra = [], afterConflict = false): { ok: boolean; keptExtra: OutboxEntry[] } => {
  if (!target) return { ok: false, keptExtra: [...extra] };
  const queue = [...extra, ...outbox];
  const { document, kept } = queue.length > 0 ? rebase(result.document, queue, afterConflict) : { document: result.document, kept: [] };
  const accepted = target.replaceDocument(document);
  if (!accepted.ok) {
    options.onIncompatible?.(accepted.diagnostics);
    report(new Error(`workbench-core/sync: the server's document was refused locally — …`));
    setPhase("incompatible");
    return { ok: false, keptExtra: [...extra] };
  }
  revision = result.revision;
  const extraSet = new Set(extra);
  outbox = kept.filter((entry) => !extraSet.has(entry));
  return { ok: true, keptExtra: kept.filter((entry) => extraSet.has(entry)) };
};
```

`SyncPhase` gained `incompatible`: the server is reachable and answered, but the local catalog cannot show its document. It is neither `offline` nor a retryable transport failure; `pump` stops on it, and recovery requires a new `attach` or a catalog change. `SyncOptions.onIncompatible(diagnostics)` carries the reason.

Rebased candidates through the target (§7.3). `applyMutations` proves structural applicability only. `rebase` now calls the optional `target.validateDocument(candidate)` before keeping an entry and drops the entry if the catalog would refuse it; the core exposes `validateDocument(document): ReplaceResult`, a non-installing validation. The test "a rebased entry the catalog would refuse is dropped, not kept on structural applicability alone" covers it.

Isolation overlay (§7.4). When a 422 arrives for a request that carried several batches and `onInvalid` is `"isolate"`, the batches are sent one at a time. Each adoption in that loop previously rebased only the outbox, so a change still pending in the loop disappeared from the screen between two requests and reappeared when it landed. `send(batches, remaining)` now takes the batches the caller still holds, threads them through every adoption as `extra`, and returns the ones that still apply; the loop splits the result back into "not yet sent in this loop" and "held by the outer caller" by identity:

```ts
let pending = [...batches];
while (pending.length > 0) {
  const entry = pending.shift()!;
  if (phase === "detached" || phase === "incompatible") return [];
  pending = await send([entry], [...pending, ...remaining]);
  const remainingSet = new Set(remaining);
  remaining = pending.filter((item) => remainingSet.has(item));
  pending = pending.filter((item) => !remainingSet.has(item));
}
return remaining;
```

One further change on the 409 path: the refused batches are put back at the front of the outbox before `adopt(fresh, [], true)` rebases, so a destructive batch among them is reported as a conflict as before, but the order of `onDropped` relative to the replacement changed (drop first, then replace). The diary flags this for a second reader.

The five sync tests added in S3 cover: creation acknowledges covered entries; work queued during creation is overlaid and sent once; an incompatible server document leaves revision and queue alone with phase `incompatible`; 422 isolation never rolls back a pending change; a rebased-and-accepted entry keeps its title. CREATE_BOOTSTRAP_DROP flipped in S3.

### 4.7 Owned state

Decision C of the design: clone at ingress, freeze in development, provide a safe snapshot, and do not clone on every `getState()`. `packages/workbench-core/src/ownership.ts` implements it:

- `defaultOwnership()` returns `"freeze"` unless `process.env.NODE_ENV === "production"`, read defensively so that an environment without `process` trusts callers.
- `own(schema, message)` is a protobuf `clone`.
- `deepFreeze(value)` freezes a value and everything reachable from it; protobuf messages are plain objects, so the traversal covers them.
- `readonlyIndex(index)` redefines `set`, `delete`, and `clear` on every `Map` in the index to throw a `TypeError` whose message names the gateway ("the index is read-only; set is not allowed (change the document through the core)"), then freezes the map and the index. The type already said `ReadonlyMap`; this makes the instance agree.

The core clones at every ingress: `initial` in the constructor, and the incoming document in `replace` (which serves `restore`, `reset`, and sync adoption). Every install passes through `owned()`, which in freeze mode deep-freezes the document and the session, wraps the index, and freezes the state object. `core.snapshot()` returns `own(WorkbenchDocumentSchema, state.document)` for an integration that wants a document to write on. An `ownership: "trust" | "freeze"` option overrides the default.

Because `applyMutations` already clones the whole document before touching it, a frozen input is safe through every planner step and the applier; no consumer broke on the freeze (shell 116, sandbox 224, ecommerce 35, plotscript 32, chat 241 unchanged). One test changed: the reset round-trip asserted `toBe` identity between the factory's document and the installed one; identity is what Decision C gives up, and the test now compares with `toEqual`. The diary records the freeze cost as one traversal per install in development, invisible against the index build at 12 tiles.

`ownership.test.ts` covers: mutating the initial document after construction changes nothing; mutating a replacement after acceptance changes nothing; in freeze mode the document, session, and index refuse writes; `snapshot()` is a clone; the same revision always means the same document and a matching index; two no-op cases; a refused execution consumes no ids. EXPOSED_STATE_MUTATION flipped in S4.

### 4.8 The id pool

§6.7 recommends a per-transaction id factory (`createIds?: () => IdGenerator`) so that each plan receives a fresh command-local stream and preview discards its stream. The kickoff rejected this for a reason the diary states: a factory restarts deterministic sequences per transaction, which either collides across transactions or must be seeded by revision, and seeding by revision changes every golden's ids. The implementation is a lookahead pool, `packages/workbench-core/src/ids.ts`:

```ts
export function createIdPool(generator: IdGenerator): IdPool {
  const buffers = new Map<string, string[]>();
  const bufferOf = (prefix) => buffers.get(prefix) ?? (buffers.set(prefix, []), buffers.get(prefix)!);
  return {
    fork() {
      const read = new Map<string, number>();
      return {
        ids: (prefix) => {
          const buffer = bufferOf(prefix);
          const position = read.get(prefix) ?? 0;
          while (buffer.length <= position) buffer.push(generator(prefix));
          read.set(prefix, position + 1);
          return buffer[position]!;
        },
        commit() {
          for (const [prefix, count] of read) bufferOf(prefix).splice(0, count);
          read.clear();
        },
      };
    },
  };
}
```

A plan reads ids from a per-prefix buffer, refilling it from the configured generator as needed, and records how many it read. Only `commit()` removes what was read from the buffer. `planned()` in the core forks the pool for each plan; `execute` calls `commitIds()` in exactly one place, after `prepare` succeeded and before `install`. A preview, or an execution refused after planning, leaves the buffer as it found it, so the next plan reads the same ids. The consequences are the ones the design's §5.5 requires plus two it did not ask for: `execute` after `preview` mints exactly the ids the preview reported; deterministic generators such as `sequentialIds()` keep their sequence; and the 44 Phase 0 goldens keep their ids. PREVIEW_ID_DRIFT flipped in S4.

### 4.9 No-op detection and the semantic edge fixes

§6.8 lists focused fixes from the review that are "safest while transaction tests are being rewritten". S4 landed all of them:

- No-op detection. After applying a transition's mutations, `isNoOp(next, session)` compares the candidate to the current document with protobuf `equals` and the session's `workspaceId` and `activePlacementId`; a match returns `{ ok: true, changed: false }` from `execute` or `apply` without touching the revision or the outbox. The diary notes the cost: `equals` runs over the whole document per command, negligible at product sizes but paid on every keystroke that goes through `apply` for a document with thousands of payloads.
- Replacement title. In `planner/show.ts`, a same-app replacement with only a title requested keeps the view's bindings and applies the title through `setTitle`; a same-app replacement with no documents requested and no title remains `unchanged`. DROPPED_REPLACE_TITLE flipped.
- Description index capture. `describe.ts`'s `describeTile(index, …)` reads one captured index throughout instead of re-reading the live state.
- Expansion refusal index. In `planner/plan.ts`, a refusal that occurs inside the expansion of a link command reports the caller's top-level command index and command rather than the planner's internal position, so an agent that retries by index refers to what it sent. The diary records that this is verified by reading, not by a test, because constructing a refusal inside a show expansion requires geometry small enough to refuse the spawn split, and a test on that would depend on the chooser's ranking; a golden is recommended when the link goldens are next touched.
- Shell completeness. `createWorkbenchShell` throws at construction when a presentation names an application with no manifest in the core, and when a manifest in the core has no presentation; a tile with no component cannot render.
- Focus scoping. `focusPlacement` searches only the mounted `rootElement` and never falls back to the global `document`.

At the end of S4 all seven probes assert the required behaviour with no `fails` marker.

## 5. Track B: the headless boundary the build proves

### 5.1 The dependency leak

The core's source never imported React, and a fence test enforced that with a regex. The package graph, however, reached React: nine production modules (`apps.ts`, `commands.ts`, `effects.ts`, `describe.ts`, `links/runtime.ts`, `links/snapshot.ts`, `links/collaborator.ts`, `links/document.ts`, `planner/links.ts`) imported `@hyperslop-systems/pbui`'s root entry, whose runtime bundle carries React components and whose `peerDependencies` name react and react-dom. A package manager installing the core alone would therefore install React, and the built `dist/*.js` of the core imported a React-bearing bundle. The inventory established that every symbol the core used was exported by `src/presentation/links/index.ts` except `createPresentationTypeGraph` (from `presentation/actions/typeGraph`), and that the links directory imports only `../actions/ids` and `../actions/typeGraph` from outside itself, both pure.

### 5.2 The `link-kernel` entry and optional peers

`src/link-kernel.ts` at the pbui root is a re-export file, not a refactor:

```ts
export * from "./presentation/links/index";
export { createPresentationTypeGraph } from "./presentation/actions/typeGraph";
export type { AncestorEntry, PresentationTypeDefinition, PresentationTypeGraph } from "./presentation/actions/typeGraph";
export type { ActionId, CandidateId, FamilyId, ModeId, PredicateId, RuleId, RuntimeTypeId, ScopeId } from "./presentation/actions/ids";
```

The root `package.json` adds the export `"./link-kernel": { types: "./dist/link-kernel.d.ts", import: "./dist/link-kernel.js" }`, and `vite.config.ts` adds the third library entry `"link-kernel": "src/link-kernel.ts"` beside `index` and `vite`. The built `dist/link-kernel.js` reaches one shared chunk and no external module.

The second change is the one the diary calls the lesson of S5. With npm ≥ 7 and pnpm, a dependency's non-optional peer is auto-installed, so declaring the core's imports as pure is not enough: as long as pbui declared React as a required peer, a consumer of the core would receive React through pbui. The root `package.json` therefore declares:

```json
"peerDependencies": { "react": "^18.3.0 || ^19.0.0", "react-dom": "^18.3.0 || ^19.0.0" },
"peerDependenciesMeta": { "react": { "optional": true }, "react-dom": { "optional": true } }
```

This is also the truthful declaration: the root entry needs React, the link-kernel entry does not. The diary flags the consequence for a second reader: a product that installs the root entry and forgot to depend on React itself now gets a runtime error rather than an auto-installed React. Every product in the repository and the four external ones declare React.

On the core's side, the nine imports were repointed to `@hyperslop-systems/pbui/link-kernel`; `fence.test.ts` now forbids the root entry as it forbids `react`; react and react-dom left the core's devDependencies, which now hold only `@types/node`, `typescript`, `vite`, and `vitest`.

### 5.3 The boundary check and the package-graph test

§8.4 states that source regex tests "remain useful but insufficient" and specifies a packed consumer. `packages/workbench-core/scripts/check-boundary.mjs`, run as `pnpm boundary`, performs the following steps in a temporary directory that is removed afterwards:

1. Scan every `dist/*.js` of the core for import specifiers; fail on any `react`, `react-dom`, the bare `@hyperslop-systems/pbui`, or any `@hyperslop-systems/pbui/*` other than `@hyperslop-systems/pbui/link-kernel`.
2. `pnpm pack` pbui, workbench-protocol, and workbench-core into the directory.
3. Write a `package.json` for an empty ES-module project and `npm install --ignore-scripts --no-audit --no-fund` the three tarballs.
4. Assert that no `react` or `react-dom` directory exists in `node_modules`.
5. Write and run an `import.mjs` that creates a core over a two-tile layout with two manifests, previews `commands.duplicate(first, "row")`, asserts no `React` global, and prints the explanation.

The recorded run printed "installed without React" and "imported and planned: split side by side". The script needs registry access for `@bufbuild/protobuf`. `packageGraph.test.ts` pins the declarations by dependency kind as a unit test, so a stray `react` in any of the three manifests fails without the packed run: the core has no React in runtime, peer, or dev dependencies and its runtime dependencies are exactly `@bufbuild/protobuf`, pbui, and protocol; pbui's React peers are declared optional and pbui exports `./link-kernel`; the protocol package has no React at all.

The core's built externals after S5 are `@bufbuild/protobuf`, `@hyperslop-systems/pbui/link-kernel`, `@hyperslop-systems/workbench-protocol`, and `@hyperslop-systems/workbench-protocol/client`. The README's "Package boundary" section now describes the three checks (source fence, declaration test, packed consumer) and, per §8.5, the phrase "No React, no DOM" is retained because the built test proves it.

## 6. Track C: binding and source semantics shared with Go

### 6.1 Four facts the old model conflated

§9.1 of the design identifies four facts that the cutover's manifest represented with one mechanism (`port.documentSlot === true`) plus one escape (`openBindings: true`): which binding names are legal; which legal bindings are required; which document formats may fill a binding; and whether the launcher may create an unbound view. The Go `ApplicationDescriptor` already separated the first two through `DocumentBindings map[string]BindingRule{ Required }` but had no format constraint and no counterpart to `openBindings`, so the two validators disagreed on any document that used the escape. The launcher, for its part, treated every application with a document-slot port as impossible to launch without a document, which is why agentlogic had declared its optional transcript binding through `openBindings` rather than as a slot: a declared slot removed the application from the launcher.

The stabilized manifest in `apps.ts` states each fact separately:

```ts
export interface WorkbenchBindingRule {
  readonly required: boolean;
  readonly formats?: readonly string[];     // absent ⇒ any format
  readonly role: "primary" | "context";     // default "primary"
}

export interface WorkbenchAdditionalBindings {
  readonly formats?: readonly string[];
}

export type LaunchPolicy = "unbound" | "requires-bindings" | "hidden";

export interface WorkbenchAppManifest {
  readonly id: string;
  readonly viewCardinality: ViewCardinality;
  readonly duplicatePlacement: DuplicatePlacement;
  readonly bindings: Readonly<Record<string, WorkbenchBindingRule>>;
  readonly additionalBindings?: WorkbenchAdditionalBindings;
  readonly launch: LaunchPolicy;
  readonly ports?: readonly PortDeclaration[];
}
```

`defineAppManifest` merges port-derived and explicit rules and derives the launch policy:

```text
bindings = {}
for each documentSlot port p:          bindings[p] = { required: false, role: "primary" }
for each explicit rule (name, rule):   bindings[name] = { required: rule.required ?? existing?.required ?? false,
                                                          role: rule.role ?? existing?.role ?? "primary",
                                                          formats: rule.formats ?? existing?.formats }
primary = any rule has role "primary"
launch  = input.launch ?? (primary ? "requires-bindings" : "unbound")
```

A `documentSlot` port still implies a binding, so every existing manifest keeps its meaning without an edit; a binding need not have a port. `bindingNames(app)` replaces `documentSlots(app)` and returns the legal keys of `view.documents` in declaration order. `isDocBound(app)` now means "declares a primary binding", and `describe.ts` reports `docBound` as `app.launch === "requires-bindings"` beside a new `launch` field; the diary records that `docBound` had carried two meanings, "has a binding" and "cannot be launched empty", which coincided until optional context bindings existed. The launcher rows in `packages/pbui-workbench/src/launcherRows.ts` offer an application when `manifest.launch === "unbound"`, replacing the earlier test on binding presence.

`openBindings` is removed with no alias. An application whose bindings are named by what it binds declares `additionalBindings: { formats? }`; the Go counterpart is `AdditionalBindings *BindingRule`, and `nil` means unknown bindings are refused. Per §9.3, a boolean that means "accept every typo" is not retained.

### 6.2 Validation in the same order, with the same codes

`validation.ts` validates each view's bindings in the order legality, existence, format, then requiredness:

```ts
for (const [slot, documentId] of Object.entries(view.documents)) {
  const bindingPath = `${path}.documents["${slot}"]`;
  const rule = app.bindings[slot];
  if (!rule && !app.additionalBindings) { report("unknown_binding", bindingPath, …); continue; }
  const payload = doc.documents[documentId];
  if (!payload) { report("unknown_document", bindingPath, …); continue; }
  const formats = rule ? rule.formats : app.additionalBindings?.formats;
  if (formats && !formats.includes(payload.format)) report("invalid_binding_format", bindingPath, …);
}
for (const [slot, rule] of Object.entries(app.bindings)) {
  if (rule.required && !view.documents[slot]) report("required_binding", `${path}.documents`, …);
}
```

`required_binding` and `invalid_binding_format` are new codes. The Go side, `pkg/workbench/validate.go`, runs the same sequence: `unknown_binding` when the key is unknown and `AdditionalBindings` is nil; `unknown_document` when the id is absent; `invalid_binding_format` when `rule.acceptsFormat(payload.Format)` is false, where the rule is the declared one or `*app.AdditionalBindings`; then `required_binding` at `path + ".documents"`. `model.go` adds `Formats []string` to `BindingRule`, `AdditionalBindings *BindingRule` to `ApplicationDescriptor`, and `acceptsFormat`, which returns true for an empty format list. Go does not receive `launch`: it validates documents, not launcher availability.

One asymmetry is recorded for a second reader. Go ranges over `view.Documents` in map order, so a view with two violations reports either first, while the TypeScript validator reports all diagnostics and the tests compare the first. The fixtures carry one violation each, which keeps the comparison well defined.

### 6.3 Shared fixtures

`packages/workbench-core/scripts/generate-binding-fixtures.ts` writes the fixture catalog under `contracts/workbench/v1`: three catalogs (`basic`, `open-context`, `additional`), nine valid cases under `binding-valid/`, and six invalid cases under `binding-invalid/`. Each fixture is `{ name, catalog, document: <protobuf JSON>, expected: { ok: true } | { ok: false, code, path } }`. The catalogs are expressed in the TypeScript manifest input shape plus a `singleton` flag; the Go test maps `singleton` to `Singleton` and the TypeScript test maps it to `viewCardinality: "one"`.

| Case | Catalog | Expected |
|---|---|---|
| unbound application with no bindings | basic | ok |
| known optional binding filled with the right format | basic | ok |
| known optional binding left empty | basic | ok |
| required binding filled | basic | ok |
| binding with no format constraint takes any document | basic | ok |
| optional transcript context absent | open-context | ok |
| optional transcript context present | open-context | ok |
| additional binding of an allowed format | additional | ok |
| additional binding with unconstrained formats | additional | ok |
| unknown key | basic | `unknown_binding` at `views["v"].documents["typo"]` |
| missing required binding | basic | `required_binding` at `views["v"].documents` |
| wrong format | basic | `invalid_binding_format` at `views["v"].documents["conversation"]` |
| missing document | basic | `unknown_document` at `views["v"].documents["conversation"]` |
| additional binding of a refused format | additional | `invalid_binding_format` at `views["v"].documents["order"]` |
| unknown key where additional bindings are not admitted | open-context | `unknown_binding` at `views["v"].documents["extra"]` |

`bindingFixtures.test.ts` loads the catalogs through `defineAppManifest` and `createManifestCatalog`, parses each document with `fromJson(WorkbenchDocumentSchema, …)`, and asserts either an empty diagnostics list or `toMatchObject({ code, path })` on the first diagnostic. `pkg/workbench/binding_fixtures_test.go` loads the same files into a `testCatalog` of `ApplicationDescriptor`s and compares the error's code and path. The first Go run failed every valid fixture with `unsupported graphic document`, because the package's test payload validator accepts one format; the fixture test now supplies an `acceptAnyDocument` validator, since the fixtures assert binding rules, not payload contents. Per §14, the comparison is on the stable code and path, never on prose or map order.

### 6.4 The sandbox decision

§9.4 preferred a shape in which the workbench view binds one program document and the program's inputs live in the program payload, with the instruction to "document why before adopting `additionalBindings`" if runtime constraints prevented it. The kickoff documented why: program inputs are per view, not per program. One program may run in two tiles bound to two different products, so the inputs cannot live in the program document and remain in `AppView.documents`. The honest declaration is therefore:

```ts
manifest: {
  id: "script",
  ports: [documentSlotPort("program")],
  bindings: { program: { required: false, formats: ["sandbox.program"] } },
  additionalBindings: {},          // formats unconstrained: an input may be any product document
  launch: "requires-bindings",
}
```

with the Go counterpart `AdditionalBindings: &BindingRule{}`. `required: true` was deliberately not applied to `program`, because an unbound script tile is legal and shows its empty state; the fixture catalog's `sku` application demonstrates the required path instead. The sandbox's library source became `{ id: "sandbox.programs", format: "sandbox.program", update: "replace-body" }`, so a renamed program renames its stub.

### 6.5 Source ownership

§9.6 replaces format-as-owner with a source identity. `DocumentSource` gained `id`, `update: "identity-only" | "replace-body"` (default identity-only), and an optional `owns(payload)` predicate. Every stub carries its writer in the reserved body field `SOURCE_OWNER_FIELD = "$source"`: `bodyOf(source, item)` is `{ ...item.body, "$source": source.id }`. The default ownership test accepts a stub whose `$source` equals the source's id, or a stub with no `$source` at all, so a layout persisted before this step is adopted rather than orphaned.

`documentSourceMutations(doc, source)` now returns `{ mutations, collisions }` and applies the rules from the design:

```text
same id + owned stub of this format        → update (replace-body, when the body differs) or nothing
same id + a document of another format     → collision, untouched
same format + owned by another source      → untouched
missing from the source + still bound      → retained (a documentDelete would be refused)
missing from the source + unbound + owned  → deleted
```

Collisions are reported once per id through `connectDocumentSource(core, source, { onCollision })` (default `console.warn`) and never applied. Two call sites in tests that asserted the old array shape were updated. The four ownership cases in `sources.test.ts` cover the collision, the other-owner case, replace-body against identity-only, and the adoption of a legacy unowned stub.

The wire representation (a reserved field in the stub's body) was chosen over a reserved id namespace, as §14 asks, once and in one module; the design notes that the field must be acceptable to Go document validators for any source format that crosses a Go host, which is a per-format decision the report's §9 carries.

### 6.6 Hydration before strict validation

Decision F: hydrate, then validate. `readWorkbenchSnapshot(key, { apps, sources, migrate, onDiscard })` now performs the boot sequence of §13.2:

```ts
const parsed = parseWorkbenchDocument(…);                    // structural parse first
if (!parsed.ok) return discard(explain(parsed.diagnostics));
let document = parsed.document;
for (const source of options.sources ?? []) {               // sources second
  const { mutations } = documentSourceMutations(document, source);
  if (mutations.length > 0) document = applyMutations(document, mutations);
}
if (options.apps) {                                          // the catalog last
  const checked = validateWorkbenchDocument(document, { apps: options.apps });
  if (!checked.ok) return discard(explain(checked.diagnostics));
}
```

A structurally valid stored layout that binds a resource whose stub was never persisted, or that predates a source, is repaired instead of being replaced by the default layout. The chat demo hydrates its stored layout with its world sources; hyperblog does the same after S7.

### 6.7 In-repo consumer changes

The manifest change was invisible to every product that declares only `documentSlotPort`s (sandbox 224, chat 241, ecommerce 35, plotscript 32 passed without a manifest edit). The explicit changes in the repository:

- The shell's launcher rows select by `launch`.
- The sandbox's `script` manifest declares `program` with its format, `additionalBindings: {}`, and `launch: "requires-bindings"`; its library source has the id `sandbox.programs` and `replace-body`.
- pbui-chat's conversation source has the id `chat.conversations`, with the format from the `conversationDocuments` option or the default.
- The chat demo's world sources carry ids and the demo hydrates its stored layout with them.
- The agent tools use `bindingNames`.
- `slate.perf.test.ts`'s wall-clock line moved from 50 ms to 1,500 ms. The reason is recorded in the test: under a full parallel Vitest run of the package the same build measures 400–600 ms of wall clock while workers compete for cores, and about 15 ms alone; the line guards against an accidental exponential, which would be seconds, so it is set where a parallel run cannot reach it.

## 7. Phase S7: verification

### 7.1 The audit

Everything that can be run without publishing was run, in the background while the consumer agents worked.

| Check | Result |
|---|---|
| `pnpm -r typecheck` | 13 projects, clean |
| `pnpm -r test` | 10 suites, 1,508 tests: protocol 40, pbui 554, core 241, shell 116, ecommerce 35, editor 12, sandbox 224, chat 241, plotscript 32, datalab 13 |
| `pnpm -r build` | 12 builds |
| Storybook builds | workbench, chat, sandbox, ecommerce, plotscript |
| `make protocol-check` | buf lint, regeneration, no diff |
| `pnpm boundary` | built imports within the boundary; installed without React; imported and planned |
| Go | `pkg/workbench` and `pkg/workbenchapi` tests, plus lint, through the pre-commit hook; `TestBindingFixtures` green |
| Browser smoke | the shop demo: the split layout from the Phase 9 smoke restored from the previous session under the stabilized core; reset through the replacement path; reload; zero console messages |

### 7.2 Versions

| Package | Before | After |
|---|---|---|
| `@hyperslop-systems/pbui` | 0.11.0 | 0.12.0 (the `link-kernel` entry; optional React peers) |
| `@hyperslop-systems/workbench-protocol` | 0.5.0 | 0.5.0 (unchanged) |
| `@hyperslop-systems/workbench-core` | 0.1.0 | 0.2.0 |
| `@hyperslop-systems/pbui-workbench` | 0.5.0 | 0.6.0 |

The publish order is pbui 0.12.0, then protocol 0.5.0, core 0.2.0, shell 0.6.0, and the Go module afterwards. `packages/pbui-workbench/MIGRATION.md` gained a "Stabilization (workbench-core 0.2 / pbui-workbench 0.6)" section listing every surface change without aliases: `onObserverError`, `reentrant_execution`, the removed link runtime methods and the publication order, the `SyncTarget` result shape and the `incompatible` phase, owned state and `snapshot()`, shell completeness and focus scoping, binding rules and launch policy, source ids and `$source`, and the hydrating `readWorkbenchSnapshot`. Commit `306a3c6` added the launch-policy default after two products hit it (§7.4).

### 7.3 The four external products

The four consumers outside the repository were re-verified in parallel by four agents working from one brief: the MIGRATION "Stabilization" section plus a concrete list of the surface changes. All four typecheck, test, and build; each is committed on its repository's `task/add-plot-editor` branch and none is pushed, by the consumers' convention that a lockfile and an embedded bundle are committed only once the packages are on the registry.

| Consumer | Commit | Changes |
|---|---|---|
| agentlogic | `8ce82b5` | The transcript binding is declared as what it is: `bindings: { transcript: { required: false, role: "context" } }` with `launch: "unbound"`, replacing `openBindings`; the launcher pane is `launch: "hidden"` |
| hyperblog | `38f53ed` | Source ids on the corpus sources; the stored snapshot hydrated with them; `launch: "unbound"` on the slotted reader, term, and map tiles |
| turboproof | `25ef8eb` | `launch` policies on the proof tiles; the `incompatible` phase in its status chip and runtime; its sync test split into probe-time refusal (the target refuses the server document) and true 422 isolation |
| rag-ttc | `ea2bc79` | `adopt` returns the core's `ReplaceResult`; the ten bindings its Go host always required are now `required: true` in the TypeScript manifests, and the parity test compares the flag |

The rag-ttc row is the parity result the fixtures were written for: the Go host had required bindings the TypeScript manifests did not declare, and the gap surfaced the moment the flag existed. The only failure during the re-verification was environmental: turboproof's first `pnpm install --force` served a stale extraction of a same-named tarball packed by a sibling agent to the same path, fixed by clearing the store entry.

### 7.4 Two facts recorded for consumers

The launch-policy default is the one migration hazard of Track C. A manifest with a `documentSlotPort` now derives a primary binding and therefore `launch: "requires-bindings"`, which removes it from the launcher. A product whose slot is filled by policy (`followTheCrowd` in hyperblog and turboproof) or left empty on purpose must declare `launch: "unbound"` on those manifests; two of four products needed it, and MIGRATION.md records the rule.

The Go side has a sequencing constraint. Three external Go hosts pin the pbui Go module by pseudo-version, so `BindingRule.Formats` and `AdditionalBindings` reach them only after the module is published and their `go.mod` is bumped. Each agent verified against the local module through a scratch `go.work` and left the pinned build untouched. The consumer catalogs will declare `formats` once their `go.mod` moves.

### 7.5 Completion gates

§16 of the design lists seventeen gates. All seventeen hold in the repository: no exception crosses a successful `execute`/`apply`/`replace` after installation; every observer is attempted independently; reentrant mutation is deterministically rejected; receipt revisions are strictly monotonic; source reconciliation cannot precede its triggering lifecycle receipt; sync create neither replays nor drops covered entries; sync refuses incompatible adoption explicitly; the core owns ingress documents and detects external mutation; preview does not consume execution ids; core and link state are staged before publication; the built core imports without React; the core imports PBUI only through the pure entry; binding legality, requiredness, formats, and launch eligibility are separate; `openBindings` is replaced by a cross-language typed rule; source ownership and hydration are explicit; TypeScript and Go pass the shared fixtures; the first-party consumers and the shop demo smoke pass. The sixteenth gate holds for this repository's Go module and for the external hosts once they take the module; the seventeenth is met for the shop demo, and a chat-demo smoke was not run because it needs the Go chat server.

## 8. Decisions and deviations recorded in the diary

Where the design left a choice or the implementation departed from its text, the diary records the decision and the reason. They are collected here.

- Reentrancy is refused, not queued (Decision B, adopted as written). The refusal code is `reentrant_execution`; it is not reported through `onRejected`; `preview` is exempt.
- Sources try synchronously and defer only on a reentrant refusal, rather than always scheduling a microtask as §6.4 sketches. The reason is the sandbox tools' put-then-open in one tick. The diary flags the protocol as one line more than the design in exchange for synchronous stubs outside publication.
- Ids come from a lookahead pool, not the per-transaction factory of §6.7. The factory would restart deterministic sequences per transaction or require revision seeding that changes every golden; the pool keeps today's ids and satisfies §5.5.
- Publication order is receipt → link observers → core observers, per §6.2; the runtime keeps a self-publishing `commit` with its own sink for runtime-only writes.
- Ownership follows §6.5's recommended first version exactly: ingress clone, development deep freeze including the index's maps, `snapshot()`, no clone on `getState()`. Identity from caller to core is given up, and one test moved from `toBe` to `toEqual`.
- No-op detection uses protobuf `equals` on the whole document plus a session comparison, after applying the mutations.
- S1 and S2 share one commit because POST_COMMIT_ESCAPE requires both.
- React became an optional peer of pbui, which the design does not mention; it is the only way to satisfy §5.7 under npm ≥ 7 and pnpm.
- Sandbox inputs remain in `AppView.documents` under `additionalBindings: {}` (§9.4's fallback, with the reason documented); the program binding is not `required`.
- Source ownership is a reserved body field `$source`; stubs without it are adopted by the source of their format.
- Shell construction requires a presentation for every manifest and refuses a presentation without a manifest.
- The expansion refusal index override is verified by reading, not by a test.
- The slate performance guard's line is set at 1,500 ms for the reason in §6.7 above.
- Go's map-order iteration means that a view with two binding violations reports either first; the fixtures carry one violation each.

## 9. What remains

The program's eight phases are complete and the tasks file marks each done; the branch is not yet published.

- The publish: npm in the order pbui 0.12.0, protocol 0.5.0, core 0.2.0, shell 0.6.0, then the Go module. agentlogic and turboproof commit their lockfiles and embedded bundles only after the npm publish; the three external Go hosts bump `go.mod` after the module push.
- The four consumer branches (`8ce82b5`, `38f53ed`, `25ef8eb`, `ea2bc79`) are committed and not pushed.
- The consumer catalogs declare `formats` on their bindings once their `go.mod` bumps bring `BindingRule.Formats` to the host.
- A chat-demo browser smoke, which needs the Go chat server, was not run; the shop demo smoke was.
- A golden for the expansion-refusal index override, when the link goldens are next touched.
- The cost of `isNoOp` on very large documents: `equals` over the whole document per command is negligible at product sizes and is paid on every `apply` for a document with thousands of payloads.
- The shell-local and placement stores still notify with a bare loop; §11 S1 leaves them optional.
- A decision, per source format, on whether the `$source` field is acceptable to the Go document validator of each host it crosses (§9.9's inventory: `sandbox.program`, `chat.conversation`, `chat.widget`, the shop formats, and product-specific reference formats).
- The items the sibling report carried that this program did not take up: turboproof's per-keystroke `documentPut` batches, the never-removed widget stubs in pbui-chat, a generic module abstraction after a second subsystem demonstrates the collaborator's lifecycle, and property and fuzz tests.

Continued in [[PROJECT REPORT - PBUI Datalab on Workbench Core - Goldens First, One Coordinated Cutover, and a Product Layer Kept Above the Engine]]: the first consumer to depend on all three tracks at once. PBUI-DATALAB-WORKBENCH-1 (same day, same branch) deleted Datalab UI's 1,162-line Redux layout slice and made the core the only owner of its workspaces, views, placements, and trees, keeping stages, the analytical world, the launcher, bundles, and the work-stage remote policy in a product layer above the engine; its identity-only document source, its strict `unknown_document` refusals in tests, and its hydrate-before-validate load order are this program's §6 and §9.7 in use.

## 10. How to read the code

The shortest path through the stabilized code, in the order the design's §15 recommends for review.

1. `packages/workbench-core/src/stabilization.probes.test.ts`: the seven cases, each now asserting the required behaviour; then the ticket's `scripts/04-implementation-review-probes.test.ts` and its output for the recorded defects.
2. `publication.ts`: `attemptAll` and `reportFailures`.
3. `createWorkbenchCore.ts`: `phase` and `REENTRANT`; `install` (stage, assign, publish); `execute` and `apply` with their conditional `finally`; `owned`, `planned`, `isNoOp`, `snapshot`, `validateDocument`.
4. `links/runtime.ts` (`reduceRuntimeEffects`, `forgetViewValues`, `install`, `publish`, the self-publishing `commit`) and `links/collaborator.ts` (`stage`, `stageReplace`).
5. `sources.ts`: the ownership rules in `documentSourceMutations` and the try-then-defer `sync` in `connectDocumentSource`; then `sources.test.ts`.
6. `sync/index.ts`: `adopt`, `rebase` with `validateDocument`, `bootstrap`, `send` with `remaining`; then the last five cases of `sync/sync.test.ts`.
7. `ownership.ts` and `ids.ts` with `ownership.test.ts`.
8. `apps.ts` (`defineAppManifest`, `isDocBound`, `bindingNames`), `validation.ts` (the binding loop), `pkg/workbench/model.go` and `validate.go` (the same loop), then `contracts/workbench/v1` with `bindingFixtures.test.ts` and `binding_fixtures_test.go`.
9. `persistence/index.ts`: `readWorkbenchSnapshot` with `sources`.
10. `src/link-kernel.ts` at the root, the root `package.json` exports and `peerDependenciesMeta`, `packages/workbench-core/scripts/check-boundary.mjs`, `packageGraph.test.ts`.
11. `packages/pbui-workbench/src/launcherRows.ts` (launch policy), `createWorkbenchShell.tsx` (completeness, focus scoping), `packages/pbui-sandbox/src/createScriptApp.tsx` and `connect.ts`, `packages/pbui-chat/src/createPbuiChat.tsx` (source ids).
12. `packages/pbui-workbench/MIGRATION.md`, section "Stabilization", then one consumer commit (agentlogic `8ce82b5` is the smallest and shows the transcript binding as a context rule).

Four rules to carry while changing anything in this area, in addition to the sibling report's three. Nothing that runs after the assignment of `state` may throw through a mutation method; if a new callback is added to publication it is attempted under `attemptAll`. An integration that reacts to a publication does not call a mutation method from its listener; it schedules the mutation for after the publication. A manifest fact is a binding rule, a launch rule, or both, and a binding rule must have a Go counterpart before a document that uses it reaches a Go host. A package claim about the dependency graph is proven by the packed consumer, not by a source regex.

## 11. Conclusion

The program did what its design states in §18: stabilization, not a second rewrite. The architecture of the cutover was kept in full; what changed is the strength of three boundaries. A transaction is now computed as values, installed without notification, and published under a primitive that cannot lose an observer or let an exception cross the point of no return, and a mutation requested during publication is refused with a named code rather than nested. The core owns what it exposes, a preview is a pure prediction of the execution that follows, and sync advances only on an answer from the target. The headless claim is proven by installing the packed core into an empty project and planning a command without React present. The manifest states four facts separately, Go validates the same facts in the same order with the same codes, fifteen fixtures assert that from both sides, and a document source names itself in every stub it writes. The evidence is the same seven probes the review recorded, now green in the package, and a whole-workspace audit plus four external products that pass against the result. The publish is the step that remains.

## Related notes

- [[PROJECT REPORT - PBUI Workbench Core - A Headless Engine, a Pure Planner, and the Hard Cutover of the React Shell]]
- [[PROJ - PBUI - Presentation-Based UIs in TypeScript and React]]
- [[PROJ - PBUI Workbench Tiles - A Reusable Server-less Shell and the Chat Agent on Tiles]]
- [[PROJECT REPORT - PBUI Workbench Control Plane - Revisioned Authoring Across React, Go, and Agents]]
- [[PROJECT REPORT - PBUI Application Views - Logical Views, Linked Placements, and the Launcher Foundation]]
- [[PROJECT REPORT - PBUI Linked Tiles - Landing the Binding Algebra in the pbui Workbench]]
- [[PROJECT REPORT - PBUI Kernel - One Compiled Presentation, Named Fragments, and the Clean Cutover of Every Consumer]]
