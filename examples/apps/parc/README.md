# Vault search

Edit `lib/config.js` to set `VAULT` to the vault directory on the server and `VAULT_NAME` to the name registered in Obsidian on the client. Allow the directory explicitly and start the local app:

```sh
go run ./cmd/ragwb --root /path/to/vault serve --watch 30s examples/apps/parc
```

Open the address printed at startup. Search by typing, select a result to read its section and path, then choose **Open in Obsidian**. The link uses the vault-relative filename, so the client's Obsidian installation must know a vault with that name.

The app indexes Markdown files only. `lib/corpus.js` excludes tooling, attachments, temporary ticket files and `_assets`, in addition to source.dir's standard exclusions. It also applies the vault-root `.vault-ignore` using the publisher's ordered rules, including `!` re-inclusions; these cannot override the app's explicit or built-in exclusions. A missing `.vault-ignore` means no extra exclusions. The file is read again during refresh; changing it changes the source digest and updates the index. Invalid patterns are skipped, following the publisher's behavior. Inspect the ignore file and search results before relying on a policy change. A policy change does not revoke text already sent to an open browser or erase retained older index artifacts.

The server has no authentication. Keep it on loopback or behind an authenticated private proxy. The index contains vault text and must be protected like the vault. Query text is logged unless `serve --quiet` is used. `--watch` polls rather than providing instantaneous filesystem events.
