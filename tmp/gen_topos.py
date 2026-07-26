#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Generate ensp-lab importable topology JSON files for lab01~lab12 (USE.md).

Each file follows the POST /api/topology (createTopologySimple) request schema:
  { "name": <str>, "nodes": [{"id","type","name"}],
    "links": [{"source_device","source_port","target_device","target_port"}] }

Validation mirrors internal/api/validation.go rules:
  - node id: ^[A-Za-z0-9_-]{1,64}$, no \\x00 \\n \\r
  - node type: one of the 12 DeviceType enum values
  - each link endpoint device must exist in nodes
  - referenced ports must exist in that device type's default interfaces
"""
import json
import os
import re

OUT_DIR = os.path.join(os.path.dirname(os.path.dirname(__file__)), "topologies")
os.makedirs(OUT_DIR, exist_ok=True)

VALID_TYPES = {
    "router", "switch", "l3_switch", "firewall", "ac", "ap",
    "pc", "client", "server", "cloud", "hub", "vtep",
}
DEFAULT_IFACES = {
    "router":   ["GigabitEthernet0/0/0", "GigabitEthernet0/0/1", "GigabitEthernet0/0/2",
                 "Serial0/0/0", "Serial0/0/1"],
    "switch":   ["GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3",
                 "GigabitEthernet0/0/4", "GigabitEthernet0/0/5", "GigabitEthernet0/0/6",
                 "GigabitEthernet0/0/7", "GigabitEthernet0/0/8"],
    "l3_switch": ["Vlanif1", "GigabitEthernet0/0/1", "GigabitEthernet0/0/2",
                  "GigabitEthernet0/0/3", "GigabitEthernet0/0/4"],
    "firewall":  ["GigabitEthernet0/0/0", "GigabitEthernet0/0/1", "GigabitEthernet0/0/2",
                  "GigabitEthernet0/0/3"],
    "ac":        ["GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3"],
    "ap":        ["Radio0", "Radio1", "GigabitEthernet0"],
    "pc":        ["Ethernet0"],
    "client":    ["Wi-Fi0"],
    "server":    ["Ethernet0"],
    "cloud":     ["Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3"],
    "hub":       ["Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3"],
    "vtep":      ["GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3",
                  "GigabitEthernet0/0/4"],
}
ID_RE = re.compile(r"^[A-Za-z0-9_-]{1,64}$")

# lab definitions: (filename, topology_name, nodes, links)
# node = (id, type, name) ; link = (src_dev, src_port, dst_dev, dst_port)
LABS = [
    ("lab01.json", "lab01 VLAN/Trunk (2×S5700 + 2×PC)",
     [("sw1", "switch", "SW1"), ("sw2", "switch", "SW2"),
      ("pc1", "pc", "PC1"), ("pc2", "pc", "PC2")],
     [("pc1", "Ethernet0", "sw1", "GigabitEthernet0/0/1"),
      ("pc2", "Ethernet0", "sw2", "GigabitEthernet0/0/1"),
      ("sw1", "GigabitEthernet0/0/2", "sw2", "GigabitEthernet0/0/2")]),

    ("lab02.json", "lab02 Eth-Trunk (2×S5700)",
     [("sw1", "switch", "SW1"), ("sw2", "switch", "SW2")],
     [("sw1", "GigabitEthernet0/0/1", "sw2", "GigabitEthernet0/0/1"),
      ("sw1", "GigabitEthernet0/0/2", "sw2", "GigabitEthernet0/0/2")]),

    ("lab03.json", "lab03 STP/RSTP (3×S5700 环形)",
     [("sw1", "switch", "SW1"), ("sw2", "switch", "SW2"), ("sw3", "switch", "SW3")],
     [("sw1", "GigabitEthernet0/0/1", "sw2", "GigabitEthernet0/0/1"),
      ("sw2", "GigabitEthernet0/0/2", "sw3", "GigabitEthernet0/0/1"),
      ("sw3", "GigabitEthernet0/0/2", "sw1", "GigabitEthernet0/0/2")]),

    ("lab04.json", "lab04 静态路由 (3×AR2220 + PC)",
     [("r1", "router", "R1"), ("r2", "router", "R2"), ("r3", "router", "R3"),
      ("pc1", "pc", "PC1")],
     [("r1", "GigabitEthernet0/0/0", "r2", "GigabitEthernet0/0/0"),
      ("r2", "GigabitEthernet0/0/1", "r3", "GigabitEthernet0/0/0"),
      ("r1", "GigabitEthernet0/0/1", "pc1", "Ethernet0")]),

    ("lab05.json", "lab05 OSPF 单区 (3×AR2220)",
     [("r1", "router", "R1"), ("r2", "router", "R2"), ("r3", "router", "R3")],
     [("r1", "GigabitEthernet0/0/0", "r2", "GigabitEthernet0/0/0"),
      ("r2", "GigabitEthernet0/0/1", "r3", "GigabitEthernet0/0/0"),
      ("r3", "GigabitEthernet0/0/1", "r1", "GigabitEthernet0/0/1")]),

    ("lab06.json", "lab06 VRRP (2×AR2220 + PC)",
     [("r1", "router", "R1"), ("r2", "router", "R2"), ("pc1", "pc", "PC1")],
     [("r1", "GigabitEthernet0/0/0", "pc1", "Ethernet0"),
      ("r2", "GigabitEthernet0/0/0", "pc1", "Ethernet0"),
      ("r1", "GigabitEthernet0/0/1", "r2", "GigabitEthernet0/0/1")]),

    ("lab07.json", "lab07 DHCP (1×AR2220 + PC)",
     [("r1", "router", "R1"), ("pc1", "pc", "PC1")],
     [("r1", "GigabitEthernet0/0/0", "pc1", "Ethernet0")]),

    ("lab08.json", "lab08 ACL+NAT (1×AR 出口 + 内网 PC + 伪公网 Cloud)",
     [("r1", "router", "R1"), ("pc1", "pc", "PC1"), ("cloud1", "cloud", "Internet")],
     [("pc1", "Ethernet0", "r1", "GigabitEthernet0/0/1"),
      ("r1", "GigabitEthernet0/0/0", "cloud1", "Ethernet0")]),

    ("lab09.json", "lab09 综合园区网 (2×Core SW + 1×Access SW + 1×AR + PC)",
     [("core1", "switch", "Core-SW1"), ("core2", "switch", "Core-SW2"),
      ("acc1", "switch", "Access-SW1"), ("ar1", "router", "AR1"), ("pc1", "pc", "PC1")],
     [("pc1", "Ethernet0", "acc1", "GigabitEthernet0/0/1"),
      ("acc1", "GigabitEthernet0/0/2", "core1", "GigabitEthernet0/0/1"),
      ("acc1", "GigabitEthernet0/0/3", "core2", "GigabitEthernet0/0/1"),
      ("core1", "GigabitEthernet0/0/2", "ar1", "GigabitEthernet0/0/0"),
      ("core2", "GigabitEthernet0/0/2", "ar1", "GigabitEthernet0/0/1")]),

    ("lab10.json", "lab10 OSPF 多区 (4×AR)",
     [("r1", "router", "R1"), ("r2", "router", "R2"), ("r3", "router", "R3"), ("r4", "router", "R4")],
     [("r1", "GigabitEthernet0/0/0", "r2", "GigabitEthernet0/0/0"),
      ("r2", "GigabitEthernet0/0/1", "r3", "GigabitEthernet0/0/0"),
      ("r3", "GigabitEthernet0/0/1", "r4", "GigabitEthernet0/0/0"),
      ("r4", "GigabitEthernet0/0/1", "r1", "GigabitEthernet0/0/1")]),

    ("lab11.json", "lab11 BGP (3×AR)",
     [("r1", "router", "R1"), ("r2", "router", "R2"), ("r3", "router", "R3")],
     [("r1", "GigabitEthernet0/0/0", "r2", "GigabitEthernet0/0/0"),
      ("r2", "GigabitEthernet0/0/1", "r3", "GigabitEthernet0/0/0")]),

    ("lab12.json", "lab12 IPSec VPN (2×AR 站点 + ISP 路由器，含私网 PC)",
     [("r1", "router", "Site1-AR"), ("r2", "router", "Site2-AR"), ("isp", "router", "ISP"),
      ("pc1", "pc", "PC1"), ("pc2", "pc", "PC2")],
     [("pc1", "Ethernet0", "r1", "GigabitEthernet0/0/1"),
      ("r1", "GigabitEthernet0/0/0", "isp", "GigabitEthernet0/0/0"),
      ("isp", "GigabitEthernet0/0/1", "r2", "GigabitEthernet0/0/0"),
      ("r2", "GigabitEthernet0/0/1", "pc2", "Ethernet0")]),
]


def validate(name, nodes, links):
    errors = []
    node_ids = set()
    node_types = {}
    for nid, ntype, _ in nodes:
        if not ID_RE.match(nid):
            errors.append(f"invalid node id: {nid!r}")
        if ntype not in VALID_TYPES:
            errors.append(f"invalid node type: {ntype!r} ({nid})")
        node_ids.add(nid)
        node_types[nid] = ntype
    for i, (sd, sp, td, tp) in enumerate(links):
        if sd not in node_ids:
            errors.append(f"link[{i}] unknown source_device {sd!r}")
        if td not in node_ids:
            errors.append(f"link[{i}] unknown target_device {td!r}")
        for dev, port in ((sd, sp), (td, tp)):
            if dev in node_types:
                ifaces = DEFAULT_IFACES.get(node_types[dev], [])
                if port not in ifaces:
                    errors.append(f"link[{i}] port {port!r} not in {dev} default ifaces")
    return errors


def main():
    total_err = 0
    for fname, tname, nodes, links in LABS:
        errs = validate(tname, nodes, links)
        total_err += len(errs)
        payload = {
            "name": tname,
            "nodes": [{"id": i, "type": t, "name": n} for i, t, n in nodes],
            "links": [{"source_device": sd, "source_port": sp,
                       "target_device": td, "target_port": tp}
                      for sd, sp, td, tp in links],
        }
        with open(os.path.join(OUT_DIR, fname), "w", encoding="utf-8") as f:
            json.dump(payload, f, ensure_ascii=False, indent=2)
        status = "OK" if not errs else f"ERRORS({len(errs)})"
        print(f"  {fname:12s} {status:12s} nodes={len(nodes)} links={len(links)}")
        for e in errs:
            print(f"      - {e}")
    print(f"\nGenerated {len(LABS)} files into {OUT_DIR}")
    print(f"Total validation errors: {total_err}")
    if total_err:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
