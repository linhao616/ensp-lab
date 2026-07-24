#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
低资源稳定性测试采集器（稳健版）。
- 存活判定走 HTTP /api/health（可靠）；内存/CPU/句柄走 Get-Process（可靠）。
- goroutine 数走 pprof /debug/pprof/goroutine?debug=1。
- 内存护栏：Go 进程工作集 > guard_mb 立即杀掉服务并中止，绝不拖垮整机。
- 单服务实例运行；服务须以 ENSP_PPROF=1 启动。
"""
import argparse, csv, json, subprocess, sys, time, urllib.request, urllib.error

def ps_metrics(pid):
    """返回 dict(ws_mb, handles, cpu_sec, ncpu) 或 None（尽力而为，不中止）。"""
    script = (
        f'$p = Get-Process -Id {pid} -ErrorAction SilentlyContinue; '
        'if ($p) { '
        '"$([int]($p.WorkingSet/1MB))|$([int]$p.HandleCount)|'
        '$([math]::Round($p.CPU,3))|'
        '$([int]((Get-CimInstance Win32_ComputerSystem).NumberOfLogicalProcessors))" '
        '} else { "DEAD" }'
    )
    try:
        out = subprocess.run(["powershell", "-NoProfile", "-Command", script],
                             capture_output=True, text=True, timeout=15).stdout.strip()
    except Exception:
        return None
    if not out or out.startswith("DEAD"):
        return None
    parts = out.split("|")
    if len(parts) < 4:
        return None
    try:
        return dict(ws_mb=int(parts[0]), handles=int(parts[1]),
                    cpu_sec=float(parts[2]), ncpu=int(parts[3]))
    except ValueError:
        return None

def server_alive(base):
    try:
        with urllib.request.urlopen(base + "/api/health", timeout=5) as r:
            return r.status == 200
    except Exception:
        return False

def http_get_json(url, timeout=8):
    try:
        with urllib.request.urlopen(url, timeout=timeout) as r:
            return json.loads(r.read().decode("utf-8"))
    except Exception:
        return None

def pprof_num(url, key):
    try:
        with urllib.request.urlopen(url, timeout=8) as r:
            line = r.readline().decode("utf-8", "ignore").strip()
        parts = line.split()
        if key == "goroutine":
            for i, p in enumerate(parts):
                if p == "total" and i + 1 < len(parts):
                    return int(parts[i + 1])
        if key == "heap":
            ints = [int(x) for x in parts if x.lstrip("-").isdigit()]
            if len(ints) >= 2:
                return int(ints[1] / (1024 * 1024))
    except Exception:
        return None
    return None

def do_ping(base, topo, src, dst, count=4):
    url = f"{base}/api/topology/{topo}/ping?src={src}&dst={dst}&count={count}"
    try:
        with urllib.request.urlopen(url, timeout=12) as r:
            d = json.loads(r.read().decode("utf-8"))
            return dict(ok=True, sent=d.get("sent"), recv=d.get("received"),
                        lost=d.get("lost"), rtt=d.get("rtt_ms"))
    except Exception as e:
        return dict(ok=False, err=str(e)[:80])

def create_topo(base, name, pcs):
    body = json.dumps({"name": name, "devices": pcs, "links": []}).encode()
    req = urllib.request.Request(base + "/api/topology", data=body,
                                 headers={"Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=12) as r:
            return json.loads(r.read().decode("utf-8")).get("id")
    except Exception:
        return None

def delete_topo(base, tid):
    req = urllib.request.Request(base + "/api/topology/" + tid, method="DELETE")
    try:
        urllib.request.urlopen(req, timeout=12)
        return True
    except Exception:
        return False

GUARD_MB = 450
BASE = "http://localhost:8080"

def run(base, pid, scenes, interval, out_csv, soak_min=30, guard=450):
    global GUARD_MB, BASE
    GUARD_MB = guard
    BASE = base
    f = open(out_csv, "w", newline="", encoding="utf-8")
    w = csv.writer(f)
    w.writerow(["ts", "scenario", "mem_mb", "handles", "cpu_pct",
                "goroutines", "heap_mb", "note"])
    f.flush()
    prev = None
    base_gor = pprof_num(base + "/debug/pprof/goroutine?debug=1", "goroutine")

    def record(scenario, note=""):
        nonlocal prev
        if not server_alive(base):
            print("[FATAL] HTTP 不可达，服务已死，终止。", flush=True)
            return False
        m = ps_metrics(pid)
        gor = pprof_num(base + "/debug/pprof/goroutine?debug=1", "goroutine")
        heap = pprof_num(base + "/debug/pprof/heap?debug=1", "heap")
        cpu = ""
        if m and prev:
            dsec = max(0.0, m["cpu_sec"] - prev["cpu_sec"])
            cpu = round(min(100.0 * m["ncpu"], dsec / max(1, interval) / max(1, m["ncpu"]) * 100), 1)
        ws = m["ws_mb"] if m else ""
        hd = m["handles"] if m else ""
        row = [time.strftime("%H:%M:%S"), scenario, ws, hd, cpu,
               gor if gor is not None else "", heap if heap is not None else "", note]
        w.writerow(row)
        f.flush()
        if m and m["ws_mb"] > GUARD_MB:
            print(f"[GUARD] 工作集 {m['ws_mb']}MB > {GUARD_MB}MB，杀掉服务！", flush=True)
            try:
                subprocess.run(["taskkill", "/PID", str(pid), "/F"], capture_output=True, timeout=10)
            except Exception:
                pass
            return False
        prev = m
        return True

    record("baseline", "server up")
    for sc in scenes:
        if sc == 1:
            print("[scene1] 最小 2-PC 拓扑 ×3", flush=True)
            for i in range(3):
                tid = create_topo(base, f"pc2-{i}", [{"name": f"pcA{i}", "type": "pc"},
                                                     {"name": f"pcB{i}", "type": "pc"}])
                if tid:
                    do_ping(base, tid, f"pcA{i}", f"pcB{i}", 4)
                    do_ping(base, tid, f"pcB{i}", f"pcA{i}", 4)
                    delete_topo(base, tid)
                if not record("s1", f"iter{i+1}"): return _finish(f, base_gor)
                time.sleep(interval)
        elif sc == 2:
            print("[scene2] VXLAN demo 跨 Leaf ping", flush=True)
            pairs = [("server-1", "server-3"), ("server-1", "vm-1"),
                     ("leaf-1", "spine-1"), ("vm-1", "vm-2"), ("leaf-1", "leaf-3")]
            for _ in range(3):
                for s, d in pairs:
                    do_ping(base, "vxlan-spine-leaf", s, d, 4)
                if not record("s2", "vxlan pings"): return _finish(f, base_gor)
                time.sleep(interval)
        elif sc == 3:
            print(f"[scene3] 持续运行 {soak_min} 分钟", flush=True)
            end = time.time() + soak_min * 60
            last = 0
            while time.time() < end:
                if time.time() - last > 300:
                    do_ping(base, "vxlan-spine-leaf", "server-1", "server-3", 4)
                    do_ping(base, "vxlan-spine-leaf", "server-1", "vm-1", 4)
                    last = time.time()
                if not record("s3-soak", ""): return _finish(f, base_gor)
                time.sleep(interval)
        elif sc == 4:
            print("[scene4] 并发 3 拓扑", flush=True)
            if not record("s4-pre", ""): return _finish(f, base_gor)
            tids = []
            for i in range(3):
                t = create_topo(base, f"c{i}", [{"name": f"p{i}a", "type": "pc"},
                                                {"name": f"p{i}b", "type": "pc"}])
                if t:
                    tids.append(t)
            for t in tids:
                do_ping(base, t, "p0a", "p0b", 4)
            if not record("s4-active", f"{len(tids)} topos"): return _finish(f, base_gor)
            for t in tids:
                delete_topo(base, t)
            if not record("s4-post", "all deleted"): return _finish(f, base_gor)
        elif sc == 5:
            print("[scene5] 50 次创建/删除轮询", flush=True)
            if not record("s5-pre", ""): return _finish(f, base_gor)
            for i in range(50):
                t = create_topo(base, f"churn{i}", [{"name": f"a{i}", "type": "pc"},
                                                    {"name": f"b{i}", "type": "pc"}])
                if t:
                    if i % 10 == 0:
                        do_ping(base, t, f"a{i}", f"b{i}", 2)
                    delete_topo(base, t)
                if i % 5 == 0:
                    if not record("s5", f"iter{i}"): return _finish(f, base_gor)
            if not record("s5-post", "50 cycles done"): return _finish(f, base_gor)
    return _finish(f, base_gor)

def _finish(f, base_gor):
    end_gor = pprof_num(BASE + "/debug/pprof/goroutine?debug=1", "goroutine")
    f.close()
    diff = "NA"
    if isinstance(base_gor, int) and isinstance(end_gor, int):
        diff = end_gor - base_gor
    print(f"[done] baseline_gor={base_gor} end_gor={end_gor} diff={diff}", flush=True)
    return dict(baseline_gor=base_gor, end_gor=end_gor, diff=diff)

if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--base-url", default="http://localhost:8080")
    ap.add_argument("--pid", type=int, required=True)
    ap.add_argument("--scenes", default="1,2")
    ap.add_argument("--interval", type=int, default=8)
    ap.add_argument("--soak-min", type=int, default=30)
    ap.add_argument("--out", default="tmp/lowres.csv")
    ap.add_argument("--guard-mb", type=int, default=450)
    a = ap.parse_args()
    scenes = [int(x) for x in a.scenes.split(",") if x.strip()]
    res = run(a.base_url, a.pid, scenes, a.interval, a.out, a.soak_min, a.guard_mb)
    print(f"[done] baseline_gor={res['baseline_gor']} end_gor={res.get('end_gor')} diff={res.get('diff')}", flush=True)
