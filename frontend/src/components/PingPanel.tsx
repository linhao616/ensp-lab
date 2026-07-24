// PingPanel - 任意两台设备之间的 ICMP 测试面板
// 支持：源/目标下拉选择、包数设置、连续 Ping（-t）模式、实时输出、历史记录。
import { type Device } from '../types';

export interface PingHistoryItem {
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

interface PingPanelProps {
  devices: Record<string, Device>;
  srcId: string;
  dstId: string;
  onSrcChange: (id: string) => void;
  onDstChange: (id: string) => void;
  count: number;
  onCountChange: (count: number) => void;
  continuous: boolean;
  onContinuousChange: (v: boolean) => void;
  running: boolean;
  output: string[];
  stats: { sent: number; received: number; lost: number; rttMs?: number } | null;
  history: PingHistoryItem[];
  onPing: () => void;
  onStop: () => void;
  onClearHistory?: () => void;
  onClose?: () => void;
}

function deviceLabel(dev: Device): string {
  const ipPart = Object.values(dev.interfaces || {})
    .filter((i) => i?.ip_address)
    .map((i) => i.ip_address)[0];
  return `${dev.name}${ipPart ? ` (${ipPart})` : ''}`;
}

function escapeId(id: string): string {
  // 仅用于生成 history key，避免特殊字符
  return id.replace(/[^a-zA-Z0-9-]/g, '_');
}

export default function PingPanel(props: PingPanelProps) {
  const {
    devices,
    srcId,
    dstId,
    onSrcChange,
    onDstChange,
    count,
    onCountChange,
    continuous,
    onContinuousChange,
    running,
    output,
    stats,
    history,
    onPing,
    onStop,
    onClearHistory,
    onClose,
  } = props;

  const deviceList = Object.values(devices).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));

  const canSwap = srcId && dstId;
  const handleSwap = () => {
    if (!canSwap) return;
    onSrcChange(dstId);
    onDstChange(srcId);
  };

  return (
    <div className="ping-panel">
      <div className="ping-header">
        <div className="ping-title">
          <span role="img" aria-label="ping">🔍</span>
          Ping 测试
        </div>
        <div className="ping-header-actions">
          {history.length > 0 && (
            <button type="button" className="ping-header-btn" onClick={onClearHistory} title="清空历史">
              清空
            </button>
          )}
          <button type="button" className="ping-header-btn" onClick={onClose} title="关闭">
            ✕
          </button>
        </div>
      </div>

      <div className="ping-form">
        <div className="ping-row">
          <label className="ping-label">源设备</label>
          <select
            className="ping-select"
            value={srcId}
            onChange={(e) => onSrcChange(e.target.value)}
            disabled={running}
          >
            <option value="">— 选择源设备 —</option>
            {deviceList.map((d) => (
              <option key={`src-${d.id}`} value={d.id}>
                {deviceLabel(d)}
              </option>
            ))}
          </select>
        </div>
        <div className="ping-row">
          <label className="ping-label">目标设备</label>
          <select
            className="ping-select"
            value={dstId}
            onChange={(e) => onDstChange(e.target.value)}
            disabled={running}
          >
            <option value="">— 选择目标设备 —</option>
            {deviceList.map((d) => (
              <option key={`dst-${d.id}`} value={d.id}>
                {deviceLabel(d)}
              </option>
            ))}
          </select>
        </div>

        <div className="ping-row ping-row-inline">
          <div className="ping-inline-group">
            <label className="ping-label">包数</label>
            <input
              className="ping-count-input"
              type="number"
              min={1}
              max={100}
              value={count}
              onChange={(e) => onCountChange(Math.max(1, Math.min(100, parseInt(e.target.value || '1', 10))))}
              disabled={running || continuous}
            />
          </div>
          <label className="ping-continuous-toggle" title="持续 Ping，直到手动停止">
            <input
              type="checkbox"
              checked={continuous}
              onChange={(e) => onContinuousChange(e.target.checked)}
              disabled={running}
            />
            <span>连续 Ping (-t)</span>
          </label>
          <button
            type="button"
            className="ping-swap-btn"
            onClick={handleSwap}
            disabled={!canSwap || running}
            title="交换源/目标"
          >
            ⇄
          </button>
        </div>

        <div className="ping-actions">
          {running ? (
            <button type="button" className="ping-btn ping-btn-stop" onClick={onStop}>
              ⏹ 停止
            </button>
          ) : (
            <button
              type="button"
              className="ping-btn ping-btn-start"
              onClick={onPing}
              disabled={!srcId || !dstId || srcId === dstId}
            >
              ▶ 执行 Ping
            </button>
          )}
        </div>
      </div>

      <div className="ping-output">
        <div className="ping-output-header">
          <span>实时输出</span>
          {stats && (
            <span className={`ping-summary ${stats.received > 0 ? 'ping-success' : 'ping-fail'}`}>
              {stats.received > 0 ? '✅ 成功' : '❌ 失败'}
              {stats.rttMs !== undefined ? ` (${stats.rttMs.toFixed(2)}ms)` : ''}
            </span>
          )}
        </div>
        <div className="ping-output-body">
          {output.length === 0 ? (
            <div className="ping-output-empty">点击「执行 Ping」开始测试</div>
          ) : (
            output.map((line, idx) => (
              <div key={`line-${idx}-${escapeId(line.slice(0, 20))}`} className="ping-line">
                {line}
              </div>
            ))
          )}
        </div>
      </div>

      {stats && (
        <div className="ping-stats">
          <div className="ping-stat">
            <span className="ping-stat-label">发送</span>
            <span className="ping-stat-value">{stats.sent}</span>
          </div>
          <div className="ping-stat">
            <span className="ping-stat-label">接收</span>
            <span className="ping-stat-value">{stats.received}</span>
          </div>
          <div className="ping-stat">
            <span className="ping-stat-label">丢失</span>
            <span className="ping-stat-value">{stats.lost}</span>
          </div>
          <div className="ping-stat">
            <span className="ping-stat-label">丢包率</span>
            <span className="ping-stat-value">
              {stats.sent > 0 ? `${Math.round((stats.lost / stats.sent) * 100)}%` : '-'}
            </span>
          </div>
        </div>
      )}

      {history.length > 0 && (
        <div className="ping-history">
          <div className="ping-history-header">历史记录</div>
          <div className="ping-history-body">
            {history.map((h) => (
              <div key={h.id} className={`ping-history-item ${h.success ? 'ping-success' : 'ping-fail'}`}>
                <span className="ping-history-time">[{h.time}]</span>
                <span className="ping-history-pair">
                  {h.srcName} → {h.dstName}
                </span>
                <span className="ping-history-result">
                  {h.success ? `✅ 成功 (${h.rttMs?.toFixed(2) ?? '-'}ms)` : '❌ 超时'}
                </span>
                <span className="ping-history-summary" title={h.summary}>
                  {h.summary}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
