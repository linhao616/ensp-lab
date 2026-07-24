// DiagnosticBandwidth - 网络诊断工具集 · 带宽测试 (iPerf 风格) Tab
// 选择源/目标设备与协议（TCP/UDP），模拟吞吐测试，输出带宽/抖动/丢包率。
import { useEffect, useRef, useState } from 'react';
import { type Device } from '../types';
import { deviceLabel } from './diagnosticUtils';

interface Props {
  devices: Record<string, Device>;
}

type Proto = 'tcp' | 'udp';

export default function DiagnosticBandwidth(props: Props) {
  const { devices } = props;
  const [srcId, setSrcId] = useState<string>('');
  const [dstId, setDstId] = useState<string>('');
  const [proto, setProto] = useState<Proto>('tcp');
  const [running, setRunning] = useState<boolean>(false);
  const [lines, setLines] = useState<string[]>([]);
  const [summary, setSummary] = useState<string | null>(null);
  const timerRef = useRef<number | null>(null);

  const allList = Object.values(devices).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));

  useEffect(() => {
    return () => {
      if (timerRef.current) window.clearInterval(timerRef.current);
    };
  }, []);

  const stop = () => {
    if (timerRef.current) window.clearInterval(timerRef.current);
    timerRef.current = null;
    setRunning(false);
  };

  const start = () => {
    if (!srcId || !dstId || srcId === dstId) return;
    setRunning(true);
    setLines([]);
    setSummary(null);
    const srcName = devices[srcId]?.name || srcId;
    const dstName = devices[dstId]?.name || dstId;
    const targetBw = 820 + Math.random() * 130; // Mbits/sec
    let t = 0;
    const id = 4;
    const pad = (n: number) => n.toFixed(2);
    const fmtMB = (mb: number) => `${Math.max(1, Math.round(mb))} MBytes`;

    timerRef.current = window.setInterval(() => {
      t += 1;
      const bw = targetBw * (0.92 + Math.random() * 0.16);
      const transfer = (bw / 8) * t;
      setLines((prev) => [
        ...prev,
        `[ ${id}] ${pad(t - 1)}.00-${t}.00 sec  ${fmtMB(transfer)}  ${bw.toFixed(0)} Mbits/sec`,
      ]);
      if (t >= 3) {
        if (timerRef.current) window.clearInterval(timerRef.current);
        timerRef.current = null;
        const total = (targetBw / 8) * 3;
        setLines((prev) => [
          ...prev,
          `[SUM] 0.00-3.00 sec  ${fmtMB(total)}  ${targetBw.toFixed(0)} Mbits/sec sender`,
        ]);
        if (proto === 'udp') {
          const jitter = (0.02 + Math.random() * 0.08).toFixed(3);
          const loss = (Math.random() * 0.3).toFixed(2);
          setLines((prev) => [
            ...prev,
            `[SUM] 0.00-3.00 sec  ${targetBw.toFixed(0)} Mbits/sec  ${jitter} ms  ${loss}% lost`,
          ]);
          setSummary(`UDP ${srcName} → ${dstName}: ${targetBw.toFixed(0)} Mbits/sec, 抖动 ${jitter} ms, 丢包 ${loss}%`);
        } else {
          setSummary(`TCP ${srcName} → ${dstName}: ${targetBw.toFixed(0)} Mbits/sec`);
        }
        setRunning(false);
      }
    }, 600);
  };

  return (
    <div className="diag-bw">
      <div className="diag-form">
        <div className="diag-row">
          <label className="diag-label">源设备</label>
          <select className="diag-select" value={srcId} onChange={(e) => setSrcId(e.target.value)} disabled={running}>
            <option value="">— 选择源 —</option>
            {allList.map((d) => (
              <option key={`bw-src-${d.id}`} value={d.id}>
                {deviceLabel(d)}
              </option>
            ))}
          </select>
        </div>
        <div className="diag-row">
          <label className="diag-label">目标设备</label>
          <select className="diag-select" value={dstId} onChange={(e) => setDstId(e.target.value)} disabled={running}>
            <option value="">— 选择目标 —</option>
            {allList.map((d) => (
              <option key={`bw-dst-${d.id}`} value={d.id}>
                {deviceLabel(d)}
              </option>
            ))}
          </select>
        </div>
        <div className="diag-row diag-row-inline">
          <label className="diag-label">协议</label>
          <div className="diag-proto-group">
            <label className="diag-radio">
              <input type="radio" name="bw-proto" checked={proto === 'tcp'} onChange={() => setProto('tcp')} disabled={running} />
              <span>TCP</span>
            </label>
            <label className="diag-radio">
              <input type="radio" name="bw-proto" checked={proto === 'udp'} onChange={() => setProto('udp')} disabled={running} />
              <span>UDP</span>
            </label>
          </div>
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
              disabled={!srcId || !dstId || srcId === dstId}
            >
              ▶ 开始测试
            </button>
          )}
        </div>
      </div>

      <div className="diag-output">
        <div className="diag-output-header">
          <span>iPerf 测试结果</span>
        </div>
        <div className="diag-output-body diag-mono">
          {lines.length === 0 ? (
            <div className="diag-output-empty">点击「开始测试」进行吞吐测试</div>
          ) : (
            lines.map((line, idx) => (
              <div key={`bw-${idx}`} className="diag-line">
                {line || ' '}
              </div>
            ))
          )}
        </div>
      </div>

      {summary && (
        <div className="diag-bw-summary">
          <span className="diag-summary-icon">📊</span> {summary}
        </div>
      )}
    </div>
  );
}
