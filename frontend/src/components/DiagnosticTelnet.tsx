// DiagnosticTelnet - 网络诊断工具集 · Telnet/SSH Tab
// 选择目标设备后模拟真实登录过程（banner），随后复用已有 CliTerminal 显示该设备 CLI。
import { useEffect, useRef, useState } from 'react';
import { type Device } from '../types';
import CliTerminal from './CliTerminal';
import { deviceLabel, firstIp } from './diagnosticUtils';

interface Props {
  topologyId: string | null;
  devices: Record<string, Device>;
}

type Proto = 'telnet' | 'ssh';

export default function DiagnosticTelnet(props: Props) {
  const { topologyId, devices } = props;
  const [targetId, setTargetId] = useState<string>('');
  const [proto, setProto] = useState<Proto>('telnet');
  const [connected, setConnected] = useState<boolean>(false);
  const [banner, setBanner] = useState<string[]>([]);
  const timersRef = useRef<number[]>([]);

  const allList = Object.values(devices).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));

  const clearTimers = () => {
    timersRef.current.forEach((t) => window.clearTimeout(t));
    timersRef.current = [];
  };

  useEffect(() => {
    return () => clearTimers();
  }, []);

  const connect = () => {
    const dev = devices[targetId];
    if (!dev) return;
    const ip = firstIp(dev) || '(本地)';
    const name = dev.name;
    const lines: string[] =
      proto === 'ssh'
        ? [
            `Connecting to ${ip} port 22 ...`,
            'Connection established.',
            "To escape to local shell, press '~'.",
            '',
            `login as: admin`,
            `${name} login successful.`,
          ]
        : [
            `Trying ${ip} ...`,
            `Connected to ${name}.`,
            "Escape character is '^]'.",
            '',
            'User Access Verification',
            '',
            'Login: admin',
            'Password: ********',
            `${name} login successful.`,
          ];

    clearTimers();
    setConnected(false);
    setBanner([]);
    lines.forEach((ln, i) => {
      const t = window.setTimeout(() => {
        setBanner((prev) => [...prev, ln]);
        if (i === lines.length - 1) {
          window.setTimeout(() => setConnected(true), 250);
        }
      }, 300 * (i + 1));
      timersRef.current.push(t);
    });
  };

  const disconnect = () => {
    clearTimers();
    setConnected(false);
    setBanner([]);
  };

  if (connected && targetId && devices[targetId]) {
    return (
      <div className="diag-telnet diag-telnet-connected">
        <div className="diag-telnet-bar">
          <span className="diag-telnet-info">
            {proto.toUpperCase()} → {deviceLabel(devices[targetId])}
          </span>
          <button type="button" className="diag-btn diag-btn-stop" onClick={disconnect}>
            断开连接
          </button>
        </div>
        <pre className="diag-telnet-banner">{banner.join('\n')}</pre>
        <CliTerminal topologyId={topologyId} selectedDevice={devices[targetId]} />
      </div>
    );
  }

  return (
    <div className="diag-telnet">
      <div className="diag-form">
        <div className="diag-row">
          <label className="diag-label">目标设备</label>
          <select
            className="diag-select"
            value={targetId}
            onChange={(e) => setTargetId(e.target.value)}
          >
            <option value="">— 选择目标设备 —</option>
            {allList.map((d) => (
              <option key={`tl-${d.id}`} value={d.id}>
                {deviceLabel(d)}
              </option>
            ))}
          </select>
        </div>
        <div className="diag-row diag-row-inline">
          <label className="diag-label">协议</label>
          <div className="diag-proto-group">
            <label className="diag-radio">
              <input
                type="radio"
                name="diag-proto"
                checked={proto === 'telnet'}
                onChange={() => setProto('telnet')}
              />
              <span>Telnet</span>
            </label>
            <label className="diag-radio">
              <input
                type="radio"
                name="diag-proto"
                checked={proto === 'ssh'}
                onChange={() => setProto('ssh')}
              />
              <span>SSH</span>
            </label>
          </div>
        </div>
        <div className="diag-actions">
          <button
            type="button"
            className="diag-btn diag-btn-start"
            onClick={connect}
            disabled={!targetId}
          >
            ▶ 连接
          </button>
        </div>
        <div className="diag-hint">
          连接后将模拟登录过程并显示该设备的 CLI 终端（复用内置终端）。
        </div>
      </div>
    </div>
  );
}
