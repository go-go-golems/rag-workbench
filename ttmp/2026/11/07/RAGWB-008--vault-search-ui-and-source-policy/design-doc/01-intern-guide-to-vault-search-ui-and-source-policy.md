---
Title: Intern guide to vault search UI and source policy
Ticket: RAGWB-008
Status: active
Topics:
    - rag
    - search
DocType: design-doc
Intent: long-term
Owners: []
RelatedFiles:
    - Path: repo://examples/apps/parc/app.js
      Note: Vault search app
    - Path: repo://examples/apps/parc/lib/corpus.js
      Note: Corpus and source policy
    - Path: repo://pkg/natives/sourcedir.go
      Note: Root checks and source matching
    - Path: repo://pkg/ragjs/assets/gui.js
      Note: Section viewer and Obsidian link
    - Path: repo://pkg/runner/serve.go
      Note: Document lookup and refresh
    - Path: repo://pkg/vaultignore/ignore.go
      Note: Vault ignore parser
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-07T18:28:49.718734457-05:00
WhatFor: ""
WhenToUse: ""
---

# Intern guide: vault search UI and source policy

## 1. Purpose and scope

This ticket turns the existing `parc` example into a usable private vault search. A person types a question, selects a ranked Markdown section, reads its contents and opens the underlying note in Obsidian. The source policy decides which files are eligible before indexing, and therefore before they can appear in search results, document metadata or the section viewer.

The system is a search application, not an Obsidian renderer or an answer generator. A policy change prevents future index results after refresh but does not revoke text already delivered to an open browser or erase retained historical artifact versions. It searches local Markdown. Attachments, full Obsidian wiki-link rendering, authentication, background job controls and production deployment are separate work. In particular, the standalone server has no authentication; keep it on loopback until protected by a private authenticated proxy. Its index is another copy of vault data.

## 2. What exists already

The workbench uses a small JavaScript API to declare a corpus and a query plan. Go hosts the JavaScript runtime and supplies filesystem access, indexing, persistence and HTTP. `examples/apps/parc/lib/corpus.js` declares the vault corpus and lexical, semantic and hybrid plans. `examples/apps/parc/app.js` describes a GUI through `kit.searchApp`; `pkg/ragjs/assets/kit.js` builds the GUI tree and `pkg/ragjs/assets/gui.js` renders its widgets. The runnable command is `cmd/ragwb/cmds/serve.go`.

A corpus is the build-time pipeline. A plan adds the live query-time pipeline. This distinction matters: parsing all notes on each keystroke would be expensive, whereas a persisted index can be opened and queried repeatedly. The vault survey preceding this ticket found roughly 2,299 Markdown notes, with frequent frontmatter and wiki links. A local smoke test built 86,395 chunks in about 25 seconds and one query completed in 32 ms; these are single observations, not capacity guarantees.

```text
vault files -> source.dir -> parse.markdown -> chunk.sections
                                                  |\
                                                  | -> BM25 index
                                                  + -> metadata terms index
                                                           |
browser -> POST /api/rag/parc/run -> retrieve -> filters -> ranked sections
   |                                                               |
   +------------------ select -> document lookup -> Obsidian link <-+
```

## 3. Source boundaries

A server-side `source.dir` reads from an allowed root. `--root` adds a filesystem allowlist entry; it does not itself choose the root. The example's `lib/config.js` selects `VAULT`, while the flag authorizes it. `VAULT_NAME` is the name registered with Obsidian on the client, which may be a different machine. Set both explicitly; no environment variable is required for these application settings.

`source.dir` resolves symlinks on the root and checks that the resolved path is inside an allowed root. Its directory walk ignores symlink and device entries. It includes Markdown by default, skips files over 4 MiB and HTML-looking files by default, and applies built-in exclusions for `.git`, `.obsidian`, `node_modules` and `.trash`. See `pkg/natives/sourcedir.go` and `pkg/natives/manifest.go`. The parc app adds exclusions for `.pi`, `ttmp`, `scripts`, `Attachments` and nested `_assets` directories. These explicit exclusions cannot be undone by an ignore-file negation.

The vault may contain `.vault-ignore` at its root. With `vaultIgnore: true`, `source.dir` uses the publisher's rule model: blank lines and comments are ignored, rules are evaluated in order, `!` re-includes a path, `/` anchors a rule, and a trailing slash targets directories. Patterns use `path.Match` syntax, not full gitignore `**` semantics. A missing file adds no exclusions; malformed patterns are skipped to match the existing publisher. The matcher is in `pkg/vaultignore/ignore.go`, derived from `publish-vault/internal/ignore`.

```text
eligible(path) = regular file
                 AND matches include globs
                 AND NOT matches built-in or app exclusion
                 AND NOT vault_ignore.last_matching_rule(path).excludes
```

The ignore matcher is applied to files after walking directories. That is intentional: pruning a directory excluded by one rule could hide a descendant re-included by a later `!` rule. Default and app exclusions remain hard exclusions and can prune directories. The ignore file must be a regular file, so a symlink cannot redirect policy loading outside the vault.

## 4. Digests and incremental changes

The source digest normally represents source options plus sorted file paths, sizes and modification times. `serve --watch 30s` periodically invokes refresh; on a changed digest the engine uses stored manifests and delta-capable components to update the index. Source, parser, chunks, BM25 and terms each participate in the build graph. `pkg/runner/serve_watch_test.go` exercises a changed document and subsequent search.

The `.vault-ignore` bytes enter the source digest and digest-cache key. Therefore changing the policy invalidates a cached digest even if the interval has not expired. The manifest identity contains the option to use vault rules, but not the rule bytes: this allows a policy edit to be compared to the previous manifest, so files becoming excluded are removals and files becoming eligible are additions. A sufficiently large delta may trigger the existing full-build fallback.

```text
on refresh:
    policy = read vault-root .vault-ignore
    digest = hash(source configuration, policy bytes, matched file stats)
    if digest == current digest: return unchanged
    compare matched files to previous manifest
    build changed records and indexes; remove disappeared records
    publish refreshed artifacts; keep/report errors if build fails
```

This is polling, not a filesystem event subscription. File contents changed while preserving both size and modification time can evade the stat digest. Periodic content reconciliation and explicit production refresh controls are future work, not claims of this ticket.

## 5. UI and HTTP data flow

`kit.searchApp` constructs a search box, status, facets, ranked results and a section viewer. The browser shell fetches app metadata from `GET /api/rag/parc`, then sends query signals to `POST /api/rag/parc/run`. Responses contain hits and the chunk records needed to display them. The UI selects a chunk by its ID. When the chunk viewer needs its document's relative filename, `web/app-shell/shell.js` fetches `GET /api/rag/parc/doc?id=...`, caches it and rerenders.

The document ID is an internal identifier and is not a safe substitute for a filename. Document metadata carries `name`, the original vault-relative file path. The viewer uses that value and the configured Obsidian vault name to construct an encoded URL:

```text
obsidian://open?vault=<percent-encoded vault name>&file=<percent-encoded relative path>
```

Both pieces are encoded as URL query values. The application creates this link from the indexed document, not from arbitrary HTML contained in a note. The browser passes the custom scheme to the client operating system; Obsidian must be installed and the vault name must exist there. Search and reading sections still work if it is not.

Search-as-you-type is debounced by the generic GUI runtime. Facets filter candidate hits using metadata. `type`, `tags` and `status` are the configured fields; missing or inconsistent frontmatter naturally limits what filters can show. The displayed section is rendered by the existing Markdown renderer, which does not implement all Obsidian syntax or resolve embedded notes. The app does not send the vault to a model in its default lexical mode.

## 6. APIs and files to read

- `examples/apps/parc/lib/config.js`: vault root and client Obsidian vault name.
- `examples/apps/parc/lib/corpus.js`: source options, hard exclusions, chunking, indexes and query plans.
- `examples/apps/parc/app.js`: search app and viewer options.
- `pkg/natives/sourcedir.go`: root authorization, source policy, walk, digest and cache.
- `pkg/natives/manifest.go`: stable source identity and added/changed/removed files.
- `pkg/vaultignore/ignore.go`: publisher-compatible rule parsing and matching.
- `pkg/ragjs/assets/kit.js` and `pkg/ragjs/assets/gui.js`: GUI builder, selected section, document path and Obsidian link.
- `web/app-shell/shell.js` and `pkg/runner/serve.go`: HTTP-backed runtime and document lookup.
- `cmd/ragwb/cmds/serve.go`: startup, source polling, bind address and access warning.

The reusable source API is `rag.source.dir({root, include, exclude, vaultIgnore: true})`. Vault matching is opt-in for this source; other corpora keep their current semantics. The reusable viewer option is `kit.searchApp(plan, {obsidianVault: 'name'})`; without it the generic section viewer remains generic.

## 7. Running and checking the app

Configure `examples/apps/parc/lib/config.js`, then run from the repository root:

```sh
go run ./cmd/ragwb --root /path/to/vault serve --watch 30s --quiet examples/apps/parc
```

Open the printed loopback URL. Search for a distinctive term in an eligible note, select the result, inspect its relative path and open it in Obsidian. Add a small temporary fixture to an isolated test vault, change its policy, then check that excluded content disappears and re-included content remains. Do not use real private notes as test fixtures or put their text in logs.

The focused source test is `go test ./pkg/natives -run TestSourceDirVaultIgnoreAndRefresh`. It checks an excluded directory, an explicit re-inclusion and policy-change digest invalidation. The served-app refresh test verifies that changing the policy removes previously indexed chunks and can add them back. A browser smoke test on a synthetic vault verifies result selection, document lookup, the displayed relative path and an encoded Obsidian URL. See the ticket diary for the exact validation performed and its limitations.

## 8. Operational implications and next work

The server defaults to loopback and explicitly has no authentication. A private authenticated reverse proxy or a service such as Tailscale Serve can provide a controlled access boundary; never expose this raw server publicly. The artifact store contains indexed text, and query logging can expose search terms. Restrict and back up the store like the source vault, use `--quiet` where appropriate, and decide how retained older artifact versions are deleted when the policy changes.

A production follow-up should add last-successful-index and error visibility, a manual refresh trigger, tests for concurrent searches during refresh, a recovery and retention policy, and explicit behavior for Obsidian links and attachments. File notification and post-sync triggers can call the same refresh operation; periodic reconciliation is still needed to recover missed events. Search quality should be measured with real, independently labeled vault questions before adding remote embeddings or answer generation.
