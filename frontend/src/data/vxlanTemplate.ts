// VXLAN 规划说明模板
// 用户在 UI 点击 "插入 VXLAN 规划模板" 时，会用此文本预填一个新 TextAnnotation。
//
// 格式约定：
//   - 纯 TXT 格式（不渲染 markdown）
//   - 使用 \n 换行，由 AnnotationLayer 自动替换为 <br/>
//   - 不带 emoji 标题 / 装饰字符，避免在密集画布上抢占视觉权重

export const VXLAN_PLANNING_TEMPLATE = `一、为什么需要 VXLAN？
传统 VLAN 技术面临两大瓶颈：4096 个隔离域限制、二层广播域无法跨越三层网络。

二、本拓扑实现的核心功能

跨三层网络的大二层互联：通过 VXLAN 隧道，Leaf-1/2/3 之间建立逻辑隧道，VM-1/3/4（VNI 5000）实现跨 Leaf 二层互通。

虚拟机动态迁移：大二层域保障 VM 可从 Server-1 迁移到 Server-2/3，IP 不变，业务不中断。

三、网络功能说明

1. 海量租户隔离：VXLAN 使用 24 比特 VNI，支持多达 1600 万个隔离域。

2. VM-2 的隔离说明：VM-2 运行在 VLAN 20（BD 20），与 VNI 5000 无关，因此与 VM-1/VM-3/VM-4 二层隔离。它属于独立的广播域，无法与 VNI 5000 内的 VM 通信。

3. 同一租户内部隔离：租户内部不同的 VLAN 可以通过不同的 VNI 实现进一步隔离，例如 VM-1（VLAN 10/BD 10/VNI 5000）和 VM-2（VLAN 20/BD 20）虽然在同一台服务器上，但处于不同广播域。

四、拓扑架构

层级    设备                  数量  功能
Spine 层  Spine-1, Spine-2    2 台  Underlay 承载网络
Leaf 层  Leaf-1, Leaf-2, Leaf-3  3 台  VTEP 节点（1.1.1.1, 2.2.2.2, 2.2.2.3）
Server 层  Server-1, Server-2, Server-3  3 台  物理服务器
VM 层  VM-1, VM-2, VM-3, VM-4  4 台  租户业务虚拟机

五、验证方式

同 VNI 互通：VM-1 ↔ VM-3/VM-4 通
跨 VNI 隔离：VM-1 ↔ VM-2 不通
隧道状态：display vxlan tunnel 显示 UP`;
