// DiagnosticTraceroute - 网络诊断工具集 · Traceroute Tab（P1-D 真实化）
// 直接调用后端统一诊断网关 api.diagnosticTraceroute，渲染返回的逐跳列表
// （hop / device / ip / rtt），不再用 computePath / randRtt / Math.random 造假。
import { useEffect, useState } from 'react';
import { type Device, type Link } from '../types';
import { api, type DiagnosticTracerouteHop } from '../api';
import { deviceLabel, firstIp } from './diagnosticUtils';

interface Props {
  topologyId: string | null;
  devices: Record<string, Device>;
  links: Link[];
  srcDevice: string;
  onSrcChange: (id: string) => void;
  targetDevice?: string;
  engineMode?: 'full' | 'lite';
}

export default function DiagnosticTraceroute(props: Props) {
  const { topologyId, devices, srcDevice, onSrcChange, targetDevice, engineMode } = props;
  const [dstId, setDstId] = useState<string>(targetDevice || '');
  const [dstIp, setDstIp] = useState<string>('');
  const [running, setRunning] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [hops, setHops] = useState<DiagnosticTracerouteHop[] | null>(null);

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

  const run = async () => {
    if (!srcDevice) {
      setError('错误：请先在拓扑中选中源设备，或在上方选择源设备。');
      return;
    }
    if (!topologyId) {
      setError('错误：当前拓扑未加载，无法发起追踪。');
      return;
    }
    const targetIp = (dstIp || '').trim() || firstIp(devices[dstId]);
    if (!targetIp) {
      setError('错误：请选择目标设备，或在「目标 IP」中输入目标地址。');
      return;
    }

    setRunning(true);
    setError(null);
    setHops(null);
    try {
      const data = await api.diagnosticTraceroute(topologyId, srcDevice, targetIp);
      if (!data.reachable || !data.hops || data.hops.length === 0) {
        setError('🌐 目标不可达（超时）');
      } else {
        setHops(data.hops);
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // 后端对未开机设备返回含"未开机"的 400 错误，前端据此给出明确提示。
      if (msg.includes('未开机')) {
        setError(`❌ 设备 ${srcDevice} 未开机，无法探测`);
      } else {
        setError(`❌ ${msg}`);
      }
    } finally {
      setRunning(false);
    }
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
          {running ? (
            <div className="diag-line">⏳ 正在探测路径...</div>
          ) : error ? (
            <div className="diag-line">{error}</div>
          ) : hops && hops.length > 0 ? (
            <table className="diag-hop-table">
              <thead>
                <tr>
                  <th>跳数</th>
                  <th>设备</th>
                  <th>IP</th>
                  <th>RTT (ms)</th>
                </tr>
              </thead>
              <tbody>
                {hops.map((h) => (
                  <tr key={`hop-${h.hop}`}>
                    <td>{h.hop}</td>
                    <td>{h.device}</td>
                    <td>{h.ip}</td>
                    <td>{h.rtt >= 0 ? h.rtt.toFixed(2) : '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <div className="diag-output-empty">点击「开始追踪」查看每一跳延迟</div>
          )}
        </div>
        {engineMode === 'lite' && (
          <div className="diag-engine-note">
            部分诊断结果基于拓扑模拟（非真实协议栈）
          </div>
        )}
      </div>
    </div>
  );
}
