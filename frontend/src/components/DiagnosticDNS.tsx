// DiagnosticDNS - 网络诊断工具集 · DNS 查询 Tab（P1-D 真实化）
// 调用后端统一诊断网关 api.diagnosticDNS 执行系统 DNS 解析；解析失败如实显示
// 错误信息，不再使用 dnsResolve 的硬编码 DNS_MAP / 哈希回退编造 IP。
import { useState } from 'react';
import { type Device } from '../types';
import { api } from '../api';

interface Props {
  devices: Record<string, Device>;
  topologyId: string | null;
  srcDevice: string;
  engineMode?: 'full' | 'lite';
}

export default function DiagnosticDNS(props: Props) {
  const { topologyId, srcDevice, engineMode } = props;
  const [domain, setDomain] = useState<string>('www.example.com');
  const [running, setRunning] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [ip, setIp] = useState<string | null>(null);

  const resolve = async () => {
    const d = (domain || '').trim();
    if (!d) {
      setError('错误：请输入域名，例如 www.example.com');
      return;
    }
    if (!topologyId) {
      setError('错误：当前拓扑未加载，无法解析。');
      return;
    }
    if (!srcDevice) {
      setError('错误：请先在拓扑中选择源设备（用于开机校验）。');
      return;
    }
    setRunning(true);
    setError(null);
    setIp(null);
    try {
      const data = await api.diagnosticDNS(topologyId, srcDevice, d);
      if (data && data.ip) {
        setIp(data.ip);
      } else if (data && data.error) {
        setError(`❌ DNS 解析失败：${data.error}`);
      } else {
        setError('❌ DNS 解析失败');
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // 后端在 sandbox 无网时返回含"DNS 解析失败"的 404，前端如实展示。
      setError(`❌ ${msg}`);
    } finally {
      setRunning(false);
    }
  };

  const lines: string[] = [];
  if (ip) {
    lines.push(`服务器:  系统 DNS`);
    lines.push('');
    lines.push(`非权威应答:`);
    lines.push(`名称:    ${domain}`);
    lines.push(`Addresses:  ${ip}`);
    lines.push('');
    lines.push(`解析成功：${domain} → ${ip}`);
  }

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
          {running ? (
            <div className="diag-line">⏳ 解析中...</div>
          ) : error ? (
            <div className="diag-line">{error}</div>
          ) : ip ? (
            lines.map((line, idx) => (
              <div key={`dns-${idx}`} className="diag-line">
                {line || ' '}
              </div>
            ))
          ) : (
            <div className="diag-output-empty">输入域名后点击「解析」</div>
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
