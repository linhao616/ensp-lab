// DiagnosticDNS - 网络诊断工具集 · DNS 查询 Tab
// 输入域名，模拟 DNS 解析返回 IP；可选择指定 DNS 服务器。
import { useState } from 'react';
import { type Device } from '../types';
import { dnsResolve } from './diagnosticUtils';

interface Props {
  devices: Record<string, Device>;
}

const DEFAULT_SERVERS = ['8.8.8.8', '114.114.114.114', '223.5.5.5'];

export default function DiagnosticDNS(props: Props) {
  const { devices } = props;
  const [domain, setDomain] = useState<string>('www.example.com');
  const [server, setServer] = useState<string>('8.8.8.8');
  const [running, setRunning] = useState<boolean>(false);
  const [output, setOutput] = useState<string[]>([]);

  // 收集拓扑中设备声明的 DNS 作为可选服务器
  const deviceDnsList = Array.from(
    new Set(
      Object.values(devices)
        .flatMap((d) => Object.values(d.interfaces || {}).map((i) => (i ? i.dns : '')))
        .filter((s) => !!s && s !== '0.0.0.0'),
    ),
  );
  const serverOptions = Array.from(new Set([...deviceDnsList, ...DEFAULT_SERVERS]));

  const resolve = () => {
    const d = (domain || '').trim();
    if (!d) {
      setOutput(['错误：请输入域名，例如 www.example.com']);
      return;
    }
    setRunning(true);
    setOutput([]);
    setTimeout(() => {
      const { ip } = dnsResolve(d);
      const lines: string[] = [];
      lines.push(`服务器:  UnKnown`);
      lines.push(`Address:  ${server}`);
      lines.push('');
      if (ip) {
        lines.push(`非权威应答:`);
        lines.push(`名称:    ${d}`);
        lines.push(`Addresses:  ${ip}`);
        lines.push('');
        lines.push(`解析成功：${d} → ${ip}`);
      } else {
        lines.push(`*** 无法找到 ${d} 的地址：$ Non-existent domain`);
        lines.push(''); 
        lines.push(`解析失败：${d} 不存在 (NXDOMAIN)`);
      }
      setOutput(lines);
      setRunning(false);
    }, 300);
  };

  return (
    <div className="diag-dns">
      <div className="diag-form">
        <div className="diag-row">
          <label className="diag-label">域名</label>
          <input
            className="diag-select"
            type="text"
            placeholder="例如 www.example.com"
            value={domain}
            onChange={(e) => setDomain(e.target.value)}
            disabled={running}
          />
        </div>
        <div className="diag-row">
          <label className="diag-label">DNS 服务器</label>
          <select
            className="diag-select"
            value={server}
            onChange={(e) => setServer(e.target.value)}
            disabled={running}
          >
            {serverOptions.map((s) => (
              <option key={`dns-${s}`} value={s}>
                {s}
              </option>
            ))}
          </select>
        </div>
        <div className="diag-actions">
          <button type="button" className="diag-btn diag-btn-start" onClick={resolve} disabled={running}>
            {running ? '解析中…' : '▶ 解析'}
          </button>
        </div>
      </div>

      <div className="diag-output">
        <div className="diag-output-header">
          <span>DNS 解析结果</span>
        </div>
        <div className="diag-output-body diag-mono">
          {output.length === 0 ? (
            <div className="diag-output-empty">输入域名后点击「解析」</div>
          ) : (
            output.map((line, idx) => (
              <div key={`dns-${idx}`} className="diag-line">
                {line || ' '}
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
