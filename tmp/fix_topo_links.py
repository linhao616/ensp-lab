#!/usr/bin/env python3
"""行级修复 data/vxlan-spine-leaf.json：
1) 按 link id 前缀将旧的 "business" link_type 重分类为语义类型
   underlay-* -> underlay, access-* -> access, virtual-* -> virtual,
   vxlan-* -> vxlan, link-* -> underlay
2) server-3 IP 冲突修复：interfaces.Ethernet0 10.0.10.30 -> 10.0.10.250
   (避免与 vm-3 的 10.0.10.30 冲突)，config_data 残留 10.0.10.300 -> 10.0.10.250/24
行级替换，保留所有其他字段（含浮点坐标）原样。
"""
import re
import sys

def fix(path: str) -> None:
    text = open(path, encoding="utf-8").read()
    lines = text.split("\n")
    cur = None          # 当前所在 link/device 的 id
    in_s3 = False       # 是否在 server-3 设备块内
    out = []
    for ln in lines:
        m = re.search(r'"id":\s*"([^"]+)"', ln)
        if m:
            cur = m.group(1)
            in_s3 = (cur == "server-3")
        # 1) link_type 重分类
        if '"link_type":' in ln and '"business"' in ln:
            if cur and cur.startswith("underlay-"):
                lt = "underlay"
            elif cur and cur.startswith("access-"):
                lt = "access"
            elif cur and cur.startswith("virtual-"):
                lt = "virtual"
            elif cur and cur.startswith("vxlan-"):
                lt = "vxlan"
            elif cur and cur.startswith("link-"):
                lt = "underlay"
            else:
                lt = "underlay"
            ln = re.sub(r'"link_type":\s*"[^"]*"', f'"link_type": "{lt}"', ln)
        # 2) server-3 IP 冲突修复（仅 server-3 块内）
        if in_s3:
            ln = re.sub(r'("ip_address":\s*)"10\.0\.10\.30"', r'\g<1>"10.0.10.250"', ln)
            ln = re.sub(r'"10\.0\.10\.300/255\.255\.255\.255"',
                        '"10.0.10.250/255.255.255.0"', ln)
        out.append(ln)
    open(path, "w", encoding="utf-8").write("\n".join(out))
    # 统计
    joined = "\n".join(out)
    for t in ("underlay", "access", "virtual", "vxlan", "business"):
        n = len(re.findall(rf'"link_type":\s*"{t}"', joined))
        print(f"  [{path}] link_type={t}: {n}")
    print(f"  server-3 interfaces.Ethernet0.ip = "
          f"{re.search(r'\"server-3\".*?\"ip_address\":\\s*\"([^\"]+)\"', joined, re.S).group(1)}")

if __name__ == "__main__":
    for p in sys.argv[1:]:
        print("fixing", p)
        fix(p)
