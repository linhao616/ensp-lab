// PacketAnimator - 数据包动画叠加层
// 迁移自 web/js/topo-canvas.js 中的 renderPackets / addPacket / tickPackets
//
// 视觉效果:
//   - 信封图标 ▶ 飞行
//   - 5 段拖尾
//   - 脉动线宽
//   - 3 层到达脉冲环
//
// 工作方式:
//   - 监听 props.events 中的新增 PacketEvent (type=send)
//   - 根据 event.path 找到拓扑中的链路序列
//   - 用 requestAnimationFrame 推进动画进度并绘制
import { useEffect, useRef } from 'react';
import { type PacketEvent, type Topology, type Viewport, type Device, type Link, getPortPosition } from '../types';

interface PacketAnimatorProps {
  events: PacketEvent[];
  topology: Topology | null;
  viewport: Viewport;
  onDone?: () => void;
}

interface AnimSegment {
  from: { x: number; y: number };
  to: { x: number; y: number };
  dx: number;
  dy: number;
  hopIndex: number;
}

interface Animation {
  id: string;
  packetId: string;
  segments: AnimSegment[];
  currentSeg: number;
  progress: number;
  speed: number;
  color: { main: string; border: string; glow: string };
  startedAt: number;
  protocol: string; // 协议名标注（如 "ICMP"/"TCP"），从 PacketEvent.description 提取
}

// extractProtocol 从 PacketEvent.description 字段中提取协议名。
//
// ns-x 引擎在 makeTransferCallback/makeReact/SendPacket 中生成的 description
// 形如 "Send ICMP to 192.168.1.1" / "Forward ICMP to dev-2" / "Received ICMP"。
// 取第二个空格分隔的 token 作为协议名；若无法识别则返回空串（animator 不绘制标注）。
function extractProtocol(description: string | undefined): string {
  if (!description) return '';
  const parts = description.split(' ');
  if (parts.length < 2) return '';
  const proto = parts[1].toUpperCase();
  // 仅在确认识别的协议名时返回，避免把 "to 192.168.x.x" 误识别为协议
  const known = ['ICMP', 'TCP', 'UDP', 'ARP', 'OSPF', 'BGP', 'VXLAN', 'DNS', 'DHCP', 'HTTP'];
  return known.includes(proto) ? proto : '';
}

function buildSegments(path: string[], devices: Record<string, Device>, links: Link[]): AnimSegment[] {
  if (!path || path.length < 2) return [];
  const segments: AnimSegment[] = [];
  for (let i = 0; i < path.length - 1; i++) {
    const a = path[i], b = path[i + 1];
    const link = links.find(
      (l) => (l.source_device === a && l.target_device === b) || (l.source_device === b && l.target_device === a),
    );
    const devA = devices[a];
    const devB = devices[b];
    if (!devA || !devB || !link) continue;
    let fromPort: string, toPort: string;
    if (link.source_device === a) {
      fromPort = link.source_port;
      toPort = link.target_port;
    } else {
      fromPort = link.target_port;
      toPort = link.source_port;
    }
    const sp = getPortPosition(devA, fromPort);
    const tp = getPortPosition(devB, toPort);
    segments.push({
      from: sp,
      to: tp,
      dx: tp.x - sp.x,
      dy: tp.y - sp.y,
      hopIndex: i,
    });
  }
  return segments;
}

export default function PacketAnimator(props: PacketAnimatorProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const animRef = useRef<Animation[]>([]);
  const processedRef = useRef<Set<string>>(new Set());
  const propsRef = useRef(props);
  propsRef.current = props;

  // 监听新事件
  useEffect(() => {
    const p = propsRef.current;
    if (!p.topology) return;
    for (const ev of p.events) {
      if (ev.type !== 'send') continue;
      if (processedRef.current.has(ev.packet_id)) continue;
      processedRef.current.add(ev.packet_id);
      // 限制已处理集合大小
      if (processedRef.current.size > 500) {
        processedRef.current = new Set([...processedRef.current].slice(-250));
      }
      const segments = buildSegments(ev.path, p.topology.devices, p.topology.links);
      if (segments.length === 0) continue;
      animRef.current.push({
        id: `anim-${ev.packet_id}-${Date.now()}`,
        packetId: ev.packet_id,
        segments,
        currentSeg: 0,
        progress: 0,
        speed: 0.012,
        color: { main: '#ff5722', border: '#bf360c', glow: 'rgba(255,87,34,0.5)' },
        startedAt: Date.now(),
        protocol: extractProtocol(ev.description),
      });
    }
  }, [props.events, props.topology]);

  // 调整 canvas 尺寸
  useEffect(() => {
    const canvas = canvasRef.current;
    const container = containerRef.current;
    if (!canvas || !container) return;
    const resize = () => {
      const r = container.getBoundingClientRect();
      canvas.width = Math.max(1, Math.floor(r.width));
      canvas.height = Math.max(1, Math.floor(r.height));
    };
    resize();
    const ro = new ResizeObserver(resize);
    ro.observe(container);
    return () => ro.disconnect();
  }, []);

  // rAF 渲染循环
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    let raf = 0;
    let lastDone = false;
    const loop = () => {
      const p = propsRef.current;
      const vp = p.viewport;
      // 推进动画
      const now = Date.now();
      const remaining: Animation[] = [];
      for (const a of animRef.current) {
        a.progress += a.speed;
        if (a.progress >= 1) {
          a.currentSeg += 1;
          a.progress = 0;
          if (a.currentSeg >= a.segments.length) {
            // 动画结束
            continue;
          }
        }
        // 30 秒兜底超时
        if (now - a.startedAt > 30_000) continue;
        remaining.push(a);
      }
      animRef.current = remaining;

      ctx.clearRect(0, 0, canvas.width, canvas.height);
      ctx.save();
      ctx.translate(vp.ox, vp.oy);
      ctx.scale(vp.scale, vp.scale);
      for (const a of animRef.current) {
        drawAnimation(ctx, a);
      }
      ctx.restore();

      const done = animRef.current.length === 0;
      if (done && !lastDone) {
        p.onDone?.();
      }
      lastDone = done;

      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(raf);
  }, []);

  return (
    <div ref={containerRef} className="packet-animator-overlay" style={{ pointerEvents: 'none' }}>
      <canvas ref={canvasRef} />
    </div>
  );
}

function drawAnimation(ctx: CanvasRenderingContext2D, a: Animation) {
  const seg = a.segments[a.currentSeg];
  if (!seg) return;
  const t = Math.min(a.progress, 1);
  const x = seg.from.x + seg.dx * t;
  const y = seg.from.y + seg.dy * t;
  const angle = Math.atan2(seg.dy, seg.dx);

  if (t < 0.95) {
    // 5 段拖尾
    const tailLen = 0.25;
    for (let i = 0; i < 5; i++) {
      const tt = t - tailLen * (i / 5);
      if (tt < 0) break;
      const tx = seg.from.x + seg.dx * tt;
      const ty = seg.from.y + seg.dy * tt;
      const alpha = (1 - i / 5) * 0.3;
      ctx.save();
      ctx.translate(tx, ty);
      ctx.rotate(angle);
      ctx.globalAlpha = alpha;
      ctx.fillStyle = a.color.main;
      ctx.beginPath();
      ctx.moveTo(-8, -4);
      ctx.lineTo(8, -4);
      ctx.lineTo(12, 0);
      ctx.lineTo(8, 4);
      ctx.lineTo(-8, 4);
      ctx.closePath();
      ctx.fill();
      ctx.restore();
    }

    // 主包体 (信封 ▶)
    ctx.save();
    ctx.translate(x, y);
    ctx.rotate(angle);
    ctx.shadowColor = a.color.main;
    ctx.shadowBlur = 12;
    ctx.fillStyle = a.color.main;
    ctx.beginPath();
    ctx.moveTo(-10, -6);
    ctx.lineTo(10, -6);
    ctx.lineTo(16, 0);
    ctx.lineTo(10, 6);
    ctx.lineTo(-10, 6);
    ctx.closePath();
    ctx.fill();
    // 脉动线宽
    const pulse = Math.sin(Date.now() * 0.01) * 0.6 + 1.4;
    ctx.strokeStyle = a.color.border;
    ctx.lineWidth = pulse;
    ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.fillStyle = '#fff';
    ctx.font = 'bold 9px sans-serif';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    // 优先渲染协议名（如 "ICMP"/"TCP"）；若未知则回退到 ▶ 占位符
    ctx.fillText(a.protocol || '▶', 0, 0);
    ctx.restore();

    // 头部小三角
    ctx.save();
    ctx.translate(x, y);
    ctx.rotate(angle);
    ctx.fillStyle = a.color.border;
    ctx.beginPath();
    ctx.moveTo(18, 0);
    ctx.lineTo(26, -5);
    ctx.lineTo(26, 5);
    ctx.closePath();
    ctx.fill();
    ctx.restore();
  } else {
    // 到达: 3 层脉冲环
    const pulsePhase = (t - 0.95) / 0.15;
    for (let i = 0; i < 3; i++) {
      const pp = (pulsePhase + i * 0.35) % 1;
      const r = 12 + pp * 30;
      const alpha = (1 - pp) * 0.6;
      ctx.strokeStyle = a.color.glow.replace('0.5', String(alpha));
      ctx.lineWidth = 3 - pp * 2;
      ctx.beginPath();
      ctx.arc(x, y, r, 0, Math.PI * 2);
      ctx.stroke();
    }
    ctx.save();
    ctx.shadowColor = a.color.main;
    ctx.shadowBlur = 15;
    ctx.fillStyle = a.color.main;
    ctx.beginPath();
    ctx.arc(x, y, 8, 0, Math.PI * 2);
    ctx.fill();
    ctx.restore();
    ctx.fillStyle = '#fff';
    ctx.beginPath();
    ctx.arc(x, y, 4, 0, Math.PI * 2);
    ctx.fill();
  }

  // 当前 hop 标记
  if (t < 0.5) {
    const mx = seg.from.x + seg.dx * 0.5;
    const my = seg.from.y + seg.dy * 0.5;
    ctx.fillStyle = 'rgba(30,30,30,0.75)';
    ctx.beginPath();
    ctx.arc(mx, my, 10, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = '#fff';
    ctx.font = 'bold 9px sans-serif';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(String(seg.hopIndex + 1), mx, my);
  }
}
