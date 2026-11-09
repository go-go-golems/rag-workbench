---
Title: Intern guide to modular runtime sources TypeScript and build tooling
Ticket: RAGWB-010
Status: active
Topics:
    - rag
    - search
DocType: design-doc
Intent: long-term
Owners: []
RelatedFiles:
    - Path: repo://Makefile
      Note: Developer commands
    - Path: repo://pkg/ragjs/assets/rag.js
      Note: Baseline monolith and future generated bundle
    - Path: repo://pkg/ragjs/module.go
      Note: Embedding and runtime publication
    - Path: repo://pkg/ragjs/module_test.go
      Note: Goja runtime integration
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-08T20:05:57.284986675-05:00
WhatFor: ""
WhenToUse: ""
---

# Modular runtime sources, TypeScript and reproducible tooling

## 1. Purpose and scope

RAGWB-010 establishes the development foundation for RAGWB-009. It separates the large runtime source into modules, adds a reproducible JavaScript build, introduces strict TypeScript where the current contracts are stable, and adds formatting, linting and executable integration checks. It does not redesign datasets, snapshots, garbage collection, cancellation or graph references. Those semantic changes belong to 009, where the new contracts can be typed as they are implemented.

The baseline is `fd53e65`, following the vault UI work and the 009 design. `pkg/ragjs/assets/rag.js` contains 2,698 lines. It includes text processing, built-in components, graph builders, compilation, editing, build execution, query execution, evaluation, workspace operations and browser export. The problem is not the line count by itself. The shared lexical scope hides dependencies and makes unrelated responsibilities appear equally available to every function.

A new intern should read this guide, then `pkg/ragjs/module.go`, then the source entry point and one path from a builder to a query. The implementation diary records commands, failures and decisions. The final section of this document will distinguish the implemented result from the initial design.

There are no compatibility layers to add. The current public behavior is retained for this mechanical foundation because combining a module move with an ownership redesign would make regressions difficult to attribute. This is a sequencing decision, not a promise to preserve these interfaces during 009.

## 2. The system and its three execution environments

The application is a Go program hosting JavaScript in Goja. A script requires `ragwb`, defines a corpus and a plan, and runs it through an engine. Go supplies services such as source files, persistent artifacts, native indexes and model calls. The same library is also delivered to browser applications and included in exported standalone HTML.

A corpus defines build work: reading files, parsing Markdown, chunking documents and constructing indexes. A plan defines query work: producing queries, retrieving candidates, combining results, grading or reranking, assembling context and generating an answer. Both compile into a graph representation. The engine evaluates build nodes and creates sessions for query execution. A workspace adds graph editing, revisions and convenience methods.

```text
Authored source modules (.js and .ts)
                |
        pinned bundle command
                |
    pkg/ragjs/assets/rag.js (generated, committed)
       /                 |                    \
Go embed + Goja     served browser       standalone HTML
require('ragwb')    globalThis.rag       embedded script
       \                 |                    /
          same runtime implementation
```

Goja does not execute TypeScript. TypeScript types disappear at build time. Source modules also do not imply that Goja must acquire an ESM loader: the generated artifact is a single script with the publication behavior the existing loader expects. It publishes `module.exports` when loaded by the private Goja wrapper and `globalThis.rag` in a browser.

The wrapper is part of the runtime boundary. `module.go` supplies a private `globalThis` argument and caches compiled programs per process. Each runtime evaluates its own library instance. Registry entries, default engine state and request scope must remain isolated between those instances. A module extraction must not move those objects into process-wide Go state or accidentally share them between evaluations.

## 3. Current code and navigation map

| Source | Responsibility | What to examine |
|---|---|---|
| `pkg/ragjs/assets/rag.js` | Current complete library | Section markers, factory closure, registry and namespace assembly |
| `pkg/ragjs/module.go` | Go embedding and runtime registration | `Source`, `load`, `ragWrapperHead`, module registration |
| `pkg/ragjs/module_test.go` | Runtime integration | `newRuntime`, `evalJSON`, corpus and plan tests |
| `pkg/ragjs/identity_test.go` | Stable identity | Native and JavaScript identity assumptions |
| `pkg/ragjs/route_test.go` | Conditional execution | Decisions and branch behavior |
| `pkg/runner/serve.go` | Long-lived served application | Runtime configuration and response packaging |
| `pkg/runner/*_parity_test.go` | Native/JavaScript agreement | Tokenization, Markdown, terms and BM25 |
| `pkg/ragjs/assets/gui.js` | GUI library | Consumer of the public rag namespace |
| `pkg/ragjs/assets/kit.js` | Application helpers | Consumer of rag and gui |
| `web/embed.go` | Browser assets | Existing application serving boundary |
| `Makefile` | Go developer commands | Add JavaScript commands without making ordinary Go builds depend on Node |

The original file's utility section includes deterministic serialization and hashing, identity helpers, object/path helpers and `RagError`. The registry section contains both the registry mechanism and built-in component registration. The specs, IR and ops sections are interdependent: builders compile graphs; compiled graphs offer editing methods; normalization consults component definitions. Extraction must account for these real dependencies rather than cutting arbitrary line ranges and injecting a giant context object everywhere.

## 4. Design principles

The foundation has five concrete rules:

1. Source modules are authoritative; generated assets are never edited by hand.
2. A module imports the dependencies it uses. A shared bag of every function would recreate the original implicit scope.
3. Runtime semantics remain the same in 010. Semantic defects identified by the 009 review remain tracked there.
4. Strict types describe implemented contracts. Proposed 009 types are documented as proposed, not exported as if usable today.
5. Build, type, lint and execution checks are separate commands with a combined CI gate.

Module splitting is successful when a reader can identify a responsibility and its dependencies without reading the whole runtime. It does not require a file per function. Closely coupled graph compilation and editing may remain together initially; stable algorithms and lifecycle-independent helpers should be easy to import and test independently.

## 5. Proposed source layout and dependency direction

Use `pkg/ragjs/src/` for runtime sources. It keeps the implementation next to the Go host and the generated assets. A root `package.json` owns tooling; do not create a second Go module or an independently published npm package merely to build this code.

```text
pkg/ragjs/src/
  core/         identity, errors, lambda and text helpers
  components/   built-in definitions and retrieval algorithms
  graph/        specs, compile/edit rules and conditions
  engine/       build execution, sessions and event scopes
  evaluation/   evaluation and sweeps
  export/       standalone HTML generation
  api/          public namespace and composition
scripts/        build and bundle verification
```

Exact file names may change after inspecting dependencies; the final inventory below will record the result. The important separation is between pure implementation, stateful orchestration and public composition.

```text
public entry / composition
    |          |             |
 graph      engine       export/evaluation
    \          |             /
       components and contracts
                 |
          core pure helpers
```

Avoid eager cycles. A source-level cycle that happens to work because functions are called later is fragile during bundling. Where a high-level convenience method needs the default engine or public API, inject that narrow factory at composition time or colocate the mutually dependent behavior. Do not introduce a generic service locator, plugin container or event bus for this extraction.

Built-in registration must finish before dynamic family methods are installed. New native components registered later through `rag.define` must still update builder families and namespace factories. Those initialization rules need a test because moving imports can change execution order without changing any function body.

## 6. TypeScript strategy

Use TypeScript modules for stable, understood boundaries. Strict checking should be enabled for these modules from the beginning. Existing JavaScript modules can coexist through `allowJs`; `checkJs` can be applied to modules when their contracts are ready. The project must describe this honestly as a typed foundation, not a fully typed engine.

The initial implementation should type a meaningful runtime boundary and pure helpers, rather than add an unused `types.ts` that only documents aspirations. Error construction, identity functions and request event scopes are candidates because they already have concrete callers and stable semantics. New modules introduced during 009 should use TypeScript by default.

```ts
// Illustrative shape: implemented types must follow the actual runtime.
interface EventDetail {
  top?: number;
  chunks?: boolean;
}
interface CancellationSignal {
  readonly aborted: boolean;
  readonly reason: unknown;
  addEventListener(type: string, listener: () => void): void;
  removeEventListener(type: string, listener: () => void): void;
}
interface RequestScope {
  readonly signal: CancellationSignal;
  emit(event: RuntimeEvent): void;
  close(): void;
  abort(reason?: unknown): void;
}
```

Go supplies a duck-typed cancellation object, not necessarily the browser's complete `AbortSignal`. Importing DOM types and asserting the host object satisfies them would obscure a real interface mismatch. Define only the operations the runtime actually requires. Similarly, values arriving from native code or JSON should be `unknown` at a validation boundary when feasible; a type annotation cannot validate external input.

Use discriminated unions for finite states when 009 introduces its new ownership protocols. Do not add branded dataset IDs to the existing global chunk lookup and claim provenance has been solved. The runtime representation, references and consumers must change together in 009.

```ts
// RAGWB-009 target sketch, not an API implemented by 010.
type Outcome<T> =
  | { kind: 'completed'; value: T }
  | { kind: 'failed'; error: Error }
  | { kind: 'cancelled'; reason: unknown };
```

Type tests should check a real boundary: valid inputs compile, invalid calls are rejected with `@ts-expect-error`, and an unused expectation fails the check. They should not merely repeat a type declaration. Runtime tests remain essential for cancellation and lifetime ordering.

## 7. Build and generated asset contract

Use a pinned bundler to resolve ESM imports and strip TypeScript. The output must be a self-contained script with no runtime imports, Node dependencies, source paths tied to the developer machine or timestamps. Do not minify: readable stack traces and source inspection matter in Goja. Preserve function names where serialization or diagnostics depend on them.

```text
pnpm install --frozen-lockfile
pnpm run build          # writes generated asset
pnpm run typecheck      # tsc --noEmit
pnpm run lint           # Biome checks authored files
pnpm run check:generated # build in memory, compare exact bytes
pnpm run test           # publication and runtime smoke tests
```

The check mode must not silently repair stale assets. It builds the expected output in memory and compares it with the committed file; a mismatch tells the contributor to regenerate. A clean Go checkout can run `go test ./...` using the committed asset without Node. JavaScript contributors and CI install the pinned development toolchain.

Bundler transformations deserve special attention because this runtime stores lambdas as source using `Function.prototype.toString`. Built-in component functions also participate in identity. Bundling can rename a local reference or insert a helper. Validate stored user lambdas and component registration explicitly. Cache identities may change after a code-generation change; do not promise byte-identical historical cache keys unless measured. No legacy cache adapter is required.

The browser export embeds the generated library into a script element, escaping closing script tags. Test the exported HTML path as well as directly loading the bundle. A passing Node CommonJS test alone does not validate browser publication or standalone export.

## 8. Linting and formatting

Biome owns formatting and selected correctness lint rules for the authored runtime and tooling. TypeScript owns type checking. The generated bundle is excluded from linting and formatting because its bytes are determined by the bundler. GUI and kit files are outside the initial extraction; broad unrelated formatting churn is unnecessary.

Start with a justified rule set that can pass without suppressing meaningful errors everywhere. Do not enable a recommended preset and then add hundreds of blanket ignores. Document intentional exceptions, such as dynamically compiled user lambdas. Formatting should make newly extracted sources readable, but do not combine automated unsafe lint fixes with this behavior-preserving move.

CI installs from the lockfile, checks formatting/linting, checks types, verifies generated bytes and executes the bundle tests. The existing Go tests are the primary evidence for Goja integration. The toolchain should expose clear `make` targets for local development as well.

## 9. Implementation sequence and commits

### Milestone A: design and evidence

Create the ticket, guide and diary. Relate primary source files. Update 009 to name 010 as its prerequisite and explain that new contracts will be implemented in TypeScript. Upload this guide to reMarkable before implementation. Commit the design checkpoint.

### Milestone B: extract and bundle

Separate foundational helpers, components, graph logic, execution, evaluation and composition. Use actual imports. Establish the generated asset build and retain the existing embedding interface. Verify initialization order and module publication before broader testing. Commit the extraction when it is coherent and reviewable.

### Milestone C: typed boundaries and quality gates

Convert stable extracted boundaries to TypeScript, add strict checks and meaningful type fixtures, configure Biome, and integrate generation verification with CI and Make. Record which files remain unchecked JavaScript and why. Commit tooling and typed boundaries together when the checks pass.

### Milestone D: integration and handoff

Run the relevant runtime/runner/web tests and browser export checks. Investigate failures at their actual boundary rather than rerunning every suite after each edit. Update this guide with implemented paths, decisions and results. Update 009 with the real module map and remaining type work. Deliver the updated result under a new reMarkable name, preserving annotations on the initial guide.

## 10. Validation matrix

| Boundary | Evidence | Failure this should reveal |
|---|---|---|
| Generation | Compare fresh output with committed bytes | Stale asset or nondeterministic build |
| CommonJS publication | Evaluate generated script with module.exports | Broken Goja-facing namespace shape |
| Browser publication | Evaluate without module/exports | Missing or leaked global namespace |
| Per-runtime isolation | Evaluate in separate contexts, register custom type | Shared registry/default-engine state |
| Dynamic registration | Define component after initialization, use builder | Initialization ordering or stale factories |
| Stored lambdas | Compile and execute closed callback | Bundler capture/renaming regression |
| Goja | Existing ragjs and runner suites | Unsupported emitted syntax or altered behavior |
| Native parity | Existing tokenizer/index/Markdown tests | Algorithm drift during extraction |
| Standalone export | Execute exported library and initialization | Incomplete bundle or export escaping issue |
| Types | Strict check and negative fixtures | Unsound caller boundary or missing type coverage |
| Lint | Biome on authored sources | Unbound names and selected correctness failures |

A VM with a minimal DOM can test exported script initialization but is not a real browser rendering test. If that is the only available browser-path evidence, label it precisely. Do not claim layout, accessibility or browser compatibility from a headless JavaScript smoke test.

## 11. How this prepares RAGWB-009

009 changes dataset access, record provenance, snapshots, resource ownership, artifact inventory, shared build ownership, transport lifetimes and graph reference traversal. The module foundation makes those edits local and type-checkable. It does not make JavaScript memory ownership linear or prove that every asynchronous interleaving is safe.

The migration order remains: implement a contract, move its consumers, delete the old mechanism, run the relevant conformance/lifecycle tests. New dataset and snapshot contracts should not be copied into several packages with similar names. Keep one authoritative declaration at the owning module and import it with `import type` where only the type is needed.

```ts
// During 009, once the implementation exists:
import type { Dataset, RecordRef } from '../data/dataset';

// Consumers should use this contract directly. Do not retain array/view forks
// behind an adapter that preserves both implementations indefinitely.
```

Update 009's code map as files move. Its historical baseline line references remain useful as investigation evidence, but implementation instructions should point to the current modules. Keep the semantic regression tasks open until their behavior is actually fixed.

## 12. External references and practical reading

- TypeScript configuration: <https://www.typescriptlang.org/tsconfig/>. Read `allowJs`, `checkJs`, `strict` and `noEmit` together; accepting JS does not mean every JS file is checked.
- Biome linting: <https://biomejs.dev/linter/>. Use it for linting alongside the TypeScript checker.
- Bundling API: <https://esbuild.github.io/api/>. Relevant options include bundle, format, target, write, banner/footer and name preservation.
- Go embedding: `pkg/ragjs/module.go` is the concrete application contract; changing output format without inspecting that wrapper is unsafe.

## 13. Implementation results

Pending at the design checkpoint. This section will be updated after extraction and validation. Proposed module names above are not claims that those files already exist.
