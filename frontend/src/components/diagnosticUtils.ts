// 诊断工具集共享工具函数：IP/名称提取、MAC 生成、拓扑路径计算、DNS 解析等。
// 这些函数均为前端模拟，不依赖后端（Ping 除外，Ping 复用 api.pingTopology）。
import { type Device, type Link } from '../types';

export function firstIp(dev: Device | undefined): string {
  if (!dev) return '';
  const ifs = Object.values(dev.interfaces || {});
  const i = ifs.find((x) => x && x.ip_address) || ifs[0];
  return i && i.ip_address ? i.ip_address : '';
}

export function deviceLabel(dev: Device): string {
  const ip = firstIp(dev);
  return `${dev.name}${ip ? ` (${ip})` : ''}`;
}

export function interfaceEntries(
  dev: Device,
): { name: string; ip: string; mac: string; status: string }[] {
  return Object.entries(dev.interfaces || {}).map(([name, i]) => ({
    name,
    ip: i?.ip_address || '',
    mac: i?.mac || '',
    status: i?.status || '',
  }));
}

// 确定性 MAC：同一设备每次生成一致，便于 ARP 表稳定
export function genMac(seed: string): string {
  const s = seed || 'x';
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  const a = (h & 0xff).toString(16).padStart(2, '0');
  const b = ((h >>> 8) & 0xff).toString(16).padStart(2, '0');
  const c = ((h >>> 16) & 0xff).toString(16).padStart(2, '0');
  return `00:0C:29:${a}:${b}:${c}`.toUpperCase();
}

// 基于链路构建邻接表并 BFS 求设备路径（含源/目标），用于 Traceroute 跳点推导
export function computePath(
  devices: Record<string, Device>,
  links: Link[],
  srcId: string,
  dstId: string,
): string[] {
  if (!devices[srcId] || !devices[dstId]) return [];
  if (srcId === dstId) return [srcId];
  const adj: Record<string, string[]> = {};
  for (const l of links) {
    if (!adj[l.source_device]) adj[l.source_device] = [];
    adj[l.source_device].push(l.target_device);
    if (!adj[l.target_device]) adj[l.target_device] = [];
    adj[l.target_device].push(l.source_device);
  }
  const prev: Record<string, string> = {};
  const seen = new Set<string>([srcId]);
  const queue: string[] = [srcId];
  while (queue.length) {
    const cur = queue.shift() as string;
    if (cur === dstId) break;
    for (const nxt of adj[cur] || []) {
      if (!seen.has(nxt)) {
        seen.add(nxt);
        prev[nxt] = cur;
        queue.push(nxt);
      }
    }
  }
  if (!seen.has(dstId)) return [];
  const path: string[] = [];
  let cur: string | undefined = dstId;
  while (cur !== srcId) {
    path.unshift(cur);
    cur = prev[cur];
    if (cur === undefined) return [];
  }
  path.unshift(srcId);
  return path;
}

// 取某设备的第一个直连邻居（用于外部 IP 的伪路径首跳）
export function firstNeighbor(links: Link[], devId: string): string | null {
  for (const l of links) {
    if (l.source_device === devId) return l.target_device;
    if (l.target_device === devId) return l.source_device;
  }
  return null;
}

const DNS_MAP: Record<string, string> = {
  'www.example.com': '93.184.216.34',
  'example.com': '93.184.216.34',
  'www.baidu.com': '183.232.231.172',
  'www.google.com': '142.250.72.206',
  'www.google.cn': '180.163.150.34',
  'www.bing.com': '204.79.197.200',
  'www.qq.com': '119.29.29.29',
  'github.com': '20.205.243.166',
};

export function dnsResolve(domain: string): { ip: string; authoritative: boolean } {
  const d = (domain || '').trim().toLowerCase().replace(/\.$/, '');
  if (DNS_MAP[d]) return { ip: DNS_MAP[d], authoritative: false };
  if (d.includes('.')) {
    // 确定性回退：基于域名哈希映射到公网 IP 段
    let h = 0;
    const s = d;
    for (let i = 0; i < s.length; i++) h = (h * 33 + s.charCodeAt(i)) >>> 0;
    const o1 = 23 + (h % 200);
    const o2 = (h >>> 8) % 256;
    const o3 = (h >>> 16) % 256;
    const o4 = ((h >>> 24) % 254) + 1;
    return { ip: `${o1}.${o2}.${o3}.${o4}`, authoritative: false };
  }
  return { ip: '', authoritative: false };
}

// 0~2*base 之间的 RTT 采样（保留 2 位小数）
export function randRtt(base = 1): number {
  return Math.round((base + Math.random() * base) * 100) / 100;
}

// 固定协议随机包长度
export function randLen(proto: string): number {
  if (proto === 'ICMP') return 64 + Math.floor(Math.random() * 8);
  if (proto === 'ARP') return 42 + Math.floor(Math.random() * 6);
  if (proto === 'DNS') return 70 + Math.floor(Math.random() * 30);
  // TCP/UDP/HTTP
  return 52 + Math.floor(Math.random() * 1400);
}
