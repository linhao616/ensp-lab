// DiagnosticARP - 网络诊断工具集 · ARP 查询 Tab
// 显示选中设备的 ARP 表：基于直连邻居推导 IP / MAC / 接口 / 状态。
import { useMemo, useState } from 'react';
import { type Device, type Link } from '../types';
import { deviceLabel, firstIp, genMac } from './diagnosticUtils';

interface Props {
  devices: Record<string, Device>;
  links: Link[];
}

interface ArpEntry {
  ip: string;
  mac: string;
  iface: string;
  status: string;
}

export default function DiagnosticARP(props: Props) {
  const { devices, links } = props;
  const [targetId, setTargetId] = useState<string>('');
  const [, setNonce] = useState(0);

  const allList = Object.values(devices).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));

  const entries: ArpEntry[] = useMemo(() => {
    const dev = devices[targetId];
    if (!dev) return [];
    const result: ArpEntry[] = [];
    for (const l of links) {
      let localPort = '';
      let neighborId = '';
      let neighborPort = '';
      if (l.source_device === targetId) {
        localPort = l.source_port;
        neighborId = l.target_device;
        neighborPort = l.target_port;
      } else if (l.target_device === targetId) {
        localPort = l.target_port;
        neighborId = l.source_device;
        neighborPort = l.source_port;
      } else {
        continue;
      }
      const nb = devices[neighborId];
      if (!nb) continue;
      const nbIface = nb.interfaces?.[neighborPort];
      const ip = (nbIface && nbIface.ip_address) || firstIp(nb);
      if (!ip) continue;
      const mac = (nbIface && nbIface.mac) || genMac(neighborId);
      result.push({
        ip,
        mac: mac.toUpperCase(),
        iface: localPort || 'Ethernet0',
        status: nb.status === 'running' ? 'C' : 'INCOMPLETE',
      });
    }
    // 网关条目（接口中声明的 gateway）
    for (const iface of Object.values(dev.interfaces || {})) {
      if (iface && iface.gateway && iface.gateway !== '0.0.0.0' && !result.some((r) => r.ip === iface.gateway)) {
        result.push({
          ip: iface.gateway,
          mac: genMac('gw-' + iface.gateway),
          iface: iface.name || 'Ethernet0',
          status: 'C',
        });
      }
    }
    result.sort((a, b) => a.ip.localeCompare(b.ip, undefined, { numeric: true }));
    return result;
  }, [devices, links, targetId]);

  const lines: string[] = [];
  lines.push('ARP 表项 ('.padEnd(0) + (devices[targetId]?.name || '') + ')');
  lines.push('');
  lines.push(
    ['Address'.padEnd(16), 'HWtype'.padEnd(8), 'HWaddress'.padEnd(20), 'Iface'].join('  '),
  );
  if (entries.length === 0) {
    lines.push('  (无直连邻居 / 无 ARP 表项)');
  } else {
    for (const e of entries) {
      lines.push(
        [e.ip.padEnd(16), 'ether'.padEnd(8), e.mac.padEnd(20), e.iface].join('  '),
      );
    }
  }

  return (
    <div className="diag-arp">
      <div className="diag-form">
        <div className="diag-row">
          <label className="diag-label">设备</label>
          <select
            className="diag-select"
            value={targetId}
            onChange={(e) => setTargetId(e.target.value)}
          >
            <option value="">— 选择设备 —</option>
            {allList.map((d) => (
              <option key={`arp-${d.id}`} value={d.id}>
                {deviceLabel(d)}
              </option>
            ))}
          </select>
        </div>
        <div className="diag-actions">
          <button type="button" className="diag-btn diag-btn-secondary" onClick={() => setNonce((n) => n + 1)}>
            ↻ 刷新
          </button>
        </div>
      </div>

      <div className="diag-output">
        <div className="diag-output-header">
          <span>ARP 表</span>
        </div>
        <div className="diag-output-body diag-mono">
          {!targetId ? (
            <div className="diag-output-empty">请选择设备查看其 ARP 表</div>
          ) : (
            lines.map((line, idx) => (
              <div key={`arp-${idx}`} className="diag-line">
                {line || ' '}
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
