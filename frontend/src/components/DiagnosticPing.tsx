// DiagnosticPing - 网络诊断工具集 · Ping Tab（P1-D 统一诊断网关）
// 改用后端统一诊断网关 api.diagnosticPing，渲染结构化 rtt.min/avg/max/loss，
// 不再依赖旧 /api/topology/:id/ping 的 rtt_ms 单值。
import { useRef, useState } from 'react';
import { type Device } from '../types';
import { api, type DiagnosticPingResult } from '../api';
import { deviceLabel } from './diagnosticUtils';

export interface DiagPingHistoryItem {
  id: string;
  time: string;
  src: string;
  dst: string;
  srcName: string;
  dstName: string;
  success: boolean;
  rttMs?: number;
  summary: string;
}

interface Props {
  topologyId: string | null;
  devices: Record<string, Device>;
  srcDevice: string;
  onSrcChange: (id: string) => void;
  onPickTarget: (dstId: string) => void;
}

function icmpCapable(dev: Device): boolean {
  return Object.values(dev.interfaces || {}).some((i) => i && i.ip_address);
}

function escapeId(id: string): string {
  return id.replace(/[^a-zA-Z0-9-]/g, '_');
}

export default function DiagnosticPing(props: Props) {
  const { topologyId, devices, srcDevice, onSrcChange, onPickTarget } = props;
  const [dstId, setDstId] = useState<string>('');
  const [count, setCount] = useState<number>(4);
  const [continuous, setContinuous] = useState<boolean>(false);
  const [running, setRunning] = useState<boolean>(false);
  const [output, setOutput] = useState<string[]>([]);
  const [stats, setStats] = useState<{ min: number; avg: number; max: number; loss: number } | null>(null);
  const [history, setHistory] = useState<DiagPingHistoryItem[]>([]);
  const abortRef = useRef(false);
  const runningRef = useRef(false);

  const srcList = Object.values(devices)
    .filter(icmpCapable)
    .sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));
  const allList = Object.values(devices).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));

  const canSwap = srcDevice && dstId;

  const stop = () => {
    abortRef.current = true;
    runningRef.current = false;
    setRunning(false);
  };

  const runOne = async (
    src: string,
    dst: string,
    c: number,
    append: boolean,
  ): Promise<DiagnosticPingResult | null> => {
    if (!topologyId) return null;
    const targetName = devices[dst]?.name || dst;
    try {
      const res = await api.diagnosticPing(topologyId, src, dst, c);
      const header = `PING ${targetName} (${targetName}) ${c * 56} bytes of data`;
      setOutput((prev) => {
        const next = append ? [...prev] : [];
        if (next.length === 0 || !next[next.length - 1].startsWith('PING')) next.push(header);
        for (const line of res.output.split('\n')) next.push(line);
        return next;
      });
      setStats({ min: res.rtt.min, avg: res.rtt.avg, max: res.rtt.max, loss: res.rtt.loss });
      return res;
    } catch (e) {
      const msg = `Ping 失败: ${e instanceof Error ? e.message : String(e)}`;
      setOutput((prev) => (append ? [...prev, msg] : [msg]));
      setStats({ min: 0, avg: 0, max: 0, loss: 100 });
      return null;
    }
  };

  const start = () => {
    if (!topologyId || !srcDevice || !dstId || srcDevice === dstId) return;
    setRunning(true);
    abortRef.current = false;
    runningRef.current = true;
    setOutput([]);
    setStats(null);

    const addHistory = (ok: boolean, rtt?: number, summary?: string) => {
      setHistory((prev) => [
        {
          id: `ping-${Date.now()}`,
          time: new Date().toLocaleTimeString('zh-CN', { hour12: false }),
          src: srcDevice,
          dst: dstId,
          srcName: devices[srcDevice]?.name || srcDevice,
          dstName: devices[dstId]?.name || dstId,
          success: ok,
          rttMs: rtt,
          summary: summary || `${devices[srcDevice]?.name || srcDevice} → ${devices[dstId]?.name || dstId}`,
        },
        ...prev.slice(0, 49),
      ]);
    };

    if (continuous) {
      let round = 0;
      const loop = async () => {
        if (abortRef.current || !runningRef.current) return;
        const r = await runOne(srcDevice, dstId, 1, round > 0);
        if (round === 0) addHistory((r?.rtt.loss ?? 100) < 100, r?.rtt.avg, '连续 Ping 开始');
        round++;
        if (abortRef.current || !runningRef.current) {
          setRunning(false);
          runningRef.current = false;
          return;
        }
        setTimeout(loop, 1000);
      };
      void loop();
    } else {
      void runOne(srcDevice, dstId, count, false).then((r) => {
        const ok = (r?.rtt.loss ?? 100) < 100;
        addHistory(ok, r?.rtt.avg, `${count} 包 / 丢失 ${r?.rtt.loss ?? 100}%`);
        setRunning(false);
        runningRef.current = false;
      });
    }
  };

  return (
    <div className="diag-ping">
      <div className="diag-form">
        <div className="diag-row">
          <label className="diag-label">源设备</label>
          <select
            className="diag-select"
            value={srcDevice}
            onChange={(e) => onSrcChange(e.target.value)}
            disabled={running}
          >
            <option value="">— 选择源设备（ICMP）—</option>
            {srcList.map((d) => (
              <option key={`src-${d.id}`} value={d.id}>
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
            onChange={(e) => setDstId(e.target.value)}
            disabled={running}
          >
            <option value="">— 选择目标设备 —</option>
            {allList.map((d) => (
              <option key={`dst-${d.id}`} value={d.id}>
                {deviceLabel(d)}
              </option>
            ))}
          </select>
        </div>

        <div className="diag-row diag-row-inline">
          <div className="diag-inline-group">
            <label className="diag-label">包数 (-c)</label>
            <input
              className="diag-count-input"
              type="number"
              min={1}
              max={100}
              value={count}
              onChange={(e) => setCount(Math.max(1, Math.min(100, parseInt(e.target.value || '1', 10))))}
              disabled={running || continuous}
            />
          </div>
          <label className="diag-continuous-toggle" title="持续 Ping，直到手动停止">
            <input
              type="checkbox"
              checked={continuous}
              onChange={(e) => setContinuous(e.target.checked)}
              disabled={running}
            />
            <span>连续 Ping (-t)</span>
          </label>
          <button
            type="button"
            className="diag-swap-btn"
            onClick={() => {
              if (canSwap) {
                onSrcChange(dstId);
                setDstId(srcDevice);
              }
            }}
            disabled={!canSwap || running}
            title="交换源/目标"
          >
            ⇄
          </button>
        </div>

        <div className="diag-actions">
          {running ? (
            <button type="button" className="diag-btn diag-btn-stop" onClick={stop}>
              ⏹ 停止
            </button>
          ) : (
            <button
              type="button"
              className="diag-btn diag-btn-start"
              onClick={start}
              disabled={!srcDevice || !dstId || srcDevice === dstId}
            >
              ▶ 执行 Ping
            </button>
          )}
        </div>
      </div>

      <div className="diag-output">
        <div className="diag-output-header">
          <span>实时输出</span>
          {stats && (
            <span className={`diag-summary ${stats.loss < 100 ? 'diag-success' : 'diag-fail'}`}>
              {stats.loss < 100 ? '✅ 成功' : '❌ 失败'}
              {` (min ${stats.min.toFixed(2)} / avg ${stats.avg.toFixed(2)} / max ${stats.max.toFixed(2)} ms, loss ${stats.loss.toFixed(0)}%)`}
            </span>
          )}
        </div>
        <div className="diag-output-body">
          {output.length === 0 ? (
            <div className="diag-output-empty">点击「执行 Ping」开始测试</div>
          ) : (
            output.map((line, idx) => (
              <div key={`line-${idx}-${escapeId(line.slice(0, 20))}`} className="diag-line">
                {line}
              </div>
            ))
          )}
        </div>
      </div>

      {stats && (
        <div className="diag-stats">
          <div className="diag-stat">
            <span className="diag-stat-label">最小</span>
            <span className="diag-stat-value">{stats.min.toFixed(2)}ms</span>
          </div>
          <div className="diag-stat">
            <span className="diag-stat-label">平均</span>
            <span className="diag-stat-value">{stats.avg.toFixed(2)}ms</span>
          </div>
          <div className="diag-stat">
            <span className="diag-stat-label">最大</span>
            <span className="diag-stat-value">{stats.max.toFixed(2)}ms</span>
          </div>
          <div className="diag-stat">
            <span className="diag-stat-label">丢包率</span>
            <span className="diag-stat-value">{stats.loss.toFixed(0)}%</span>
          </div>
        </div>
      )}

      {history.length > 0 && (
        <div className="diag-history">
          <div className="diag-history-header">历史记录（双击可填入 Traceroute 目标）</div>
          <div className="diag-history-body">
            {history.map((h) => (
              <div
                key={h.id}
                className={`diag-history-item ${h.success ? 'diag-success' : 'diag-fail'}`}
                title="双击：将目标设备填入 Traceroute"
                onDoubleClick={() => onPickTarget(h.dst)}
              >
                <span className="diag-history-time">[{h.time}]</span>
                <span className="diag-history-pair">
                  {h.srcName} → {h.dstName}
                </span>
                <span className="diag-history-result">
                  {h.success ? `✅ 成功 (${h.rttMs?.toFixed(2) ?? '-'}ms)` : '❌ 超时'}
                </span>
                <span className="diag-history-summary">{h.summary}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
