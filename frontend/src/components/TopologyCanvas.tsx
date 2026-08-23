// TopologyCanvas - 拓扑画布组件
// 迁移自 web/js/topo-canvas.js + web/js/topology-renderer.js
//
// 负责:
//   - 网格/设备/链路/端口的 Canvas 渲染
//   - 鼠标交互: 选中、拖拽设备、画布缩放/平移、连线
//   - 通过 props 通知父组件: 选中变化、设备移动、新增设备/链路
import { useEffect, useRef, useState } from 'react';
import {
  type Topology,
  type Viewport,
  type Device,
  type DeviceType,
  type Link,
  DEVICE_META,
  getDeviceMeta,
  getPortPosition,
  getDirectedPortPosition,
} from '../types';

export type CanvasMode = 'select' | 'add' | 'link';

interface TopologyCanvasProps {
  topology: Topology | null;
  viewport: Viewport;
  onViewportChange: (vp: Viewport) => void;
  selectedDeviceId: string | null;
  onSelectDevice: (deviceId: string | null) => void;
  onDeviceMove?: (deviceId: string, x: number, y: number) => void;
  onPowerDevice?: (deviceId: string, action: 'on' | 'off') => void;
  onAddDeviceAt?: (x: number, y: number, type: DeviceType) => void;
  onCreateLink?: (srcId: string, srcPort: string, dstId: string, dstPort: string) => void;
  onBackgroundClick?: () => void;
  onLinkChange?: (linkId: string, fields: Partial<Link>) => void;
  onLinkPersist?: (linkId: string, fields: Partial<Link>) => void;
  selectedLinkId?: string | null;
  onSelectLink?: (linkId: string | null) => void;
  onDeleteLink?: (linkId: string) => void;
  // 双击设备或右键「查看详情」时打开该设备的浮动窗口
  onOpenDevice?: (deviceId: string) => void;
  // Ping 测试时高亮显示源/目标设备
  highlightDeviceIds?: string[];
  mode: CanvasMode;
  addType: DeviceType | null;
  linkSource: { deviceId: string; port: string } | null;
  onLinkSourceChange: (src: { deviceId: string; port: string } | null) => void;
}

interface DragState {
  kind: 'device' | 'pan' | 'portlabel';
  deviceId?: string;
  linkId?: string;
  end?: 'src' | 'dst';
  lastX: number;
  lastY: number;
  baseDx?: number; // 拖拽开始时该标签的偏移量
  baseDy?: number;
}

interface HitPortResult {
  device: Device;
  port: string;
}

function hitTestDevice(devices: Device[], wx: number, wy: number): Device | null {
  for (let i = devices.length - 1; i >= 0; i--) {
    const d = devices[i];
    const m = getDeviceMeta(d.type);
    const x = Number(d.position_x) || 0;
    const y = Number(d.position_y) || 0;
    if (m.shape === 'circle') {
      const dx = wx - x, dy = wy - y;
      const r = m.radius;
      if (dx * dx + dy * dy <= r * r) return d;
    } else if (m.shape === 'rect') {
      if (wx >= x - 35 && wx <= x + 35 && wy >= y - 25 && wy <= y + 25) return d;
    } else if (m.shape === 'cloud') {
      if (wx >= x - 40 && wx <= x + 40 && wy >= y - 20 && wy <= y + 20) return d;
    } else if (m.shape === 'diamond') {
      const dx = Math.abs(wx - x), dy = Math.abs(wy - y);
      if (dx / 40 + dy / 30 <= 1) return d;
    } else if (m.shape === 'hex') {
      const dx = Math.abs(wx - x), dy = Math.abs(wy - y);
      if (dx / 35 + dy / 30 <= 1) return d;
    } else if (m.shape === 'tri') {
      if (wx >= x - 32 && wx <= x + 32 && wy >= y - 28 && wy <= y + 22) {
        const ry = wy - (y - 28);
        if (Math.abs(wx - x) <= (ry * 32) / 50) return d;
      }
    }
  }
  return null;
}

function hitTestPort(devices: Device[], wx: number, wy: number): HitPortResult | null {
  for (let i = devices.length - 1; i >= 0; i--) {
    const d = devices[i];
    const ifs = Object.keys(d.interfaces || {});
    for (const ifName of ifs) {
      const pos = getPortPosition(d, ifName);
      const dx = wx - pos.x, dy = wy - pos.y;
      if (dx * dx + dy * dy <= 25) return { device: d, port: ifName };
    }
  }
  return null;
}

// 为设备分配下一个可用接口：按接口名末尾数字序号升序，取最小未占用者。
// 例：已有 10GE1/0/1~10GE1/0/3 被占用时，返回 10GE1/0/4。
function parsePortIndex(name: string): number {
  const m = name.match(/(\d+)\s*$/);
  return m ? parseInt(m[1], 10) : 0;
}

// —— 链路中点网段标签：由两端接口 IP + 掩码计算共同子网（如 192.168.2.0/24）——
function ipv4ToInt(ip: string): number | null {
  const parts = ip.split('.');
  if (parts.length !== 4) return null;
  let n = 0;
  for (const p of parts) {
    const v = parseInt(p, 10);
    if (Number.isNaN(v) || v < 0 || v > 255) return null;
    n = (n << 8) | v;
  }
  return n >>> 0;
}

function maskToPrefix(mask: string): number {
  const n = ipv4ToInt(mask);
  if (n === null) return 24;
  let cnt = 0;
  for (let i = 0; i < 32; i++) if ((n >> (31 - i)) & 1) cnt++;
  return cnt;
}

function intToIpv4(n: number): string {
  return `${(n >>> 24) & 255}.${(n >>> 16) & 255}.${(n >>> 8) & 255}.${n & 255}`;
}

// 给定一个或多个 (ip, mask) 对，计算共同子网标签；无有效 IP 返回 ''。
function subnetLabelFromIps(pairs: Array<{ ip: string; mask: string }>): string {
  const valid = pairs.filter((p) => p.ip && /^\d+\.\d+\.\d+\.\d+$/.test(p.ip));
  if (valid.length === 0) return '';
  const nets = valid.map((p) => {
    const ip = ipv4ToInt(p.ip)!;
    const prefix = maskToPrefix(p.mask);
    const maskInt = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0;
    return { net: (ip & maskInt) >>> 0, prefix };
  });
  // 取第一个有效接口的掩码长度；若各接口不一致，用最短掩码（最大网段）保证同网段可并
  const prefix = Math.min(...nets.map((x) => x.prefix));
  const maskInt = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0;
  // 两端 IP 若在同一网段则合并；否则按源端网段显示
  const net = (nets[0].net & maskInt) >>> 0;
  return `${intToIpv4(net)}/${prefix}`;
}


function nextAvailablePort(device: Device, links: Link[]): string {
  const ifs = Object.keys(device.interfaces || {});
  if (ifs.length === 0) return '-';
  const used = new Set<string>();
  for (const l of links) {
    if (l.source_device === device.id) used.add(l.source_port);
    if (l.target_device === device.id) used.add(l.target_port);
  }
  const sorted = ifs.slice().sort((a, b) => parsePortIndex(a) - parsePortIndex(b));
  for (const p of sorted) if (!used.has(p)) return p;
  return ifs[0];
}

function pointToLineDistance(px: number, py: number, x1: number, y1: number, x2: number, y2: number): number {
  const A = px - x1, B = py - y1, C = x2 - x1, D = y2 - y1;
  const dot = A * C + B * D;
  const lenSq = C * C + D * D;
  let param = -1;
  if (lenSq !== 0) param = dot / lenSq;
  let xx: number, yy: number;
  if (param < 0) {
    xx = x1; yy = y1;
  } else if (param > 1) {
    xx = x2; yy = y2;
  } else {
    xx = x1 + param * C;
    yy = y1 + param * D;
  }
  const dx = px - xx, dy = py - yy;
  return Math.sqrt(dx * dx + dy * dy);
}

// 找最近的连线（命中区域）。从上层（数组末尾，后绘制的）往下遍历，
// 重叠时显示在上层的优先；返回阈值内、垂直距离最短的连线。
function findNearestEdge(
  wx: number,
  wy: number,
  devices: Device[],
  links: Link[],
  threshold = 10,
): Link | null {
  let best: Link | null = null;
  let bestDist = Infinity;
  for (let i = links.length - 1; i >= 0; i--) {
    const l = links[i];
    const s = devices.find((d) => d.id === l.source_device);
    const t = devices.find((d) => d.id === l.target_device);
    if (!s || !t) continue;
    const sp = getLinkEndpoint(s, t);
    const tp = getLinkEndpoint(t, s);
    const d = pointToLineDistance(wx, wy, sp.x, sp.y, tp.x, tp.y);
    // 严格小于：保证重叠时上层（先命中）优先；更近的连线覆盖更远的
    if (d <= threshold && d < bestDist) {
      best = l;
      bestDist = d;
    }
  }
  return best;
}

function hitTestLink(devices: Device[], links: Link[], wx: number, wy: number): Link | null {
  return findNearestEdge(wx, wy, devices, links, 10);
}

// 端口标签中心（世界坐标）：源/目标端贴在连线端点旁、垂直于连线方向偏移 16px，
// 再叠加用户拖拽产生的偏移（source_label_dx/dy 等）。
function portLabelCenter(
  l: Link, sp: { x: number; y: number }, tp: { x: number; y: number }, end: 'src' | 'dst',
): { x: number; y: number } {
  const angle = Math.atan2(tp.y - sp.y, tp.x - sp.x);
  const perp = angle + Math.PI / 2;
  const off = 16;
  const base = end === 'src' ? sp : tp;
  const dx = (end === 'src' ? l.source_label_dx : l.target_label_dx) || 0;
  const dy = (end === 'src' ? l.source_label_dy : l.target_label_dy) || 0;
  return { x: base.x + off * Math.cos(perp) + dx, y: base.y + off * Math.sin(perp) + dy };
}

// 命中测试：判断点击是否落在某个端口标签上（用于拖拽）。返回被命中的链路与端点。
type PortLabelHit = { linkId: string; end: 'src' | 'dst' } | null;
function hitTestPortLabel(links: Link[], devices: Device[], wx: number, wy: number): PortLabelHit {
  for (let i = links.length - 1; i >= 0; i--) {
    const l = links[i];
    // 仅对带物理端口的链路显示端口标签，与 drawLinks 保持一致
    const s = devices.find((d) => d.id === l.source_device);
    const t = devices.find((d) => d.id === l.target_device);
    if (!s || !t) continue;
    const hasVxlan = !!l.vxlan_vni && l.vxlan_vni > 0;
    const sHasPort = !!(l.source_port && l.source_port !== '-' && s.interfaces && s.interfaces[l.source_port]);
    const tHasPort = !!(l.target_port && l.target_port !== '-' && t.interfaces && t.interfaces[l.target_port]);
    if (hasVxlan || !sHasPort || !tHasPort) continue;
    const sp = getLinkEndpoint(s, t);
    const tp = getLinkEndpoint(t, s);
    for (const end of ['src', 'dst'] as const) {
      const c = portLabelCenter(l, sp, tp, end);
      const label = end === 'src' ? l.source_port : l.target_port;
      const w = (label ? Array.from(label).length : 4) * 5.5 + 6;
      const h = 14;
      if (Math.abs(wx - c.x) <= w / 2 + 3 && Math.abs(wy - c.y) <= h / 2 + 3) {
        return { linkId: l.id, end };
      }
    }
  }
  return null;
}

export default function TopologyCanvas(props: TopologyCanvasProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  // 用 ref 保存最新的 props，避免 rAF 循环依赖频繁变化
  const propsRef = useRef(props);
  propsRef.current = props;

  // 鼠标位置（屏幕坐标），用于绘制 link 模式下的橡皮筋线
  const mouseRef = useRef<{ x: number; y: number } | null>(null);
  const dragRef = useRef<DragState | null>(null);

  // 右键菜单状态：容器坐标 + 目标 linkId 或 deviceId（二选一）
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; linkId?: string; deviceId?: string } | null>(null);

  // 自适应画布大小
  useEffect(() => {
    const canvas = canvasRef.current;
    const container = containerRef.current;
    if (!canvas || !container) return;

    const resize = () => {
      const r = container.getBoundingClientRect();
      const newW = Math.max(1, Math.floor(r.width));
      const newH = Math.max(1, Math.floor(r.height));
      if (canvas.width === newW && canvas.height === newH) return;
      // 保持画布中心的世界坐标不变
      const vp = propsRef.current.viewport;
      const cx = canvas.width / 2, cy = canvas.height / 2;
      const wx = (cx - vp.ox) / vp.scale, wy = (cy - vp.oy) / vp.scale;
      canvas.width = newW;
      canvas.height = newH;
      propsRef.current.onViewportChange({
        scale: vp.scale,
        ox: newW / 2 - wx * vp.scale,
        oy: newH / 2 - wy * vp.scale,
      });
    };

    resize();
    const ro = new ResizeObserver(resize);
    ro.observe(container);
    return () => ro.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // rAF 渲染循环
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    let raf = 0;
    const render = () => {
      draw(ctx, canvas, propsRef.current, mouseRef.current);
      raf = requestAnimationFrame(render);
    };
    raf = requestAnimationFrame(render);
    return () => cancelAnimationFrame(raf);
  }, []);

  // ---- 鼠标事件 ----
  const onMouseDown = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const p = propsRef.current;
    // 任何点击先关闭右键菜单
    setCtxMenu(null);
    const canvas = canvasRef.current!;
    const rect = canvas.getBoundingClientRect();
    const vp = p.viewport;
    const wx = (e.clientX - rect.left - vp.ox) / vp.scale;
    const wy = (e.clientY - rect.top - vp.oy) / vp.scale;
    const devices = p.topology ? Object.values(p.topology.devices) : [];
    const links = p.topology ? p.topology.links : [];

    if (e.button === 0) {
      if (p.mode === 'add' && p.addType) {
        const hit = hitTestDevice(devices, wx, wy);
        if (!hit) p.onAddDeviceAt?.(wx, wy, p.addType);
        return;
      }
      if (p.mode === 'link') {
        // 拖拽连线：按下源设备即记录拖拽起点（橡皮筋预览由 drawLinkPreview 绘制），
        // 在目标设备上松开鼠标时由 onMouseUp 创建连线。
        const hit = hitTestDevice(devices, wx, wy);
        if (hit) {
          p.onLinkSourceChange({ deviceId: hit.id, port: nextAvailablePort(hit, links) });
        } else {
          p.onLinkSourceChange(null);
        }
        return;
      }
      // select 模式
      // 先检测是否点中端口标签（端口名），命中则进入标签拖拽
      const plHit = hitTestPortLabel(links, devices, wx, wy);
      if (plHit) {
        const lk = links.find((x) => x.id === plHit.linkId);
        const baseDx = plHit.end === 'src' ? (lk?.source_label_dx || 0) : (lk?.target_label_dx || 0);
        const baseDy = plHit.end === 'src' ? (lk?.source_label_dy || 0) : (lk?.target_label_dy || 0);
        dragRef.current = {
          kind: 'portlabel', linkId: plHit.linkId, end: plHit.end,
          lastX: e.clientX, lastY: e.clientY, baseDx, baseDy,
        };
        return;
      }
      const hit = hitTestDevice(devices, wx, wy);
      if (hit) {
        p.onSelectDevice(hit.id);
        p.onSelectLink?.(null);
        dragRef.current = { kind: 'device', deviceId: hit.id, lastX: e.clientX, lastY: e.clientY };
      } else {
        // 未命中设备：尝试命中连线（select 模式下可选中删除）
        const link = hitTestLink(devices, links, wx, wy);
        if (link) {
          p.onSelectLink?.(link.id);
          p.onSelectDevice(null);
          return; // 选中连线，不进入平移拖拽
        }
        p.onSelectLink?.(null);
        p.onSelectDevice(null);
        p.onBackgroundClick?.();
        dragRef.current = { kind: 'pan', lastX: e.clientX, lastY: e.clientY };
      }
      return;
    }
  };

  // 右键菜单：在连线上右键弹出「删除连线」
  const onContextMenu = (e: React.MouseEvent<HTMLCanvasElement>) => {
    e.preventDefault();
    setCtxMenu(null);
    const p = propsRef.current;
    if (p.mode !== 'select') return;
    const canvas = canvasRef.current!;
    const container = containerRef.current;
    const crect = container ? container.getBoundingClientRect() : canvas.getBoundingClientRect();
    const rect = canvas.getBoundingClientRect();
    const vp = p.viewport;
    const wx = (e.clientX - rect.left - vp.ox) / vp.scale;
    const wy = (e.clientY - rect.top - vp.oy) / vp.scale;
    const devices = p.topology ? Object.values(p.topology.devices) : [];
    const links = p.topology ? p.topology.links : [];
    // 优先命中设备：右键「查看详情」打开浮动窗口
    const dev = hitTestDevice(devices, wx, wy);
    if (dev) {
      p.onSelectDevice?.(dev.id);
      p.onSelectLink?.(null);
      setCtxMenu({
        x: e.clientX - crect.left,
        y: e.clientY - crect.top,
        deviceId: dev.id,
      });
      return;
    }
    const link = hitTestLink(devices, links, wx, wy);
    if (link) {
      p.onSelectLink?.(link.id);
      p.onSelectDevice(null);
      setCtxMenu({
        x: e.clientX - crect.left,
        y: e.clientY - crect.top,
        linkId: link.id,
      });
    }
  };

  // 双击设备：打开/聚焦该设备的浮动窗口（类 eNSP 双击设备弹出配置窗口）
  const onDoubleClick = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const p = propsRef.current;
    if (p.mode !== 'select') return;
    const canvas = canvasRef.current!;
    const rect = canvas.getBoundingClientRect();
    const vp = p.viewport;
    const wx = (e.clientX - rect.left - vp.ox) / vp.scale;
    const wy = (e.clientY - rect.top - vp.oy) / vp.scale;
    const devices = p.topology ? Object.values(p.topology.devices) : [];
    const hit = hitTestDevice(devices, wx, wy);
    if (hit) p.onOpenDevice?.(hit.id);
  };

  const onMouseMove = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current!;
    const rect = canvas.getBoundingClientRect();
    mouseRef.current = { x: e.clientX - rect.left, y: e.clientY - rect.top };

    const drag = dragRef.current;
    if (!drag) {
      // 非拖拽态：悬停在端口标签上时光标变为 move，提示可拖拽
      if (propsRef.current.mode === 'select') {
        const p = propsRef.current;
        const vp = p.viewport;
        const wx = (e.clientX - rect.left - vp.ox) / vp.scale;
        const wy = (e.clientY - rect.top - vp.oy) / vp.scale;
        const devices = p.topology ? Object.values(p.topology.devices) : [];
        const links = p.topology ? p.topology.links : [];
        const overLabel = hitTestPortLabel(links, devices, wx, wy);
        const overLink = !overLabel && hitTestLink(devices, links, wx, wy);
        canvas.style.cursor = overLabel ? 'move' : overLink ? 'pointer' : '';
      }
      return;
    }
    const p = propsRef.current;
    const dx = e.clientX - drag.lastX;
    const dy = e.clientY - drag.lastY;
    drag.lastX = e.clientX;
    drag.lastY = e.clientY;

    if (drag.kind === 'pan') {
      const vp = p.viewport;
      p.onViewportChange({ ...vp, ox: vp.ox + dx, oy: vp.oy + dy });
    } else if (drag.kind === 'device' && drag.deviceId) {
      const t = p.topology;
      if (!t) return;
      const dev = t.devices[drag.deviceId];
      if (!dev) return;
      const newX = (Number(dev.position_x) || 0) + dx / p.viewport.scale;
      const newY = (Number(dev.position_y) || 0) + dy / p.viewport.scale;
      p.onDeviceMove?.(drag.deviceId, newX, newY);
    } else if (drag.kind === 'portlabel' && drag.linkId && drag.end) {
      // 累计偏移（baseDx/baseDy 在拖拽中不断累加每帧位移）
      drag.baseDx = (drag.baseDx || 0) + dx / p.viewport.scale;
      drag.baseDy = (drag.baseDy || 0) + dy / p.viewport.scale;
      const fields = drag.end === 'src'
        ? { source_label_dx: drag.baseDx, source_label_dy: drag.baseDy }
        : { target_label_dx: drag.baseDx, target_label_dy: drag.baseDy };
      p.onLinkChange?.(drag.linkId, fields);
    }
  };

  const onMouseUp = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const drag = dragRef.current;
    if (drag && drag.kind === 'portlabel' && drag.linkId && drag.end) {
      const p = propsRef.current;
      const fields = drag.end === 'src'
        ? { source_label_dx: drag.baseDx || 0, source_label_dy: drag.baseDy || 0 }
        : { target_label_dx: drag.baseDx || 0, target_label_dy: drag.baseDy || 0 };
      p.onLinkPersist?.(drag.linkId, fields);
    }
    dragRef.current = null;

    // 拖拽连线：从源设备拖到目标设备松开即创建（Auto 模式下由父组件按约束推导类型）
    const p = propsRef.current;
    if (p.mode === 'link' && p.linkSource) {
      const canvas = canvasRef.current!;
      const rect = canvas.getBoundingClientRect();
      const vp = p.viewport;
      const wx = (e.clientX - rect.left - vp.ox) / vp.scale;
      const wy = (e.clientY - rect.top - vp.oy) / vp.scale;
      const devs = p.topology ? Object.values(p.topology.devices) : [];
      const lks = p.topology ? p.topology.links : [];
      const hit = hitTestDevice(devs, wx, wy);
      if (hit && hit.id !== p.linkSource.deviceId) {
        p.onCreateLink?.(p.linkSource.deviceId, p.linkSource.port, hit.id, nextAvailablePort(hit, lks));
      }
      p.onLinkSourceChange(null);
    }
  };

  const onMouseLeave = () => {
    mouseRef.current = null;
    dragRef.current = null;
  };

  // ── 平滑滚轮缩放 ──
  // React 合成 onWheel 默认是 passive，preventDefault 可能无效（页面会跟着滚），
  // 且固定 ±10% 步长对触摸板不友好。改为：原生非 passive 监听 + rAF 合并多次事件，
  // 用指数因子按 deltaY 比例缩放，鼠标滚轮与触摸板都顺滑。
  const wheelRef = useRef<{ accum: number; raf: number; lastX: number; lastY: number; deltaMode: number }>({
    accum: 0,
    raf: 0,
    lastX: 0,
    lastY: 0,
    deltaMode: 0,
  });
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const handleWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = canvas.getBoundingClientRect();
      const w = wheelRef.current;
      w.lastX = e.clientX - rect.left;
      w.lastY = e.clientY - rect.top;
      w.deltaMode = e.deltaMode;
      w.accum += e.deltaY;
      if (w.raf) return;
      w.raf = requestAnimationFrame(() => {
        w.raf = 0;
        const acc = w.accum;
        w.accum = 0;
        const p = propsRef.current;
        const vp = p.viewport;
        // 把不同 deltaMode（像素/行/页）统一换算成"像素当量"
        let dy = acc;
        if (w.deltaMode === 1) dy *= 16;
        else if (w.deltaMode === 2) dy *= window.innerHeight;
        // 指数缩放：与 deltaY 成正比，平滑且对触摸板小步长友好
        const factor = Math.exp(-dy * 0.0011);
        const ns = Math.max(0.2, Math.min(5, vp.scale * factor));
        const wx = (w.lastX - vp.ox) / vp.scale;
        const wy = (w.lastY - vp.oy) / vp.scale;
        p.onViewportChange({ scale: ns, ox: w.lastX - wx * ns, oy: w.lastY - wy * ns });
      });
    };
    canvas.addEventListener('wheel', handleWheel, { passive: false });
    return () => {
      canvas.removeEventListener('wheel', handleWheel);
      if (wheelRef.current.raf) cancelAnimationFrame(wheelRef.current.raf);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ── 键盘删除连线 ──
  // 选中连线后按 Delete / Backspace 删除。window 级监听，保证画布聚焦与否都能触发；
  // 当焦点在输入框/文本域（如 CLI 终端）时不拦截，避免误删。
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
      if (e.key === 'Delete' || e.key === 'Backspace') {
        const p = propsRef.current;
        if (p.selectedLinkId) {
          e.preventDefault();
          p.onDeleteLink?.(p.selectedLinkId);
        }
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 拖拽设备类型到画布
  const onDrop = (e: React.DragEvent<HTMLCanvasElement>) => {
    e.preventDefault();
    const type = e.dataTransfer.getData('deviceType') as DeviceType;
    if (!type) return;
    const p = propsRef.current;
    const canvas = canvasRef.current!;
    const rect = canvas.getBoundingClientRect();
    const vp = p.viewport;
    const wx = (e.clientX - rect.left - vp.ox) / vp.scale;
    const wy = (e.clientY - rect.top - vp.oy) / vp.scale;
    p.onAddDeviceAt?.(wx, wy, type);
  };

  const onDragOver = (e: React.DragEvent<HTMLCanvasElement>) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
  };

  return (
    <div ref={containerRef} className="topo-canvas-wrap">
      <canvas
        ref={canvasRef}
        className={`topo-canvas mode-${props.mode}`}
        onMouseDown={onMouseDown}
        onMouseMove={onMouseMove}
        onMouseUp={onMouseUp}
        onMouseLeave={onMouseLeave}
        onDoubleClick={onDoubleClick}
        onDrop={onDrop}
        onDragOver={onDragOver}
        onContextMenu={onContextMenu}
      />
      {ctxMenu && (
        <div className="link-ctx-menu" style={{ left: ctxMenu.x, top: ctxMenu.y }}>
          {ctxMenu.deviceId && (
            <button
              type="button"
              className="link-ctx-item"
              onClick={() => {
                props.onOpenDevice?.(ctxMenu.deviceId!);
                setCtxMenu(null);
              }}
            >
              查看详情
            </button>
          )}
          {ctxMenu.linkId && (
            <button
              type="button"
              className="link-ctx-item danger"
              onClick={() => {
                props.onDeleteLink?.(ctxMenu.linkId!);
                setCtxMenu(null);
              }}
            >
              删除连线
            </button>
          )}
        </div>
      )}
    </div>
  );
}

// ---- 渲染逻辑 ----
function draw(
  ctx: CanvasRenderingContext2D,
  canvas: HTMLCanvasElement,
  p: TopologyCanvasProps,
  mouse: { x: number; y: number } | null,
) {
  const w = canvas.width, h = canvas.height;
  const vp = p.viewport;
  ctx.clearRect(0, 0, w, h);
  ctx.fillStyle = '#f8f9fa';
  ctx.fillRect(0, 0, w, h);

  ctx.save();
  ctx.translate(vp.ox, vp.oy);
  ctx.scale(vp.scale, vp.scale);

  drawGrid(ctx, w, h, vp);

  const devices = p.topology ? Object.values(p.topology.devices) : [];
  const links = p.topology ? p.topology.links : [];

  drawLinks(ctx, links, devices, p.selectedLinkId ?? null);
  drawLinkPreview(ctx, p, mouse, devices);
  drawDevices(ctx, devices, p.selectedDeviceId, new Set(p.highlightDeviceIds || []));

  ctx.restore();
}

function drawGrid(ctx: CanvasRenderingContext2D, w: number, h: number, vp: Viewport) {
  ctx.strokeStyle = '#e8e8e8';
  ctx.lineWidth = 0.5 / vp.scale;
  const startX = -vp.ox / vp.scale - 50;
  const endX = (w - vp.ox) / vp.scale + 50;
  const startY = -vp.oy / vp.scale - 50;
  const endY = (h - vp.oy) / vp.scale + 50;
  ctx.beginPath();
  for (let x = Math.floor(startX / 50) * 50; x < endX; x += 50) {
    ctx.moveTo(x, startY);
    ctx.lineTo(x, endY);
  }
  for (let y = Math.floor(startY / 50) * 50; y < endY; y += 50) {
    ctx.moveTo(startX, y);
    ctx.lineTo(endX, y);
  }
  ctx.stroke();
}

// ── 连线端点定位（统一使用方向感知）─────────────────────────────
// 所有链路（物理端口 / VXLAN 隧道 / 虚拟链路）均从设备边框朝对端方向引出，
// 彻底解决"连线悬浮在设备外"的问题，达到 eNSP 的连线标准。
function getLinkEndpoint(device: Device, peerDevice: Device): { x: number; y: number } {
  return getDirectedPortPosition(
    device,
    Number(peerDevice.position_x) || 0,
    Number(peerDevice.position_y) || 0,
  );
}

function drawLinks(ctx: CanvasRenderingContext2D, links: Link[], devices: Device[], selLinkId: string | null) {
  for (const l of links) {
    const s = devices.find((d) => d.id === l.source_device);
    const t = devices.find((d) => d.id === l.target_device);
    if (!s || !t) continue;

    // ── 核心修复：所有链路端点均用方向感知定位 ──
    const sp = getLinkEndpoint(s, t); // 从源设备边框（朝向目标）引出
    const tp = getLinkEndpoint(t, s); // 从目标设备边框（朝向源）引出

    let color = '#333';
    let dash: number[] = [];
    let label = '';
    let showPortLabels = true;
    let lineWidth = 2;

    // 优先按 link_type 决定样式（与约束矩阵一致）；兼容旧的 vxlan_vni/vlan 字段
    const lt = l.link_type;
    const isSemantic = lt === 'underlay' || lt === 'vxlan' || lt === 'access' || lt === 'virtual';
    if (lt === 'vxlan' || (!isSemantic && l.vxlan_vni && l.vxlan_vni > 0)) {
      color = '#dc3545';
      dash = [8, 4];
      label = l.vxlan_vni && l.vxlan_vni > 0 ? `VNI ${l.vxlan_vni}` : 'VXLAN';
      showPortLabels = false;
    } else if (lt === 'virtual') {
      // 虚拟链路（如 VXLAN server↔vm）→ 虚线 + "虚拟接入"
      color = '#888';
      dash = [4, 4];
      label = '虚拟接入';
      showPortLabels = true;
    } else if (lt === 'access' && l.vlan && l.vlan > 0) {
      // Access 且有 VLAN 标记 → 虚线 + "VLAN X"
      color = '#888';
      dash = [4, 4];
      label = `VLAN ${l.vlan}`;
      showPortLabels = true;
    } else if (!isSemantic && l.vlan && l.vlan > 0) {
      // 旧版 vlan 字段无 link_type
      color = '#888';
      dash = [4, 4];
      label = `VLAN ${l.vlan}`;
      showPortLabels = true;
    } else {
      // underlay / business / 默认 → 物理链路（实线，黑色），中点标注两端共同子网
      color = '#333';
      dash = [];
      // 数据驱动优先：链路自带的 subnet 字段（覆盖两端无 IP 的 SW↔SW 链路场景）；
      // 兜底再走算法从两端 IP 算。
      const linkSubnet = (l as { subnet?: string }).subnet || '';
      const computedSubnet = subnetLabelFromIps([
        { ip: s.interfaces?.[l.source_port]?.ip_address || '', mask: s.interfaces?.[l.source_port]?.subnet_mask || '' },
        { ip: t.interfaces?.[l.target_port]?.ip_address || '', mask: t.interfaces?.[l.target_port]?.subnet_mask || '' },
      ]);
      label = linkSubnet || computedSubnet;
      showPortLabels = true;
    }

    // 无物理端口（VXLAN/虚拟链路）不绘制端口编号标签
    const sHasPort = !!(l.source_port && l.source_port !== '-' && s.interfaces && s.interfaces[l.source_port]);
    const tHasPort = !!(l.target_port && l.target_port !== '-' && t.interfaces && t.interfaces[l.target_port]);
    showPortLabels = showPortLabels && sHasPort && tHasPort;

    if (l.status !== 'up') {
      color = '#999';
    }

    const isSelected = l.id === selLinkId;
    if (isSelected) {
      color = '#e53935';
      lineWidth = 4;
    }

    // 选中连线：先画一层半透明红色光晕，增强高亮
    if (isSelected) {
      ctx.beginPath();
      ctx.moveTo(sp.x, sp.y);
      ctx.lineTo(tp.x, tp.y);
      ctx.strokeStyle = 'rgba(229,57,53,0.25)';
      ctx.lineWidth = 10;
      ctx.stroke();
    }

    ctx.beginPath();
    ctx.moveTo(sp.x, sp.y);
    ctx.lineTo(tp.x, tp.y);
    ctx.strokeStyle = color;
    ctx.lineWidth = lineWidth;
    if (dash.length > 0) ctx.setLineDash(dash);
    ctx.stroke();
    ctx.setLineDash([]);

    const angle = Math.atan2(tp.y - sp.y, tp.x - sp.x);

    // 箭头
    const ax = tp.x - 15 * Math.cos(angle);
    const ay = tp.y - 15 * Math.sin(angle);
    ctx.beginPath();
    ctx.moveTo(ax, ay);
    ctx.lineTo(ax - 8 * Math.cos(angle - 0.5), ay - 8 * Math.sin(angle - 0.5));
    ctx.lineTo(ax - 8 * Math.cos(angle + 0.5), ay - 8 * Math.sin(angle + 0.5));
    ctx.closePath();
    ctx.fillStyle = color;
    ctx.fill();

    // 接口标签（附着在连线端点附近——紧贴设备边框，带背景色确保可读）
    if (showPortLabels) {
      ctx.font = 'bold 9px sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';

      const srcLabel = `${l.source_port}`;
      const dstLabel = `${l.target_port}`;

      const perpAngle = angle + Math.PI / 2;
      const labelOffset = 16;

      // 源端接口名（带浅色背景），叠加用户拖拽偏移
      const srcLabelX = sp.x + labelOffset * Math.cos(perpAngle) + (l.source_label_dx || 0);
      const srcLabelY = sp.y + labelOffset * Math.sin(perpAngle) + (l.source_label_dy || 0);
      const srcW = ctx.measureText(srcLabel).width + 6;
      ctx.fillStyle = 'rgba(255,255,255,0.85)';
      ctx.fillRect(srcLabelX - srcW / 2, srcLabelY - 7, srcW, 14);
      ctx.strokeStyle = '#ccc';
      ctx.lineWidth = 0.5;
      ctx.strokeRect(srcLabelX - srcW / 2, srcLabelY - 7, srcW, 14);
      ctx.fillStyle = '#1a73e8';
      ctx.fillText(srcLabel, srcLabelX, srcLabelY);

      // 目标端接口名（带浅色背景），叠加用户拖拽偏移
      const dstLabelX = tp.x + labelOffset * Math.cos(perpAngle) + (l.target_label_dx || 0);
      const dstLabelY = tp.y + labelOffset * Math.sin(perpAngle) + (l.target_label_dy || 0);
      const dstW = ctx.measureText(dstLabel).width + 6;
      ctx.fillStyle = 'rgba(255,255,255,0.85)';
      ctx.fillRect(dstLabelX - dstW / 2, dstLabelY - 7, dstW, 14);
      ctx.strokeStyle = '#ccc';
      ctx.lineWidth = 0.5;
      ctx.strokeRect(dstLabelX - dstW / 2, dstLabelY - 7, dstW, 14);
      ctx.fillStyle = '#d32f2f';
      ctx.fillText(dstLabel, dstLabelX, dstLabelY);
    }

    // 中点标签（VXLAN VNI 或 VLAN）
    const midX = (sp.x + tp.x) / 2, midY = (sp.y + tp.y) / 2;
    if (label) {
      ctx.font = 'bold 10px sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillStyle = color;
      ctx.fillText(label, midX, midY);
    }
  }
}

function drawLinkPreview(
  ctx: CanvasRenderingContext2D,
  p: TopologyCanvasProps,
  mouse: { x: number; y: number } | null,
  devices: Device[],
) {
  const linkSource = p.linkSource;
  if (!linkSource || !mouse) return;
  const src = devices.find((d) => d.id === linkSource.deviceId);
  if (!src) return;
  const sp = getPortPosition(src, linkSource.port);
  const vp = p.viewport;
  const mx = (mouse.x - vp.ox) / vp.scale;
  const my = (mouse.y - vp.oy) / vp.scale;
  ctx.beginPath();
  ctx.moveTo(sp.x, sp.y);
  ctx.lineTo(mx, my);
  ctx.strokeStyle = '#1a73e8';
  ctx.lineWidth = 2 / vp.scale;
  ctx.setLineDash([5 / vp.scale, 5 / vp.scale]);
  ctx.stroke();
  ctx.setLineDash([]);
}

function drawDevices(ctx: CanvasRenderingContext2D, devices: Device[], selId: string | null, highlightIds: Set<string>) {
  for (const d of devices) {
    const m = getDeviceMeta(d.type);
    const x = Number(d.position_x) || 0;
    const y = Number(d.position_y) || 0;
    const highlighted = highlightIds.has(d.id);
    ctx.fillStyle = m.bg;
    ctx.strokeStyle = selId === d.id ? '#1a73e8' : highlighted ? '#ff9800' : m.color;
    ctx.lineWidth = selId === d.id ? 3 : highlighted ? 4 : 2;

    if (m.shape === 'circle') {
      ctx.beginPath();
      ctx.arc(x, y, 35, 0, Math.PI * 2);
      ctx.fill();
      ctx.stroke();
      if (highlighted) {
        ctx.beginPath();
        ctx.arc(x, y, 42, 0, Math.PI * 2);
        ctx.strokeStyle = 'rgba(255, 152, 0, 0.6)';
        ctx.lineWidth = 2;
        ctx.stroke();
      }
    } else if (m.shape === 'rect') {
      roundRect(ctx, x - 35, y - 25, 70, 50, 8);
      ctx.fill();
      ctx.stroke();
      if (highlighted) {
        ctx.save();
        roundRect(ctx, x - 41, y - 31, 82, 62, 11);
        ctx.strokeStyle = 'rgba(255, 152, 0, 0.6)';
        ctx.lineWidth = 2;
        ctx.stroke();
        ctx.restore();
      }
    } else if (m.shape === 'cloud') {
      roundRect(ctx, x - 40, y - 20, 80, 40, 10);
      ctx.fill();
      ctx.stroke();
      if (highlighted) {
        ctx.save();
        roundRect(ctx, x - 46, y - 26, 92, 52, 13);
        ctx.strokeStyle = 'rgba(255, 152, 0, 0.6)';
        ctx.lineWidth = 2;
        ctx.stroke();
        ctx.restore();
      }
    } else if (m.shape === 'diamond') {
      ctx.beginPath();
      ctx.moveTo(x, y - 30);
      ctx.lineTo(x + 40, y);
      ctx.lineTo(x, y + 30);
      ctx.lineTo(x - 40, y);
      ctx.closePath();
      ctx.fill();
      ctx.stroke();
    } else if (m.shape === 'hex') {
      ctx.beginPath();
      for (let i = 0; i < 6; i++) {
        const a = (Math.PI / 3) * i - Math.PI / 6;
        const px = x + 35 * Math.cos(a);
        const py = y + 30 * Math.sin(a);
        if (i === 0) ctx.moveTo(px, py);
        else ctx.lineTo(px, py);
      }
      ctx.closePath();
      ctx.fill();
      ctx.stroke();
    } else if (m.shape === 'tri') {
      ctx.beginPath();
      ctx.moveTo(x, y - 28);
      ctx.lineTo(x + 32, y + 22);
      ctx.lineTo(x - 32, y + 22);
      ctx.closePath();
      ctx.fill();
      ctx.stroke();
    }

    // 设备图标（纯文本）
    ctx.font = 'bold 14px sans-serif';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillStyle = m.color;
    ctx.fillText(m.icon, x, y - 10);

    // 名称
    ctx.fillStyle = '#333';
    ctx.font = 'bold 11px sans-serif';
    ctx.fillText(d.name, x, y + 8);
    ctx.fillStyle = '#666';
    ctx.font = '9px sans-serif';
    ctx.fillText(d.model || '', x, y + 20);

    // 关键 IP 标注（紧贴设备图标下方，eNSP 风格：名称/机型/IP 一体）
    // 优先级：LoopBack0 > 第一个有 IP 的接口
    let ipCaption = '';
    const ifEntries = Object.entries(d.interfaces || {});
    if (d.type === 'switch' || d.type === 'vtep') {
      // Spine/VTEP 优先显示环回口 IP（华为标准：VTEP 用 Loopback 作源地址）
      const loopBack = ifEntries.find(([k]) => k.toLowerCase().startsWith('loop'));
      if (loopBack?.[1]?.ip_address) ipCaption = loopBack[1].ip_address;
    }
    if (!ipCaption) {
      for (const [, ifc] of ifEntries) {
        if (ifc && ifc.ip_address) { ipCaption = ifc.ip_address; break; }
      }
    }
    if (ipCaption) {
      ctx.fillStyle = '#0a66c2';
      ctx.font = '9px sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText(ipCaption, x, y + m.radius + 16);
    }

    // （已移除状态圆点和端口绿点，保持设备图标简洁）
  }
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.lineTo(x + w - r, y);
  ctx.quadraticCurveTo(x + w, y, x + w, y + r);
  ctx.lineTo(x + w, y + h - r);
  ctx.quadraticCurveTo(x + w, y + h, x + w - r, y + h);
  ctx.lineTo(x + r, y + h);
  ctx.quadraticCurveTo(x, y + h, x, y + h - r);
  ctx.lineTo(x, y + r);
  ctx.quadraticCurveTo(x, y, x + r, y);
  ctx.closePath();
}

// 重新导出供其他组件复用
export { hitTestDevice, hitTestLink, hitTestPort, findNearestEdge, getPortPosition, DEVICE_META };
