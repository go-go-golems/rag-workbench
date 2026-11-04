# RAGWB-007 spike: latency and token cost of djev passage-relevance batches over
# real report sections. Reads the LunaRoute key from Pi's models.json; never prints it.
# usage: python3 -I 01-relevance-batch-spike.py <corpus-dir> "<query>"
import glob, json, os, re, sys, time, urllib.request, urllib.error
key = json.load(open(os.path.expanduser("~/.pi/agent/models.json")))["providers"]["lunaroute"]["apiKey"]
corpus, query = sys.argv[1], sys.argv[2]
secs = []
for f in sorted(glob.glob(os.path.join(corpus, "*.md"))):
    title = os.path.basename(f)[:-3]
    for part in re.split(r"\n(?=## )", open(f).read()):
        text = re.sub(r"\s+", " ", part).strip()
        if len(text) > 200:
            secs.append((title, text[:1500]))
def decide(passages):
    qs = {f"p{i}": {"type": "noul", "instructions": {"passage": f"{t} :: {x}", "question": "Does this passage help answer the query?"},
                    "criteria": {"true": "The passage contains facts needed to answer the query", "false": "Off-topic or only loosely related"}}
          for i, (t, x) in enumerate(passages)}
    body = json.dumps({"model": "djev", "state": {"query": query, "corpus": "engineering project reports"}, "questions": qs}).encode()
    req = urllib.request.Request("https://gw.lunaroute.com/v1/systemone", data=body, method="POST",
                                 headers={"content-type": "application/json", "Authorization": "Bearer " + key})
    t = time.time()
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            d = json.loads(r.read())
    except urllib.error.HTTPError as e:
        return {"status": e.code, "error": e.read().decode()[:300], "ms": round((time.time() - t) * 1000)}
    ps = sorted(((v["noul"], k) for k, v in d["answers"].items()), reverse=True)
    return {"status": 200, "ms": round((time.time() - t) * 1000), "bytes": len(body), "usage": d["usage"],
            "top": [(k, round(p, 3), passages[int(k[1:])][0][:60]) for p, k in ps[:3]],
            "above_0.5": sum(1 for p, _ in ps if p >= 0.5)}
print(json.dumps({"sections": len(secs), "query": query}))
for n in (5, 10, 20, 40, 80):
    print(n, json.dumps(decide(secs[:n])))
