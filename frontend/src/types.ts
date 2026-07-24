// 共享类型定义 - 与 Go 后端 internal/topology/model.go 和 internal/sim/types.go 对齐

export type DeviceType =
  | 'router'
  | 'switch'
  | 'l3_switch'
  | 'firewall'
  | 'ac'
  | 'ap'
  | 'pc'
  | 'client'
  | 'server'
  | 'cloud'
  | 'hub'
  | 'vtep';

export type DeviceStatus = 'power_off' | 'running' | 'configuring';

export type PortType =
  | 'copper'
  | 'ethernet'
  | 'fiber'
  | 'serial'
  | 'console'
  | 'management';

export type LinkType =
  | 'business'
  | 'oob'
  | 'console'
  | 'power'
  | 'wireless'
  | 'underlay' // 物理链路（实线，黑色）
  | 'vxlan' // VXLAN 隧道（虚线，红色）
  | 'access' // 接入链路（虚线，灰色）
  | 'virtual'; // 虚拟接入（虚线，灰色）

export interface Interface {
  name: string;
  ip_address: string;
  subnet_mask: string;
  gateway: string;
  dns: string;
  mac: string;
  status: string;
  vlan: number;
  port_type: PortType;
  bandwidth: number;
  delay: number;
}

export interface DeviceConfigData {
  device_name: string;
  interfaces: Record<string, string>;
  default_gateway: string;
}

export interface Device {
  id: string;
  name: string;
  type: DeviceType;
  model: string;
  status: DeviceStatus;
  position_x: number;
  position_y: number;
  interfaces: Record<string, Interface>;
  config: string;
  config_data: DeviceConfigData | null;
  vrp_version: string;
  created_at: string;
  updated_at: string;
}

export interface Link {
  id: string;
  source_device: string;
  source_port: string;
  target_device: string;
  target_port: string;
  link_type: LinkType;
  cable_type: PortType;
  bandwidth: number;
  delay: number;
  status: string;
  created_at: string;
  vxlan_vni: number;
  vxlan_peer_list: string[];
  vlan: number;
  // 端口标签偏移（世界坐标 px），用于拖拽端口名标签
  source_label_dx?: number;
  source_label_dy?: number;
  target_label_dx?: number;
  target_label_dy?: number;
}

export interface TextAnnotation {
  id: string;
  text: string;
  position_x: number;
  position_y: number;
  // 样式字段（标注设置面板控制）
  font_size?: number; // 0 = 默认
  font_family?: string; // 字体族
  text_align?: string; // left | center | right
  border_style?: string; // solid | dashed | hidden
  background?: string; // 背景色
  width?: number; // 显式宽度，0 = 自适应
  height?: number; // 显式高度，0 = 自适应（显示全部内容）
  created_at: string;
}

export interface Topology {
  id: string;
  name: string;
  devices: Record<string, Device>;
  links: Link[];
  annotations: TextAnnotation[];
  created_at: string;
  updated_at: string;
  canvas_scale: number;
  canvas_offset_x: number;
  canvas_offset_y: number;
  device_count?: number;
  link_count?: number;
}

export type PacketEventType = 'send' | 'forward' | 'receive' | 'drop';

export interface PacketEvent {
  packet_id: string;
  type: PacketEventType;
  device_id: string;
  interface: string;
  timestamp: string;
  description: string;
  path: string[];
}

export interface DeviceTypeEntry {
  type: string;
  name: string;
}

export interface SimStatus {
  platform: string;
  mode: string;
  error?: string;
}

export interface Viewport {
  scale: number;
  ox: number;
  oy: number;
}

export interface CliResponse {
  output?: string;
  view?: string;
  sub?: string;
  targetDeviceID?: string;
}

// IP 配置（方案二 Web UI / 方案三 REST API 共用）
export interface IPConfig {
  device_id: string;
  type: string;
  mode: 'host' | 'interface'; // host=终端主机(PC/Client/Server)；interface=网络接口
  interface?: string; // 接口模式下为接口名（如 10GE5/0/1）；host 模式下为 Ethernet0
  ip: string;
  subnet_mask: string;
  gateway?: string;
  dns?: string;
  cidr?: string;
}

export interface SetIPConfigRequest {
  ip?: string;
  subnet_mask?: string;
  gateway?: string;
  dns?: string;
  interface?: string; // 接口模式：接口名
  mode?: 'host' | 'interface';
}

export interface PathSegment {
  linkId: string;
  from: string;
  to: string;
  fromPort: string;
  toPort: string;
  linkType: string;
  status: string;
}

export interface SimulatePacketResult {
  path: PathSegment[];
  totalHops: number;
  reachable: boolean;
  ttl?: number;
  message?: string;
}

// 设备元数据 - 镜像 web/js/topology-renderer.js 中的 DEV_META
export interface DeviceMeta {
  color: string;
  bg: string;
  label: string;
  shape: 'circle' | 'rect' | 'cloud' | 'diamond' | 'hex' | 'tri';
  radius: number;
  ports: number;
  icon: string;
}

export const DEVICE_META: Record<DeviceType, DeviceMeta> = {
  router: { color: '#1a73e8', bg: '#e8f0fe', label: '路由器', shape: 'circle', radius: 35, ports: 8, icon: '[R]' },
  switch: { color: '#28a745', bg: '#d4edda', label: '交换机', shape: 'rect', radius: 35, ports: 24, icon: '[S]' },
  l3_switch: { color: '#1e7e34', bg: '#c3e6cb', label: '三层交换机', shape: 'rect', radius: 35, ports: 24, icon: '[L3]' },
  firewall: { color: '#dc3545', bg: '#f8d7da', label: '防火墙', shape: 'diamond', radius: 40, ports: 8, icon: '[FW]' },
  ac: { color: '#6f42c1', bg: '#e2d4f0', label: 'AC', shape: 'hex', radius: 35, ports: 4, icon: '[AC]' },
  ap: { color: '#fd7e14', bg: '#ffe5cc', label: 'AP', shape: 'tri', radius: 32, ports: 2, icon: '[AP]' },
  pc: { color: '#17a2b8', bg: '#d1ecf1', label: 'PC', shape: 'rect', radius: 35, ports: 2, icon: '[PC]' },
  client: { color: '#6c757d', bg: '#e2e3e5', label: '客户端', shape: 'rect', radius: 35, ports: 2, icon: '[C]' },
  server: { color: '#0d47a1', bg: '#c5cae9', label: '服务器', shape: 'rect', radius: 35, ports: 2, icon: '[SRV]' },
  cloud: { color: '#6c757d', bg: '#f8f9fa', label: '云', shape: 'cloud', radius: 40, ports: 4, icon: '[CLD]' },
  hub: { color: '#ffc107', bg: '#fff3cd', label: '集线器', shape: 'circle', radius: 35, ports: 8, icon: '[HUB]' },
  vtep: { color: '#e91e63', bg: '#f8e8f0', label: 'VTEP', shape: 'hex', radius: 38, ports: 8, icon: '[VTEP]' },
};

export interface LinkMeta {
  color: string;
  label: string;
  dash: number[];
  width: number;
  icon: string;
}

export const LINK_META: Record<LinkType, LinkMeta> = {
  business: { color: '#28a745', label: '业务', dash: [], width: 2, icon: '—' },
  oob: { color: '#6f42c1', label: '带外', dash: [8, 4], width: 2, icon: 'O' },
  console: { color: '#dc3545', label: 'Console', dash: [4, 4], width: 2, icon: 'C' },
  power: { color: '#ffc107', label: '电源', dash: [2, 6], width: 3, icon: 'P' },
  wireless: { color: '#fd7e14', label: '无线', dash: [6, 3], width: 2, icon: 'W' },
  underlay: { color: '#333333', label: '物理链路', dash: [], width: 2, icon: '—' },
  vxlan: { color: '#dc3545', label: 'VXLAN隧道', dash: [8, 4], width: 2, icon: 'V' },
  access: { color: '#888888', label: '接入链路', dash: [4, 4], width: 2, icon: 'A' },
  virtual: { color: '#888888', label: '虚拟接入', dash: [4, 4], width: 2, icon: 'v' },
};

export function getDeviceMeta(type: string): DeviceMeta {
  return (DEVICE_META as Record<string, DeviceMeta>)[type] || DEVICE_META.router;
}

export function getLinkMeta(type: string): LinkMeta {
  return (LINK_META as Record<string, LinkMeta>)[type] || LINK_META.business;
}

// ── 连线类型中文标签 ──
export const LINK_TYPE_LABEL: Record<string, string> = {
  underlay: '物理链路',
  business: '物理链路',
  vxlan: 'VXLAN隧道',
  access: '接入链路',
  virtual: '虚拟接入',
  oob: '带外',
  console: 'Console',
  power: '电源',
  wireless: '无线',
};

export function getLinkTypeLabel(type: string): string {
  return LINK_TYPE_LABEL[type] || type;
}

// ── 连线合法性约束（按设备角色）─────────────────────────────
// 演示数据中：spine 用 switch 类型，leaf 用 vtep，server 用 server，vm/pc 用 pc。
export type LinkAllowedResult = { allowed: boolean; linkType?: LinkType; message?: string };

export function deviceTypeToRole(type: string): string {
  switch (type) {
    case 'switch':
    case 'router':
    case 'l3_switch':
      return 'Spine';
    case 'vtep':
      return 'Leaf';
    case 'server':
      return 'Server';
    case 'pc':
    case 'client':
      return 'PC'; // pc 同时承载 VM 与 PC
    default:
      return 'Unknown';
  }
}

// 无序角色对的允许连线类型；值为 null 表示禁止。
const LINK_RULE_MATRIX: Record<string, LinkType | null> = {
  'Leaf-Spine': 'underlay', // 物理链路（实线，黑）
  'Leaf-Leaf': 'vxlan', // VXLAN 隧道（虚线，红）
  'Leaf-PC': 'access', // 接入链路（虚线，灰）
  'Leaf-Server': 'access', // 接入链路（虚线，灰）
  'PC-Server': 'virtual', // 虚拟接入（虚线，灰）—— 覆盖 Server-VM / Server-PC
  'Spine-Spine': 'underlay', // 物理链路（实线，黑）
  'PC-Spine': null, // 禁止 Spine-VM / Spine-PC
  'Server-Spine': null, // 禁止 Spine-Server
  'Server-Server': null, // 禁止 Server-Server
};

export function isLinkAllowed(srcType: string, dstType: string): LinkAllowedResult {
  const a = deviceTypeToRole(srcType);
  const b = deviceTypeToRole(dstType);
  if (a === 'Unknown' || b === 'Unknown') return { allowed: true, linkType: 'underlay' };
  const key = [a, b].sort().join('-');
  if (!(key in LINK_RULE_MATRIX)) return { allowed: true, linkType: 'underlay' };
  const v = LINK_RULE_MATRIX[key];
  if (v === null) return { allowed: false, message: `不允许 ${a} 与 ${b} 直接连线` };
  return { allowed: true, linkType: v };
}

// 计算设备端口在画布上的世界坐标（基于接口索引——仅用于端口圆点渲染）
// 注意：此函数不用于连线端点定位，连线端点应使用 getDirectedPortPosition
export function getPortPosition(device: Device, portName: string): { x: number; y: number } {
  const m = getDeviceMeta(device.type);
  const x = Number(device.position_x) || 0;
  const y = Number(device.position_y) || 0;
  const ifs = Object.keys(device.interfaces || {});
  const idx = ifs.indexOf(portName);
  if (idx === -1) return { x, y };
  const count = Math.min(ifs.length, 12);
  const angle = (Math.PI * 2 / count) * idx - Math.PI / 2;
  const portOffset = m.radius + 5;
  return {
    x: x + portOffset * Math.cos(angle),
    y: y + portOffset * Math.sin(angle),
  };
}

// ── 方向感知的端口定位（连线专用）─────────────────────────────
// 根据【对端设备方位】计算本端设备边框上的锚点，
// 确保连线始终从设备边缘朝对端方向引出（eNSP 风格）。
// 用于 drawLinks 中替代 getPortPosition，解决"连线悬浮"问题。
export function getDirectedPortPosition(
  device: Device,
  targetX: number,
  targetY: number,
): { x: number; y: number } {
  const m = getDeviceMeta(device.type);
  const x = Number(device.position_x) || 0;
  const y = Number(device.position_y) || 0;
  const dx = targetX - x;
  const dy = targetY - y;
  const len = Math.hypot(dx, dy) || 1;
  // 根据设备形状计算边框碰撞点偏移
  let offset: number;
  if (m.shape === 'circle') {
    offset = m.radius; // 圆形：半径即为边框距离
  } else if (m.shape === 'rect' || m.shape === 'cloud') {
    // 矩形/云形：用近似等效半径（取半宽+半高的均值方向投影）
    // rect 实际尺寸 ~70×50，等效半径约 35-40
    offset = m.radius;
  } else if (m.shape === 'hex') {
    offset = m.radius;
  } else if (m.shape === 'diamond') {
    offset = m.radius * 0.85; // 菱形角点略远
  } else if (m.shape === 'tri') {
    offset = m.radius * 0.9;
  } else {
    offset = m.radius;
  }
  return {
    x: x + (dx / len) * offset,
    y: y + (dy / len) * offset,
  };
}

// ── 连线类型选择模式（左侧“连线种类”面板选择）──
// 'auto' 表示按设备角色自动推导类型；其余为手动指定的具体连线类型。
export type LinkTypeMode = 'auto' | LinkType;

export const LINK_TYPE_MODES: {
  value: LinkTypeMode;
  label: string;
  color: string;
  dash: number[];
  hint: string;
}[] = [
  { value: 'auto', label: '自动 (Auto)', color: '#1a73e8', dash: [], hint: '按设备角色自动匹配类型（拒绝非法组合）' },
  { value: 'underlay', label: '物理链路', color: '#333333', dash: [], hint: 'Spine↔Leaf / Spine↔Spine 实体互联' },
  { value: 'vxlan', label: 'VXLAN 隧道', color: '#dc3545', dash: [8, 4], hint: 'Leaf↔Leaf Overlay 隧道' },
  { value: 'access', label: '接入链路', color: '#888888', dash: [4, 4], hint: 'Leaf↔Server / Leaf↔PC 接入' },
  { value: 'virtual', label: '虚拟接入', color: '#888888', dash: [4, 4], hint: 'Server↔VM 虚拟连接' },
];
