#!/usr/bin/env python3
"""Startup / light-load benchmark for ensp-lab.

Measures idle footprint + CONCURRENT burst (to exercise GOMAXPROCS and
per-request access-log I/O), sampling CPU/RSS/handles DURING the burst.
Single instance, short duration, guard-rail built in.

Usage:
  python tmp/bench_startup.py --base-url http://localhost:8080 --pid <pid> \
        --idle 30 --interval 3 --workers 16 --burst-dur 8 --out tmp/x.csv --label X
"""
import argparse, csv, os, subprocess, sys, threading, time
import urllib.request

def get_proc(pid):
    out = subprocess.run(['powershell','-NoProfile','-Command',
                          f'Get-Process -Id {pid} | Select-Object WorkingSet,CPU,HandleCount | ConvertTo-Json'],
                         capture_output=True, text=True, encoding='utf-8', errors='replace')
    if out.returncode != 0 or not out.stdout.strip():
        return None
    try:
        import json
        d = json.loads(out.stdout)
        return int(d.get('WorkingSet',0)), float(d.get('CPU',0.0) or 0.0), int(d.get('HandleCount',0))
    except Exception:
        return None

def health_ok(base):
    for path in ('/api/health','/health'):
        try:
            with urllib.request.urlopen(base+path, timeout=3) as r:
                if r.status == 200:
                    return True
        except Exception:
            pass
    return False

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--base-url', default='http://localhost:8080')
    ap.add_argument('--pid', type=int, required=True)
    ap.add_argument('--idle', type=int, default=30)
    ap.add_argument('--interval', type=int, default=3)
    ap.add_argument('--workers', type=int, default=16)
    ap.add_argument('--burst-dur', type=int, default=8)
    ap.add_argument('--guard-mb', type=int, default=450)
    ap.add_argument('--out', default='tmp/bench_out.csv')
    ap.add_argument('--label', default='run')
    args = ap.parse_args()

    if not health_ok(args.base_url):
        print('HEALTH_NOT_OK'); sys.exit(2)

    # --- idle phase ---
    idle_rows = []
    end = time.time() + args.idle
    while time.time() < end:
        p = get_proc(args.pid)
        if p is None:
            print('PROC_GONE'); break
        rss, cpu, hnd = p
        mb = rss/1024/1024
        idle_rows.append((round(mb,1), round(cpu,1), hnd))
        if mb > args.guard_mb:
            print(f'GUARD_TRIPPED {mb:.0f}MB'); break
        time.sleep(args.interval)

    # --- concurrent burst phase ---
    stop = threading.Event()
    counter = {'ok':0}
    lock = threading.Lock()
    burst_samples = []

    def worker():
        while not stop.is_set():
            if health_ok(args.base_url):
                with lock:
                    counter['ok'] += 1

    def sampler():
        while not stop.is_set():
            p = get_proc(args.pid)
            if p:
                rss, cpu, hnd = p
                burst_samples.append((round(rss/1024/1024,1), round(cpu,1), hnd))
            time.sleep(0.4)

    threads = [threading.Thread(target=worker) for _ in range(args.workers)]
    s = threading.Thread(target=sampler)
    t0 = time.time()
    for th in threads: th.start()
    s.start()
    time.sleep(args.burst_dur)
    stop.set()
    for th in threads: th.join()
    s.join()
    dt = time.time() - t0
    rps = counter['ok']/dt if dt > 0 else 0

    if burst_samples:
        b_avg_mb = sum(x[0] for x in burst_samples)/len(burst_samples)
        b_peak_mb = max(x[0] for x in burst_samples)
        b_peak_cpu = max(x[1] for x in burst_samples)
        b_avg_h = sum(x[2] for x in burst_samples)/len(burst_samples)
    else:
        b_avg_mb = b_peak_mb = b_peak_cpu = b_avg_h = 0

    if idle_rows:
        i_avg_mb = sum(r[0] for r in idle_rows)/len(idle_rows)
        i_max_mb = max(r[0] for r in idle_rows)
        i_peak_cpu = max(r[1] for r in idle_rows)
        i_avg_h = sum(r[2] for r in idle_rows)/len(idle_rows)
    else:
        i_avg_mb = i_max_mb = i_peak_cpu = i_avg_h = 0

    os.makedirs(os.path.dirname(args.out) or '.', exist_ok=True)
    with open(args.out,'w',newline='') as f:
        w = csv.writer(f)
        w.writerow(['phase','rss_mb','cpu_pct','handles'])
        for r in idle_rows:
            w.writerow(['idle',r[0],r[1],r[2]])
        for r in burst_samples:
            w.writerow(['burst',r[0],r[1],r[2]])

    print(f"=== {args.label} ===")
    print(f"IDLE   : avgRSS={i_avg_mb:.1f}MB maxRSS={i_max_mb:.1f}MB peakCPU={i_peak_cpu:.1f}% avgHandles={i_avg_h:.0f}")
    print(f"BURST  : {counter['ok']} ok in {dt:.1f}s -> {rps:.0f} rps | avgRSS={b_avg_mb:.1f}MB peakRSS={b_peak_mb:.1f}MB peakCPU={b_peak_cpu:.1f}% avgHandles={b_avg_h:.0f}")

if __name__ == '__main__':
    main()
