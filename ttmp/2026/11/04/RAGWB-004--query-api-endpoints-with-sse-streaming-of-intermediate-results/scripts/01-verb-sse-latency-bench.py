import json, time, statistics, urllib.request
B = "http://127.0.0.1:8799/api/rag/parc/verbs/search"
qs = ["goja regexp unicode", "sqlite forum for agents", "bleve scorch segments", "lunaroute embeddings cache", "failure modes", "project status", "incremental reindex", "remarkable upload"]
def post(q, sse, extra=""):
    req = urllib.request.Request(B + extra, data=json.dumps({"q": q, "k": 8}).encode(), method="POST",
                                 headers={"Accept": "text/event-stream"} if sse else {})
    t0 = time.perf_counter(); first = None; n = 0
    with urllib.request.urlopen(req) as r:
        if not sse:
            r.read(); return (time.perf_counter() - t0) * 1000, None, 0
        for line in r:
            line = line.decode()
            if line.startswith("event: "):
                n += 1
                if line.strip() == "event: hits" and first is None: first = (time.perf_counter() - t0) * 1000
    return (time.perf_counter() - t0) * 1000, first, n
for q in qs: post(q, False)  # warm
for mode in ("json", "sse", "sse+chunks"):
    tot, firsts, ns = [], [], []
    for _ in range(3):
        for q in qs:
            t, f, n = post(q, mode != "json", "?chunks=1" if mode == "sse+chunks" else "")
            tot.append(t); ns.append(n)
            if f is not None: firsts.append(f)
    s = f"{mode:11} median total {statistics.median(tot):6.1f} ms  p90 {sorted(tot)[int(len(tot)*.9)]:6.1f} ms"
    if firsts: s += f"  first hits event {statistics.median(firsts):5.1f} ms  events/request {statistics.median(ns):.0f}"
    print(s)
