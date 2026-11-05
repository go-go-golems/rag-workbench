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

## Step 2: intern guide written and uploaded

This step produced the guide the user asked for before implementation: how `sample()` calls travel from a pipeline step to a provider and back to the browser, why they do not stream today, the concepts of Pi's agent directory and Geppetto, the design of `pkg/llm/gepp`, the test plan and the phases. It was uploaded to reMarkable.

### Prompt Context
**User prompt (verbatim):** see Step 1.
**Assistant interpretation:** Write the intern guide into the ticket and upload it before implementing.
**Inferred user intent:** A reviewable plan and an onboarding document.
**Commit (code):** 645b882 — "RAGWB-006: ticket, intern guide, geppetto v0.13.13 spike (glazed v1.4: verbs use --format)"

### What I did
- Wrote `design-doc/01-intern-guide-…md` (15 sections: summary, scope, orientation, callers table, the `sample` contract, the current path with file references, why there is no streaming, Pi and Geppetto concepts, spike results, design with pseudocode and two sequence diagrams, decisions D1–D6, test strategy, phases, risks, API and file references).
- Related five files to the guide with `docmgr doc relate`.
- Uploaded: `remarquee upload bundle … --name "RAGWB-006 Intern guide - Geppetto LLM backend with Pi credentials and token streaming" --remote-dir "/ai/2026/11/04/RAGWB-006" --toc-depth 2`.

### What worked
```
OK: uploaded RAGWB-006 Intern guide - Geppetto LLM backend with Pi credentials and token streaming.pdf -> /ai/2026/11/04/RAGWB-006
```

### What didn't work
- Nothing in this step.

### What I learned
- The design reduces to one new package and one new case in `cmds.LLM()`: every surface already takes its backend from there, and everything downstream of `onText` already streams.

### What was tricky to build
- Deciding tool execution order: slop-computer runs tools in parallel (they run in separate browser pages); here they all run on one JavaScript event loop, so the design runs them sequentially in call order (D4).

### What warrants a second pair of eyes
- D5 (stream first, correct leaked tool markup afterwards with a reset) changes what a client may briefly see.

### What should be done in the future
- Nothing beyond the plan.

### Code review instructions
- Read the guide's sections 10 and 11; they are the specification for the next steps.

## Step 3: the Geppetto backend, `--llm pi`, and streaming verified through the engine, SSE, the browser and the workbench

This step implements the design in three commits. `pkg/llm/gepp` runs `sample()` on Geppetto with Pi's agent directory and streams every provider delta to `onText`. `--llm pi` selects it from every command. Tests prove the deltas reach the engine's `text` events and SSE clients.

Live, the reports app's `ask` verb answered over SSE in 23 `text` events between 4.7 s (after the LLM rerank) and 6.3 s. The deltas joined exactly to the answer. In the browser, the answer grew from 25 to 1,266 characters over ten repaints. The workbench's `/_rt/sample` delivered 35 `text` events for one call.

### Prompt Context
**User prompt (verbatim):** see Step 1.
**Assistant interpretation:** Implement the guide's phases 1–3, committing per phase.
**Inferred user intent:** Token streaming in practice, with Pi's credentials.
**Commit (code):**
- `1359767` — Phase 1, `pkg/llm/gepp`;
- `e5284f2` — Phase 2, `--llm pi`;
- `cb8da58` — Phase 3, streaming tests and `gepptest`.

### What I did
- **`pkg/llm/gepp/backend.go`:**
  - `New` loads the agent dir. Tiers resolve from `provider/id[:level]`, or from a bare id that names exactly one available model; an empty tier falls back to Pi's default model if it is enabled.
  - Thinking level: the reference's level, else `off` for `quick`, else settings' default.
  - `pick` lets `Request.Model` override the tier, falling back to the tier when that model is unavailable.
  - `Describe` names the tier models.
- **`pkg/llm/gepp/sample.go`:**
  - The round loop, with the tool registry offered on all but the last round, and `llm.ForcedAnswer` before a last round.
  - `MaxResponseTokens` per round from `llm.TierBudget`.
  - A sink that turns `EventTextDelta` into `onText(all, delta)` with `\n\n` between rounds, and records usage and truncation.
  - Tools run sequentially through `exec`.
  - `StripToolMarkup` runs on the final text, followed by a correcting `onText(clean, "")` when it changed.
  - `classify` maps errors: `NoCredentialError` and 401/403 → `not_granted`, 429 → `rate_limited`, context length → `prompt_too_large`, 400/404/422 → `invalid_request`, otherwise `upstream_error`. Messages are scrubbed of key-like strings and capped at 300 characters. Cancellation returns `ctx.Err()`.
- **`pkg/llm/lunaroute.go`:** exported `TierBudget`, `ForcedAnswer` and `StripToolMarkup`, so both backends share them.
- **`cmd/ragwb/cmds/roots.go`:**
  - `--llm pi` and `--pi-agent-dir`.
  - Tier flags default to "", and `tiersOr` fills them per backend: `gepp.DefaultTiers()` for `pi` (`lunaroute/muse-spark-open`, `lunaroute/glm-5.3-flash`, `lunaroute/glm-5.3`), `llm.DefaultTiers()` for `lunaroute`.
  - `DescribeLLM` for startup lines (workbench, serve).
- **Tests:**
  - `pkg/llm/gepp/gepptest`: a scripted OpenAI streaming server and a temporary agent dir with providers `lunaroute` (big, small) and `other` (small).
  - `gepp_test.go`, 8 tests: streaming, the tool loop, the forced last round, tier lookup and ambiguity, thinking levels, failures (with a key in the error body), cancellation, withdrawn markup.
  - `pkg/ragjs/stream_test.go`: the answer step's text events equal the provider's deltas.
  - `pkg/server/verbs_test.go`: geppetto deltas 50 ms apart arrive as separate SSE events; the fake-backend test now shares the helper.

### What worked
```
go test ./pkg/llm/gepp -v: 8/8 PASS
mutation check: dropping the markup correction and replacing the tier budget with 1234
  → TestSampleStreamsDeltas, TestSampleWithdrawsLeakedMarkup FAIL; restored → PASS
go vet ./... && go test ./... -count=1: all ok (runner 84.3 s)

./dist/ragwb --no-store --llm pi verbs --dir examples/apps/reports reports ask "What happens to product flags when a product is deleted?" --format json
  → cited [E1 E4 E2], errors [], 5.3 s wall

serve --llm pi (8795), POST /api/rag/reports/verbs/ask (SSE):
  Counter({'text': 23, 'node': 14, 'hits': 5, 'start': 1, 'run': 1, 'context': 1, 'runEnd': 1, 'result': 1})
  text events: 23, first at t=4677 ms, last at t=6349 ms; joined deltas == answer: True

browser, answer-first on 8797 with --llm pi, Answer tab sampled every 100 ms (chars):
  2969ms:25 3175ms:133 3379ms:151 3561ms:220 4077ms:617 4281ms:621 5306ms:641 5407ms:980 5510ms:1266

workbench --llm pi (8794), POST /_rt/sample: Counter({'text': 35, 'call': 1, 'result': 1})
```

### What didn't work
- No test failures in this step. Editor diagnostics flagged `llm.TierBudget` and others as undefined after the export; they were stale, and `go build`/`go vet` passed.

### What I learned
- The engine, the SSE pump and the shell needed no change. The non-streaming client really was the only gap.
- The 4.7 s before the first answer text is the `rerank.llm` step (`quick` tier, JSON), which does not display partial output.

### What was tricky to build
- Tier defaults that depend on the backend. Cobra flag defaults are static, so the tier flags now default to "" and are filled per backend. Their help text names both sets of defaults.
- A shared test fixture. The scripted provider is needed by three packages (`gepp`, `ragjs`, `server`), so it moved to a non-test helper package, `gepptest`, instead of being copied.

### What warrants a second pair of eyes
- `classify`'s key scrubbing is a heuristic regex over the provider's error text. Geppetto never puts credentials in errors as far as I read, but the scrub is a second line of defense, not a guarantee.
- Thinking levels are only tested at the selection level, not on the wire (reasoning knobs per protocol are Geppetto's job).

### What should be done in the future
- Use the remaining 4.7 s: `rerank.llm` could stream its progress (it is JSON, so only as a `progress` note).
- Decide whether `--llm pi` should become the default when an agent directory exists.

### Code review instructions
- Start with `pkg/llm/gepp/sample.go` (`Sample`, `stream`, `sink`, `classify`), then `backend.go` (`lookup`, `pick`, `level`), then `cmd/ragwb/cmds/roots.go` (`LLM`, `tiersOr`).
- Validate:
  - `go test ./pkg/llm/... ./pkg/ragjs -run 'Answer|Sample' ./pkg/server -run 'VerbSSE' -count=1 -v`;
  - live: `ragwb --llm pi serve examples/apps/reports`, then `curl -N -H 'Accept: text/event-stream' -d '{"q":"…"}' localhost:8791/api/rag/reports/verbs/ask | grep -c '^event: text'`.
