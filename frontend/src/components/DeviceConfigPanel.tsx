// DeviceConfigPanel - 设备 IP 地址配置面板（方案二：Web UI 配置界面）
//
// 与方案三的 REST 端点 /api/topologies/:id/devices/:deviceId/ip-config 对接，
// 让用户在图形界面直接配置 IP，而不必敲 CLI。三套入口（CLI / Web UI / REST API）
// 共享同一套后端持久化逻辑，配置结果完全一致。
//
//   - 终端主机（pc / client / server）：配置 IP / 子网掩码 / 默认网关 / DNS
//   - 网络设备（交换机 / 路由 / VTEP 等）：选择接口后配置 IP / 子网掩码
import { useCallback, useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import { type Device, type IPConfig, type SetIPConfigRequest } from '../types';

interface DeviceConfigPanelProps {
  topologyId: string | null;
  selectedDevice: Device | null;
  onApplied?: (cfg: IPConfig) => void;
}

const TERMINAL_TYPES = new Set(['pc', 'client', 'server']);

export default function DeviceConfigPanel(props: DeviceConfigPanelProps) {
  const { topologyId, selectedDevice, onApplied } = props;
  const deviceId = selectedDevice?.id ?? null;

  const isTerminal = selectedDevice ? TERMINAL_TYPES.has(selectedDevice.type) : false;
  const mode: 'host' | 'interface' = isTerminal ? 'host' : 'interface';

  // 网络设备的接口列表（用于下拉）
  const interfaceOptions = useMemo<string[]>(() => {
    if (!selectedDevice || isTerminal) return [];
    return Object.keys(selectedDevice.interfaces || {});
  }, [selectedDevice, isTerminal]);

  // 全部接口（用于「配置信息」表格的只读概览）
  const interfaceList = useMemo(() => Object.values(selectedDevice?.interfaces || {}), [selectedDevice]);

  const [ip, setIp] = useState('');
  const [mask, setMask] = useState('');
  const [gateway, setGateway] = useState('');
  const [dns, setDns] = useState('');
  const [iface, setIface] = useState('');

  const [current, setCurrent] = useState<IPConfig | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  // 设备切换时加载当前配置并填充表单
  useEffect(() => {
    if (!topologyId || !deviceId) {
      setCurrent(null);
      return;
    }
    setMsg(null);
    setLoading(true);
    (async () => {
      try {
        const cfg = await api.getIPConfig(topologyId, deviceId);
        setCurrent(cfg);
        // 用返回值预填表单
        if (cfg.mode === 'host') {
          setIp(cfg.ip || '');
          setMask(cfg.subnet_mask || '');
          setGateway(cfg.gateway || '');
          setDns(cfg.dns || '');
        } else {
          setIface(cfg.interface || interfaceOptions[0] || '');
          setIp(cfg.ip || '');
          setMask(cfg.subnet_mask || '');
        }
      } catch (e) {
        setMsg({ kind: 'err', text: `加载配置失败: ${e instanceof Error ? e.message : String(e)}` });
      } finally {
        setLoading(false);
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [topologyId, deviceId]);

  const handleApply = useCallback(async () => {
    if (!topologyId || !deviceId) return;
    if (mode === 'interface' && interfaceOptions.length === 0) {
      setMsg({ kind: 'err', text: '该设备没有可用接口' });
      return;
    }
    if (!ip.trim()) {
      setMsg({ kind: 'err', text: 'IP 地址不能为空' });
      return;
    }
    setSaving(true);
    setMsg(null);
    const body: SetIPConfigRequest = { mode, ip: ip.trim(), subnet_mask: mask.trim() };
    if (mode === 'host') {
      body.gateway = gateway.trim();
      body.dns = dns.trim();
    } else {
      body.interface = iface || interfaceOptions[0] || '';
    }
    try {
      const cfg = await api.setIPConfig(topologyId, deviceId, body);
      setCurrent(cfg);
      setMsg({ kind: 'ok', text: `已应用：${cfg.ip}${cfg.cidr ? ` (${cfg.cidr})` : ''}` });
      onApplied?.(cfg);
    } catch (e) {
      setMsg({ kind: 'err', text: `应用失败: ${e instanceof Error ? e.message : String(e)}` });
    } finally {
      setSaving(false);
    }
  }, [topologyId, deviceId, mode, ip, mask, gateway, dns, iface, interfaceOptions, onApplied]);

  if (!selectedDevice) {
    return (
      <div className="ip-config-panel ip-config-hidden">
        <div className="ip-config-header">IP 配置</div>
        <div className="ip-config-empty">请选择一个设备以配置 IP 地址</div>
      </div>
    );
  }

  return (
    <div className="ip-config-panel">
      <div className="ip-config-header">
        <span>
          IP 配置 · {selectedDevice.name} ({selectedDevice.id})
        </span>
        <span className="ip-config-mode-tag">{mode === 'host' ? '终端主机' : '网络接口'}</span>
      </div>

      {loading ? (
        <div className="ip-config-loading">加载中…</div>
      ) : (
        <div className="ip-config-body">
          {interfaceList.length > 0 && (
            <div className="ip-config-table-wrap">
              <div className="ip-config-table-title">接口配置</div>
              <table className="ip-config-table">
                <thead>
                  <tr>
                    <th>接口名</th>
                    <th>IP 地址</th>
                    <th>子网掩码</th>
                    <th>状态</th>
                  </tr>
                </thead>
                <tbody>
                  {interfaceList.map((itf) => {
                    const st = (itf.status || '').toLowerCase();
                    const badgeClass = st === 'up' ? 'status-up' : st === 'down' ? 'status-down' : 'status-unknown';
                    return (
                      <tr key={itf.name}>
                        <td>{itf.name}</td>
                        <td>{itf.ip_address || '—'}</td>
                        <td>{itf.subnet_mask || '—'}</td>
                        <td>
                          <span className={`status-badge ${badgeClass}`}>{itf.status || '—'}</span>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}

          <div className="ip-config-form">
          {mode === 'interface' && (
            <div className="ip-config-row">
              <label className="ip-config-label">接口</label>
              {interfaceOptions.length > 0 ? (
                <select
                  className="ip-config-input"
                  value={iface}
                  onChange={(e) => {
                    const newIface = e.target.value;
                    setIface(newIface);
                    // 同步刷新该接口已配置的 IP/掩码（未配置则清空），避免点"应用"时
                    // 用上一个接口的 IP 写到新接口。未配接口同步清空让用户重新输入。
                    const itf = selectedDevice?.interfaces?.[newIface];
                    setIp(itf?.ip_address || '');
                    setMask(itf?.subnet_mask || '');
                  }}
                >
                  {interfaceOptions.map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
                </select>
              ) : (
                <span className="ip-config-note">（无可用接口）</span>
              )}
            </div>
          )}

          <div className="ip-config-row">
            <label className="ip-config-label">IP 地址</label>
            <input
              className="ip-config-input"
              value={ip}
              placeholder="如 10.0.10.10"
              onChange={(e) => setIp(e.target.value)}
              spellCheck={false}
            />
          </div>

          <div className="ip-config-row">
            <label className="ip-config-label">子网掩码</label>
            <input
              className="ip-config-input"
              value={mask}
              placeholder="如 255.255.255.0 或 24"
              onChange={(e) => setMask(e.target.value)}
              spellCheck={false}
            />
          </div>

          {mode === 'host' && (
            <>
              <div className="ip-config-row">
                <label className="ip-config-label">默认网关</label>
                <input
                  className="ip-config-input"
                  value={gateway}
                  placeholder="如 10.0.10.1"
                  onChange={(e) => setGateway(e.target.value)}
                  spellCheck={false}
                />
              </div>
              <div className="ip-config-row">
                <label className="ip-config-label">DNS</label>
                <input
                  className="ip-config-input"
                  value={dns}
                  placeholder="如 8.8.8.8"
                  onChange={(e) => setDns(e.target.value)}
                  spellCheck={false}
                />
              </div>
            </>
          )}

          <div className="ip-config-actions">
            <button className="btn btn-primary btn-sm" onClick={handleApply} disabled={saving || (mode === 'interface' && interfaceOptions.length === 0)}>
              {saving ? '应用中…' : '应用'}
            </button>
          </div>

          {msg && (
            <div className={`ip-config-msg ${msg.kind === 'ok' ? 'ip-config-msg-ok' : 'ip-config-msg-err'}`}>{msg.text}</div>
          )}

          {current && (
            <div className="ip-config-current">
              <div className="ip-config-current-title">当前配置</div>
              <div>IP: {current.ip || '—'}{current.cidr ? ` (${current.cidr})` : ''}</div>
              {mode === 'host' && (
                <>
                  <div>网关: {current.gateway || '—'}</div>
                  <div>DNS: {current.dns || '—'}</div>
                </>
              )}
            </div>
          )}
          </div>
        </div>
      )}
    </div>
  );
}
