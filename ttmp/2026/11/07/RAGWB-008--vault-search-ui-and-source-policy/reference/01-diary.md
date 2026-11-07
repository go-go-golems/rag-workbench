---
Title: Diary
Ticket: RAGWB-008
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
LastUpdated: 2026-11-07T18:28:49.84983694-05:00
WhatFor: ""
WhenToUse: ""
---

# Diary

## Step 1: Implement vault source policy and a note-oriented search viewer

Created a ticket and intern guide, then extended the existing parc example. The source applies the same ordered `.vault-ignore` rules used by the vault publisher; the UI resolves a selected hit to its vault-relative path and constructs an Obsidian link.

### Prompt Context
**User prompt (verbatim):** "ok, do vault UI and source policy. Create a new docmgr ticket, Create  a detailed analysis / design / implementation guide that is for a new intern, explaining all the parts of the system needed to understand what it is, with prose paragraphs and bullet point sand pseudocode and diagrams and api references and file references. It should be very clear and technical. Store in the ticket and the nupload to remarkable.\n (the design is elegant and streamlined, the document can be a bit detailed)."
**Assistant interpretation:** Deliver the vault search UI and source inclusion policy, document the system for an intern and send the guide to reMarkable.
**Inferred user intent:** Build on the existing search app with an understandable, maintainable source boundary.

### What I did
- Added `examples/apps/parc/lib/config.js`, enabled `vaultIgnore` in the parc corpus and documented configuration, exclusions and operating constraints.
- Added `pkg/vaultignore`, based on the publisher's existing matcher, including its matching tests. Extended `source.dir` with opt-in ignore-file matching and added policy bytes to the source digest and digest-cache key. The manifest identity includes the option but remains stable across policy contents, so exclusion changes produce meaningful additions/removals.
- Added an optional Obsidian vault name to `kit.searchApp` and the section widget. The browser fetches document metadata, displays the original relative path and encodes the vault name and path in `obsidian://open`.
- Added source tests for negation and digest invalidation, and a served-app test proving previously indexed excluded content disappears from subsequent search and reappears when allowed.
- Wrote the intern guide with architecture, source rules, digest and refresh, UI and HTTP flows, APIs, pseudocode, operating instructions, validation and limitations.
- Started a server with a synthetic vault in tmux, using a temporary store. In Playwright, the excluded matching fixture did not appear, and selecting the permitted note displayed `Projects/Hello World.md` and `obsidian://open?vault=Fixture%20Vault&file=Projects%2FHello%20World.md`. Rechecked vault-specific empty and submit labels after correcting inherited report wording.

### Why
- Filtering at the source means the index, query results and document lookup use the same eligibility policy. Preserving publisher rule ordering avoids a separate, surprising interpretation of `.vault-ignore`.
- Document names, not slugs or titles, preserve the correct relative path when different notes have similar names.

### What worked
- `go test ./pkg/vaultignore ./pkg/natives ./pkg/runner -run 'Test(Parse|Match|SourceDir|ServeRefreshAppliesVaultIgnore)' -count=1` passed using the shared workspace and temporary Go cache.
- The served-app refresh test uses a one-hour digest TTL and verifies policy edits still invalidate the source. It covers initial exclusion, re-inclusion, and exclusion after an existing index.
- Browser smoke test covered search, facets, section selection, async document lookup, source path and URL encoding using synthetic content.

### What didn't work
- The first version of the integration test looked for the marker anywhere in the JSON result, where it also occurred in the query signal. It failed with `excluded content appeared`; changed the assertion to inspect result hits, then the test passed.

### What I learned
- The publisher's `.vault-ignore` permits a negation to re-include a descendant of an excluded directory. To preserve that behavior, matching occurs on files rather than pruning paths excluded solely by the policy file. Explicit app and default source excludes still prune and win.
- The browser shell caches retrieved chunks and document metadata. A policy change prevents new index results but cannot revoke content already delivered to a browser or delete old persisted artifact versions.

### What was tricky to build
- Policy changes must affect both the digest and the matched file set, while not changing the source lineage identity on each edit. Otherwise a cached result can retain excluded files or a rebuild can lose its comparable manifest.

### What warrants a second pair of eyes
- The copied matcher must track future changes to the publisher's rule semantics; it is currently a separate package, not a shared dependency. Review the handling of retained artifacts and already-open browser sessions before presenting exclusion as access revocation.

### What should be done in the future
- Add production authentication, refresh status and controls, recovery/retention policy, and richer Obsidian-specific rendering as separate work.

### Code review instructions
- Read the intern guide, `pkg/natives/sourcedir.go`, `pkg/vaultignore/ignore.go`, `pkg/runner/serve_watch_test.go`, and the parc app and GUI files. Follow the README to run against an explicitly authorized vault.

### Technical details
- `.vault-ignore` must be a regular file; missing is accepted. Invalid rule patterns are skipped to match the publisher. Built-in/app exclusions cannot be overridden by negation. The root still must be allowed with `--root`.

## Step 2: Validate and deliver the guide

Ran the full project suite with an isolated artifact store and local test sockets. Uploaded the completed intern guide to the ticket-specific reMarkable directory.

### Prompt Context
See Step 1 for the user's request.

### What I did
- Ran `GOCACHE=/tmp/geppetto-bound-go-cache RAGWB_STORE=/tmp/ragwb-008-tests GOPROXY=off GOFLAGS=-buildvcs=false go test ./...` with local HTTP test sockets permitted.
- Uploaded the guide with `remarquee upload bundle ... --name 'RAGWB 008 Vault Search Intern Guide' --remote-dir '/ai/2026/11/07/RAGWB-008' --toc-depth 2 --non-interactive`.
- Stopped the synthetic-vault server after browser testing.

### Why
- Validate the integration across the project and provide the requested reading artifact.

### What worked
- The full Go suite passed, including source, runner, server and matcher tests.
- Upload returned `OK: uploaded RAGWB 008 Vault Search Intern Guide.pdf -> /ai/2026/11/07/RAGWB-008`. This confirms cloud delivery, not physical device synchronization.

### What didn't work
- No new failures during this step.

### What I learned
- The final browser fixture reproduced actual note lookup and the encoded custom-scheme link without exposing real vault data.

### What was tricky to build
- None beyond the policy and cache boundaries recorded in Step 1.

### What warrants a second pair of eyes
- Production access, stale browser caches and retained index versions remain outside this scope.

### What should be done in the future
- Review the new branch and merge its prerequisite stacked PR before deployment.

### Code review instructions
- Start with the intern guide and the source-policy and served-refresh tests cited in Step 1.

### Technical details
- Upload destination is `/ai/2026/11/07/RAGWB-008`; no existing reMarkable document was overwritten.
