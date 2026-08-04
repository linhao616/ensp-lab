// 诊断工具集共享工具函数：IP/名称提取、MAC 生成等（纯前端辅助，不含造假逻辑）。
//
// 注意（P1-D）：原先的 computePath / randRtt / dnsResolve / firstNeighbor 等
// "前端编造跳数/RTT/DNS" 的造假函数已移除——诊断数据一律来自后端统一诊断网关
// （api.diagnosticPing / diagnosticTraceroute / diagnosticDNS），前端只渲染。
import { type Device } from '../types';

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

