---
Title: Fresh reading notes before independent code inspection
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
LastUpdated: 2026-11-08T21:21:55.1695922-05:00
WhatFor: ""
WhenToUse: ""
---

# Fresh reading, before inspecting the implementation

These notes were written after compaction and a fresh end-to-end reading of the original and candidates A, B and C. They precede the independent code inspection requested by the user. They are reading judgments and questions, not verified implementation findings. The drafts remain unchanged.

## What the design is about

The engine currently composes build outputs, persistent readers, asynchronous providers, graph edits and HTTP responses. The proposed simplification makes each boundary explicit: datasets define collection meaning and provenance; query snapshots fix the generation being read; a registry owns open resources; store metadata records durable ownership; operations separate outcome from cleanup; shared builds track interested callers; graph transformations share reference enumeration. Ordinary callers retain a simple search API. New contracts replace existing paths, with no compatibility adapters.

This is a semantic refactor after 010's source extraction. It is not a plan to replace the retrieval algorithms or to make TypeScript prove ownership. All four documents distinguish design from implemented/tested behavior, although the original's opening source map predates its own TypeScript appendix.

## Whole-document reading judgments

| Part | Original | A | B | C |
|---|---|---|---|---|
| Opening and orientation | Good corpus/plan explanation; stale monolithic-source claim | Best immediate concrete query and four-object orientation | Clear purpose; status/commit history interrupts the first page | Concise overlap problem; many target terms introduced together |
| Evidence/navigation | Strong table of historical symbols; current map deferred | Most useful historical/current reference tables | Current map useful; historical symbols become one dense paragraph | Same dense historical paragraph; current map clear |
| Collections/provenance | Precise contract, less help discovering why it is needed | Good cold/warm and competing-text examples | Best operation-by-operation explanation and explicit caller responsibility | Clear, economical API migration and version example |
| Snapshots/results/resources | Already a useful A/B sequence | Strong separate resource chapter and sharing diagram | Clear packing explanation; retirement wording needs care | Strongest numbered-in-prose resource-user sequence |
| Storage/publication | Technically detailed but compressed | Build 33 explains history failure; metadata paragraph still compressed | Same improvement and same metadata gap | Adds old-but-still-used distinction; same metadata gap |
| Outcomes/shared jobs | Good requirements; little provider-error chronology | Error/cleanup/callback chronology is clear | Particularly clear separation of classification and later delivery | Clear and economical; promise versus lifetime distinction useful |
| Transport | Good constraints without much physical failure explanation | Explains blocked write versus queue | Best socket-buffer explanation and outcome versus delivery distinction | Best concrete byte-limit example and request-lifetime diagram |
| Graphs/export | Useful canonical representation; dense policy description | Concrete rename failure and separated export/callback subsections | Strongest classifier-removal example and author restriction | Clear transformation-specific policies; pinned-output terminology questionable |
| Modularity/migration | Strong removal list; pre-extraction instructions need updating | Thorough current tooling and separately headed phases | Good prose explaining what is shared versus specialized | Compact table and coherent cutovers; less narrative navigation |
| Tests/risks/end | Strong test matrix and genuine risks | Most detailed implementation reference; repeated invariants lengthen it | Balanced acceptance/risks; final compressed rule paragraph less teachable | Compact checklist after teaching; precise limits without much repetition |

B is my provisional preferred base for connected explanatory prose. A is more useful as a detailed implementation reference. C is the most economical complete version. This is not yet a correctness ranking.

## Successful passages and remaining writing problems

A's query example identifies the text/citation that could change, rather than saying only “generation consistency.” Its resource diagram shows why releasing A does not close I while B uses it. Its async section also explains a failure not supplied as one of the conversation's main examples. However, repeated review invariants, decision records and the final twelve questions add repetition. The store chapter introduces descriptors, manifests, roots and pointers in one paragraph without showing their contents. Its async pseudocode still looks synchronous despite comments saying otherwise.

B explains each dataset operation before introducing the interface, and distinguishes the application's choice to materialize from the engine's choice of backend. Its export example explains why removing a model classifier requires deliberately changing retrieval behavior. Its transport section distinguishes a settled internal outcome from successful client receipt. However, the historical anchor paragraph is difficult to scan, and “Retiring A releases A's use of I” initially sounds immediate; the next sentence supplies the missing lease condition. It also retains the metadata cluster and synchronous-looking operation sketch. “Use go run ./... with the actual command package” should become a real command or an unambiguous placeholder.

C's shared-resource trace names each remaining user. Its sentence “A queue of ten events can still contain ten enormous strings” makes byte limits concrete without an analogy. Its review checklist comes after the explanations. However, the opening lists several abstractions before developing them, the historical anchors are dense, and the implementation phases have less explanation between list items. Its graph text treats pinned outputs as node dependencies without first explaining whether output pins belong to nodes or the plan.

All three add useful explanation beyond the supplied conversation examples: deferred cleanup versus outcome, blocked writes versus bounded queues, and deliberate classifier removal. All three still need a small descriptor/manifest/current-pointer example to teach storage authority. More words alone did not solve that problem.

## Questions for independent code inspection

1. Is the public builder example actually executable as labelled, especially `.retrieve('text', {k:30})`? Does each claimed source module exist with the stated responsibilities?
2. Exactly how do arrays and views choose duplicate IDs? Which callbacks receive views, and what does the bake key include?
3. When are handles retired/closed, which delta paths register closers, and does post-query packing actually re-read current artifacts?
4. Does GC discover only lineage versions? What files already constitute inventory, and what exactly does a dotted root skip?
5. Which callback reads a context after deferred cancel? Can outcome acceptance and cancellation be serialized as sketched? What does runtime disposal do?
6. Which code binds a build to its initiating request? What joins and event routing already exist?
7. Are HTTP writes/flushes unbounded, and how is admission released? Is the proposed bounded join compatible with a worker that ignores cancellation?
8. What reference kinds actually exist? Are plan outputs, signal references and decision references covered by a node visitor alone?
9. What does lexical export remove and what data does it bundle? Is every proposed unsupported capability actually unsupported?
10. Which restrictions are established design choices, which are unverified assumptions, and which need a concrete policy before implementation?

The follow-up comparison must separate confirmed source behavior, plausible defects needing regression reproduction, proposed design choices and actual documentation errors. Fidelity to the original is not independent correctness evidence.
