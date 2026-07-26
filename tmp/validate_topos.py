import json, glob
defaults = {
 "router":["GigabitEthernet0/0/0","GigabitEthernet0/0/1","GigabitEthernet0/0/2","Serial0/0/0","Serial0/0/1"],
 "switch":["GigabitEthernet0/0/1","GigabitEthernet0/0/2","GigabitEthernet0/0/3","GigabitEthernet0/0/4","GigabitEthernet0/0/5","GigabitEthernet0/0/6","GigabitEthernet0/0/7","GigabitEthernet0/0/8"],
 "l3switch":["Vlanif1","GigabitEthernet0/0/1","GigabitEthernet0/0/2","GigabitEthernet0/0/3","GigabitEthernet0/0/4"],
 "pc":["Ethernet0"],"server":["Ethernet0"],"cloud":["Ethernet0"],"hub":["Ethernet0"],
 "firewall":["GigabitEthernet0/0/0","GigabitEthernet0/0/1"],"vtep":["GigabitEthernet0/0/1","GigabitEthernet0/0/2"],
 "ac":["GigabitEthernet0/0/1"],"ap":["GigabitEthernet0/0/1"],"client":["Ethernet0"],
}
valid=set(defaults)
files=sorted(glob.glob("topologies/lab*.json"))
print("找到 %d 个拓扑文件" % len(files))
errs=0; warns=0
for f in files:
    d=json.load(open(f,encoding="utf-8"))
    nodes={n["id"]:n for n in d.get("nodes",[])}
    ids=[n["id"] for n in d.get("nodes",[])]
    if len(ids)!=len(set(ids)):
        print("[FAIL]%s: 重复节点id" % f); errs+=1
    for n in d.get("nodes",[]):
        if n.get("type") not in valid:
            print("[FAIL]%s: 节点 %s 类型 %s 非法" % (f, n.get("id"), n.get("type"))); errs+=1
    for l in d.get("links",[]):
        for dev,port in ((l["source_device"],l["source_port"]),(l["target_device"],l["target_port"])):
            if dev not in nodes:
                print("[FAIL]%s: 链路端口 %s/%s 设备不存在" % (f, dev, port)); errs+=1; continue
            dt=nodes[dev]["type"]
            if port not in defaults.get(dt,[]):
                print("[WARN]%s: %s(%s) 端口 %s 不在默认接口表" % (f, dev, dt, port)); warns+=1
    print("  %-18s %2d节点 %2d链路  name=%s" % (f, len(d.get("nodes",[])), len(d.get("links",[])), d.get("name")))
print("ERRORS=%d  WARNS=%d" % (errs, warns))
