import json, sys

def check(path):
    with open(path, encoding='utf-8') as f:
        topo = json.load(f)
    devices = topo.get('devices', {})
    links = topo.get('links', [])
    print(f"\n=== {path} : {len(devices)} devices, {len(links)} links ===")
    problems = []
    # map device -> set of ports used by links
    used_ports = {d: set() for d in devices}
    for l in links:
        sd, sp = l.get('source_device'), l.get('source_port')
        td, tp = l.get('target_device'), l.get('target_port')
        if sd in used_ports and sp and sp != '-':
            used_ports[sd].add(sp)
        if td in used_ports and tp and tp != '-':
            used_ports[td].add(tp)
    for d, dev in devices.items():
        ifs = set((dev.get('interfaces') or {}).keys())
        # For switches, LoopBack/Vlanif are logical; only check physical Ethernet/10GE/GE ports referenced by links
        used = used_ports[d]
        missing = used - ifs
        if missing:
            problems.append(f"  [LINK REFS MISSING PORT] {d}: links use {sorted(missing)} but interfaces={sorted(ifs)}")
        # report port count per device
        phys = [p for p in ifs if not (p.lower().startswith('loop') or p.lower().startswith('vlanif'))]
        print(f"  {d:10s} type={dev.get('type'):8s} phys_ports={len(phys):2d} link_ports_used={len(used):2d}  {sorted(phys)}")
    if problems:
        print("PROBLEMS:")
        for p in problems:
            print(p)
    else:
        print("  -> no link/interface mismatches (servers/VMs trunk via single NIC expected)")
    return problems

if __name__ == '__main__':
    for p in sys.argv[1:]:
        check(p)
