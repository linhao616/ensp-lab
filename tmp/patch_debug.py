import io

f = "D:/Projects/Go/src/ensp-lab/internal/sim/engine_nsx.go"
s = io.open(f, encoding="utf-8").read()

# 1) 确保导入 os
if '"os"' not in s:
    s = s.replace('\t"net"\n', '\t"net"\n\t"os"\n', 1)

# 2) 在 import 块结束后插入 debugSim 开关 + 帮助函数
helper = '''\n// debugSim 控制是否输出“每个数据包”级别的仿真跟踪日志。\n// 默认关闭，避免在高负载下刷爆 stdout/磁盘（此前曾产生上 GB 日志并拖死 HTTP 服务）。\n// 设置环境变量 ENSP_DEBUG=1 可重新开启，用于深度排错。\nvar debugSim = os.Getenv("ENSP_DEBUG") == "1"\n\nfunc dbgSim(format string, a ...interface{}) {\n\tif debugSim {\n\t\tfmt.Printf(format, a...)\n\t}\n}\n\n'''
marker = ')\n\ntype BridgeNode struct {'
assert marker in s, "marker not found in engine_nsx.go"
s = s.replace(marker, ')\n' + helper + 'type BridgeNode struct {', 1)

# 3) 把所有无条件的 DEBUG 打印改为受开关控制
n = s.count('fmt.Printf("DEBUG:')
s = s.replace('fmt.Printf("DEBUG:', 'dbgSim("DEBUG:')

io.open(f, "w", encoding="utf-8").write(s)
print("replaced", n, "DEBUG prints; os imported:", '"os"' in s)
