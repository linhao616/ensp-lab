#!/usr/bin/env python3
import json, time, urllib.request, urllib.error

BASE = "http://localhost:8080"
TOPO = "vxlan-spine-leaf"

def get(path, timeout=10):
    return json.load(urllib.request.urlopen(BASE + path, timeout=timeout))

# 1) health 重试
ok = False
for i in range(25):
    try:
        r = urllib.request.urlopen(BASE + "/api/health", timeout=2)
        if r.status == 200:
            print(f"[health] OK ({i}s)")
            ok = True
            break
    except Exception:
        pass
    time.sleep(1)
if not ok:
    print("[health] FAILED — 服务未就绪")
    raise SystemExit(1)

# 2) 拓扑接口 / 链路类型
topo = get(f"/api/topologies/{TOPO}")
s1 = topo["devices"]["server-1"]
print(f"[server-1] 物理接口: {list(s1['interfaces'].keys())}  (数量={len(s1['interfaces'])})")
from collections import Counter
c = Counter(l["link_type"] for l in topo["links"])
print(f"[links] 类型分布: {dict(c)}")

# 3) ping 可达性
def ping(src, dst, count=4):
    try:
        d = get(f"/api/topology/{TOPO}/ping?src={src}&dst={dst}&count={count}")
    except urllib.error.HTTPError as e:
        return f"HTTP {e.code}: {e.read().decode()[:120]}"
    return f"sent={d.get('sent')} recv={d.get('received')} lost={d.get('lost')} rtt={d.get('rtt_ms')}"

print("\n[ping] leaf-1  -> server-1 :", ping("leaf-1", "server-1"))
print("[ping] server-1 -> vm-1     :", ping("server-1", "vm-1"))
print("[ping] server-1 -> vm-2     :", ping("server-1", "vm-2"))
print("[ping] leaf-3  -> server-3  :", ping("leaf-3", "server-3"))   # 验证新 IP 10.0.10.250 无冲突
print("[ping] server-3 -> vm-4     :", ping("server-3", "vm-4"))
print("[ping] vm-1    -> vm-3      :", ping("vm-1", "vm-3"))         # 同 VNI 5000 跨 Leaf 应通
