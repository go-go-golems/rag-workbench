#!/usr/bin/env python3
"""Doc-id collisions and slug lengths for a vault (RAGWB-003 §4.3/§6.2).

Re-implements rag.js's slug() and parc's include/exclude rules, then reports how
many files share an id today (64-char cap), how many are true collisions (equal
uncapped slugs), and how many ids a given cap would hash-suffix.

    python3 01-doc-id-collisions.py [vault-root]
"""
import os, re, sys, collections

root = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/code/wesen/go-go-golems/go-go-parc")
EXCL = [".pi/", "ttmp/", ".trash/", "scripts/", "Attachments/", ".git/", ".obsidian/", "node_modules/"]
names = []
for dp, dn, fn in os.walk(root):
    rel = os.path.relpath(dp, root)
    for f in fn:
        if not f.lower().endswith(".md"):
            continue
        p = os.path.normpath(os.path.join(rel, f)).replace(os.sep, "/")
        if any(p.startswith(e) for e in EXCL) or "/_assets/" in "/" + p:
            continue
        names.append(p)

def slug_uncapped(s):
    s = s.lower(); s = re.sub(r"[`'\"’]", "", s); s = re.sub(r"[^a-z0-9]+", "-", s); return s.strip("-")

stem = lambda n: re.sub(r"\.md$", "", n, flags=re.I)
today = collections.Counter((slug_uncapped(stem(n))[:64] or "x") for n in names)
full = collections.Counter(slug_uncapped(stem(n)) for n in names)
print(f"files {len(names)} · distinct ids today {len(today)} · files sharing an id today {sum(c for c in today.values() if c > 1)}")
print(f"true collisions (equal uncapped slugs): {sum(c for c in full.values() if c > 1)} {[k for k, c in full.items() if c > 1][:5]}")
L = sorted(len(slug_uncapped(stem(n))) for n in names)
q = lambda p: L[int(p * (len(L) - 1))]
print(f"uncapped slug length: median {q(.5)} · p90 {q(.9)} · p99 {q(.99)} · max {L[-1]}")
for cap in (64, 96, 128, 160, 200):
    print(f"cap {cap}: {sum(1 for x in L if x > cap)} ids over the cap")
