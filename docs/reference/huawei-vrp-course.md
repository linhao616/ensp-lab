# 华为 VRP / eNSP 实训课程 · 参考索引（源自百度网盘）

> **来源**：百度网盘 `/我的资源/渗透/华为 路由器交换机配置课程/`（4 部分 ~36 讲 + 每部分 `说明.html`）
> **用途**：作为 ensp-lab（华为 VRP CLI 模拟器）的**命令行为验收基准 / 知识参考**。
> **Constraint**：百度网盘 MCP **无"下载到本地"能力**（仅 list / search / 上传类工具）。本索引依据网盘返回的"课程摘要（abstract）"整理，把视频知识转成可版本化参考。若本地存有视频，可据其逐帧核对 `display` 输出格式后再补 golden 测试。

## 如何使用本索引

1. 实现 / 校验某条 VRP 命令时，先查下方「逐讲索引」定位讲次与行为摘要；
2. 再对照「功能覆盖矩阵」确认 ensp-lab 当前状态（✅ 已实现 / 🟡 待校验 / 📋 Roadmap / 🟠 部分）；
3. 高价值命令建议补 golden-output 测试（见矩阵备注）。

---

## 一、逐讲索引

### 第一部分 · VRP 基础与入门

| # | 讲次 | 行为摘要（来自网盘） | 关键命令 / 概念 |
|---|------|----------------------|----------------|
| 1 | VRP基础 | VRP 命令行基础、视图结构、命令级别与常用快捷键 | `system-view`、`?`、`Tab`、`undo`、`<sysname>` |
| 2 | eNSP入门 | eNSP 拓扑搭建、设备连线、命令行登录方式 | 拓扑 / 设备模型 |
| 5 | VRP文件系统基础 | VRP 文件系统、目录与文件操作、配置文件保存与加载 | `dir`、`save`、`startup saved-configuration` |
| 6 | VRP系统管理(1) | 设备命名、时区 / 时钟、登录认证基础 | `sysname`、`clock`、`user-interface` |
| 7 | VRP系统管理(2) | 系统管理进阶（续 6） | — |

### 第二部分 · 路由 / 广域 / 安全

| # | 讲次 | 行为摘要 | 关键命令 |
|---|------|---------|---------|
| 21 | OSPF多区域配置 | 多区域降低 LSDB 复杂度、ABR 路由汇总（汇总需覆盖所有明细） | `area`、`abr-summary` |
| 22 | OSPF中的DR选举 | Router-ID、DR/BDR 选举（接口最大 IP / 优先级）、重置选举 | `ospf router-id`、`ospf dr-priority` |
| 23 | OSPF开销、认证、杂项 | cost = 参考带宽 / 接口带宽（默认 100M）、参考带宽需全网一致、接口认证 | `ospf cost`、`authentication-mode` |
| 27 | 配置DHCP中继 | 中继代理转发（同网段不需中继；跨网段需指定服务器） | `dhcp select relay`、`dhcp relay server-ip` |
| 29 | PAP认证配置 | PAP 明文认证、双向、域方式 | `ppp pap` |
| 30 | CHAP认证配置 | CHAP 密文三次握手、更安全 | `ppp chap` |
| 31 | 帧中继原理 | DLCI、映射表建立、互通测试 | `fr map` |
| 37 | 高级ACL | **高级 ACL 精确匹配 TCP/UDP 端口实现防火墙（内网可出、外网不可入）** | `acl 3000`、`rule permit/deny tcp` |
| 38 | 静态NAT、动态NAT | 私网↔公网地址转换、地址池、接口下 NAT 规则 | `nat static`、`nat outbound` |

### 第三部分 · 交换 / IPv6 / 管理

| # | 讲次 | 行为摘要 | 关键命令 |
|---|------|---------|---------|
| 41 | SNMP原理与配置 | v1 明文 / v2c 增强 / v3 加密认证的差异与配置 | `snmp-agent` |
| 42 | eSight简介 | 网管软件、拓扑发现与设备管理 | — |
| 43 | IPv6基础 | 地址结构（网络前缀 + 接口标识）、链路本地 `fe80::/10`、EUI-64 | `ipv6 enable`、`ipv6 address` |
| 44 | IPv6路由基础 | IPv6 下 RIPng / OSPFv3、全球单播与链路本地配置 | `ospfv3` |
| 48 | 以太网工作原理 | 单播 / 广播 / 冲突域、MAC 表学习 / 转发 / 泛洪 | — |
| 49 | 交换机的端口安全 | 限制接口 MAC 数量、粘滞 MAC、`display` 接口状态 | `port-security` |
| 50 | VLAN原理和配置 | VLAN 隔离广播域、access/trunk、跨交换机 tag / pvid | `vlan`、`port link-type`、`port trunk allow-pass vlan` |
| 51 | Hybrid接口 | 默认 hybrid、tagged / untagged 发送控制 | `port link-type hybrid`、`port hybrid` |
| 52 | 基于MAC划分VLAN | 静态 / 动态基于 MAC 划分 VLAN | `vlan mac-address` |
| 53 | GARP&GVRP | GARP 框架、GVRP 动态 VLAN 注册 | `gvrp` |

### 第四部分 · 高可用 / 二层

| # | 讲次 | 行为摘要 | 关键命令 |
|---|------|---------|---------|
| 55 | STP配置案例分析 | 桥ID / 路径成本 / 发送者桥ID / 端口ID 四要素控制指定端口 | `stp priority`、`stp port priority` |
| 56 | RSTP原理与配置 | 备份 / 替代 / 边缘端口、与普通 STP 差异 | `stp mode rstp`、`stp edged-port` |
| 57 | MSTP原理与配置 | 每 VLAN 独立生成树、区域 / 实例映射、根桥控制 | `stp region-configuration` |
| 58 | 单臂路由实现VLAN间路由 | 子接口 dot1q 终结、网关 | `interface g0/0.1`、`dot1q termination vid` |
| 59 | 三层交换实现VLAN间路由 | VLANIF 接口 IP、三层交换速度优势 | `interface Vlanif` |
| 60 | VRRP原理及基本配置 | 虚拟网关 IP / 优先级 / 抢占、主备切换 | `vrrp vrid`、`vrrp priority` |
| 61 | 配置VRRP端口跟踪、多备份组 | 端口跟踪联动优先级、多备份组负载 | `vrrp vrid track` |
| 63 | 链路聚合(手工模式) | 多链路捆绑逻辑链路、负载均衡、手工 / LACP | `eth-trunk`、`mode manual` |
| 69 | GRE原理与配置 | 公网建隧道承载私网、站点互联 | `interface Tunnel`、`tunnel-protocol gre` |
| 71 | AAA | 认证 / 授权 / 计费、本地与域方式 | `aaa`、`local-user` |

---

## 二、ensp-lab 功能覆盖矩阵

图例：✅ 已实现 ｜ 🟡 已实现·待逐帧校验 ｜ 📋 Roadmap（待开发） ｜ 🟠 部分（空桩）

| 课程主题 | ensp-lab 对应能力 | 状态 | 备注 |
|---------|------------------|------|------|
| VRP基础 / 文件系统 / 系统管理 | CLI 框架、视图、`save`/`load`、`undo` | ✅ 已实现 | `parser.go` / `state.go` |
| eNSP入门 | 拓扑 / 设备建模 | ✅ 已实现 | `topology` + 前端 Canvas |
| OSPF（多区域 / DR / cost / 认证） | OSPF 模拟、`display ospf peer/route` | ✅ 已实现 | P1-F；输出格式待逐帧校验 |
| BGP / ISIS | 路由协议 | ✅ 已实现 | P1-F（`undo isis`、reload 已修） |
| **高级ACL + 防火墙** | `acl_eval.go` 路径 ACL、隐式 deny any、方向模型 | ✅ 已实现 | **P1-C 交付**；视频 37 为验收 oracle |
| 静态 / 动态 NAT | `evaluateNATACL` | 🟠 部分（P2 空桩） | 设计就位，未实现匹配逻辑 |
| VLAN / Hybrid / MAC-VLAN | `vlan`、access/trunk/hybrid、`display vlan` | 🟡 待校验 | 需据视频 50/51/52 校验输出 |
| IPv6 / OSPFv3 | `ipv6 address`、`ospfv3` | 📋 Roadmap | — |
| 端口安全 | `port-security` | 📋 Roadmap | 安全特性，建议 P2 |
| STP / RSTP / MSTP | 生成树 | 📋 Roadmap | 视频 55/56/57 |
| VRRP | `vrrp vrid` | 📋 Roadmap | 视频 60/61 |
| 链路聚合 | `eth-trunk` | ✅ 已实现 | 视频 63 |
| DHCP 中继 | `dhcp relay` | ✅ 已实现 | 视频 27 |
| GRE | `tunnel gre` | 📋 Roadmap | 视频 69 |
| AAA | `aaa` / `local-user` | 📋 Roadmap | 视频 71（安全相关） |
| PAP / CHAP | `ppp` 认证 | 📋 Roadmap | 视频 29/30 |
| SNMP / eSight | `snmp-agent` | 📋 Roadmap | 视频 41/42 |
| 帧中继 | `fr map` | 📋 Roadmap | 视频 31（legacy） |
| GVRP | `gvrp` | 📋 Roadmap | 视频 53 |

---

## 三、安全视角（SecurityEngineer）

- **最高优先借鉴**：`37 高级ACL` —— 视频明确演示「用 ACL 实现防火墙、内网可出外网不可入」。直接校验 P1-C 的**隐式 deny any** 与**方向模型**（src=outbound / dst=inbound / transit=both，first-deny-wins）。
- **次高优先**：`38 NAT`（对齐 `evaluateNATACL` 空桩）、`49 端口安全`、`71 AAA`、`29/30 PAP/CHAP`。
- 上述均为 ensp-lab 安全能力路线（P2+）的核心验收素材，建议在实现对应功能时回查本索引。

---

## 四、说明

- 每部分含 `说明.html`（课程说明），网盘 MCP 无法下载；如有本地副本可补充命令速查表。
- 视频文件较大（单讲 30–220 MB），不建议入库；以本索引 + 课程摘要作为可版本化知识载体。
- 本索引为「行为摘要」级，精确 `display` 输出格式需以真机 / 视频逐帧核对后补 golden 测试（见 ROADMAP）。

---
_Last updated: 2026-08-04 · 由百度网盘扫描 + SecurityEngineer 参考整理_
