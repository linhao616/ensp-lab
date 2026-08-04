// DiagnosticBandwidth - 网络诊断工具集 · 带宽测试 (iPerf 风格) Tab
// 选择源/目标设备与协议（TCP/UDP）。真实带宽/吞吐监测尚未接入仿真引擎，
// 为保证数据真实，已停用模拟吞吐，结果区仅给出诚实占位提示，不返回编造数值。
import { useState } from 'react';
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
  const [notice, setNotice] = useState<string | null>(null);

  const allList = Object.values(devices).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));

  const start = () => {
    if (!srcId || !dstId || srcId === dstId) return;
    const srcName = devices[srcId]?.name || srcId;
    const dstName = devices[dstId]?.name || dstId;
    setNotice(
      `已选择 ${proto.toUpperCase()} ${srcName} → ${dstName}，但真实带宽/吞吐监测尚未接入仿真引擎（当前为 lite 模式）。为保证数据真实，已停用模拟吞吐，不返回编造的带宽/抖动/丢包数值。`,
    );
  };

  return (
    <div className="diag-bw">
      <div className="diag-form">
        <div className="diag-row">
          <label className="diag-label">源设备</label>
          <select className="diag-select" value={srcId} onChange={(e) => setSrcId(e.target.value)}>
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
          <select className="diag-select" value={dstId} onChange={(e) => setDstId(e.target.value)}>
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
              <input type="radio" name="bw-proto" checked={proto === 'tcp'} onChange={() => setProto('tcp')} />
              <span>TCP</span>
            </label>
            <label className="diag-radio">
              <input type="radio" name="bw-proto" checked={proto === 'udp'} onChange={() => setProto('udp')} />
              <span>UDP</span>
            </label>
          </div>
        </div>
        <div className="diag-actions">
          <button
            type="button"
            className="diag-btn diag-btn-start"
            onClick={start}
            disabled={!srcId || !dstId || srcId === dstId}
          >
            ▶ 开始测试
          </button>
        </div>
      </div>

      <div className="diag-output">
        <div className="diag-output-header">
          <span>iPerf 测试结果</span>
        </div>
        <div className="diag-output-body diag-mono">
          {notice ? (
            <div className="diag-output-empty">{notice}</div>
          ) : (
            <div className="diag-output-empty">点击「开始测试」进行吞吐测试</div>
          )}
        </div>
      </div>
    </div>
  );
}
