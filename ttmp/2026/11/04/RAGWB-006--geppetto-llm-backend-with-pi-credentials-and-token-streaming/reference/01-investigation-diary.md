---
Title: Investigation diary
Ticket: RAGWB-006
Status: active
Topics:
    - rag
    - backend
    - llm
    - goja
DocType: reference
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-04T17:43:52.823422099-05:00
WhatFor: ""
WhenToUse: ""
---

# Investigation diary

## Goal

Chronological record of RAGWB-006: replacing the hand-written LunaRoute chat client with a Geppetto backend that reads Pi's agent directory (models, credentials, settings), so that `sample()` calls stream token deltas end to end.

## Context

- Design and intern guide: `design-doc/01-intern-guide-language-model-calls-in-rag-workbench-and-a-geppetto-backend-with-pi-credentials-and-token-streaming.md`.
- Reference implementation: slop-computer `pkg/llm/gepp` (`backend.go`, `sample.go`, `gepp_test.go`).
- Geppetto v0.13.13, `pkg/steps/ai/providers/pi`.

## Quick Reference

```bash
ragwb --llm pi serve examples/apps/reports          # tiers default to lunaroute/{muse-spark-open,glm-5.3-flash,glm-5.3}
ragwb --llm pi --llm-default openai-codex/gpt-5.6-luna:low run …
go run ./ttmp/2026/11/04/RAGWB-006--*/scripts/01-geppetto-pi-spike lunaroute/glm-5.3-flash off
```

## Usage Examples

See the steps below; each has the exact commands and results.

## Related

- RAGWB-004 (events, SSE), RAGWB-005 (trace details), RAGWB-001 diary steps 9–10 (LunaRoute probes, the original client).

## Step 1: investigation and spike — Geppetto's Pi provider streams deltas with Pi's credentials

The user asked how token streaming works, learned that it does not (the LunaRoute client is non-streaming and calls `onText` once per tool round), and asked to switch to Geppetto as slop-computer does, loading Pi's credentials. Mid-turn they added the request for a ticket, an intern guide uploaded to reMarkable, then the implementation with this diary.

I read slop-computer's backend, Geppetto's Pi provider, and rag-workbench's `llm.Backend` and its four call sites, then ran a spike against the real provider. Streaming works: `lunaroute/glm-5.3-flash` delivered 43 text deltas, the first after 402 ms of a 1.76 s answer. Adding Geppetto v0.13.13 upgrades glazed from v1.3.5 to v1.4.3, which renames the verbs' output flags; nothing else broke.

### Prompt Context
**User prompt (verbatim):** "ok, use geppetto like in ~/code/wesen/slop-computer or so, loading pi credentials, that will make it easier"
**User prompt (verbatim, mid-turn):** "Create a new docmgr ticket and Create  a detailed analysis / design / implementation guide that is for a new intern, explaining all the parts of the system needed to understand what it is, with prose paragraphs and bullet point sand pseudocode and diagrams and api references and file references. It should be very clear and technical. Store in the ticket and the nupload to remarkable.


Then implement, commit at appropriate intervals and keep a detailed diary as you work (using the diary format from the skill) (diary format: MANDATORY)"
**Assistant interpretation:** New ticket RAGWB-006; an intern guide covering how language-model calls flow through rag-workbench and the Geppetto/Pi design; upload it; implement a Geppetto backend (`--llm pi`) with streaming; diary and commits.
**Inferred user intent:** Real token streaming in answers and tool loops, and one place (Pi's agent directory) for models and credentials, so any Pi-authenticated model can be used.
**Commit (code):** pending — the dependency upgrade and test fix are committed with the guide.

### What I did
- Read slop-computer `pkg/llm/gepp/backend.go` (tier selection from Pi settings, `lookup` of `provider/id[:level]`), `sample.go` (rounds, tool registry, sink of Geppetto events → text deltas, budget, error classification) and `gepp_test.go` (a scripted OpenAI streaming server and a temporary agent directory).
- Read Geppetto `pkg/steps/ai/providers/pi` (`LoadAgentDir`, `Resolve`, `Available`, `thinkingConfig`) and `engine.MergeInferenceConfig` (per-field merge: a per-turn `MaxResponseTokens` does not erase the resolved thinking settings).
- Listed the Pi agent directory's structure without printing secrets: settings default `openai-codex/gpt-5.6-luna`, thinking `minimal`; credentials for anthropic, zai, minimax, fireworks, kimi-coding, umans, lunaroute (oauth), openai-codex (oauth); the store has the three LunaRoute models the current tiers use.
- Created branch `task/ragwb-006-geppetto-llm`; `go get github.com/go-go-golems/geppetto@v0.13.13` (it was already an indirect dependency at v0.11.7); `go mod tidy`.
- Wrote `scripts/01-geppetto-pi-spike/main.go`: load the agent dir, resolve a model, run one inference with a sink counting text and reasoning deltas.

### What worked
```
go: upgraded go 1.26.1 => 1.26.6
go: upgraded github.com/go-go-golems/geppetto v0.11.7 => v0.13.13
go: upgraded github.com/go-go-golems/glazed v1.3.5 => v1.4.3
go-go-goja stays v0.10.7; goja stays 97b96e1 (TestTokenizeUsesUnicodeClassesInGoja PASS)

agent dir loaded in 2ms; 43 available models
model lunaroute/glm-5.3-flash thinking=off:     43 text deltas, first after 402ms, total 1.756s, reasoning deltas 0,  usage in=24 out=112
model lunaroute/muse-spark-open thinking=off:   13 text deltas, first after 1.348s, total 1.496s, reasoning deltas 81, usage in=156 out=474
model lunaroute/glm-5.3-flash thinking=minimal: 44 text deltas, first after 335ms, total 1.811s, reasoning deltas 0,  usage in=24 out=120
```

### What didn't work
- `go test ./...` after the upgrade: `cmd/ragwb/cmds` failed with `verbs_test.go:36: unknown flag: --output`. glazed v1.4 renamed `--output` to `--format` (choices table, json, jsonl, csv, tsv, yaml) and `--fields` to `--output-fields`. Fixed the test (`--format json`); no other file used the old flags.
- `go run` of the spike first failed with `go: updates to go.mod needed; to update it: go mod tidy`.
- My first loop over models passed `"lunaroute/glm-5.3-flash off"` as one argument (zsh does not split a quoted variable), so `Resolve` reported `unknown Pi model`. Ran the binary with separate arguments.

### What I learned
- Geppetto's engines stream; the provider's text arrives as `events.EventTextDelta` on the sinks attached to the context.
- `muse-spark-open` reasons even at thinking level `off` (81 reasoning deltas, 474 output tokens for an 80-word answer). Reasoning counts against the per-round output limit, which matters for the `quick` tier's 2,048-token budget used by JSON steps.
- Pi's lunaroute credential is an OAuth entry in `auth.json`; Geppetto refreshes and persists it under Pi's lock. The current client reads a static `apiKey` from `models.json`.

### What was tricky to build
- Nothing yet; the spike only needed the event sink.

### What warrants a second pair of eyes
- The glazed v1.4 flag rename is user-visible for every verb (`--output json` → `--format json`).

### What should be done in the future
- Embeddings and rerank stay on the direct LunaRoute client (Pi has no embedding/rerank catalog); consider reading its key from Pi's `auth.json` too.

### Code review instructions
- `go.mod`/`go.sum`, `cmd/ragwb/cmds/verbs_test.go`, and the spike script; run `go test ./... -count=1`.
