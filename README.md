# eNSP Web Lab - Go重构版

## 项目概述

eNSP Web Lab是基于Go语言重构的网络设备模拟平台，支持多种网络设备类型和协议模拟，提供RESTful API和VRP CLI命令行接口。

## 快速开始

### 启动服务

```bash
cd src/ensp-lab
go run cmd/server/main.go
```

服务启动后默认监听 `http://localhost:8080`（端口可经 `-port` 参数或 `PORT` 环境变量修改，详见下文「配置项说明」）。

> 项目使用 `make build` 构建（Windows 未装 make 时用等价的 `./build.ps1`），前端 `frontend/dist` 通过 `embed.go` 一并嵌入二进制；Windows 下直接运行 `ensp-lab.exe` 即可，无需额外依赖。

### 从源码构建（产出含前端的独立二进制）

仓库默认忽略 `frontend/dist`，构建入口会自动先构建前端再嵌入 Go 二进制（前端为增量构建，源码没变则跳过）：

```bash
make build            # Linux / macOS / CI
./build.ps1           # Windows（未安装 make 时的等价入口）

# 运行（产物名在 Windows 上自动带 .exe）
./ensp-lab.exe        # 默认监听 http://localhost:8080（可用 -port 9090 改端口）
```

> ⚠️ **禁止直接 `go build`。** 构建入口会通过 ldflags 把版本、构建时间、git commit 注入
> `internal/buildinfo`；绕过它直接 `go build` 出来的二进制没有这些信息，会在启动日志与
> `/version` 中自报 **`stale=true`**。这条防线专治「跑的是旧产物、源码其实早修好了」的幽灵 bug——
> 排查任何"改了没生效"的问题时，请先 `curl http://localhost:8080/version` 看 `stale` 字段。

> 本地调试也可直接 `go run cmd/server/main.go`——同样会触发前端嵌入，但每次运行都重新编译，速度较慢。

### 启动 VXLAN Spine-Leaf 演示拓扑

使用 `-demo-vxlan` 参数一键创建并启动完整的 VXLAN Spine-Leaf 组网拓扑：

```bash
go run cmd/server/main.go -demo-vxlan
```

**演示拓扑包含：**
- 2 台 Spine 交换机
- 3 台 Leaf 交换机（VTEP）
- 3 台服务器
- 4 台虚拟机（VLAN 10 和 VLAN 20）
- VXLAN 隧道（VNI 5000）

**验证连通性：**

```bash
# 查看 VXLAN 隧道状态
curl http://localhost:8080/api/topology/vxlan-spine-leaf/vxlan-status

# vm-1 ping vm-3（同 VNI，跨服务器通信）
curl "http://localhost:8080/api/topology/vxlan-spine-leaf/ping?src=vm-1&dst=vm-3"
```

### 完整工作流示例 (curl)

**创建拓扑 → Ping（引擎懒启动）→ 删除**

```bash
# 1. 创建简单拓扑（2台PC + 1条链路）
curl -X POST http://localhost:8080/api/topology \
  -H "Content-Type: application/json" \
  -d '{
    "name":"TestTopo",
    "nodes":[{"id":"h1","type":"pc","name":"Host1"},{"id":"h2","type":"pc","name":"Host2"}],
    "links":[{"source_device":"h1","source_port":"eth0","target_device":"h2","target_port":"eth0"}]
  }'

# 响应示例: {"id":"d45f51dcfca51bd8","name":"TestTopo","device_count":2,"link_count":1,"created_at":"..."}

# 2. 引擎懒启动（无需显式启动；首次 Ping/CLI 自动 eng.Start()）
#    直接执行下面的 Ping 即可触发引擎启动

# 3. 执行 Ping 测试
curl "http://localhost:8080/api/topology/d45f51dcfca51bd8/ping?src=h1&dst=h2"

# 成功响应示例: {"src":"h1","dst":"h2","dst_ip":"192.168.1.2","sent":1,"received":1,"lost":0,"details":["ICMP echo reply received"]}

# 4. 删除拓扑（自动释放引擎资源）
curl -X DELETE http://localhost:8080/api/topologies/d45f51dcfca51bd8

# 响应: 204 No Content
```

### API 使用示例 (PowerShell)

**创建拓扑：**

```powershell
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies" -Method Post -ContentType "application/json" -Body '{"id":"lab1","name":"Test Lab"}'
```

**添加设备：**

```powershell
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/lab1/devices" -Method Post -ContentType "application/json" -Body '{"id":"r1","name":"Router1","type":"router"}'
```

**执行 CLI 命令：**

```powershell
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/lab1/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"system-view"}'
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/lab1/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"bfd enable"}'
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/lab1/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"display bfd"}'
```

**查看设备类型：**

```powershell
Invoke-RestMethod -Uri "http://localhost:8080/api/devices/types"
```

**健康检查：**

```powershell
Invoke-RestMethod -Uri "http://localhost:8080/health"
```

### 典型配置流程示例

配置一台路由器并启用 BFD + OSPF：

```powershell
# 1. 创建拓扑
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies" -Method Post -ContentType "application/json" -Body '{"id":"demo","name":"Demo Topology"}'

# 2. 添加路由器
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices" -Method Post -ContentType "application/json" -Body '{"id":"r1","name":"R1","type":"router"}'

# 3. 进入系统视图
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"system-view"}'

# 4. 配置接口IP
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"interface g0/0/0"}'
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"ip address 192.168.1.1 255.255.255.0"}'
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"quit"}'

# 5. 启用 BFD
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"bfd enable"}'
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"bfd 192.168.1.2 192.168.1.1 500 500 3"}'

# 6. 启动 OSPF
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"ospf 1 area 0"}'

# 7. 查看配置状态
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"display current-configuration"}'
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"display bfd"}'
Invoke-RestMethod -Uri "http://localhost:8080/api/topologies/demo/devices/r1/cli" -Method Post -ContentType "application/json" -Body '{"command":"display ospf"}'
```

## 配置项说明

服务支持通过**命令行参数**或**环境变量**配置，二者等价（命令行优先于环境变量）。所有配置项均有默认值，开箱即用。

| 配置项 | 命令行参数 | 环境变量 | 默认值 | 说明 |
|--------|-----------|----------|--------|------|
| 绑定地址 | `-bind` | `BIND_ADDR` | `127.0.0.1` | HTTP 监听绑定地址；**默认仅本地**（安全默认）。设为 `0.0.0.0` 暴露到所有网卡（仅限可信网络 / 反代之后） |
| 服务端口 | `-port` | `PORT` | `8080` | HTTP 监听端口；前端 API 使用相对路径，改端口后无需改动前端 |
| 存储目录 | `-data-dir` | `DATA_DIR` | `./data` | 拓扑 JSON 持久化目录 |
| 日志级别 | `-log-level` | — | `info` | `debug` / `info` / `warn` / `error` |
| 日志格式 | `-log-format` | — | `console` | `console` / `json` |
| 演示拓扑 | `-demo-vxlan` | — | `false` | 启动时自动创建 VXLAN Spine-Leaf 演示拓扑 |

> 端口配置示例：
> ```bash
> ./ensp-lab -port 9090          # 或
> PORT=9090 ./ensp-lab
> ```
> 之后在 `http://localhost:9090` 访问前端即可，功能不受影响。

### 安全相关环境变量

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `ENS_ACCESS_LOG` | 未设置 | 设置任意值即开启每个请求的访问日志（默认关闭以降低开销） |
| `ENS_CORS_ORIGINS` | 空 | 逗号分隔的可信 CORS 源白名单补充项（默认仅放行 `127.0.0.1` / `localhost` 同源源）。CORS `AllowHeaders` 仅含 `Origin` / `Content-Type`（**不含 `Authorization`**：本应用无鉴权，F8 已移除该冗余头）。前端同源 `localhost` 默认放行；跨源可信前端用逗号分隔追加 |
| `ENS_DIAG_ALLOW_EXTERNAL` | 未设置 | 设置 `1` 才允许对「拓扑外」目标诊断（外部 IP 的 ping/traceroute、公网 DNS 解析）；默认禁止，防止服务端被用作网络侦察 / DoS 放大跳板 |
| `ENSP_PPROF` | 未设置 | 设置任意值挂载 `net/http/pprof` 调试端点（默认关闭）。**开启时强制 token 守卫（F10）**：需携带 `ENSP_PPROF_TOKEN`（或启动时自动生成并写入日志的 token），通过 `?token=` 查询参数或 `X-Pprof-Token` 请求头校验；校验失败返回 403。该端点**仅应在 `-bind 127.0.0.1` 时使用**，切勿在 `0.0.0.0` 暴露下开启 |

> **安全默认说明**：本工具定位为「本地单用户实验工具」，服务端**默认仅绑定 `127.0.0.1`**，所有 API 端点无独立鉴权。请勿在不可信网络中以 `-bind 0.0.0.0` 暴露服务；如确需远程访问，应置于带鉴权的反向代理之后，并仅对可信前端源配置 `ENS_CORS_ORIGINS`。外部诊断需显式开启 `ENS_DIAG_ALLOW_EXTERNAL=1`。性能剖析端点（`ENSP_PPROF`）仅限回环绑定并须配 token（F10），绝不可在暴露网络下启用。

## 支持的设备类型

### 网络设备

| 设备类型  | 设备标识       | 描述                |
| ----- | ---------- | ----------------- |
| 路由器   | `router`   | 支持路由协议、ACL、M-LAG等 |
| 交换机   | `switch`   | 二层交换功能            |
| 三层交换机 | `l3_switch` | 支持三层路由功能          |
| 防火墙   | `firewall` | 安全策略、NAT、VPN      |
| AC控制器 | `ac`       | 无线接入点管理           |
| AP接入点 | `ap`       | 无线接入点             |
| 云设备   | `cloud`    | 模拟网络云             |
| VTEP  | `vtep`     | VXLAN 隧道端点（Overlay 边缘） |

### 终端设备

| 设备类型 | 设备标识     | 描述    |
| ---- | -------- | ----- |
| PC   | `pc`     | 个人计算机 |
| 客户端  | `client` | 网络客户端 |
| 服务器  | `server` | 应用服务器 |
| 集线器  | `hub`    | 二层集线器 |

## 链路类型

系统支持多种链路类型，用于区分不同用途的物理连接：

| 链路类型 | 标识       | 颜色 | 线型 | 图标 | 描述 |
| ---- | -------- | ---- | ---- | ---- | ---- |
| 业务线 | `business` | 绿色 | 实线 | 🔗 | 普通业务数据链路 |
| 带外管理 | `oob` | 紫色 | 长虚线 | 🔌 | 带外管理通道 |
| Console | `console` | 红色 | 短虚线 | 💻 | 控制台管理线 |
| 电源线 | `power` | 黄色 | 点划线 | ⚡ | 电源连接 |
| 无线 | `wireless` | 橙色 | 虚线 | 📶 | 无线连接 |

## 端口管理

每个设备都有多个接口（端口），连线时从具体端口出发：

- 设备周围显示所有可用端口，端口标签显示接口名称（如 `GigabitEthernet0/0/0`）
- 端口状态通过颜色区分：绿色表示启用，灰色表示关闭
- 创建连线时可以直接点击端口，或点击设备自动选择第一个可用端口
- 连线上显示两端端口名称，中间显示链路类型标识

## 支持的协议

> **仿真级别说明**：协议能力由 `internal/protocol` 包提供（20+ 协议的状态模型与处理逻辑）。CLI 侧绝大多数命令为**状态记录型**——解析后写入设备 `CLIState`，`display` 仅回显已配置状态；当前唯一具备真实端到端仿真行为的是 **`ping`**（走 `protocol` 的 `Ping` + 拓扑可达性 BFS 校验）。动态路由协议（OSPF/BGP/RIP）在 **Linux + gont + FRR** 模式下通过 `internal/router` 真正下发；在其他模式下仅记录配置。下文按 OSI 层与类别列出已建模的协议。

### 路由协议

#### BGP (Border Gateway Protocol)

- **功能**: 跨自治系统(AS)动态路由协议，用于专线对接和跨域路由
- **OSI层**: 网络层(L3)
- **支持特性**:
  - AS号配置
  - Router ID配置
  - 邻居配置 (IBGP/EBGP)
  - 路由策略
  - 邻居状态监控

**使用示例**:

```bash
# 进入系统视图
system-view

# 启动BGP进程
bgp 65001 router-id 1.1.1.1

# 配置BGP邻居
peer 192.168.100.2 65002

# 查看BGP状态
display bgp
```

#### OSPF (Open Shortest Path First)

- **功能**: 数据中心内部(Underlay)动态路由协议
- **OSI层**: 网络层(L3)
- **支持特性**:
  - 进程配置 (`ospf <process-id> area <area-id>`)
  - 区域配置
  - 邻居发现
  - LSA (链路状态通告) 存储
  - 路由表计算

**使用示例**:

```bash
# 进入系统视图
system-view

# 启动OSPF进程
ospf 1 area 0

# 查看OSPF状态
display ospf
```

#### 静态路由

- **功能**: 手动配置静态路由条目
- **OSI层**: 网络层(L3)
- **支持特性**:
  - 目标网络配置
  - 子网掩码配置
  - 下一跳配置
  - 最长前缀匹配

**使用示例**:

```bash
# 配置静态路由
ip route-static 10.0.0.0 255.0.0.0 192.168.1.2

# 查看路由表
display routing-table
```

### 覆盖/隧道协议

#### VXLAN (Virtual Extensible LAN)

- **功能**: 构建跨物理三层IP网络的大二层逻辑隧道(Overlay)
- **OSI层**: 覆盖层(L2/L3)
- **支持特性**:
  - VNI配置 (24位网络标识符)
  - VTEP IP配置
  - 对等VTEP配置
  - VRF绑定
  - 隧道状态监控

**使用示例**:

```bash
# 进入系统视图
system-view

# 配置VXLAN
vxlan 39999 10.0.0.1 10.0.0.2 tenant-vrf

# 查看VXLAN状态
display vxlan
```

### 安全协议

#### ACL (访问控制列表)

- **功能**: 流量过滤和安全访问控制
- **OSI层**: 网络层/传输层(L3/L4)
- **支持特性**:
  - 基本ACL (2000-2999)
  - 扩展ACL (3000-3999)
  - 源IP/目标IP匹配
  - 通配符掩码支持
  - 协议过滤 (TCP/UDP/ICMP)
  - 端口过滤
  - 动作配置 (permit/deny)

**使用示例**:

```bash
# 创建ACL
acl 2000

# 添加规则
rule permit ip source 192.168.1.0 0.0.0.255
rule deny tcp source any destination 192.168.2.0 0.0.0.255 destination-port eq 80

# 应用ACL到接口
interface g0/0/1
traffic-filter inbound acl 2000

# 查看ACL配置
display acl
```

#### IPsec

- **功能**: 加密隧道协议，用于专线加密互联
- **OSI层**: 网络层/传输层(L3/L4)
- **支持特性**:
  - 隧道配置
  - 加密算法配置 (AES)
  - 认证算法配置 (SHA)
  - 隧道模式配置 (tunnel/transport)

**使用示例**:

```bash
# 进入系统视图
system-view

# 配置IPsec隧道
ipsec tunnel1 10.0.0.1 20.0.0.1 tunnel aes sha

# 查看IPsec状态
display ipsec
```

### 高可用协议

#### VRRP (Virtual Router Redundancy Protocol)

- **功能**: 虚拟路由冗余协议，提供网关高可用性(HA)
- **OSI层**: 网络层/传输层(L3/L4)
- **支持特性**:
  - 组ID配置
  - 虚拟IP配置
  - 优先级配置
  - 抢占模式
  - 延迟配置
  - 主备切换模拟

**使用示例**:

```bash
# 进入接口视图
system-view
interface g0/0/1

# 配置VRRP组
vrrp 1 192.168.1.254 priority 120 preempt enable

# 查看VRRP状态
display vrrp
```

#### M-LAG (跨设备链路聚合组)

- **功能**: 双活冗余技术，提供链路级高可用性
- **OSI层**: 数据链路层(L2)
- **支持特性**:
  - 域配置 (Domain ID)
  - 系统优先级配置
  - 系统MAC地址配置
  - 对等IP地址配置
  - 对等链路 (Peer Link) 配置
  - DFS组配置
  - 双活模式 (all-active)
  - 接口绑定
  - 故障切换模拟

**使用示例**:

```bash
# 进入系统视图
system-view

# 创建M-LAG域
m-lag domain 1

# 配置系统参数
system-priority 100
system-mac 00e0-fc12-3456
peer-ip 192.168.100.2

# 配置对等链路
peer-link g0/0/10

# 配置DFS组
dfs-group 1
dfs-mode all-active

# 绑定M-LAG接口
interface g0/0/1
m-lag group-id 1 mode lacp

# 查看M-LAG状态
display m-lag
```

### 链路层协议

#### LLDP (Link Layer Discovery Protocol)

- **功能**: 链路层发现协议，自动发现邻居设备
- **OSI层**: 数据链路层(L2)
- **支持特性**:
  - 使能/禁用
  - 邻居发现
  - 设备信息收集

**使用示例**:

```bash
# 进入系统视图
system-view

# 启用LLDP
lldp enable

# 查看LLDP邻居
display lldp
```

#### STP/RSTP (Spanning Tree Protocol)

- **功能**: 生成树协议，防止二层环路和广播风暴
- **OSI层**: 数据链路层(L2)
- **支持特性**:
  - STP/RSTP/MSTP模式
  - 桥优先级配置
  - 端口优先级配置
  - 端口开销配置
  - 收敛模拟

**使用示例**:

```bash
# 进入系统视图
system-view

# 启用STP
stp enable

# 设置STP模式
stp mode rstp

# 设置桥优先级
stp priority 4096

# 查看STP状态
display stp
```

### 运维管理协议

#### SNMP (Simple Network Management Protocol)

- **功能**: 网络管理协议，用于设备监控和告警
- **OSI层**: 应用层(L7)
- **支持特性**:
  - SNMPv2c/SNMPv3版本
  - 社区字符串配置
  - 管理站IP配置
  - Trap配置

**使用示例**:

```bash
# 进入系统视图
system-view

# 配置SNMP
snmp v2c public 192.168.100.100

# 查看SNMP配置
display snmp
```

#### Syslog

- **功能**: 系统日志协议，记录设备操作和故障日志
- **OSI层**: 应用层(L7)
- **支持特性**:
  - 日志服务器配置
  - 端口配置
  - 日志级别配置

**使用示例**:

```bash
# 进入系统视图
system-view

# 配置Syslog服务器
syslog 192.168.100.101 514

# 查看Syslog配置
display syslog
```

#### NTP (Network Time Protocol)

- **功能**: 网络时间协议，设备时间同步
- **OSI层**: 应用层(L7)
- **支持特性**:
  - NTP服务器配置
  - 端口配置
  - 同步状态监控

**使用示例**:

```bash
# 进入系统视图
system-view

# 配置NTP服务器
ntp 192.168.100.102

# 查看NTP状态
display ntp
```

#### SSH

- **功能**: 安全远程登录协议
- **OSI层**: 应用层(L7)
- **支持特性**:
  - SSH版本配置
  - 端口配置
  - 认证方式配置
  - 最大会话数配置

**使用示例**:

```bash
# 进入系统视图
system-view

# 修改SSH端口
ssh port 2222

# 查看SSH配置
display ssh
```

### 协议全景汇总

| 分类 | 协议 | OSI层 | 主要功能 |
| --- | --- | --- | --- |
| **路由协议** | BGP | L3 | 跨 AS 动态路由 |
| | OSPF | L3 | Underlay 动态路由 |
| | 静态路由 | L3 | 手动路由配置 |
| | RIP | L3 | 距离矢量动态路由（配置态） |
| | BFD | L3 | 快速故障检测 |
| **覆盖/隧道** | VXLAN | L2/L3 | 大二层 Overlay |
| | VNI | 覆盖层 | 租户隔离标识 |
| | VTEP | 覆盖层 | 隧道端点 |
| | VRF | L3 | 多租户路由隔离 |
| | GRE | L3 | 通用路由封装 |
| | IPsec | L3/L4 | 加密隧道 |
| **流量工程** | PBR | L3/L4 | 策略路由 |
| **安全协议** | ACL | L3/L4 | 访问控制 |
| | 防火墙 / NAT | L3/L4 | 安全策略与地址转换 |
| | 802.1X | L2 | 接入认证 |
| | RADIUS | L7 | 远程认证 |
| **高可用** | VRRP | L3/L4 | 网关冗余 |
| | M-LAG | L2 | 链路双活 |
| **链路层** | LLDP | L2 | 邻居发现 |
| | STP/RSTP | L2 | 环路消除 |
| | 端口安全 | L2 | MAC 粘滞/数量限制 |
| **服务质量** | QoS | L2/L3/L4 | DiffServ 服务质量 |
| **流量监控** | NetFlow | L3/L4 | 流采样分析 |
| **传输/应用** | TCP / UDP | L4 | 传输层 |
| | TLS | L4–L7 | 传输层安全 |
| | HTTP / HTTPS | L7 | Web 服务 |
| | FTP | L7 | 文件传输 |
| | SMTP | L7 | 邮件 |
| | DNS | L7 | 域名解析 |
| | DHCP | L7 | 地址分配 |
| **网络层扩展** | IPv6 | L3 | IPv6 接口与路由 |
| | MPLS | L2.5 | 标签交换 |
| | PPP / PPPoE | L2 | 点到点/拨号 |
| **运维管理** | SNMP | L7 | 设备监控 |
| | Syslog | L7 | 日志记录 |
| | NTP | L7 | 时间同步 |
| | SSH | L7 | 安全登录 |

> 表中协议均由 `internal/protocol` 提供状态模型；其中 **ICMP（`ping`）** 由 ns-x/gont 引擎真实驱动，OSPF/BGP 在 Linux+FRR 模式下真实下发，其余命令目前以配置态记录为主。

## API接口

> **路由约定**：完整拓扑 CRUD 使用复数 `/api/topologies`；`/api/topology`（单数）用于「快速创建（含链路）」「启动」「Ping」等便捷端点。所有路由在 `internal/api/router.go` 的 `NewRouter()` 中注册。

### 拓扑管理

```
GET    /api/topologies                  # 获取所有拓扑
GET    /api/topologies/:id              # 获取单个拓扑（含 annotations）
POST   /api/topologies                  # 创建拓扑（完整，含 devices/links）
PUT    /api/topologies/:id              # 更新拓扑
DELETE /api/topologies/:id              # 删除拓扑（自动释放引擎资源）
POST   /api/topology                    # 快速创建拓扑（含 nodes/links）
POST   /api/topologies/import           # 导入拓扑（JSON 体，同快速创建）
GET    /api/topologies/:id/export       # 导出拓扑 JSON
GET    /api/topology/:id/ping           # Ping 测试（?src=&dst=，首次调用自动启动引擎）
GET    /api/topology/:id/pcap           # 实时抓包（SSE，?device=&interface=）
GET    /api/topology/:id/vxlan-status   # VXLAN 隧道状态
POST   /api/topologies/:id/simulate-packet # 包路径模拟（BFS 计算）
```

### 设备管理

```
POST   /api/topologies/:id/devices               # 添加设备
PUT    /api/topologies/:id/devices/:deviceId     # 更新设备
DELETE /api/topologies/:id/devices/:deviceId     # 删除设备
POST   /api/topologies/:id/devices/:deviceId/power # 电源控制
POST   /api/topologies/:id/devices/:deviceId/cli    # 执行 VRP CLI 命令
GET    /api/topologies/:id/devices/:deviceId/ip-config    # 读取接口 IP 配置（?interface= 可选）
POST   /api/topologies/:id/devices/:deviceId/ip-config    # 设置接口 IP 配置
GET    /api/devices/types                         # 获取支持的设备类型列表
```

### 链路管理

```
POST   /api/topologies/:id/links          # 添加链路
PUT    /api/topologies/:id/links/:linkId  # 更新链路
DELETE /api/topologies/:id/links/:linkId  # 删除链路
```

**添加链路请求体：**

```json
{
  "id": "link1",
  "source_device": "dev1",
  "source_port": "GigabitEthernet0/0/0",
  "target_device": "dev2",
  "target_port": "GigabitEthernet0/0/1",
  "link_type": "business",
  "cable_type": "ethernet",
  "status": "up"
}
```

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `id` | string | 是 | 链路ID |
| `source_device` | string | 是 | 源设备ID |
| `source_port` | string | 是 | 源设备端口名称 |
| `target_device` | string | 是 | 目标设备ID |
| `target_port` | string | 是 | 目标设备端口名称 |
| `link_type` | string | 否 | 链路类型（默认 business） |
| `cable_type` | string | 否 | 线缆类型（默认 ethernet） |
| `status` | string | 否 | 状态（默认 up） |

### 标注（Annotation）

```
POST   /api/topologies/:id/annotations               # 新增标注
PUT    /api/topologies/:id/annotations/:annotationId # 更新标注（text / 坐标）
DELETE /api/topologies/:id/annotations/:annotationId # 删除标注
```

标注为纯文本（TXT），包含 `text`、`position_x`、`position_y` 字段，用于在前端画布上给拓扑添加文字说明（如 VXLAN 规划说明）。

### 路由器（FRR，仅 Linux 生效）

```
POST   /api/topology/:id/router/:device/ospf   # 下发 OSPF 配置
POST   /api/topology/:id/router/:device/bgp    # 下发 BGP 配置
GET    /api/topology/:id/router/:device/routes # 读取路由表
```

非 Linux 平台或未使用 gont 引擎时返回 `501 Not Implemented`。

### 仿真状态与监控

```
GET    /api/sim/status       # 引擎模式（auto / ns-x / gont）
GET    /api/sim/events       # SSE 事件流（?topology=）
GET    /api/sim/queue-depth  # 事件队列深度（?topology=）
```

### 系统接口

```
GET    /health    # 健康检查（status / platform / engine_count / 资源读数 / timestamp）
GET    /version   # 版本与构建信息（version / build_time / commit / dirty / stale / stale_reason）
GET    /api/system/status    # 后端全局状态（engine_mode: full/lite、platform、资源读数）
GET    /api/system/metrics   # 进程资源使用率与引擎活动计数（CPU%/goroutine/heap/GC + 业务计数 + 尖峰诊断）
GET    /          # 前端入口（embed 静态资源）
```

### 诊断网关（真实仿真引擎）

统一诊断网关把「真实仿真引擎 / 系统能力」以结构化 JSON 暴露，前端只渲染、不解析 CLI 文本、不编造数据。三个端点均要求 `src` 设备已开机，否则返回 `400`；目标地址 `/` 域名非法返回 `400`，DNS 解析失败返回 `404`（绝不返回假 IP）。

```
POST /api/diagnostic/:id/ping        # 真实 ping（默认 4 次探测），返回 RTT 统计
POST /api/diagnostic/:id/traceroute  # 真实拓扑路径发现，返回逐跳列表
POST /api/diagnostic/:id/dns         # 系统 DNS 解析（失败如实返回 404，不编造 IP）
```

**请求 / 响应示例：**

```bash
# Ping：src 为设备ID，dst 可为设备ID 或 IP，count 可选（默认 4，最大 100）
curl -X POST http://localhost:8080/api/diagnostic/abc123/ping \
  -H "Content-Type: application/json" \
  -d '{"src":"pc1","dst":"192.168.1.2","count":4}'
# → {"success":true,"output":"...","rtt":{"min":4.83,"avg":5.06,"max":5.44,"loss":0}}

# Traceroute
curl -X POST http://localhost:8080/api/diagnostic/abc123/traceroute \
  -H "Content-Type: application/json" \
  -d '{"src":"pc1","dst":"192.168.1.2"}'
# → {"reachable":true,"hops":[{"hop":1,"ip":"10.0.10.1","device":"r1","rtt":0}]}

# DNS
curl -X POST http://localhost:8080/api/diagnostic/abc123/dns \
  -H "Content-Type: application/json" \
  -d '{"src":"pc1","domain":"www.baidu.com"}'
# → {"ip":"110.242.69.21","ips":["110.242.69.21",...]}
```

SPA 路由兜底：未匹配的路径回退到 `index.html`。

## CLI命令参考

### 视图切换

| 命令                         | 描述         |
| -------------------------- | ---------- |
| `system-view`              | 进入系统视图     |
| `interface <if-name>`      | 进入接口视图     |
| `acl <acl-num>`            | 进入ACL视图    |
| `m-lag domain <domain-id>` | 进入M-LAG域视图 |
| `bgp <as-number>`          | 进入BGP视图    |
| `quit`                     | 退出当前视图     |

### 接口配置

| 命令                       | 描述        |
| ------------------------ | --------- |
| `ip address <ip> <mask>` | 配置接口IP地址  |
| `interface g0/0/0`       | 进入千兆以太网接口 |

### 路由配置

| 命令                                         | 描述       |
| ------------------------------------------ | -------- |
| `ip route-static <dest> <mask> <next-hop>` | 配置静态路由   |
| `ospf <process-id> area <area-id>`         | 启动OSPF进程 |
| `bgp <as-number> router-id <ip>`           | 启动BGP进程  |
| `peer <ip> <remote-as>`                    | 配置BGP邻居  |

### 覆盖/隧道配置

| 命令                                                       | 描述      |
| -------------------------------------------------------- | ------- |
| `vxlan <vni> <vtep-ip> <peer-vtep-ip> [vrf]`             | 配置VXLAN |
| `gre <tunnel-name> <src-ip> <dest-ip> [key] [keepalive]` | 配置GRE隧道 |
| `ip vpn-instance <name> [rd]`                            | 创建VRF实例 |

### 路由与流量工程配置

| 命令                                                                         | 描述      |
| -------------------------------------------------------------------------- | ------- |
| `bfd enable`                                                               | 启用BFD   |
| `bfd <peer-ip> <local-ip> <min-tx> <min-rx> [detect-mult]`                 | 配置BFD会话 |
| `policy-based-route <policy> <rule-id> <match-acl> <next-hop> [interface]` | 配置策略路由  |

### 安全配置

| 命令                                                                    | 描述                                        | <br />   |
| --------------------------------------------------------------------- | ----------------------------------------- | :------- |
| `acl <acl-num>`                                                       | 创建ACL                                     | <br />   |
| \`rule \<permit                                                       | deny> <protocol> source <ip> <wildcard>\` | 添加ACL规则  |
| \`traffic-filter \<inbound                                            | outbound> acl <acl-num>\`                 | 应用ACL到接口 |
| `ipsec <tunnel-id> <local-ip> <remote-ip> <mode> <encryption> <auth>` | 配置IPsec隧道                                 | <br />   |
| `dot1x enable`                                                        | 启用802.1X                                  | <br />   |
| `dot1x <port> <auth-method> [reauth] [quiet-timer]`                   | 配置端口802.1X                                | <br />   |
| `radius <primary-server> <secret> [secondary-server]`                 | 配置RADIUS服务器                               | <br />   |

### 高可用配置

| 命令                                                                  | 描述          | <br />  |
| ------------------------------------------------------------------- | ----------- | :------ |
| \`vrrp <group-id> <virtual-ip> \[priority <val>] \[preempt \<enable | disable>]\` | 配置VRRP组 |
| `m-lag domain <domain-id>`                                          | 创建M-LAG域    | <br />  |
| `m-lag group-id <id> mode <mode>`                                   | 绑定M-LAG接口   | <br />  |

### 链路层配置

| 命令                   | 描述     | <br />  | <br />  |
| -------------------- | ------ | :------ | :------ |
| `lldp enable`        | 启用LLDP | <br />  | <br />  |
| `lldp disable`       | 禁用LLDP | <br />  | <br />  |
| `stp enable`         | 启用STP  | <br />  | <br />  |
| \`stp mode \<stp     | rstp   | mstp>\` | 设置STP模式 |
| `stp priority <val>` | 设置桥优先级 | <br />  | <br />  |

### 服务质量配置

| 命令                                                   | 描述       |
| ---------------------------------------------------- | -------- |
| `qos enable`                                         | 启用QoS    |
| `qos classifier <name> <acl> [dscp]`                 | 创建QoS分类器 |
| `qos behavior <name> <bandwidth> [priority] [queue]` | 创建QoS行为  |
| `qos policy <name> <classifier> <behavior>`          | 创建QoS策略  |

### 流量监控配置

| 命令                             | 描述           |
| ------------------------------ | ------------ |
| `netflow enable`               | 启用NetFlow    |
| `netflow <exporter-ip> [port]` | 配置NetFlow导出器 |

### 运维管理配置

| 命令                                        | 描述          |
| ----------------------------------------- | ----------- |
| `snmp <version> <community> [manager-ip]` | 配置SNMP      |
| `syslog <server-ip> [port]`               | 配置Syslog服务器 |
| `ntp <server-ip> [port]`                  | 配置NTP服务器    |
| `ssh port <port>`                         | 修改SSH端口     |
| `ssh disable`                             | 禁用SSH       |

### 显示命令

| 命令                              | 描述          |
| ------------------------------- | ----------- |
| `display current-configuration` | 显示当前配置      |
| `display ip interface`          | 显示接口IP信息    |
| `display routing-table`         | 显示路由表       |
| `display ospf`                  | 显示OSPF状态    |
| `display bgp`                   | 显示BGP状态     |
| `display bfd`                   | 显示BFD会话     |
| `display vxlan`                 | 显示VXLAN配置   |
| `display vrf`                   | 显示VRF实例     |
| `display pbr`                   | 显示策略路由      |
| `display gre`                   | 显示GRE隧道     |
| `display acl`                   | 显示ACL配置     |
| `display ipsec`                 | 显示IPsec状态   |
| `display vrrp`                  | 显示VRRP状态    |
| `display m-lag`                 | 显示M-LAG状态   |
| `display lldp`                  | 显示LLDP邻居    |
| `display stp`                   | 显示STP状态     |
| `display qos`                   | 显示QoS配置     |
| `display dot1x`                 | 显示802.1X配置  |
| `display radius`                | 显示RADIUS配置  |
| `display netflow`               | 显示NetFlow配置 |
| `display snmp`                  | 显示SNMP配置    |
| `display syslog`                | 显示Syslog配置  |
| `display ntp`                   | 显示NTP状态     |
| `display ssh`                   | 显示SSH配置     |

### 系统配置

| 命令               | 描述      |
| ---------------- | ------- |
| `sysname <name>` | 配置设备名称  |
| `ping <ip>`      | 网络连通性测试 |

## 项目结构

```
src/ensp-lab/
├── cmd/
│   └── server/
│       └── main.go              # 服务入口：解析命令行参数、装配存储/路由/引擎
├── internal/
│   ├── api/                     # REST API 层（handler 已按资源拆分到多个文件）
│   │   ├── router.go            # Gin 路由注册（NewRouter）
│   │   ├── topology_handlers.go # 拓扑 CRUD handler
│   │   ├── device_handlers.go   # 设备 CRUD / 电源控制 / 设备类型 handler
│   │   ├── link_handlers.go     # 链路 CRUD handler
│   │   ├── cli_handlers.go      # VRP CLI 执行 handler
│   │   ├── annotation_handlers.go # 拓扑标注（Annotation）CRUD handler
│   │   ├── system_handlers.go   # /health、/version handler
│   │   ├── vxlan_topo.go        # VXLAN Spine-Leaf 演示拓扑构建
│   │   └── api_types.go         # API 层数据结构
│   ├── cli/                     # VRP 风格 CLI 模拟器
│   │   ├── parser.go            # 命令解析与分发（核心，~3800 行）
│   │   ├── capabilities.go      # 命令能力矩阵（顶层命令声明）
│   │   ├── host.go              # 主机类设备命令（ipconfig/ping/arp 等）
│   │   ├── state.go             # CLI 状态存储
│   │   └── tools.go             # 辅助工具
│   ├── logging/                 # 结构化日志（基于 zap）
│   │   └── logger.go
│   ├── protocol/                # 协议模拟模块（20+ 协议实现）
│   │   ├── protocol.go          # ProtocolSimulator 核心
│   │   ├── icmp.go / arp.go / ospf.go / bgp.go / vxlan.go ...
│   │   └── ...（dhcp, dns, firewall, ftp, http, ipv6, mpls, ppp, pppoe, rip, smtp, stp, tcp, tls, udp）
│   ├── router/                  # FRR 路由器集成（仅 Linux 生效）
│   │   ├── router.go            # //go:build linux：基于 gont.Host 的 FRRRouter
│   │   └── router_other.go      # 非 Linux 桩实现（返回 "FRR requires Linux"）
│   ├── sim/                     # 模拟引擎核心
│   │   ├── engine.go            # Engine 接口定义
│   │   ├── platform.go          # 引擎工厂与平台/能力检测
│   │   ├── engine_nsx.go        # ns-x 跨平台事件驱动引擎
│   │   ├── gont_emulator.go     # gont（Linux 真实网络命名空间）
│   │   ├── engine_stub.go       # 桩实现
│   │   └── types.go
│   ├── storage/                 # 存储层
│   │   ├── file_storage.go      # 文件持久化（每个拓扑一个 JSON）
│   │   └── memory.go            # 纯内存存储实现
│   ├── testutil/                # 测试工具（资源清理/监控）
│   │   └── testutil.go
│   └── topology/                # 拓扑数据模型
│       ├── model.go            # 设备/链路/标注等数据结构
│       ├── graph.go            # 图可视化类型（Cytoscape/G6）
│       └── manager.go          # 拓扑管理器（线程安全 + 变更订阅）
├── frontend/                    # React + TypeScript 前端
│   ├── src/
│   │   ├── App.tsx
│   │   ├── components/          # TopologyCanvas / AnnotationLayer / CliTerminal / PacketAnimator ...
│   │   ├── data/vxlanTemplate.ts # VXLAN 规划模板（插入标注用）
│   │   └── ...
│   └── dist/                    # 构建产物（由 embed.go 嵌入二进制）
├── data/                        # 拓扑持久化目录（默认 ./data，*.json）
│   └── vxlan-spine-leaf.json    # VXLAN 演示拓扑种子数据
├── docs/                        # 产品手册等文档
├── embed.go                     # //go:embed frontend/dist
├── go.mod
├── go.sum
└── Makefile
```

> 说明：早期文档中提到的 `pkg/`（含 `pkg/ensp`、`pkg/utils`、`pkg/api`、`pkg/topology`）**已删除**，相关代码已并入 `internal/` 各包；前端静态资源由 `web/dist` 改为 `frontend/dist` 并通过 `embed.go` 嵌入。API handler 已从单一的 `router.go` 拆分为 `topology_/device_/link_/cli_/annotation_/system_handlers.go`，拆分已完成，`router.go` 仅负责路由注册，整体构建可正常通过。


## 技术栈

- **语言**: Go 1.26+
- **Web框架**: Gin
- **网络仿真引擎**: 
  - gont (Linux): 基于真实网络命名空间
  - ns-x (跨平台): 纯Go事件驱动模拟
- **前端**: React + TypeScript + Vite
- **并发控制**: sync.RWMutex
- **CLI解析**: 正则表达式 + 状态机

## 依赖项

**后端（Go 模块，详见 `go.mod`）：**

| 依赖 | 版本 | 用途 |
|------|------|------|
| `github.com/gin-gonic/gin` | v1.12.0 | HTTP Web 框架与路由 |
| `github.com/gin-contrib/cors` | v1.7.7 | 跨域（CORS）中间件，严格白名单：默认仅 `127.0.0.1` / `localhost` 同源源 + `ENS_CORS_ORIGINS` 追加的可信源；`AllowHeaders` 仅含 `Origin` / `Content-Type`（不含 `Authorization`，因无鉴权，F8） |
| `github.com/bytedance/ns-x/v2` | v2.4.5 | 跨平台事件驱动网络仿真引擎 |
| `github.com/stv0g/gont/v2` | v2.3.6 | Linux 真实网络命名空间（gont + FRR，构建 tag `linux && gont` 生效） |
| `github.com/google/gopacket` | v1.1.19 | 数据包构造 / 解析 |
| `go.uber.org/zap` | — | 结构化日志 |
| `golang.org/x/sys` | — | 系统调用 |

**前端（npm 包，详见 `frontend/package.json`）：**

| 依赖 | 版本 | 用途 |
|------|------|------|
| `react` / `react-dom` | ^18.3.1 | UI 框架 |
| `typescript` | ^5.6.3 | 类型系统 |
| `vite` | ^5.4.10 | 构建与开发服务器 |
| `@vitejs/plugin-react` | — | React 集成插件 |

> 运行环境要求：Go 1.26+、Node 22（构建前端）、npm。Windows 直接运行 ns-x 引擎无需额外依赖；Linux 真实网络模式需 root 权限、Open vSwitch 与 FRRouting。

## Web 前端界面

启动服务后访问 `http://localhost:8080`（默认端口，可用 `-port` 修改）即为 React 单页前端。核心交互如下（详细说明见 `docs/ensp-lab_manual.md`）：

### 拓扑画布
- Canvas 渲染设备与连线，单击设备选中并高亮；连线清单中点击某条连线可高亮该链路并自动平移视口居中。
- 左侧面板为 2-Tab（设备库 / 连线种类），宽度可拖拽（200–460px）。「连线种类」Tab 含连线种类选择器（带线型预览）与连线清单；选定类型后从源设备拖到目标设备即可创建链路，端口自动分配最小未占用接口；auto 模式按约束矩阵派生类型，非法直连（如 PC-Spine）前端拒绝且后端返回 400。

### 设备详情浮动窗口（类 eNSP）
- 设备的 **CLI 终端**与**配置信息**不再是底部固定面板，而是**可拖动的浮动小窗口**：双击拓扑中的设备、右键设备选择「查看详情」，或点击设备列表项均可弹出。
- 窗口内分 **CLI** / **配置** 两个 Tab；标题栏可拖动（限制在视口内），支持最小化 / 最大化 / 关闭，右下角可拖拽调整大小。
- **多窗口**：每台设备一个独立窗口，已打开不会重复；右上角任务栏显示设备名与状态标记（✓ 点击聚焦）。窗口位置与大小通过 `localStorage`（键名 `ensp-lab-windows-<拓扑ID>`）持久化，刷新页面后恢复布局。

### Ping 测试面板
- 点击工具栏「**Ping 测试**」打开右上角面板。可**自由选源 / 目标设备**（任意 PC/Server/交换机组合），设置探测包数，并开启**连续 Ping**（对应 `ping -t`，每秒一次实时累积输出，点击「停止」结束）。
- 面板打开时，源 / 目标设备会在画布上显示**橙色高亮光环**；切换拓扑自动停止 Ping 并清空输出。本轮 Ping 历史以 `[时间] 源 → 目标 ✅/❌` 形式记录在面板内。

### 仿真引擎懒启动
前端**无独立的「启动拓扑」按钮**，后端也**无 `POST /api/topology/:id/start` 端点**。仿真引擎在首次调用 Ping 或 CLI 时通过 `getOrCreateEngine` 自动创建并 `eng.Start()`，整张拓扑即「上电」运行；删除拓扑时自动释放引擎资源。

## 常见问题

### Q: Linux 下运行需要什么权限？

A: gont 引擎需要 root 权限（CAP_NET_ADMIN），建议使用 `sudo` 运行：

```bash
sudo go run cmd/server/main.go
```

如果不加 `sudo`，系统会自动降级为 ns-x 模式（软件模拟），并在日志中输出警告。

### Q: Linux 下需要安装什么依赖？

A: gont 模式需要 Open vSwitch 和 FRRouting：

```bash
# Debian/Ubuntu
sudo apt install openvswitch-switch frr

# CentOS/RHEL
sudo yum install openvswitch frr
```

如果 OVS 未安装，系统会自动降级为 ns-x 模式。

### Q: Windows 下运行需要什么？

A: Windows 下使用 ns-x 引擎，无需额外依赖，直接运行即可：

```powershell
go run cmd/server/main.go
```

ns-x 是纯 Go 实现的数据包模拟引擎，不依赖 Linux 网络命名空间。

### Q: Ping 返回 `received: 0` 怎么办？

A: 请检查：
1. 引擎是否已在运行——无需手动启动，首次 Ping / CLI 会自动 `eng.Start()`；若返回 0 多半是链路/IP 问题
2. 设备之间是否有链路连接
3. 设备是否有 IP 地址分配（创建拓扑时自动分配）

### Q: 如何查看当前引擎模式？

A: 

```bash
curl http://localhost:8080/api/sim/status
# 或指定拓扑
curl "http://localhost:8080/api/sim/status?topology=your-topo-id"
```

响应示例：
```json
{"platform":"windows","mode":"ns-x"}
```

### Q: 健康检查接口返回什么？

A: 

```bash
curl http://localhost:8080/health
```

响应示例：
```json
{"status":"ok","platform":"windows","engine_count":1,"timestamp":"2026-07-19T12:24:00Z"}
```

### Q: 前端界面在哪里访问？

A: 启动服务后，浏览器访问 `http://localhost:8080`（默认端口，可用 `-port` 或 `PORT` 修改）即可看到 React 前端界面。

## 开发计划

- [x] 实现拓扑管理功能（设备、链路 CRUD）
- [x] 端口级别的链路连接（支持端口选择、端口标签显示）
- [x] 多种链路类型支持（业务线、带外管理、Console、电源、无线）
- [x] 双引擎支持（gont 真实网络命名空间 + ns-x 纯Go模拟）
- [x] 引擎自动降级（Linux 非 root 或 OVS 缺失时自动使用 ns-x）
- [x] Ping 功能（ICMP echo 模拟，真实端到端可达性）
- [x] 左侧面板整合：移除右侧「拓扑资源」面板，改为左侧 2-Tab 标签栏（设备库 / 连线种类），宽度可拖拽（200–460px）；连线种类 Tab = 连线种类选择器（含 auto，带线型预览）+ 连线清单
- [x] 连线种类选择器 + 拖拽创建：「连线种类」Tab 选类型（自动 / 物理链路 / VXLAN / 接入 / 虚拟接入）后进入连线模式，从源设备拖到目标设备创建；auto 按约束矩阵派生类型，非法组合前端拒绝 + 后端 400；端口自动分配最小未占用接口
- [x] 连线类型约束：按设备角色限制非法直连（PC-Spine / Server-Spine / Server-Server），派生 underlay / vxlan / access / virtual 类型
- [x] 健康检查接口 `/health` 与 `/version`
- [x] 前端界面适配（Canvas 拓扑视图 + Ping 测试 + 标注层）
- [x] 文件持久化存储（每个拓扑一个 JSON，重启自动加载）
- [x] 拓扑标注（Annotation）API + 前端画布标注层
- [x] VXLAN Spine-Leaf 演示拓扑（`-demo-vxlan` + 前端规划模板）
- [x] 协议模块扩展（20+ 协议状态模型：OSPF/BGP/VXLAN/ACL/IPsec/STP/LLDP/DHCP/DNS/HTTP/FTP/SMTP/MPLS/PPP 等）
- [x] SSE 仿真事件流 + 队列深度监控
- [x] 包路径模拟端点 `/api/topologies/:id/simulate-packet`
- [x] API handler 拆分重构（router.go → topology_/device_/link_/cli_/annotation_/system_handlers.go，拆分已完成，`router.go` 仅保留 `NewRouter()` 路由注册，构建通过）
- [ ] 实现 eNSP 格式导入导出
- [ ] WebSocket 实时 CLI 交互（终端仿真）
- [ ] 性能优化和压力测试
- [ ] 拓扑导入/导出（JSON/YAML）

## 已知问题

- CLI 绝大多数命令为状态记录型，仅 `ping` 具备真实仿真；动态路由（OSPF/BGP）仅 Linux + gont + FRR 模式真实生效。

## 许可证

MIT License
