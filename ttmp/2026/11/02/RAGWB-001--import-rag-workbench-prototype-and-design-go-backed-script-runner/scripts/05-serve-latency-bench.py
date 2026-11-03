# Query latency of a running `ragwb serve` (parc app): python3 05-serve-latency-bench.py [port]
import json, time, urllib.request, statistics, sys
port = sys.argv[1] if len(sys.argv) > 1 else "8793"
def run(q):
    t=time.perf_counter()
    req=urllib.request.Request(f"http://127.0.0.1:{port}/api/rag/parc/run", data=json.dumps({"signals":{"q":q},"trigger":"change"}).encode(), headers={"Content-Type":"application/json"})
    d=json.load(urllib.request.urlopen(req))
    return (time.perf_counter()-t)*1000, d["timing"], len(d["result"]["hits"])
w="obsidian vault search"
qs=[w[:i] for i in range(3,len(w)+1,2)] + ["goja regexp unicode", "protobuf schema exchange", "remarkable upload", "go-go-goja module authoring", "the", "xyzzy"]
rows=[]
print(f'{"q":30} {"hits":>4} {"engine":>7} {"pack":>6} {"eval":>7} {"server":>7} {"rtt":>7} {"KB":>6}')
for q in qs:
    rtt,tm,n=run(q)
    rows.append(rtt)
    print(f'{q[:30]:30} {n:4} {tm["engineMs"]:7.1f} {tm["packMs"]:6.1f} {tm["evalMs"]:7.1f} {tm["totalMs"]:7.1f} {rtt:7.1f} {tm["bytes"]/1024:6.1f}')
print(f"median rtt {statistics.median(rows):.1f} ms, max {max(rows):.1f} ms")
