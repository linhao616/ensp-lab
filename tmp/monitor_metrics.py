#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
实时监听 ensp-lab 资源使用率的轻量客户端（纯标准库，无第三方依赖）。

用法:
    python tmp/monitor_metrics.py [--url http://localhost:8080] [--interval 2] [--once]

它会每 interval 秒轮询一次 GET /api/system/metrics，打印一张紧凑表格，并在
出现资源尖峰时打标记：
    !HIGHCPU     进程 CPU% 超过阈值（默认 50）
    !REBUILD     近 10s 引擎重建次数 >= 3（拓扑编辑突发，R1）
    !PINGBURST   在途 Ping >= 5（流量突发，R2）
    !GCPRESS     GC 占用 CPU% >= 15（短命对象大量分配，R5）
    !IDLEBASE    CPU 高但重建/Ping 业务计数低（ns-x 1ms 轮询常驻 / 前端轮询，R4 基线浪费）

响应里的 diagnosis 字段会直接给出"为什么现在飙高"的中文归因，脚本一并打印。

注意：若服务未启动或端口不对，脚本会持续打印 CONNECT ERROR 并等待，不退出。
"""
import argparse
import json
import sys
import time
import urllib.request

CPU_WARN = 20.0
REBUILD_WARN = 3
PING_WARN = 5
GC_WARN = 15.0


def fetch(url):
    try:
        with urllib.request.urlopen(url, timeout=5) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except Exception as exc:  # noqa: BLE001 - 监听脚本需对网络错误健壮
        return {"_error": str(exc)}


def flags(s):
    out = []
    if s.get("cpu_percent", 0) >= CPU_WARN:
        out.append("!HIGHCPU")
    if s.get("rebuilds_last_10s", 0) >= REBUILD_WARN:
        out.append("!REBUILD")
    if s.get("pings_active", 0) >= PING_WARN:
        out.append("!PINGBURST")
    if s.get("gc_cpu_percent", 0) >= GC_WARN:
        out.append("!GCPRESS")
    if (
        s.get("cpu_percent", 0) >= CPU_WARN
        and s.get("rebuilds_last_10s", 0) < REBUILD_WARN
        and s.get("pings_active", 0) < PING_WARN
    ):
        out.append("!IDLEBASE")
    return " ".join(out)


def fmt(s):
    if "_error" in s:
        return f"CONNECT ERROR: {s['_error']}"
    diag = s.get("diagnosis") or []
    diag_str = " | ".join(diag) if diag else ""
    row = (
        f"{s['timestamp'][:19]}  "
        f"cpu={s.get('cpu_percent',0):5.1f}%  "
        f"gor={s.get('goroutines',0):4d}  "
        f"heap={s.get('heap_alloc_mb',0):7.1f}MB  "
        f"gc={s.get('gc_cpu_percent',0):4.1f}%  "
        f"rebuild10s={s.get('rebuilds_last_10s',0):3d}  "
        f"pings={s.get('pings_active',0):3d}  "
        f"pkts={s.get('packets_processed',0):8d}  "
        f"{flags(s)}"
    )
    if diag_str:
        row += f"\n            diag> {diag_str}"
    return row


def main():
    ap = argparse.ArgumentParser(description="ensp-lab 资源使用率监听器")
    ap.add_argument("--url", default="http://localhost:8080", help="服务基址")
    ap.add_argument("--interval", type=float, default=2.0, help="轮询间隔(秒)")
    ap.add_argument("--once", action="store_true", help="只采样一次并退出")
    args = ap.parse_args()

    target = args.url.rstrip("/") + "/api/system/metrics"
    if args.once:
        print(fmt(fetch(target)))
        return

    print(f"monitoring {target} every {args.interval}s (Ctrl+C to stop)\n")
    try:
        while True:
            print(fmt(fetch(target)))
            time.sleep(args.interval)
    except KeyboardInterrupt:
        print("\nstopped.")
        sys.exit(0)


if __name__ == "__main__":
    main()
