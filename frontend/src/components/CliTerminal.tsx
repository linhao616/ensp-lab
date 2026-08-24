// CliTerminal - 类华为 VRP / eNSP 终端
// 迁移自 web/js/web-terminal.js + web/js/topo-canvas.js 中的 CLI 部分
//
// 特性:
//   - 仅当选中设备时才显示该设备的 CLI 面板（未选中则整体隐藏）
//   - 每个设备独立会话：命令与输出历史按设备分别保留，切换/重载均不丢失
//   - 命令历史上下键导航（按设备独立）
//   - 拖拽 ⋮ 调整高度 (min/max 限制)
//   - 暗色主题、等宽字体
//   - 输入框在底部
//   - 解析响应 JSON: {output, view, sub, targetDeviceID?}
import { useEffect, useRef, useState, useCallback } from 'react';
import { type Device, type CliResponse } from '../types';
import { api } from '../api';
import { requestCliComplete } from '../cliCompleteClient';

interface CliTerminalProps {
  topologyId: string | null;
  selectedDevice: Device | null;
  onOutput?: (output: string) => void;
  onTargetDevice?: (targetDeviceId: string) => void;
}

export interface LogEntry {
  time: string;
  prompt?: string;
  command?: string;
  output?: string;
  error?: boolean;
}

interface CliState {
  view: string;
  sub: string;
}

// 每个设备的独立会话：日志(命令+输出)、当前视图状态、命令回放历史
interface DeviceSession {
  logs: LogEntry[];
  cliState: CliState;
  cmdHistory: string[];
}

const MIN_HEIGHT = 120;
const MAX_HEIGHT_RATIO = 0.8; // 占视口高度
const DEFAULT_HEIGHT = 240;
const MAX_HISTORY = 200;
const MAX_LOGS = 500;
const EMPTY_SESSION: DeviceSession = { logs: [], cliState: { view: 'user', sub: '' }, cmdHistory: [] };

const SESSIONS_KEY = (topo: string) => `ensp-lab-cli-sessions-${topo}`;

function buildPrompt(device: Device | null, state: CliState): string {
  const sysname = device?.name || 'Huawei';
  if (state.view === 'system') return `[${sysname}] `;
  if (state.view === 'interface') return `[${sysname}-${state.sub || 'GigabitEthernet0/0/1'}] `;
  if (state.view === 'acl') return `[${sysname}-acl-basic-${state.sub || '2000'}] `;
  if (state.view === 'ospf') return `[${sysname}-ospf-${state.sub || '1'}] `;
  if (state.view === 'bgp') return `[${sysname}-bgp] `;
  if (state.view === 'aaa') return `[${sysname}-aaa] `;
  if (state.view === 'aaa-authen') return `[${sysname}-aaa-authen-${state.sub || ''}] `;
  if (state.view === 'aaa-domain') return `[${sysname}-aaa-domain-${state.sub || ''}] `;
  if (state.view === 'vty') return `[${sysname}-vty${state.sub || ''}] `;
  if (state.view === 'mlag') return `[${sysname}-mlag] `;
  if (state.view === 'isis') return `[${sysname}-isis] `;
  if (state.view === 'dhcp-pool') return `[${sysname}-dhcp-pool-${state.sub || ''}] `;
  if (state.view === 'mst-region') return `[${sysname}-mst-region] `;
  // P1-1 EVPN-BGP 控制面三视图：CurrentSub 已含完整后缀（evpn-instance-1 / bd-10 / bgp-<as>-l2vpn-evpn）。
  if (state.view === 'evpn-instance') return `[${sysname}-${state.sub || 'evpn-instance'}] `;
  if (state.view === 'bd') return `[${sysname}-${state.sub || 'bd'}] `;
  if (state.view === 'l2vpn-evpn') return `[${sysname}-${state.sub || 'l2vpn-evpn'}] `;
  return `<${sysname}> `;
}

function loadSessions(topo: string | null): Record<string, DeviceSession> {
  if (!topo) return {};
  try {
    const raw = localStorage.getItem(SESSIONS_KEY(topo));
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

function saveSessions(topo: string | null, sessions: Record<string, DeviceSession>) {
  if (!topo) return;
  try {
    localStorage.setItem(SESSIONS_KEY(topo), JSON.stringify(sessions));
  } catch {
    // ignore quota / serialization errors
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

// 简化的 ANSI 解析：支持颜色码
function parseAnsi(text: string): string {
  return text.replace(/\x1b\[([0-9;]*)m/g, (_match, codes: string) => {
    const list = (codes || '').split(';').filter(Boolean);
    const styles: string[] = [];
    const colors: Record<string, string> = {
      '30': '#1a1a2e', '31': '#f38ba8', '32': '#a6e3a1', '33': '#f9e2af',
      '34': '#89b4fa', '35': '#cba6f7', '36': '#94e2d5', '37': '#cdd6f4',
      '90': '#6c7086', '91': '#f38ba8', '92': '#a6e3a1', '93': '#f9e2af',
      '94': '#89b4fa', '95': '#cba6f7', '96': '#94e2d5', '97': '#cdd6f4',
    };
    for (const c of list) {
      if (c === '0') {
        styles.length = 0;
      } else if (c === '1') {
        styles.push('font-weight:bold');
      } else if (c === '4') {
        styles.push('text-decoration:underline');
      } else if (colors[c]) {
        styles.push(`color:${colors[c]}`);
      }
    }
    return styles.length ? `</span><span style="${styles.join(';')}">` : '</span><span>';
  });
}

function formatEntryHtml(entry: LogEntry): string {
  const time = entry.time;
  let html = `<span class="cli-time">[${time}]</span> `;
  if (entry.command !== undefined) {
    html += `<span class="cli-prompt">${escapeHtml(entry.prompt || '')}</span>${escapeHtml(entry.command)}`;
  } else if (entry.error) {
    html += `<span class="cli-error">${escapeHtml(entry.output || '')}</span>`;
  } else {
    html += `<span class="cli-out">${parseAnsi(escapeHtml(entry.output || ''))}</span>`;
  }
  return html;
}

// 计算候选集合的最长公共前缀（LCP），用于多候选时续补公共部分。
function longestCommonPrefix(cands: string[]): string {
  if (cands.length === 0) return '';
  let prefix = cands[0];
  for (const c of cands.slice(1)) {
    let i = 0;
    while (i < prefix.length && i < c.length && prefix[i] === c[i]) i++;
    prefix = prefix.slice(0, i);
    if (prefix === '') break;
  }
  return prefix;
}

// 用候选替换当前输入的最后一个 token，并附加尾随空格（模拟 VRP 补全后空格）。
function applyCompletion(input: string, candidate: string): string {
  const parts = input.split(' ');
  parts[parts.length - 1] = candidate;
  return parts.join(' ') + ' ';
}

export default function CliTerminal(props: CliTerminalProps) {
  const { topologyId, selectedDevice, onOutput, onTargetDevice } = props;
  const [sessions, setSessions] = useState<Record<string, DeviceSession>>(() => loadSessions(topologyId));
  const [input, setInput] = useState('');
  const [historyIdx, setHistoryIdx] = useState(-1);
  const [height, setHeight] = useState(DEFAULT_HEIGHT);
  const [busy, setBusy] = useState(false);

  // Tab 补全候选浮层状态
  const [candidates, setCandidates] = useState<string[]>([]);
  const [showCands, setShowCands] = useState(false);
  const [hlIdx, setHlIdx] = useState(0);

  const outputRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const resizeStateRef = useRef<{ startY: number; startH: number } | null>(null);
  // 已"连接"过的设备集合，用于仅在首次连接时写入欢迎语/自动 ipconfig
  const initializedRef = useRef<Set<string>>(new Set());

  const deviceId = selectedDevice?.id ?? null;
  const currentSession: DeviceSession = deviceId ? sessions[deviceId] ?? EMPTY_SESSION : EMPTY_SESSION;
  const logs = currentSession.logs;
  const cliState = currentSession.cliState;
  const cmdHistory = currentSession.cmdHistory;

  const stateRef = useRef(cliState);
  stateRef.current = cliState;

  // 用 ref 持有最新的 selectedDevice，避免 executeCommandFor 闭包捕获到过期设备
  const selectedDeviceRef = useRef(selectedDevice);
  selectedDeviceRef.current = selectedDevice;

  // 切换拓扑：重新加载对应拓扑的会话缓存
  useEffect(() => {
    setSessions(loadSessions(topologyId));
    initializedRef.current = new Set();
    setHistoryIdx(-1);
  }, [topologyId]);

  // 切换设备：首次连接该设备时写入欢迎语，PC 类自动获取 IP
  useEffect(() => {
    if (!selectedDevice) return;
    const id = selectedDevice.id;
    if (!initializedRef.current.has(id)) {
      initializedRef.current.add(id);
      setSessions((prev) => {
        const existing = prev[id];
        // 已有历史则保留，不重复写欢迎语
        if (existing && existing.logs.length > 0) return prev;
        return {
          ...prev,
          [id]: {
            logs: [{ time: new Date().toLocaleTimeString(), prompt: '', output: `连接到 ${selectedDevice.name} (${selectedDevice.id})` }],
            cliState: { view: 'user', sub: '' },
            cmdHistory: existing?.cmdHistory || [],
          },
        };
      });
      if (selectedDevice.type === 'pc' || selectedDevice.type === 'client' || selectedDevice.type === 'server') {
        const dev = selectedDevice;
        // 延迟到本轮渲染后执行，确保会话已就绪
        window.setTimeout(() => {
          void executeCommandFor(dev.id, 'ipconfig');
        }, 0);
      }
    }
    setHistoryIdx(-1);
    inputRef.current?.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedDevice?.id]);

  // 会话变更后持久化到 localStorage
  useEffect(() => {
    saveSessions(topologyId, sessions);
  }, [sessions, topologyId]);

  // 自动滚动到底部
  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [logs]);

  const appendLog = useCallback((devId: string, entry: LogEntry) => {
    setSessions((prev) => {
      const s = prev[devId] ?? { logs: [], cliState: { view: 'user', sub: '' }, cmdHistory: [] };
      return { ...prev, [devId]: { ...s, logs: [...s.logs.slice(-MAX_LOGS), entry] } };
    });
  }, []);

  const executeCommandFor = useCallback(
    async (devId: string, rawCmd: string) => {
      const cmd = rawCmd.trim();
      if (!cmd || !topologyId) return;
      // 写入命令回放历史
      setSessions((prev) => {
        const s = prev[devId] ?? { logs: [], cliState: { view: 'user', sub: '' }, cmdHistory: [] };
        const nextHist = s.cmdHistory[s.cmdHistory.length - 1] === cmd ? s.cmdHistory : [...s.cmdHistory, cmd];
        return { ...prev, [devId]: { ...s, cmdHistory: nextHist.slice(-MAX_HISTORY) } };
      });
      setHistoryIdx(-1);

      const prompt = buildPrompt(selectedDeviceRef.current, stateRef.current);
      appendLog(devId, { time: new Date().toLocaleTimeString(), prompt, command: cmd });
      setBusy(true);
      try {
        const res: CliResponse = await api.executeCli(topologyId, devId, cmd);
        if (res.view) {
          setSessions((prev) => {
            const s = prev[devId] ?? { logs: [], cliState: { view: 'user', sub: '' }, cmdHistory: [] };
            return { ...prev, [devId]: { ...s, cliState: { view: res.view || 'user', sub: res.sub || '' } } };
          });
        }
        if (res.output) {
          appendLog(devId, { time: new Date().toLocaleTimeString(), output: res.output });
          onOutput?.(res.output);
        }
        if (res.targetDeviceID && res.targetDeviceID !== devId) {
          onTargetDevice?.(res.targetDeviceID);
        }
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        appendLog(devId, { time: new Date().toLocaleTimeString(), output: `Error: ${msg}`, error: true });
      } finally {
        setBusy(false);
      }
    },
    [topologyId, appendLog, onOutput, onTargetDevice],
  );

  const executeCommand = useCallback(
    (rawCmd: string) => {
      if (!deviceId) return;
      void executeCommandFor(deviceId, rawCmd);
    },
    [deviceId, executeCommandFor],
  );

  const closeCands = useCallback(() => {
    setShowCands(false);
    setCandidates([]);
    setHlIdx(0);
  }, []);

  // Tab 补全：仅计算候选、零命令提交；后端 cli.Complete 同源、不执行。
  const doComplete = useCallback(async () => {
    if (!topologyId || !deviceId || !input.trim()) return;
    try {
      const cands = await requestCliComplete(
        { view: cliState.view, sub: cliState.sub, input },
        { topoId: topologyId, deviceId },
      );
      if (cands.length === 0) {
        closeCands();
        return;
      }
      if (cands.length === 1) {
        setInput(applyCompletion(input, cands[0]));
        closeCands();
        return;
      }
      // 多候选：先把公共前缀续补到最后一个 token，再展示浮层
      const lcp = longestCommonPrefix(cands);
      const parts = input.split(' ');
      const last = parts[parts.length - 1];
      if (lcp.length > last.length) {
        parts[parts.length - 1] = lcp;
        setInput(parts.join(' '));
      }
      setCandidates(cands);
      setHlIdx(0);
      setShowCands(true);
    } catch {
      // 补全失败（网络/超时）不应打断输入，静默忽略
      closeCands();
    }
  }, [topologyId, deviceId, input, cliState.view, cliState.sub, setInput, closeCands]);

  // ? 就地帮助（VRP 风格）：复用同一补全后端（单一事实源、零副作用）。
  // 与 Tab 不同，? 只“显示”候选、不自动续补——即便只有一个候选也只弹浮层。
  // 关键：后端 completeParams 按“下一 token”计算候选，故 ? 触发前须在输入尾补一个空格
  // （把光标推进到下一参数位置），否则 "dis aaa" 会被当作“补全当前 token”而非“列出 aaa 的子参数”。
  const doHelp = useCallback(async () => {
    if (!topologyId || !deviceId) return;
    const query = input === '' || input.endsWith(' ') ? input : `${input.trimEnd()} `;
    try {
      const cands = await requestCliComplete(
        { view: cliState.view, sub: cliState.sub, input: query },
        { topoId: topologyId, deviceId },
      );
      if (cands.length === 0) {
        closeCands();
        return;
      }
      setInput(query); // 推进光标，命令行走 VRP 风格空格（不替写候选）
      setCandidates(cands);
      setHlIdx(0);
      setShowCands(true);
    } catch {
      // 补全失败（网络/超时）不应打断输入，静默忽略
      closeCands();
    }
  }, [topologyId, deviceId, input, cliState.view, cliState.sub, setInput, closeCands]);

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Tab') {
      e.preventDefault();
      if (showCands && candidates.length > 0) {
        // 浮层已开：Tab 循环高亮
        setHlIdx((i) => (i + 1) % candidates.length);
      } else {
        void doComplete();
      }
      return;
    }
    if (e.key === '?' && !e.ctrlKey && !e.metaKey && !e.altKey) {
      // VRP 风格 ? 就地帮助：在输入尾补一个空格把光标推进到下一 token，
      // 复用同一补全后端（单一事实源、零副作用）展示可接参数；? 只“显示”不“替写”。
      e.preventDefault();
      void doHelp();
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (showCands && candidates.length > 0) {
        // 浮层已开：Enter 确认高亮候选
        const c = candidates[hlIdx];
        if (c) setInput(applyCompletion(input, c));
        closeCands();
        return;
      }
      const cmd = input;
      setInput('');
      executeCommand(cmd);
      return;
    }
    if (e.key === 'Escape' && showCands) {
      e.preventDefault();
      closeCands();
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (showCands && candidates.length > 0) {
        setHlIdx((i) => (i - 1 + candidates.length) % candidates.length);
        return;
      }
      if (cmdHistory.length === 0) return;
      const idx = historyIdx === -1 ? cmdHistory.length - 1 : Math.max(0, historyIdx - 1);
      setHistoryIdx(idx);
      setInput(cmdHistory[idx] || '');
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (showCands && candidates.length > 0) {
        setHlIdx((i) => (i + 1) % candidates.length);
        return;
      }
      if (historyIdx === -1) return;
      const idx = historyIdx + 1;
      if (idx >= cmdHistory.length) {
        setHistoryIdx(-1);
        setInput('');
      } else {
        setHistoryIdx(idx);
        setInput(cmdHistory[idx] || '');
      }
      return;
    }
    if (e.key === 'l' && e.ctrlKey) {
      e.preventDefault();
      if (deviceId) {
        setSessions((prev) => {
          const s = prev[deviceId] ?? EMPTY_SESSION;
          return { ...prev, [deviceId]: { ...s, logs: [] } };
        });
      }
      return;
    }
    if (e.key === 'c' && e.ctrlKey) {
      e.preventDefault();
      const prompt = buildPrompt(selectedDevice, stateRef.current);
      appendLog(deviceId ?? '', { time: new Date().toLocaleTimeString(), prompt, command: `${input}^C` });
      setInput('');
    }
  };

  // 拖拽调整高度
  const onResizerMouseDown = (e: React.MouseEvent) => {
    e.preventDefault();
    resizeStateRef.current = { startY: e.clientY, startH: height };
    const onMove = (ev: MouseEvent) => {
      const st = resizeStateRef.current;
      if (!st) return;
      const delta = st.startY - ev.clientY;
      const maxH = Math.floor(window.innerHeight * MAX_HEIGHT_RATIO);
      const newH = Math.max(MIN_HEIGHT, Math.min(maxH, st.startH + delta));
      setHeight(newH);
    };
    const onUp = () => {
      resizeStateRef.current = null;
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      document.body.classList.remove('resizing-ns');
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    document.body.classList.add('resizing-ns');
  };

  const prompt = buildPrompt(selectedDevice, cliState);

  return (
    <div className={`cli-section ${selectedDevice ? '' : 'cli-hidden'}`} style={{ height }}>
      {selectedDevice && (
        <>
          <div className="cli-resizer" onMouseDown={onResizerMouseDown} title="拖拽调整高度">
            <span className="cli-resizer-handle">⋮</span>
          </div>
          <div className="cli-header">
            <span>
              {selectedDevice ? `CLI - ${selectedDevice.name} (${selectedDevice.id})` : 'CLI - 未选择设备'}
            </span>
            <span className="cli-header-state">
              view: {cliState.view || 'user'} {busy ? '· 执行中…' : ''}
            </span>
          </div>
          <div className="cli-terminal" ref={outputRef}>
            {logs.map((entry, idx) => (
              <div
                key={idx}
                className={`cli-line ${entry.error ? 'cli-line-error' : ''} ${entry.command !== undefined ? 'cli-line-echo' : ''}`}
                dangerouslySetInnerHTML={{ __html: formatEntryHtml(entry) }}
              />
            ))}
          </div>
          <div className="cli-input" style={{ position: 'relative' }}>
            <span className="cli-input-prompt">{prompt}</span>
            <input
              ref={inputRef}
              type="text"
              value={input}
              onChange={(e) => {
                setInput(e.target.value);
                if (showCands) closeCands();
              }}
              onKeyDown={onKeyDown}
              disabled={busy}
              placeholder={busy ? '执行中…' : '输入命令，按 Enter 执行'}
              spellCheck={false}
              autoComplete="off"
            />
            {showCands && candidates.length > 0 && (
              <div
                className="cli-complete-popup"
                style={{
                  position: 'absolute',
                  bottom: '100%',
                  left: 0,
                  marginBottom: 4,
                  background: '#1e1e2e',
                  border: '1px solid #45475a',
                  borderRadius: 4,
                  padding: 4,
                  maxHeight: 160,
                  overflowY: 'auto',
                  zIndex: 20,
                  minWidth: 160,
                }}
              >
                {candidates.map((c, i) => (
                  <div
                    key={c}
                    onMouseDown={(ev) => {
                      ev.preventDefault();
                      setInput(applyCompletion(input, c));
                      closeCands();
                    }}
                    style={{
                      padding: '2px 8px',
                      cursor: 'pointer',
                      fontFamily: 'monospace',
                      color: i === hlIdx ? '#1e1e2e' : '#cdd6f4',
                      background: i === hlIdx ? '#89b4fa' : 'transparent',
                    }}
                  >
                    {c}
                  </div>
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
