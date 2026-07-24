// DiagnosticPCAP - 网络诊断工具集 · 抓包 (PCAP) Tab
// 选择设备与接口，启动后实时生成捕获的数据包（时间/源IP/目标IP/协议/长度）。
import { useEffect, useMemo, useRef, useState } from 'react';
import { type Device } from '../types';
import { firstIp, interfaceEntries, randLen } from './diagnosticUtils';

interface Props {
  devices: Record<string, Device>;
}

interface Pkt {
  id: number;
  time: string;
  src: string;
  dst: string;
  proto: string;
  len: number;
}

const PROTOS = ['TCP', 'UDP', 'ICMP', 'DNS', 'ARP', 'HTTP'];

let pktSeq = 0;

export default function DiagnosticPCAP(props: Props) {
  const { devices } = props;
  const [targetId, setTargetId] = useState<string>('');
  const [iface, setIface] = useState<string>('');
  const [capturing, setCapturing] = useState<boolean>(false);
  const [packets, setPackets] = useState<Pkt[]>([]);
  const timerRef = useRef<number | null>(null);

  const allList = Object.values(devices).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));
  const ifaces = useMemo(() => (targetId ? interfaceEntries(devices[targetId]) : []), [devices, targetId]);

  useEffect(() => {
    if (targetId && ifaces.length > 0 && !ifaces.some((i) => i.name === iface)) {
      setIface(ifaces[0].name);
    }
  }, [targetId, ifaces, iface]);

  useEffect(() => {
    return () => {
      if (timerRef.current) window.clearInterval(timerRef.current);
    };
  }, []);

  // 所有设备 IP 池（含若干外部地址），用于生成更真实的流量
  const ipPool = useMemo(() => {
    const pool = Object.values(devices).map((d) => firstIp(d)).filter(Boolean);
    pool.push('8.8.8.8', '114.114.114.114', '1.1.1.1');
    return Array.from(new Set(pool));
  }, [devices]);

  const stop = () => {
    if (timerRef.current) window.clearInterval(timerRef.current);
    timerRef.current = null;
    setCapturing(false);
  };

  const start = () => {
    if (!targetId || !iface) return;
    setCapturing(true);
    setPackets([]);
    const localIp = firstIp(devices[targetId]) || '0.0.0.0';
    timerRef.current = window.setInterval(() => {
      const now = new Date();
      const ms = String(now.getMilliseconds()).padStart(3, '0');
      const time = `${now.toLocaleTimeString('zh-CN', { hour12: false })}.${ms}`;
      const proto = PROTOS[Math.floor(Math.random() * PROTOS.length)];
      // 一半概率以本机为源或目标，贴近接口真实流量
      const useLocalAsSrc = Math.random() < 0.5;
      const other = ipPool[Math.floor(Math.random() * ipPool.length)] || '0.0.0.0';
      const src = useLocalAsSrc ? localIp : other;
      const dst = useLocalAsSrc ? other : localIp;
      const len = randLen(proto);
      const pkt: Pkt = { id: ++pktSeq, time, src, dst, proto, len };
      setPackets((prev) => [...prev, pkt].slice(-200));
    }, 450);
  };

  return (
    <div className="diag-pcap">
      <div className="diag-form diag-form-inline">
        <div className="diag-row">
          <label className="diag-label">设备</label>
          <select className="diag-select" value={targetId} onChange={(e) => setTargetId(e.target.value)} disabled={capturing}>
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
          <select className="diag-select" value={iface} onChange={(e) => setIface(e.target.value)} disabled={capturing || ifaces.length === 0}>
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
          {capturing ? (
            <button type="button" className="diag-btn diag-btn-stop" onClick={stop}>
              ⏹ 停止抓包
            </button>
          ) : (
            <button type="button" className="diag-btn diag-btn-start" onClick={start} disabled={!targetId || !iface}>
              ▶ 开始抓包
            </button>
          )}
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
          {!capturing && packets.length === 0 ? (
            <div className="diag-output-empty">选择设备与接口后点击「开始抓包」</div>
          ) : (
            packets.map((p) => (
              <div key={p.id} className="diag-pcap-row">
                <span className="diag-pcap-c diag-pcap-time">{p.time}</span>
                <span className="diag-pcap-c">{p.src}</span>
                <span className="diag-pcap-c">{p.dst}</span>
                <span className={`diag-pcap-c diag-pcap-proto diag-proto-${p.proto}`}>{p.proto}</span>
                <span className="diag-pcap-c diag-pcap-len">{p.len}</span>
              </div>
            ))
          )}
        </div>
      </div>

      <div className="diag-pcap-foot">
        已捕获 {packets.length} 个数据包{targetId ? ` · 接口 ${iface}` : ''}
      </div>
    </div>
  );
}
