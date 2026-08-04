// DiagnosticPCAP - 网络诊断工具集 · 抓包 (PCAP) Tab
// 选择设备与接口。真实数据包捕获（PCAP）尚未接入，为保证数据真实，
// 已停用模拟抓包，表格区仅给出诚实占位提示，不返回编造的源/目的 IP 与协议分布。
import { useEffect, useMemo, useState } from 'react';
import { type Device } from '../types';
import { interfaceEntries } from './diagnosticUtils';

interface Props {
  devices: Record<string, Device>;
}

export default function DiagnosticPCAP(props: Props) {
  const { devices } = props;
  const [targetId, setTargetId] = useState<string>('');
  const [iface, setIface] = useState<string>('');
  const [notice, setNotice] = useState<string | null>(null);

  const allList = Object.values(devices).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));
  const ifaces = useMemo(() => (targetId ? interfaceEntries(devices[targetId]) : []), [devices, targetId]);

  useEffect(() => {
    if (targetId && ifaces.length > 0 && !ifaces.some((i) => i.name === iface)) {
      setIface(ifaces[0].name);
    }
  }, [targetId, ifaces, iface]);

  const start = () => {
    if (!targetId || !iface) return;
    const devName = devices[targetId]?.name || targetId;
    setNotice(
      `已选择设备 ${devName} · 接口 ${iface}，但真实数据包捕获（PCAP）尚未接入。为保证数据真实，已停用模拟抓包，不返回编造的源/目的 IP 与协议分布。`,
    );
  };

  return (
    <div className="diag-pcap">
      <div className="diag-form diag-form-inline">
        <div className="diag-row">
          <label className="diag-label">设备</label>
          <select className="diag-select" value={targetId} onChange={(e) => setTargetId(e.target.value)}>
            <option value="">— 选择设备 —</option>
            {allList.map((d) => (
              <option key={`pc-${d.id}`} value={d.id}>
                {d.name}
              </option>
            ))}
          </select>
        </div>
        <div className="diag-row">
          <label className="diag-label">接口</label>
          <select className="diag-select" value={iface} onChange={(e) => setIface(e.target.value)} disabled={ifaces.length === 0}>
            {ifaces.length === 0 ? (
              <option value="">— 无接口 —</option>
            ) : (
              ifaces.map((i) => (
                <option key={`pc-if-${i.name}`} value={i.name}>
                  {i.name}
                  {i.ip ? ` (${i.ip})` : ''}
                </option>
              ))
            )}
          </select>
        </div>
        <div className="diag-actions">
          <button type="button" className="diag-btn diag-btn-start" onClick={start} disabled={!targetId || !iface}>
            ▶ 开始抓包
          </button>
        </div>
      </div>

      <div className="diag-pcap-table">
        <div className="diag-pcap-head">
          <span className="diag-pcap-c diag-pcap-time">时间</span>
          <span className="diag-pcap-c">源 IP</span>
          <span className="diag-pcap-c">目标 IP</span>
          <span className="diag-pcap-c diag-pcap-proto">协议</span>
          <span className="diag-pcap-c diag-pcap-len">长度</span>
        </div>
        <div className="diag-pcap-body">
          {notice ? (
            <div className="diag-output-empty">{notice}</div>
          ) : (
            <div className="diag-output-empty">选择设备与接口后点击「开始抓包」</div>
          )}
        </div>
      </div>
    </div>
  );
}
