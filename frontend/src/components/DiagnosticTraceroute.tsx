// DiagnosticTraceroute - 网络诊断工具集 · Traceroute Tab
// 目标支持"设备下拉"或"直接输入目标 IP"。基于拓扑链路 BFS 推导路径，输出每跳 IP + 3 次 RTT。
import { useEffect, useState } from 'react';
import { type Device, type Link } from '../types';
import {
  computePath,
  deviceLabel,
  firstIp,
  firstNeighbor,
  randRtt,
} from './diagnosticUtils';

interface Props {
  devices: Record<string, Device>;
  links: Link[];
  srcDevice: string;
  onSrcChange: (id: string) => void;
  targetDevice?: string;
}

export default function DiagnosticTraceroute(props: Props) {
  const { devices, links, srcDevice, onSrcChange, targetDevice } = props;
  const [dstId, setDstId] = useState<string>(targetDevice || '');
  const [dstIp, setDstIp] = useState<string>('');
  const [running, setRunning] = useState<boolean>(false);
  const [output, setOutput] = useState<string[]>([]);

  useEffect(() => {
    if (targetDevice) {
      setDstId(targetDevice);
      setDstIp('');
    }
  }, [targetDevice]);

  const allList = Object.values(devices).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));
  const srcList = Object.values(devices)
    .filter((d) => Object.values(d.interfaces || {}).some((i) => i && i.ip_address))
    .sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));

  const run = () => {
    if (!srcDevice) {
      setOutput(['错误：请先在拓扑中选中源设备，或在上方选择源设备。']);
      return;
    }
    const targetIp = (dstIp || '').trim() || firstIp(devices[dstId]);
    const targetName = (dstIp || '').trim() || devices[dstId]?.name || dstId;
    if (!targetIp) {
      setOutput(['错误：请选择目标设备，或在「目标 IP」中输入目标地址。']);
      return;
    }
    setRunning(true);
    setOutput([]);

    // 推导跳点 IP 序列
    const hopIps: string[] = [];
    if ((dstIp || '').trim()) {
      // 外部 IP：首跳为源设备的直连邻居（网关），末跳为目标 IP
      const gw = firstNeighbor(links, srcDevice);
      const gwIp = gw ? firstIp(devices[gw]) : firstIp(devices[srcDevice]);
      if (gwIp) hopIps.push(gwIp);
      hopIps.push(targetIp);
    } else if (dstId === srcDevice) {
      setOutput([`traceroute to ${targetIp} (${targetIp}), 30 hops max`, '', '目标即源设备，无需追踪。']);
      setRunning(false);
      return;
    } else {
      const path = computePath(devices, links, srcDevice, dstId);
      if (path.length < 2) {
        setOutput([
          `traceroute to ${targetIp} (${targetIp}), 30 hops max`,
          '',
          `目标主机不可达 (Host Unreachable) — 拓扑中无可达路径`,
        ]);
        setRunning(false);
        return;
      }
      // path[0] 为源，从 path[1] 起每一跳为响应设备
      for (let i = 1; i < path.length; i++) {
        const ip = firstIp(devices[path[i]]);
        if (ip) hopIps.push(ip);
      }
    }

    const lines: string[] = [];
    lines.push(`traceroute to ${targetIp} (${targetIp}), 30 hops max`);
    lines.push('');
    let cum = 0.3;
    hopIps.forEach((ip, idx) => {
      cum += randRtt(0.6);
      const a = Math.max(0.1, cum + (Math.random() - 0.5) * 0.25);
      const b = Math.max(0.1, cum + (Math.random() - 0.5) * 0.25);
      const c = Math.max(0.1, cum + (Math.random() - 0.5) * 0.25);
      const fmt = (n: number) => n.toFixed(1);
      lines.push(` ${idx + 1}  ${ip}  ${fmt(a)}ms  ${fmt(b)}ms  ${fmt(c)}ms`);
    });
    lines.push('');
    lines.push(`跟踪完成：${hopIps.length} 跳到达 ${targetName} (${targetIp})`);

    // 轻微延时模拟探测过程
    setTimeout(() => {
      setOutput(lines);
      setRunning(false);
    }, 350);
  };

  return (
    <div className="diag-trace">
      <div className="diag-form">
        <div className="diag-row">
          <label className="diag-label">源设备</label>
          <select
            className="diag-select"
            value={srcDevice}
            onChange={(e) => onSrcChange(e.target.value)}
            disabled={running}
          >
            <option value="">— 选择源设备 —</option>
            {srcList.map((d) => (
              <option key={`ts-${d.id}`} value={d.id}>
                {deviceLabel(d)}
              </option>
            ))}
          </select>
        </div>
        <div className="diag-row">
          <label className="diag-label">目标设备</label>
          <select
            className="diag-select"
            value={dstId}
            onChange={(e) => {
              setDstId(e.target.value);
              setDstIp('');
            }}
            disabled={running}
          >
            <option value="">— 选择目标设备 —</option>
            {allList.map((d) => (
              <option key={`td-${d.id}`} value={d.id}>
                {deviceLabel(d)}
              </option>
            ))}
          </select>
        </div>
        <div className="diag-row">
          <label className="diag-label">目标 IP</label>
          <input
            className="diag-select"
            type="text"
            placeholder="或输入目标 IP（如 10.0.10.30）"
            value={dstIp}
            onChange={(e) => setDstIp(e.target.value)}
            disabled={running}
          />
        </div>
        <div className="diag-actions">
          <button
            type="button"
            className="diag-btn diag-btn-start"
            onClick={run}
            disabled={running || !srcDevice}
          >
            {running ? '追踪中…' : '▶ 开始追踪'}
          </button>
        </div>
      </div>

      <div className="diag-output">
        <div className="diag-output-header">
          <span>路由追踪结果</span>
        </div>
        <div className="diag-output-body diag-mono">
          {output.length === 0 ? (
            <div className="diag-output-empty">点击「开始追踪」查看每一跳延迟</div>
          ) : (
            output.map((line, idx) => (
              <div key={`tr-${idx}`} className="diag-line">
                {line || ' '}
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
