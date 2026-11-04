#!/usr/bin/env python3
"""Quality gate, presets part (RAGWB-002 D2): run every preset under the memory and
the bleve index.bm25 backend and compare the reports goldens (hit@5, MRR, nDCG@10)
and the top-10 of each preset's default query.

    python3 01-presets-quality.py [ragwb] [presets-dir] [corpus-dir]
"""
import json, subprocess, sys, pathlib, statistics

ragwb = sys.argv[1] if len(sys.argv) > 1 else "./dist/ragwb"
presets = pathlib.Path(sys.argv[2] if len(sys.argv) > 2 else "examples/presets")
corpus = sys.argv[3] if len(sys.argv) > 3 else "examples/apps/reports/corpus"

def run(preset, backend):
    p = subprocess.run([ragwb, "--no-store", "--index-backend", backend, "run", str(preset),
                        "--files", corpus, "--evals", "--k", "10"], capture_output=True, text=True, timeout=600)
    if p.returncode != 0:
        return {"error": (p.stderr or p.stdout).strip().splitlines()[-1][:120]}
    d = json.loads(p.stdout)
    ev = d.get("evals") or {}
    return {"hit5": ev.get("hit@5"), "mrr": ev.get("mrr"), "ndcg": ev.get("ndcg@10"),
            "top": [h["id"] for h in (d.get("run") or {}).get("top") or []]}

rows = []
print(f"{'preset':20} {'hit@5 mem→bleve':>17} {'MRR mem→bleve':>17} {'nDCG mem→bleve':>17} {'top10 ∩':>8}")
for f in sorted(presets.glob("*.js")):
    m, b = run(f, "memory"), run(f, "bleve")
    if "error" in m or "error" in b:
        print(f"{f.stem:20} ERROR memory={m.get('error')} bleve={b.get('error')}")
        rows.append((f.stem, None))
        continue
    inter = len(set(m["top"]) & set(b["top"]))
    ov = inter / max(1, len(set(m["top"]) | set(b["top"]))) if (m["top"] or b["top"]) else 1.0
    fmt = lambda x: "—" if x is None else f"{x:.3f}"
    print(f"{f.stem:20} {fmt(m['hit5']):>7} → {fmt(b['hit5']):<7} {fmt(m['mrr']):>7} → {fmt(b['mrr']):<7} {fmt(m['ndcg']):>7} → {fmt(b['ndcg']):<7} {ov:8.2f}")
    rows.append((f.stem, {"m": m, "b": b, "ov": ov}))

ok = [r for _, r in rows if r]
def mean(key, side):
    xs = [r[side][key] for r in ok if r[side][key] is not None]
    return statistics.mean(xs) if xs else float("nan")
print()
for key in ("hit5", "mrr", "ndcg"):
    print(f"mean {key:5} memory {mean(key,'m'):.3f}  bleve {mean(key,'b'):.3f}  Δ {mean(key,'b')-mean(key,'m'):+.3f}")
print(f"median top-10 Jaccard overlap {statistics.median(r['ov'] for r in ok):.2f}")
worst = [(n, r["m"]["hit5"] - r["b"]["hit5"]) for n, r in rows if r and r["m"]["hit5"] is not None and r["b"]["hit5"] is not None]
print("hit@5 drops > 0.1:", [n for n, d in worst if d > 0.1] or "none")
