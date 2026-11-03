---
title: "PBUI Workbench Identity: Local Revisions, Opaque Server Tokens, and Idempotent Sync Operations"
aliases:
  - PBUI-IDENTITY-REVISION-1 report
  - Workbench revision identity
  - Workbench operation idempotency
  - LocalRevision ServerRevision OperationId
  - syncRequestOperationId
  - pbui-workbench-sync-v1
tags:
  - project-report
  - pbui
  - workbench
  - typescript
  - architecture
  - sync
  - identity
  - idempotency
  - optimistic-concurrency
  - sha256
  - testing
status: complete
type: project-report
created: 2026-09-03
updated: 2026-09-03
repo: /home/manuel/workspaces/2026-09-01/add-plot-editor/pbui
branch: task/consolidate-pbui-kernel
source_ticket: PBUI-IDENTITY-REVISION-1
source_ticket_path: /home/manuel/workspaces/2026-09-01/add-plot-editor/pbui/ttmp/2026/09/03/PBUI-IDENTITY-REVISION-1--formalize-revision-and-operation-identity-semantics
source_design_doc: design-doc/01-intern-guide-to-revision-and-operation-identity-semantics.md
related_vault_notes:
  - "[[PROJECT REPORT - PBUI Workbench Core - A Headless Engine, a Pure Planner, and the Hard Cutover of the React Shell]]"
  - "[[PROJECT REPORT - PBUI Workbench Stabilization - Safe Publication, a Proven Headless Boundary, and Binding Semantics Shared with Go]]"
  - "[[PROJ - PBUI - Presentation-Based UIs in TypeScript and React]]"
  - "[[PROJ - PBUI Workbench Tiles - A Reusable Server-less Shell and the Chat Agent on Tiles]]"
---

# PBUI Workbench Identity: Local Revisions, Opaque Server Tokens, and Idempotent Sync Operations

The PBUI Workbench originally used revision-like numbers and request-like strings in several places without a precise account of what each value identified. That ambiguity was not merely terminological. A process-local state generation, a server-issued optimistic-concurrency token, and an idempotency key answer different questions, have different authorities, and obey different transition rules. Treating them as variants of one broad `Revision` concept made incorrect substitutions easy. Generating transport request identifiers from a small non-cryptographic hash further allowed separately intended operations to collapse onto the same identity.

Ticket `PBUI-IDENTITY-REVISION-1` replaced that ambiguity with three explicit types and an executable set of laws. `LocalRevision` identifies the installed generation of one in-memory Workbench core. `ServerRevision` carries an opaque token issued by persistence. `OperationId` identifies an idempotent operation across delivery retries. The sync layer now gives every enqueued local batch a UUID and derives each concrete send identity from the server revision, ordered batch identities, and exact ordered mutation payloads using UTF-8 length framing and SHA-256.

This report develops the resulting model from first principles, traces it through the implementation, explains retry, conflict, and invalid-batch behavior, and records the proof supplied by tests and repository-wide validation. It deliberately does not generalize the three types into a repository-wide identity framework. Their semantics belong to the Workbench state and synchronization boundary.

> [!summary]
> - `LocalRevision` is a non-negative safe integer owned by one Workbench core instance. It advances once for every successful local installation and is never sent as server authority.
> - `ServerRevision` is a non-empty opaque string owned by the persistence service. The client compares and transports it but does not parse, increment, or reinterpret it.
> - `OperationId` identifies an idempotent operation. One UUID is minted when a local batch enters the outbox; a concrete send ID is derived from the current server revision, ordered batch UUIDs, and canonical protobuf JSON mutations.
> - Exact transport retries produce the same send ID. A changed revision, payload, order, batch occurrence, 409 rebase, or 422 isolation send produces a different ID.
> - The old broad `Revision` alias, sync `requestId` vocabulary, `tx-N` identities, FNV constants, and 32-bit hash loop were deleted without compatibility aliases.
> - The final audit passed 250 Workbench core tests, 860 root PBUI tests, 1,565 child-package tests across ten suites, all applicable package typechecks and builds, packed consumer checks, two Storybook builds, frozen installation, repository Go checks, and hard-cutover searches.

## 1. The problem was semantic, not syntactic

An identity-bearing value is meaningful only when its authority and equality rules are known. Before this work, the Workbench had values called revisions, transaction IDs, and request IDs, but their names did not consistently expose four essential facts:

1. **What entity does the value identify?**
2. **Who is allowed to issue it?**
3. **When must it remain stable?**
4. **Which event requires a fresh value?**

A local core revision answers whether a command was planned against the currently installed state. A server revision answers whether a mutation request was based on the persistence service's current state. An operation ID lets a receiver recognize retransmission of an operation it may already have applied. None can substitute for another.

The previous implementation obscured these boundaries in two ways. First, a broad revision type allowed local and remote revision values to appear structurally interchangeable. Second, sync request IDs were derived by a 32-bit FNV-style hash from content that was insufficient to distinguish all intended operation occurrences. Two independently intended batches with identical mutations could therefore receive the same identity. That violates idempotency semantics: identical content is not necessarily the same operation.

The hard cutover established separate types, constructors, transition laws, and test oracles. Alpha status allowed removal rather than deprecation. No aliases preserve the old vocabulary.

## 2. The three-domain identity model

The complete model is compact:

```text
Workbench core process                    Persistence service
──────────────────────                    ───────────────────
LocalRevision                             ServerRevision
  installed state generation               concurrency token
  numeric and monotonic                     opaque string
  issued by the core                        issued by the server
  process-local                             transported across requests
           │                                      │
           └──────── sync outbox ─────────────────┘
                          │
                          ▼
                    OperationId
                    idempotency identity
                    UUID per local batch
                    digest per concrete send
```

These identities occupy different domains even when JavaScript ultimately serializes two of them as strings. The distinction is preserved at compile time with brands and at runtime with constructor validation.

### 2.1 `LocalRevision`

`LocalRevision` identifies one installed state generation within one core lifetime. It is:

- a non-negative safe integer;
- minted and advanced only by the local core;
- monotonic within that core instance;
- incremented once per successful installation;
- unsuitable as a persistence version or idempotency key.

Its equality question is: **Was this operation prepared against the state generation that is still installed?**

### 2.2 `ServerRevision`

`ServerRevision` is the persistence service's optimistic-concurrency token. It is:

- a non-empty string;
- accepted at a decode boundary;
- carried back to the server on mutation;
- replaced only by a newer server response;
- never parsed, incremented, normalized, or compared numerically.

Its equality question is: **Does the server still recognize this token as the current base for mutation?**

Opacity is essential. A server may issue decimal text today and an ETag, UUID, signed token, or compound cursor later. Client arithmetic would silently couple the Workbench to one representation.

### 2.3 `OperationId`

`OperationId` identifies one logical operation through possible retransmission. It is:

- a non-empty string;
- stable when the same request is retried after uncertain delivery;
- fresh for a separately intended occurrence, even when its payload is identical;
- sensitive to the concrete send's base revision, ordered batches, and ordered mutations;
- distinct from correlation IDs used only for tracing or UI acceptance.

Its equality question is: **Should the receiver treat this delivery as another attempt of an operation it may already have processed?**

## 3. Branded primitives make invalid substitutions visible

The implementation lives in `packages/workbench-core/src/identity.ts`:

```ts
declare const localRevisionBrand: unique symbol;
declare const serverRevisionBrand: unique symbol;
declare const operationIdBrand: unique symbol;

export type LocalRevision = number & {
  readonly [localRevisionBrand]: "LocalRevision";
};

export type ServerRevision = string & {
  readonly [serverRevisionBrand]: "ServerRevision";
};

export type OperationId = string & {
  readonly [operationIdBrand]: "OperationId";
};
```

The brands are erased at runtime. They do not alter wire formats and do not create wrapper allocations. Their purpose is static separation. A plain number cannot be passed where a `LocalRevision` is required without using the constructor. A `ServerRevision` cannot be passed as an `OperationId` merely because both serialize as strings.

The constructors establish the runtime boundary:

```ts
export function localRevision(value: number): LocalRevision {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error("local revision must be a non-negative safe integer");
  }
  return value as LocalRevision;
}

export function serverRevision(value: string): ServerRevision {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error("server revision must be a non-empty string");
  }
  return value as ServerRevision;
}

export function operationId(value: string): OperationId {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error("operation id must be a non-empty string");
  }
  return value as OperationId;
}
```

Runtime `typeof` checks matter because TypeScript annotations do not protect JSON, JavaScript callers, test doubles, or data cast through `unknown`. The final implementation rejects non-string values as well as empty strings at server and operation identity ingress.

`nextLocalRevision` keeps arithmetic in the identity module:

```ts
export function nextLocalRevision(value: LocalRevision): LocalRevision {
  return localRevision(value + 1);
}
```

This prevents scattered casts and ensures safe-integer overflow is detected by the same invariant.

## 4. Local revision is an installed-state generation

`createWorkbenchCore.ts` now exposes `LocalRevision` throughout core state, receipts, command descriptions, and preconditions. The revision is not a count of attempted commands. It advances at the point where a successful transition is installed.

That placement establishes the following law:

```text
failed plan                    -> revision unchanged
failed validation              -> revision unchanged
refused reentrant execution    -> revision unchanged
successful no-op               -> revision unchanged
successful installed mutation  -> exactly +1
accepted document replacement  -> exactly +1
```

The distinction between attempt and installation is required for stale-plan detection. If failed attempts advanced the revision, a plan could become stale without any state change. If a successful installation failed to advance it, a plan prepared against the previous document could be committed after its assumptions no longer held.

A local revision is also scoped to one core lifetime. Persisting `17` and restoring it into another process does not make the restored core causally continuous with the previous one. The value remains useful for local preconditions, diagnostics, and observer receipts. It does not become a distributed version vector.

## 5. Server revision is an opaque concurrency token

The sync contract now makes remote authority explicit:

```ts
export interface SyncResult {
  document: WorkbenchDocument;
  revision: ServerRevision;
}

export interface SyncClient {
  get(): Promise<SyncResult | null>;
  create(document: WorkbenchDocument): Promise<SyncResult>;
  mutate(
    revision: ServerRevision,
    mutations: Mutation[],
    operationId: OperationId,
  ): Promise<SyncResult>;
  stream?(onChange: (revision?: ServerRevision) => void): () => void;
}
```

The client receives the token from `get`, `create`, or `mutate`; stores it only after a server document is accepted locally; and supplies it unchanged to the next mutation request. A 409 response means that token is stale. The client does not guess the next value. It fetches the authoritative document and token, rebases pending local batches, and constructs a new send attempt against the new token.

This separation corrected a common source of accidental coupling. `LocalRevision` may be `4` while `ServerRevision` is `"etag:83c1"`. Neither is behind or ahead of the other in a meaningful numeric sense. They describe different state machines.

## 6. Two levels of operation identity

The Workbench sync design uses operation identity at two related levels.

### 6.1 Batch identity

When `enqueue` accepts a committed mutation batch, it mints one UUID:

```ts
outbox = [
  ...outbox,
  {
    id: nextOperationId(),
    mutations: [...mutations],
    destructive: isDestructive(mutations),
  },
];
```

The corresponding `OutboxEntry` preserves that value for the entry's lifetime:

```ts
export interface OutboxEntry {
  readonly id: OperationId;
  readonly mutations: readonly Mutation[];
  readonly destructive: boolean;
}
```

This UUID distinguishes **occurrences**. If a user performs the same logical edit twice, the mutation payloads may be byte-for-byte identical, but the outbox entries receive different UUIDs. The receiver must not collapse the second intended occurrence into a retry of the first.

The UUID factory is injectable through `SyncOptions.operationIds`. Production uses `crypto.randomUUID()`. Tests and deterministic replay can provide a controlled sequence.

### 6.2 Concrete send identity

One HTTP mutation request may contain one or more whole batches. Its operation ID must identify the exact request being sent: the base server revision, batch order, batch occurrences, and mutation sequence. `syncRequestOperationId(...)` derives this identity from all of those fields.

This separation satisfies both requirements:

- batch UUIDs represent independently intended local occurrences;
- send digests represent exact transport operations assembled from those occurrences.

A batch keeps its UUID after a transient network failure. The request is reconstructed from unchanged content and obtains the same send digest. After a 409, the same batch UUID remains in the outbox, but the fetched server revision changes; therefore the new request digest changes. During 422 isolation, each individual batch receives a digest for its one-batch send, distinct from the rejected aggregate request.

## 7. The operation-ID preimage

The operation-ID algorithm is implemented in `packages/workbench-core/src/sync/index.ts`. Its logical preimage is:

```text
version
server revision
batch count
for each batch, in order:
    batch operation UUID
    mutation count
    for each mutation, in order:
        canonical protobuf JSON
```

Every field is framed before concatenation:

```ts
const frame = (value: string): string =>
  `${new TextEncoder().encode(value).byteLength}:${value}`;
```

The current namespace is `pbui-workbench-sync-v1`. The function constructs the framed sequence, hashes it with Web Crypto SHA-256, and prefixes the hexadecimal digest:

```ts
export async function syncRequestOperationId(
  revision: ServerRevision,
  batches: readonly OutboxEntry[],
): Promise<OperationId> {
  const framed = [
    frame("pbui-workbench-sync-v1"),
    frame(revision),
    frame(String(batches.length)),
  ];

  for (const batch of batches) {
    framed.push(frame(batch.id), frame(String(batch.mutations.length)));
    for (const mutation of batch.mutations) {
      framed.push(frame(toJsonString(MutationSchema, mutation)));
    }
  }

  const bytes = new TextEncoder().encode(framed.join(""));
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  const hex = [...new Uint8Array(digest)]
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");

  return operationId(`wb-sha256-${hex}`);
}
```

The output grammar is stable and inspectable:

```text
wb-sha256-<64 lowercase hexadecimal digits>
```

SHA-256 provides collision resistance appropriate for an idempotency namespace. The design does not use the digest for authentication. A server that needs authenticity must apply its own authenticated transport and authorization controls.

## 8. Why byte-length framing is necessary

Concatenating variable-length strings with a separator does not define an unambiguous encoding unless escaping and decoding rules are complete. Values can contain the separator. Unicode further separates JavaScript string length from encoded byte length.

The framing rule encodes each value as:

```text
<UTF-8 byte count>:<value>
```

For example:

```text
3:abc
6:μ-test
```

The second length must be computed from UTF-8 bytes, not JavaScript UTF-16 code units. `TextEncoder` makes that rule explicit and portable. The tests pin a Unicode-containing fixture to the digest:

```text
wb-sha256-d2ce51d8d36a730e10bad3e1cba21763edf2b169446d15dfe9333a30d15a24a2
```

That fixed vector detects changes in field order, framing, canonical JSON, encoding, digest algorithm, output prefix, or hexadecimal formatting. It is a compatibility test, not merely an implementation test.

If the algorithm ever changes intentionally, the namespace must change from `pbui-workbench-sync-v1`. Reusing the namespace with new semantics would allow old and new clients to assign different meanings to the same idempotency domain.

## 9. Canonical mutation serialization

The digest includes each mutation using:

```ts
toJsonString(MutationSchema, mutation)
```

This is the protobuf library's schema-aware canonical JSON projection for the mutation value. The sync layer hashes the semantic payload representation it sends through its protocol boundary rather than an arbitrary JavaScript object traversal.

Ordering remains part of the identity. The following are different operations:

```text
[open view, move tile]
[move tile, open view]
```

Even if both sequences happen to converge to the same final document in one initial state, they are not interchangeable mutation programs. The request identity therefore includes:

- batch order;
- each batch's mutation count;
- mutation order within each batch;
- each mutation's serialized content.

This fidelity matters when server validation, failure reporting, or atomic semantics depend on sequence.

## 10. Exact retry semantics

A transport failure can leave the client unable to know whether the server applied a request. Retrying with a fresh ID risks applying the operation twice. Reusing an ID for unrelated work risks dropping intended work. The new contract resolves that ambiguity by deriving IDs from stable logical inputs.

Suppose the client sends:

```text
revision = "r41"
batches = [batch UUID A, batch UUID B]
payloads = [mutations of A, mutations of B]
```

A timeout puts the same in-flight entries back in front of the outbox. No field in the digest preimage changes. The next request therefore has the same `OperationId`. A server-side idempotency store can return the prior result instead of applying the mutations twice.

The law is:

```text
same namespace
+ same server revision
+ same ordered batch UUIDs
+ same ordered mutations
= same operation ID
```

This is stronger than “same payload gives same ID.” Batch UUIDs intentionally prevent that latter rule.

## 11. Separately intended identical work

Consider two user actions that produce identical mutation arrays. They may occur seconds apart or arise from replaying a command after an earlier operation was fully acknowledged. They are separately intended operations.

At enqueue time:

```text
first occurrence  -> batch UUID A
second occurrence -> batch UUID B
```

Even if server revision and mutation payload are otherwise equal, the preimages differ because `A != B`. Their concrete send IDs differ. The idempotency layer therefore suppresses duplicate delivery of an occurrence without suppressing a distinct occurrence that happens to have the same content.

This is why a content hash alone is insufficient. Content equality and operation identity are different relations.

## 12. Conflict rebasing changes the send identity

A 409 means the server rejected the request's concurrency base. The sync loop:

1. fetches the current server document and `ServerRevision`;
2. puts refused batches back before the queued batches;
3. rebases batches in order against the fetched document;
4. drops batches that no longer apply or conflict under the established policy;
5. sends surviving batches against the fetched revision.

The surviving batch UUIDs remain stable because they still represent the same local occurrences. The send identity changes because the server revision in the preimage changed. If rebase also changes which batches survive, the batch sequence changes as well.

Formally:

```text
send(revision R1, batches A,B) -> 409
fetch -> revision R2
send(revision R2, surviving batches A,B) -> new OperationId
```

The second request is not a blind delivery retry. It is a new optimistic-concurrency attempt built against new authoritative state.

## 13. Invalid aggregate isolation creates distinct sends

A 422 means the server considers a request invalid. With `onInvalid: "isolate"`, an aggregate request containing several batches is retried one batch at a time so valid batches can land while the invalid batch is dropped whole.

Given aggregate `[A, B, C]`, the sequence may be:

```text
send [A,B,C] -> 422
send [A]     -> success
send [B]     -> 422, drop B
send [C]     -> success
```

Each line has a distinct preimage and therefore a distinct `OperationId`. The one-batch requests must not reuse the aggregate request's identity because they ask the server to apply different mutation sets. The batch UUID itself remains stable within each isolated send.

The implementation also preserves visual continuity by keeping not-yet-sent entries overlaid while each result is adopted. Identity changes do not alter the existing batch-preserving sync invariant: a locally atomic transition remains whole through enqueue, retry, rebase, isolation, or drop.

## 14. Identity across the sync state machine

The identity model can be summarized alongside sync phases:

```text
local command commits
        │
        │ mint batch UUID exactly once
        ▼
     outbox entry
        │
        │ bootstrap obtains opaque ServerRevision
        ▼
   concrete send preimage
        │
        │ SHA-256 -> OperationId
        ▼
 client.mutate(revision, mutations, operationId)
        │
        ├── success -> adopt returned document and ServerRevision
        │
        ├── transport failure -> same entries + same revision
        │                       -> same OperationId on retry
        │
        ├── 409 -> fetch new ServerRevision, rebase
        │         -> new OperationId
        │
        └── 422 isolate -> changed batch grouping
                          -> distinct OperationId per send
```

The local revision runs alongside this state machine but does not enter the send digest. Server synchronization can replace the installed document and thereby advance local installed-state generation; nevertheless, the local generation remains a process concern rather than remote concurrency authority.

## 15. The hard cutover

The migration deliberately removed old concepts instead of preserving aliases. The cutover included:

- deleting the broad Workbench sync `Revision` alias;
- changing state and receipt APIs to `LocalRevision`;
- changing persistence callbacks and status to `ServerRevision`;
- changing `requestId` parameters and vocabulary to `operationId`;
- replacing local `tx-N` identifiers with UUID-backed `OperationId`s;
- deleting FNV constants and the 32-bit hash loop;
- exporting the branded types and constructors through the public package surface;
- updating README and migration documentation;
- updating public-surface snapshots and fixtures;
- searching production and test code for legacy terms.

No deprecated alias maps `Revision` to one of the new types. No compatibility overload accepts `requestId`. This is important because a compatibility layer would retain the semantic ambiguity the ticket exists to remove.

## 16. Public API consequences

The principal consumer-visible changes are explicit:

```ts
import {
  localRevision,
  serverRevision,
  operationId,
  newOperationId,
  type LocalRevision,
  type ServerRevision,
  type OperationId,
} from "@hyperslop-systems/workbench-core";
```

Sync clients now implement:

```ts
mutate(
  revision: ServerRevision,
  mutations: Mutation[],
  operationId: OperationId,
): Promise<SyncResult>
```

A JSON transport adapter should construct server values at decode time:

```ts
const result: SyncResult = {
  document: decodeWorkbenchDocument(body.document),
  revision: serverRevision(body.revision),
};
```

A server handler should treat the operation ID as a key in the scope appropriate to its persistence model, generally including tenant and resource identity outside the Workbench-provided value. The Workbench ID describes the operation; it does not by itself authorize access or globally namespace every server row.

## 17. Boundaries that intentionally did not change

The audit distinguished Workbench synchronization identity from unrelated repository identities. The following were not renamed or branded under this ticket:

- Datalab analysis request IDs;
- Datalab whole-document replacement request IDs;
- PBUI presentation acceptance and correlation IDs;
- Chat message, run, or session identities;
- PlotScript computation tickets;
- deferred identity-cell persistence and `seed-class` work.

Datalab's `useRemoteWorkbench.ts` uses a whole-document replacement protocol rather than `WorkbenchSync`. Its request ID has domain-specific semantics and does not become a Workbench mutation `OperationId` merely because both may support retry handling.

This restraint prevents a local correction from becoming an abstract identity framework with no shared lifecycle. Types should be unified when their authorities and laws are genuinely identical, not because their runtime representation is similar.

## 18. Tests as identity laws

`packages/workbench-core/src/identity.test.ts` covers constructor and primitive behavior:

- valid local revisions are accepted;
- negative, fractional, unsafe, and non-finite local values are rejected;
- local increment preserves the invariant;
- empty and non-string server revisions are rejected;
- empty and non-string operation IDs are rejected;
- UUID injection makes operation minting deterministic in tests;
- branded primitives retain their normal JSON representation.

`packages/workbench-core/src/sync/sync.test.ts` covers behavioral laws:

- a network retry sends the same operation ID;
- a second enqueued occurrence with identical mutations receives a different ID;
- payload changes alter the ID;
- server revision changes alter the ID;
- batch or mutation ordering changes alter the ID;
- a 409 rebase produces a new send ID;
- 422 isolation produces distinct per-send IDs;
- the Unicode fixed vector pins the framing and digest contract.

These tests are more valuable than checking only output shape. They state when identity equality must and must not hold.

A useful review matrix is:

| Case | Batch UUID | Server revision | Payload/grouping | Send ID |
|---|---|---|---|---|
| transient retry | same | same | same | same |
| separately intended identical batch | new | possibly same | same | different |
| payload changed | same or new | same | changed | different |
| ordering changed | same | same | reordered | different |
| 409 rebase | same surviving UUIDs | changed | possibly changed | different |
| 422 isolated child send | same child UUID | current | aggregate → child | different |

## 19. Consumer and packaging verification

The ticket did not stop at package unit tests. Identity types cross exported declarations and packed-package boundaries, so validation included consumers that install tarballs rather than resolving workspace sources.

The Datalab consumer smoke script was repaired to pack PBUI, Workbench protocol, Workbench core, the React shell, Plot, and Datalab locally. This removed two false external requirements from the check:

1. private GitHub Package Registry credentials;
2. a publication order in which every coordinated package version already exists remotely.

The smoke check now verifies the repository checkout as a coordinated release candidate. It also preserves the distinction between Datalab's whole-document transport and the core mutation sync contract.

The final recorded validation included:

- Workbench core: 32 files, 250 tests;
- root PBUI: 51 files, 860 tests;
- child packages: ten suites, 1,565 tests;
- all twelve applicable child-package typechecks and builds;
- root and Datalab packed consumer checks;
- packed Workbench boundary verification;
- root and Datalab Storybook builds;
- frozen dependency installation;
- `make logcopter-check`;
- `make test`;
- `make glazed-lint`;
- hard-cutover searches for legacy revision/request vocabulary and FNV code.

The ticket's evidence is retained in:

- `reference/03-full-validation-output.txt`;
- `reference/04-hard-cutover-audit.txt`;
- `reference/05-validation-summary.md`;
- `reference/07-completion-audit.md`.

## 20. Implementation history

The implementation followed reviewable phases on branch `task/consolidate-pbui-kernel`:

| Commit | Purpose |
|---|---|
| `73316e8` | Design and identity inventory |
| `6d14f0f` | Branded local, server, and operation identities |
| `4f98d7c` | Collision-resistant operation IDs |
| `40c0410` | Phase diary and bookkeeping |
| `82a994a` | Consumer and public guidance |
| `1f47d3e` | Executable identity laws |
| `320c758` | Self-contained Datalab packed-consumer smoke |
| `0caac3c` | Validation documentation |
| `7884426` | reMarkable delivery documentation |
| `36d67a9` | Ticket closure and completion audit |

Across the full ticket range after the dependency-DAG baseline, 27 files changed. Production package and consumer code accounted for 315 insertions and 72 deletions across 13 files; the larger total also includes the detailed design guide, diary, validation logs, audit artifacts, and ticket bookkeeping.

## 21. Review guidance

An engineer reviewing the implementation should proceed in this order:

1. Read `packages/workbench-core/src/identity.ts` and verify each constructor's authority and invariant.
2. Inspect revision use in `packages/workbench-core/src/createWorkbenchCore.ts`; confirm advancement occurs only at successful installation.
3. Read `OutboxEntry`, `SyncClient`, `syncRequestOperationId`, `enqueue`, and `send` in `packages/workbench-core/src/sync/index.ts`.
4. Trace the transport-error, 409, and 422 paths and list which preimage fields remain stable in each.
5. Read `identity.test.ts` and the operation-ID sections of `sync.test.ts` as executable specification.
6. Inspect public exports, README guidance, and `packages/pbui-workbench/MIGRATION.md` for obsolete terminology.
7. Inspect `packages/datalab-ui/scripts/consumer-smoke.mjs` and confirm it validates packed local artifacts without registry assumptions.
8. Run the hard-cutover searches retained in the ticket evidence.

The most important review question is not whether every identifier is a valid string. It is whether equality of two identifiers means exactly the same operation under every retry and conflict path.

## 22. Operational guidance for server implementers

A persistence service receiving `mutate(revision, mutations, operationId)` should define an atomic contract. One suitable sequence is:

```text
begin transaction

if operationId is already recorded for this resource:
    return the recorded result

if supplied serverRevision is not current:
    return 409

validate and atomically apply the ordered mutations
mint the next opaque serverRevision
record operationId with the resulting response
commit
return document and serverRevision
```

The operation record and state mutation must commit atomically. Recording the key before applying state can suppress work after a crash. Applying state before recording the key can duplicate work after a lost response. The exact storage mechanism is server-specific, but the invariant is not.

A 422 response should not be recorded as successful idempotent completion unless the server contract explicitly guarantees replay of the same invalid request returns the same validation result. The client already treats 422 as semantic refusal and may construct isolated child sends with different IDs.

Retention policy also matters. If operation records expire sooner than network retries are possible, old retries may be applied again. The Workbench package does not select server retention duration; deployments must align retention with transport and offline behavior.

## 23. Security and privacy properties

The SHA-256 digest is collision-resistant, but it is not a message authentication code. Anyone able to choose all preimage fields can compute the same ID. The server must still authenticate the caller, authorize the resource, validate the supplied revision, and validate mutations.

The digest hides mutation content only to the limited extent of one-way hashing. Low-entropy payloads may be guessable, and IDs may appear in logs. Applications should not place secrets in mutation fields on the assumption that hashing creates confidentiality.

The UUID source must be cryptographically strong in production. `crypto.randomUUID()` satisfies that expectation in supported environments. Injection exists for deterministic tests and controlled replay, not for replacing production randomness with counters.

## 24. Alternatives rejected

### 24.1 One universal `Revision` type

Rejected because local generation and server concurrency token have different authorities, representations, and transitions. A union or broad alias would require runtime interpretation at every use site and preserve accidental substitution.

### 24.2 Content hash as the operation identity

Rejected because separately intended identical operations have identical content. Content equality cannot distinguish occurrence identity.

### 24.3 Fresh random request ID on every HTTP attempt

Rejected because an uncertain delivery followed by retry would receive a new key, preventing server deduplication.

### 24.4 Reuse the batch UUID directly for every send

Rejected because one send may aggregate several batches, a 409 changes the server base, and 422 isolation changes grouping. The same batch can participate in several semantically different concrete requests.

### 24.5 Delimiter-separated hash input

Rejected because arbitrary values can imitate delimiters and boundaries. UTF-8 length framing gives each field a precise extent.

### 24.6 Retain FNV with more input fields

Rejected because a 32-bit non-cryptographic hash has an inappropriately small collision space for durable idempotency. Input completeness and collision resistance are separate requirements.

### 24.7 Brand every identifier in PBUI

Rejected because identities in other subsystems do not yet share Workbench lifecycle laws. Premature unification would reduce clarity rather than improve it.

## 25. Remaining work and stable boundaries

This ticket is complete. The following items are intentionally separate:

- property and conformance testing across broader Workbench behaviors;
- Sandbox capability threat modeling;
- later reconsideration of generalized effect envelopes if multiple consumers establish a shared contract;
- `seed-class` work;
- identity-cell persistence.

Future changes to sync identity must preserve the current laws or introduce a versioned namespace. In particular:

- never parse `ServerRevision` as a number;
- never mint a new batch UUID solely because delivery is retried;
- never reuse a concrete send ID after revision, grouping, order, or payload changes;
- never collapse independent identical operations by hashing payload alone;
- never change framing under `pbui-workbench-sync-v1` silently.

## 26. Final state

At commit `36d67a9de3b04fa167c649157534ec1eb9fba2b3`, the Workbench has three named identity domains with explicit ownership:

```text
LocalRevision  = which local installed generation?
ServerRevision = which authoritative remote base?
OperationId    = which idempotent logical send?
```

The type system prevents casual substitution. Constructors guard untyped ingress. The outbox preserves occurrence identity. The send digest preserves retry identity while responding correctly to changed context. Tests encode equality and inequality laws across transient retries, duplicate intent, rebases, isolation, payload changes, and ordering changes. Public documentation describes the hard cutover, and packed consumers prove the declarations work outside workspace resolution.

The result is a synchronization boundary in which retry safety is derived from explicit semantics rather than naming convention or hash coincidence. Local state generation, optimistic concurrency, and idempotent delivery can now evolve independently without losing their contracts.

## Source references

### Ticket documentation

- `ttmp/2026/09/03/PBUI-IDENTITY-REVISION-1--formalize-revision-and-operation-identity-semantics/design-doc/01-intern-guide-to-revision-and-operation-identity-semantics.md`
- `ttmp/2026/09/03/PBUI-IDENTITY-REVISION-1--formalize-revision-and-operation-identity-semantics/reference/01-investigation-diary.md`
- `ttmp/2026/09/03/PBUI-IDENTITY-REVISION-1--formalize-revision-and-operation-identity-semantics/reference/05-validation-summary.md`
- `ttmp/2026/09/03/PBUI-IDENTITY-REVISION-1--formalize-revision-and-operation-identity-semantics/reference/07-completion-audit.md`

### Implementation

- `packages/workbench-core/src/identity.ts`
- `packages/workbench-core/src/identity.test.ts`
- `packages/workbench-core/src/createWorkbenchCore.ts`
- `packages/workbench-core/src/describe.ts`
- `packages/workbench-core/src/sync/index.ts`
- `packages/workbench-core/src/sync/sync.test.ts`
- `packages/workbench-core/src/index.ts`
- `packages/workbench-core/README.md`
- `packages/pbui-workbench/README.md`
- `packages/pbui-workbench/MIGRATION.md`
- `packages/datalab-ui/scripts/consumer-smoke.mjs`
- `packages/datalab-ui/src/appkit/useRemoteWorkbench.ts`
