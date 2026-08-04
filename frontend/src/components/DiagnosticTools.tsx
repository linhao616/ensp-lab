// DiagnosticTools - 网络诊断工具集窗口（可拖动浮动窗口）
// 复用 FloatWindow 容器；内部以 Tab 切换 7 个诊断工具；
// 联动：
//   - 拓扑选中设备 → 自动填充「源设备」（srcDevice 由 App 提供）
//   - Ping 历史双击 → 将目标设备填入 Traceroute（traceTarget 内部状态）
import { useEffect, useState } from 'react';
import { type Device, type Link } from '../types';
import { api } from '../api';
import FloatWindow, { type Rect } from './FloatWindow';
import DiagnosticPing from './DiagnosticPing';
import DiagnosticTraceroute from './DiagnosticTraceroute';
import DiagnosticTelnet from './DiagnosticTelnet';
import DiagnosticARP from './DiagnosticARP';
import DiagnosticDNS from './DiagnosticDNS';
import DiagnosticBandwidth from './DiagnosticBandwidth';
import DiagnosticPCAP from './DiagnosticPCAP';

type TabKey = 'ping' | 'traceroute' | 'telnet' | 'arp' | 'dns' | 'bandwidth' | 'pcap';

const TABS: { key: TabKey; label: string; icon: string }[] = [
  { key: 'ping', label: 'Ping', icon: '📶' },
  { key: 'traceroute', label: 'Traceroute', icon: '🛰' },
  { key: 'telnet', label: 'Telnet/SSH', icon: '🔌' },
  { key: 'arp', label: 'ARP', icon: '📋' },
  { key: 'dns', label: 'DNS', icon: '🌐' },
  { key: 'bandwidth', label: '带宽测试', icon: '📊' },
  { key: 'pcap', label: '抓包', icon: '📡' },
];

interface Props {
  topologyId: string | null;
  devices: Record<string, Device>;
  links: Link[];
  srcDevice: string;
  onSrcChange: (id: string) => void;
  title: string;
  zIndex: number;
  initialRect: Rect;
  minimized: boolean;
  maximized: boolean;
  onFocus: () => void;
  onClose: () => void;
  onMinimize: () => void;
  onToggleMaximize: () => void;
  onRectChange: (rect: Rect) => void;
}

export default function DiagnosticTools(props: Props) {
  const {
    topologyId,
    devices,
    links,
    srcDevice,
    onSrcChange,
    title,
    zIndex,
    initialRect,
    minimized,
    maximized,
    onFocus,
    onClose,
    onMinimize,
    onToggleMaximize,
    onRectChange,
  } = props;

  const [active, setActive] = useState<TabKey>('ping');
  const [traceTarget, setTraceTarget] = useState<string>('');
  // 引擎能力级别（由后端 /api/system/status 提供），用于展示真实/仿真标签。
  const [engineMode, setEngineMode] = useState<'full' | 'lite'>('lite');

  useEffect(() => {
    let cancelled = false;
    api
      .systemStatus()
      .then((s) => {
        if (!cancelled) setEngineMode(s.engine_mode === 'full' ? 'full' : 'lite');
      })
      .catch(() => {
        /* 状态不可达时保持默认 lite，不阻塞 UI */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const onPickTarget = (dstId: string) => {
    setTraceTarget(dstId);
    setActive('traceroute');
  };

  return (
    <FloatWindow
      title={title}
      subtitle="7 个诊断工具"
      zIndex={zIndex}
      initialRect={initialRect}
      minimized={minimized}
      maximized={maximized}
      onFocus={onFocus}
      onClose={onClose}
      onMinimize={onMinimize}
      onToggleMaximize={onToggleMaximize}
      onRectChange={onRectChange}
    >
      <div className="diag-tools">
        <div className="diag-engine-bar">
          <span
            className={`diag-engine-badge ${engineMode === 'full' ? 'diag-engine-full' : 'diag-engine-lite'}`}
            title={engineMode === 'full' ? '基于真实协议栈（Linux gont）' : '基于拓扑模拟（ns-x 事件驱动，非真实协议栈）'}
          >
            {engineMode === 'full' ? '🔵 真实引擎（Linux）' : '🟡 仿真子集（Windows）'}
          </span>
          {engineMode === 'lite' && (
            <span className="diag-engine-hint">部分诊断结果基于拓扑模拟（非真实协议栈）</span>
          )}
        </div>
        <div className="diag-tabs" role="tablist">
          {TABS.map((t) => (
            <button
              key={t.key}
              type="button"
              role="tab"
              aria-selected={active === t.key}
              className={`diag-tab ${active === t.key ? 'diag-tab-active' : ''}`}
              onClick={() => setActive(t.key)}
              title={t.label}
            >
              <span className="diag-tab-icon">{t.icon}</span>
              <span className="diag-tab-label">{t.label}</span>
            </button>
          ))}
        </div>

        <div className="diag-body">
          <div className={active === 'ping' ? 'diag-pane' : 'diag-pane diag-pane-hidden'}>
            <DiagnosticPing
              topologyId={topologyId}
              devices={devices}
              srcDevice={srcDevice}
              onSrcChange={onSrcChange}
              onPickTarget={onPickTarget}
            />
          </div>

          <div className={active === 'traceroute' ? 'diag-pane' : 'diag-pane diag-pane-hidden'}>
            <DiagnosticTraceroute
              topologyId={topologyId}
              devices={devices}
              links={links}
              srcDevice={srcDevice}
              onSrcChange={onSrcChange}
              targetDevice={traceTarget}
              engineMode={engineMode}
            />
          </div>

          <div className={active === 'telnet' ? 'diag-pane' : 'diag-pane diag-pane-hidden'}>
            <DiagnosticTelnet topologyId={topologyId} devices={devices} />
          </div>

          <div className={active === 'arp' ? 'diag-pane' : 'diag-pane diag-pane-hidden'}>
            <DiagnosticARP devices={devices} links={links} />
          </div>

          <div className={active === 'dns' ? 'diag-pane' : 'diag-pane diag-pane-hidden'}>
            <DiagnosticDNS
              devices={devices}
              topologyId={topologyId}
              srcDevice={srcDevice}
              engineMode={engineMode}
            />
          </div>

          <div className={active === 'bandwidth' ? 'diag-pane' : 'diag-pane diag-pane-hidden'}>
            <DiagnosticBandwidth devices={devices} />
          </div>

          <div className={active === 'pcap' ? 'diag-pane' : 'diag-pane diag-pane-hidden'}>
            <DiagnosticPCAP devices={devices} />
          </div>
        </div>
      </div>
    </FloatWindow>
  );
}
