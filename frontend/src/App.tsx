// App - eNSP Web Lab 主应用
// 组合 TopologyCanvas / PacketAnimator / AnnotationLayer / CliTerminal
// （说明文字统一以 TextAnnotation 形式展示，不再使用独立的 DescriptionBox）
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import './styles.css';
import TopologyCanvas, { type CanvasMode } from './components/TopologyCanvas';
import PacketAnimator from './components/PacketAnimator';
import AnnotationLayer from './components/AnnotationLayer';
import DeviceDetail from './components/DeviceDetail';
import FloatWindow, { type Rect } from './components/FloatWindow';
import LeftPanel from './components/LeftPanel';
import DiagnosticTools from './components/DiagnosticTools';
import { useSimEvents } from './hooks/useSimEvents';
import { api } from './api';
import { VXLAN_PLANNING_TEMPLATE } from './data/vxlanTemplate';
import {
  type Topology,
  type Device,
  type Link,
  type Viewport,
  type SimStatus,
  type DeviceTypeEntry,
  type DeviceType,
  type LinkType,
  type LinkTypeMode,
  type DeviceStatus,
  type TextAnnotation,
  DEVICE_META,
  isLinkAllowed,
} from './types';

const DEFAULT_VIEWPORT: Viewport = { scale: 1, ox: 0, oy: 0 };

export default function App() {
  const [topologies, setTopologies] = useState<Topology[]>([]);
  const [selectedTopoId, setSelectedTopoId] = useState<string | null>(null);
  const [topology, setTopology] = useState<Topology | null>(null);
  const [selectedDeviceId, setSelectedDeviceId] = useState<string | null>(null);
  const [selectedLinkId, setSelectedLinkId] = useState<string | null>(null);
  const [viewport, setViewport] = useState<Viewport>(DEFAULT_VIEWPORT);
  const [mode, setMode] = useState<CanvasMode>('select');
  const [addType, setAddType] = useState<DeviceType | null>(null);
  const [linkSource, setLinkSource] = useState<{ deviceId: string; port: string } | null>(null);
  const [deviceTypes, setDeviceTypes] = useState<DeviceTypeEntry[]>([]);
  const [simStatus, setSimStatus] = useState<SimStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [, setLoading] = useState<string | null>(null);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [createTopoName, setCreateTopoName] = useState('');
  const [createTopoDesc, setCreateTopoDesc] = useState('');
  const [showEditDescModal, setShowEditDescModal] = useState(false);
  const [editDescText, setEditDescText] = useState('');

  // ---- 网络诊断工具集窗口状态 ----
  const [diagOpen, setDiagOpen] = useState(false);
  const [diagSrc, setDiagSrc] = useState<string>('');
  const [diagMin, setDiagMin] = useState(false);
  const [diagMax, setDiagMax] = useState(false);
  const [diagZ, setDiagZ] = useState(1000);
  const [diagRect, setDiagRect] = useState<Rect>(() => {
    const DW = 560;
    const DH = 460;
    const x = Math.max(24, window.innerWidth - DW - 24);
    const y = Math.max(72, window.innerHeight - DH - 24);
    return { x, y, w: DW, h: DH };
  });

  const openDiag = useCallback(() => {
    setDiagOpen(true);
    setDiagZ((z) => z + 1);
    if (selectedDeviceId) setDiagSrc(selectedDeviceId);
  }, [selectedDeviceId]);

  // 连线类型模式：'auto' 自动按约束推导类型；其余为手动指定的具体连线类型
  const [linkTypeMode, setLinkTypeMode] = useState<LinkTypeMode>('auto');
  // 左侧面板宽度（可拖拽调整）
  const [leftWidth, setLeftWidth] = useState(280);
  const leftWidthRef = useRef(leftWidth);
  leftWidthRef.current = leftWidth;

  // ---- 浮动窗口（设备详情）状态 ----
  // 每个打开的设备对应一个独立窗口：位置/大小/最小化/最大化
  interface WinState {
    rect: Rect;
    minimized: boolean;
    maximized: boolean;
  }
  const [windows, setWindows] = useState<Record<string, WinState>>({});
  const [, setZSeq] = useState(10);
  const [zMap, setZMap] = useState<Record<string, number>>({});

  const bringToFront = useCallback((deviceId: string) => {
    setZSeq((seq) => {
      const nz = seq + 1;
      setZMap((m) => ({ ...m, [deviceId]: nz }));
      return nz;
    });
  }, []);

  const openWindow = useCallback(
    (deviceId: string) => {
      const dev = topology?.devices[deviceId];
      if (!dev) return;
      setWindows((prev) => {
        const existing = prev[deviceId];
        if (existing) {
          // 已打开：聚焦 + 若最小化则还原（不重复创建）
          return { ...prev, [deviceId]: { ...existing, minimized: false } };
        }
        const count = Object.keys(prev).length;
        // 默认位置：级联偏移，避免完全重叠
        const rect: Rect = {
          x: 140 + (count % 6) * 32,
          y: 96 + (count % 6) * 28,
          w: 480,
          h: 380,
        };
        return { ...prev, [deviceId]: { rect, minimized: false, maximized: false } };
      });
      setSelectedDeviceId(deviceId);
      setSelectedLinkId(null);
      bringToFront(deviceId);
    },
    [topology, bringToFront],
  );

  const closeWindow = useCallback((deviceId: string) => {
    setWindows((prev) => {
      const n = { ...prev };
      delete n[deviceId];
      return n;
    });
    setZMap((m) => {
      const n = { ...m };
      delete n[deviceId];
      return n;
    });
  }, []);

  const minimizeWindow = useCallback((deviceId: string) => {
    setWindows((prev) =>
      prev[deviceId] ? { ...prev, [deviceId]: { ...prev[deviceId], minimized: !prev[deviceId].minimized } } : prev,
    );
  }, []);

  const toggleMaxWindow = useCallback((deviceId: string) => {
    setWindows((prev) =>
      prev[deviceId] ? { ...prev, [deviceId]: { ...prev[deviceId], maximized: !prev[deviceId].maximized } } : prev,
    );
  }, []);

  const updateWindowRect = useCallback((deviceId: string, rect: Rect) => {
    setWindows((prev) => (prev[deviceId] ? { ...prev, [deviceId]: { ...prev[deviceId], rect } } : prev));
  }, []);

  // 切换拓扑时加载该拓扑已持久化的窗口布局（仅保留仍存在设备的窗口）
  useEffect(() => {
    if (!selectedTopoId || !topology?.id) {
      setWindows({});
      setZMap({});
      setZSeq(10);
      return;
    }
    try {
      const raw = localStorage.getItem(`ensp-lab-windows-${selectedTopoId}`);
      const parsed = raw ? (JSON.parse(raw) as Record<string, WinState>) : {};
      const filtered: Record<string, WinState> = {};
      for (const [id, st] of Object.entries(parsed)) {
        if (topology.devices[id]) filtered[id] = st;
      }
      setWindows(filtered);
    } catch {
      setWindows({});
    }
    setZMap({});
    setZSeq(10);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedTopoId, topology?.id]);

  // 窗口布局变更后持久化到 localStorage（刷新后恢复上次位置/大小）
  useEffect(() => {
    if (!selectedTopoId) return;
    try {
      localStorage.setItem(`ensp-lab-windows-${selectedTopoId}`, JSON.stringify(windows));
    } catch {
      /* ignore quota */
    }
  }, [windows, selectedTopoId]);

  const canvasHostRef = useRef<HTMLDivElement>(null);

  const { events, isConnected } = useSimEvents(selectedTopoId);

  const selectedDevice = useMemo<Device | null>(() => {
    if (!topology || !selectedDeviceId) return null;
    return topology.devices[selectedDeviceId] || null;
  }, [topology, selectedDeviceId]);

  // 加载设备类型
  useEffect(() => {
    api
      .getDeviceTypes()
      .then(setDeviceTypes)
      .catch((e) => setError(`加载设备类型失败: ${e.message}`));
    api
      .getSimStatus()
      .then(setSimStatus)
      .catch(() => {
        /* 后端未启动时静默忽略 */
      });
  }, []);

  // 加载拓扑列表
  const loadTopologies = useCallback(async () => {
    try {
      const list = await api.listTopologies();
      setTopologies(list || []);
    } catch (e) {
      setError(`加载拓扑列表失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, []);

  useEffect(() => {
    void loadTopologies();
  }, [loadTopologies]);

  // 加载选中拓扑详情
  const loadTopology = useCallback(async (id: string) => {
    try {
      const t = await api.getTopology(id);
      setTopology(t);
      // 同步视口（如果有保存的）
      if (t.canvas_scale && t.canvas_scale > 0) {
        setViewport({ scale: t.canvas_scale, ox: t.canvas_offset_x || 0, oy: t.canvas_offset_y || 0 });
      } else {
        setViewport(DEFAULT_VIEWPORT);
      }
    } catch (e) {
      setError(`加载拓扑失败: ${e instanceof Error ? e.message : String(e)}`);
      setTopology(null);
    }
  }, []);

  useEffect(() => {
    if (selectedTopoId) {
      void loadTopology(selectedTopoId);
      api.getSimStatus(selectedTopoId).then(setSimStatus).catch(() => {});
    } else {
      setTopology(null);
      setViewport(DEFAULT_VIEWPORT);
    }
    setSelectedDeviceId(null);
    setDiagOpen(false);
    setDiagMin(false);
    setDiagMax(false);
    setMode('select');
    setAddType(null);
    setLinkSource(null);
  }, [selectedTopoId, loadTopology]);

  // 拓扑切换/加载时，初始化诊断工具「源设备」默认值（优先 vm-1，否则首个设备）
  useEffect(() => {
    if (!topology) {
      setDiagSrc('');
      return;
    }
    const ids = Object.keys(topology.devices);
    if (ids.length === 0) {
      setDiagSrc('');
      return;
    }
    const src = ids.includes('vm-1') ? 'vm-1' : ids[0];
    setDiagSrc(src);
  }, [topology?.id]);

  // ---- 事件处理 ----
  const handleSelectDevice = useCallback((deviceId: string | null) => {
    setSelectedDeviceId(deviceId);
    if (deviceId) {
      setSelectedLinkId(null); // 选中设备时清除连线高亮（互斥）
      setDiagSrc(deviceId); // 联动：诊断工具「源设备」自动填充
    }
  }, []);

  const handleSelectLink = useCallback((linkId: string | null) => {
    setSelectedLinkId(linkId);
  }, []);

  const handleDeleteLink = useCallback(
    async (linkId?: string) => {
      const id = linkId ?? selectedLinkId;
      if (!selectedTopoId || !topology || !id) return;
      const link = topology.links.find((l) => l.id === id);
      const label = link
        ? `${link.source_device}:${link.source_port} ↔ ${link.target_device}:${link.target_port}`
        : '该连线';
      if (!confirm(`确定删除连线 ${label}?`)) return;
      try {
        await api.deleteLink(selectedTopoId, id);
        setTopology((prev) => {
          if (!prev) return prev;
          const links = prev.links.filter((l) => l.id !== id);
          return { ...prev, links };
        });
        if (selectedLinkId === id) setSelectedLinkId(null);
      } catch (e) {
        setError(`删除连线失败: ${e instanceof Error ? e.message : String(e)}`);
      }
    },
    [selectedTopoId, topology, selectedLinkId],
  );

  // 点击清单项时把视口平移到该连线中点（居中定位）
  const handleLocateLink = useCallback(
    (linkId: string) => {
      if (!topology) return;
      const link = topology.links.find((l) => l.id === linkId);
      if (!link) return;
      const s = topology.devices[link.source_device];
      const t = topology.devices[link.target_device];
      if (!s || !t) return;
      const mx = ((Number(s.position_x) || 0) + (Number(t.position_x) || 0)) / 2;
      const my = ((Number(s.position_y) || 0) + (Number(t.position_y) || 0)) / 2;
      const host = canvasHostRef.current;
      const w = host ? host.clientWidth : window.innerWidth;
      const h = host ? host.clientHeight : window.innerHeight;
      setViewport((v) => ({ ...v, ox: w / 2 - mx * v.scale, oy: h / 2 - my * v.scale }));
    },
    [topology],
  );

  const handleDeviceMove = useCallback(
    (deviceId: string, x: number, y: number) => {
      setTopology((prev) => {
        if (!prev) return prev;
        const dev = prev.devices[deviceId];
        if (!dev) return prev;
        dev.position_x = x;
        dev.position_y = y;
        return { ...prev, devices: { ...prev.devices, [deviceId]: { ...dev } } };
      });
    },
    [],
  );

  // 链路端口标签偏移：拖动时仅更新本地状态（实时跟手），松开时再持久化
  const handleLinkChange = useCallback((linkId: string, fields: Partial<Link>) => {
    setTopology((prev) => {
      if (!prev) return prev;
      const links = prev.links.map((l) => (l.id === linkId ? { ...l, ...fields } : l));
      return { ...prev, links };
    });
  }, []);

  const handleLinkPersist = useCallback(
    async (linkId: string, fields: Partial<Link>) => {
      if (!selectedTopoId) return;
      try {
        await api.updateLink(selectedTopoId, linkId, fields);
      } catch (e) {
        setError(`保存端口标签位置失败: ${e instanceof Error ? e.message : String(e)}`);
      }
    },
    [selectedTopoId],
  );

  const handleAddDeviceAt = useCallback(
    async (x: number, y: number, type: DeviceType) => {
      if (!selectedTopoId) {
        setError('请先选择或创建拓扑');
        return;
      }
      const id = `dev-${Date.now()}`;
      const name = DEVICE_META[type]?.label || `Device-${id.slice(-4)}`;
      try {
        const dev = await api.addDevice(selectedTopoId, {
          id,
          name,
          type,
          status: 'running' as DeviceStatus,
          position_x: x,
          position_y: y,
        });
        setTopology((prev) =>
          prev ? { ...prev, devices: { ...prev.devices, [dev.id]: dev } } : prev,
        );
        setSelectedDeviceId(dev.id);
        setMode('select');
        setAddType(null);
      } catch (e) {
        setError(`添加设备失败: ${e instanceof Error ? e.message : String(e)}`);
      }
    },
    [selectedTopoId],
  );

  const handleCreateLink = useCallback(
    async (srcId: string, srcPort: string, dstId: string, dstPort: string, explicitType?: LinkType) => {
      if (!selectedTopoId || !topology) return;
      const src = topology.devices[srcId];
      const dst = topology.devices[dstId];
      if (!src || !dst) return;
      // 连线类型约束：Auto 模式按约束矩阵自动推导并拒绝非法组合；
      // 手动模式由调用方传入 explicitType（后端仍会校验非法组合并返回明确错误）。
      // 无论 Auto 还是手动，先校验非法组合（后端同样会拦截，这里给即时提示）
      const rule = isLinkAllowed(src.type, dst.type);
      if (!rule.allowed) {
        setError(rule.message || '不允许建立该连线');
        setLinkSource(null);
        return;
      }
      const linkType = explicitType !== undefined ? explicitType : (rule.linkType || 'underlay');
      const id = `link-${Date.now()}`;
      const link: Partial<Link> = {
        id,
        source_device: srcId,
        source_port: srcPort,
        target_device: dstId,
        target_port: dstPort,
        link_type: linkType,
        cable_type: 'ethernet',
        status: 'up',
      };
      // VXLAN 隧道自动分配 VNI（以 5000 为基，避免与已有冲突）
      if (linkType === 'vxlan') {
        const n = topology.links.filter((l) => l.link_type === 'vxlan').length;
        link.vxlan_vni = 5000 + n;
      }
      try {
        const created = await api.addLink(selectedTopoId, link);
        setTopology((prev) => (prev ? { ...prev, links: [...prev.links, created] } : prev));
      } catch (e) {
        setError(`创建链路失败: ${e instanceof Error ? e.message : String(e)}`);
      }
    },
    [selectedTopoId, topology],
  );

  const handlePowerDevice = useCallback(
    async (deviceId: string, action: 'on' | 'off') => {
      if (!selectedTopoId || !topology) return;
      try {
        const updated = await api.powerDevice(selectedTopoId, deviceId, action);
        setTopology((prev) =>
          prev ? { ...prev, devices: { ...prev.devices, [deviceId]: updated } } : prev,
        );
      } catch (e) {
        setError(`电源操作失败: ${e instanceof Error ? e.message : String(e)}`);
      }
    },
    [selectedTopoId, topology],
  );

  const handleAnnotationChange = useCallback(
    async (id: string, fields: Partial<TextAnnotation>) => {
      if (!selectedTopoId || !topology) return;
      // 本地先更新
      setTopology((prev) => {
        if (!prev) return prev;
        const annotations = prev.annotations.map((a) => (a.id === id ? { ...a, ...fields } : a));
        return { ...prev, annotations };
      });
      try {
        await api.updateAnnotation(selectedTopoId, id, fields);
      } catch (e) {
        setError(`更新标注失败: ${e instanceof Error ? e.message : String(e)}`);
      }
    },
    [selectedTopoId, topology],
  );

  const handleAnnotationDelete = useCallback(
    async (id: string) => {
      if (!selectedTopoId || !topology) return;
      // 乐观删除：先更新本地状态（即时反馈），后端失败再回滚
      const removed = topology.annotations.find((a) => a.id === id);
      setTopology((prev) => {
        if (!prev) return prev;
        return { ...prev, annotations: prev.annotations.filter((a) => a.id !== id) };
      });
      try {
        await api.deleteAnnotation(selectedTopoId, id);
      } catch (e) {
        if (removed) {
          setTopology((prev) =>
            prev ? { ...prev, annotations: [...prev.annotations, removed] } : prev,
          );
        }
        setError(`删除标注失败: ${e instanceof Error ? e.message : String(e)}`);
      }
    },
    [selectedTopoId, topology],
  );

  const handleAddAnnotation = useCallback(async () => {
    if (!selectedTopoId || !topology) return;
    // 在画布可视区域中央创建标注 (近似：扣除设备面板 180px 和顶部工具栏)
    const canvasW = Math.max(200, window.innerWidth - 180);
    const canvasH = Math.max(200, window.innerHeight - 100);
    const cx = (canvasW / 2 - viewport.ox) / viewport.scale;
    const cy = (canvasH / 2 - viewport.oy) / viewport.scale;
    const id = `anno-${Date.now()}`;
    try {
      const anno = await api.addAnnotation(selectedTopoId, {
        id,
        text: '新标注',
        position_x: cx,
        position_y: cy,
      });
      setTopology((prev) => (prev ? { ...prev, annotations: [...prev.annotations, anno] } : prev));
    } catch (e) {
      setError(`创建标注失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [selectedTopoId, topology, viewport]);

  // 创建一个预填 VXLAN 规划说明的 TextAnnotation（纯 TXT 格式）。
  // 位置在画布右上角避开设备密集区，方便用户直接拖到合适位置。
  const handleAddVxlanTemplate = useCallback(async () => {
    if (!selectedTopoId || !topology) return;
    const canvasW = Math.max(200, window.innerWidth - 180);
    const tx = (canvasW - 60 - viewport.ox) / viewport.scale;
    const ty = (60 - viewport.oy) / viewport.scale;
    const id = `anno-vxlan-${Date.now()}`;
    try {
      const anno = await api.addAnnotation(selectedTopoId, {
        id,
        text: VXLAN_PLANNING_TEMPLATE,
        position_x: tx,
        position_y: ty,
      });
      setTopology((prev) => (prev ? { ...prev, annotations: [...prev.annotations, anno] } : prev));
    } catch (e) {
      setError(`创建 VXLAN 规划模板失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [selectedTopoId, topology, viewport]);

  const handleSaveLayout = useCallback(async () => {
    if (!selectedTopoId || !topology) return;
    setSaving(true);
    try {
      // 保存所有设备位置
      for (const dev of Object.values(topology.devices)) {
        await api.updateDevice(selectedTopoId, dev.id, {
          position_x: dev.position_x,
          position_y: dev.position_y,
        });
      }
      await api.updateTopology(selectedTopoId, {
        canvas_scale: viewport.scale,
        canvas_offset_x: viewport.ox,
        canvas_offset_y: viewport.oy,
      });
      setError(null);
    } catch (e) {
      setError(`保存布局失败: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setSaving(false);
    }
  }, [selectedTopoId, topology, viewport]);

  const handleCreateTopology = useCallback(() => {
    setCreateTopoName('');
    setShowCreateModal(true);
  }, []);

  const handleConfirmCreateTopology = useCallback(async () => {
    const name = createTopoName.trim();
    if (!name) return;
    // 后端 validateTopoID 仅接受 [A-Za-z0-9_-]{1,64}；中文等非 ASCII 名生成不了合法 id 时
    // 不传 id，由后端 generateID() 自动生成（hex），避免 400。
    const slug = name.toLowerCase().replace(/\s+/g, '-');
    const id = /^[A-Za-z0-9_-]{1,64}$/.test(slug) ? slug : undefined;
    setShowCreateModal(false);
    setLoading('创建拓扑中...');
    try {
      const t = await api.createTopology({ id, name, description: createTopoDesc.trim() || undefined });
      setTopologies((prev) => [...prev, t]);
      setSelectedTopoId(t.id);
    } catch (e) {
      setError(`创建拓扑失败: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setLoading(null);
    }
  }, [createTopoName, createTopoDesc]);

  const openEditDesc = useCallback(() => {
    if (!selectedTopoId) return;
    const t = topologies.find((x) => x.id === selectedTopoId);
    setEditDescText(t?.description ?? '');
    setShowEditDescModal(true);
  }, [selectedTopoId, topologies]);

  const handleSaveDescription = useCallback(async () => {
    if (!selectedTopoId) return;
    const desc = editDescText.trim();
    try {
      const updated = await api.updateTopology(selectedTopoId, { description: desc });
      setTopologies((prev) => prev.map((x) => (x.id === selectedTopoId ? { ...x, description: updated.description } : x)));
      setTopology((prev) => (prev && prev.id === selectedTopoId ? { ...prev, description: updated.description } : prev));
      setShowEditDescModal(false);
    } catch (e) {
      setError(`保存拓扑说明失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [selectedTopoId, editDescText]);

  const handleDeleteTopology = useCallback(async () => {
    if (!selectedTopoId) return;
    const t = topologies.find((x) => x.id === selectedTopoId);
    if (!confirm(`确定删除拓扑「${t?.name ?? selectedTopoId}」？该操作不可恢复。`)) return;
    try {
      await api.deleteTopology(selectedTopoId);
      setTopologies((prev) => prev.filter((x) => x.id !== selectedTopoId));
      setSelectedTopoId(null);
      setTopology(null);
      setSelectedDeviceId(null);
      setSelectedLinkId(null);
    } catch (e) {
      setError(`删除拓扑失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [selectedTopoId, topologies]);

  // ---- 网络诊断工具集：源设备联动 / 窗口已在 App 顶层统一管理 ----

  const handleDeleteDevice = useCallback(async () => {
    if (!selectedTopoId || !topology || !selectedDeviceId) return;
    if (!confirm(`确定删除设备 ${selectedDevice?.name}?`)) return;
    try {
      await api.deleteDevice(selectedTopoId, selectedDeviceId);
      setTopology((prev) => {
        if (!prev) return prev;
        const devices = { ...prev.devices };
        delete devices[selectedDeviceId];
        const links = prev.links.filter(
          (l) => l.source_device !== selectedDeviceId && l.target_device !== selectedDeviceId,
        );
        return { ...prev, devices, links };
      });
      setSelectedDeviceId(null);
      setSelectedLinkId(null);
    } catch (e) {
      setError(`删除设备失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [selectedTopoId, topology, selectedDeviceId, selectedDevice]);

  const handleResetView = useCallback(() => {
    setViewport(DEFAULT_VIEWPORT);
  }, []);

  const handleZoom = useCallback(
    (delta: number) => {
      setViewport((vp) => ({ ...vp, scale: Math.max(0.2, Math.min(5, vp.scale + delta)) }));
    },
    [],
  );

  // 拖拽调整左侧面板宽度（分隔条）
  const startResize = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startW = leftWidthRef.current;
    const onMove = (ev: MouseEvent) => {
      const nw = Math.min(460, Math.max(200, startW + (ev.clientX - startX)));
      setLeftWidth(nw);
    };
    const onUp = () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      document.body.style.cursor = '';
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    document.body.style.cursor = 'col-resize';
  }, []);

  const zoomPct = Math.round(viewport.scale * 100) + '%';
  const deviceCount = topology ? Object.keys(topology.devices).length : 0;
  const linkCount = topology ? topology.links.length : 0;
  const annoCount = topology ? topology.annotations.length : 0;

  return (
    <div className="app-shell">
      <header className="app-header">
        <h1>eNSP Web Lab</h1>
        <select
          value={selectedTopoId || ''}
          onChange={(e) => setSelectedTopoId(e.target.value || null)}
          title="选择拓扑"
        >
          <option value="">— 选择拓扑 —</option>
          {topologies.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name} ({t.id})
            </option>
          ))}
        </select>
        <button className="btn btn-secondary btn-sm" onClick={handleCreateTopology}>
          + 新建拓扑
        </button>
        <button
          className="btn btn-danger btn-sm"
          onClick={handleDeleteTopology}
          disabled={!selectedTopoId}
          title={selectedTopoId ? '删除当前选中的拓扑' : '请先选择拓扑'}
        >
          删除拓扑
        </button>
        {selectedTopoId && topologies.find((x) => x.id === selectedTopoId)?.description && (
          <span
            className="topo-desc-badge"
            title={topologies.find((x) => x.id === selectedTopoId)?.description}
            style={{
              maxWidth: 220,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
              fontSize: 12,
              color: '#666',
              background: '#f0f0f0',
              borderRadius: 4,
              padding: '2px 8px',
            }}
          >
            📝 {topologies.find((x) => x.id === selectedTopoId)?.description}
          </span>
        )}
        <button
          className="btn btn-secondary btn-sm"
          onClick={openEditDesc}
          disabled={!selectedTopoId}
          title={selectedTopoId ? '编辑当前拓扑的说明' : '请先选择拓扑'}
        >
          说明
        </button>
        <div className="header-spacer" />
        <span className={`status-pill ${isConnected ? 'connected' : 'disconnected'}`}>
          SSE: {isConnected ? '已连接' : '未连接'}
        </span>
        {simStatus && (
          <span className="status-pill">
            平台: {simStatus.platform} | 模式: {simStatus.mode}
          </span>
        )}
      </header>

      {error && (
        <div
          style={{
            background: '#f8d7da',
            color: '#721c24',
            padding: '6px 16px',
            fontSize: 12,
            cursor: 'pointer',
          }}
          onClick={() => setError(null)}
        >
          ⚠ {error} (点击关闭)
        </div>
      )}

      <div className="app-main">
        <aside className="device-panel" style={{ width: leftWidth }}>
          <LeftPanel
            deviceTypes={deviceTypes}
            onAddDeviceClick={(t) => {
              setMode('add');
              setAddType(t);
            }}
            onAddAnnotation={handleAddAnnotation}
            onAddVxlanTemplate={handleAddVxlanTemplate}
            hasTopology={!!topology}
            links={topology?.links || []}
            devices={topology?.devices || {}}
            selectedLinkId={selectedLinkId}
            onSelectLink={handleSelectLink}
            onLocateLink={handleLocateLink}
            onDeleteLink={handleDeleteLink}
            linkTypeMode={linkTypeMode}
            onSelectLinkType={setLinkTypeMode}
          />
          <div className="panel-resizer" onMouseDown={startResize} title="拖拽调整宽度" />
        </aside>

        <div className="topo-area">
          <div className="topo-toolbar">
            <button
              className={`btn btn-sm ${mode === 'select' ? 'btn-primary' : 'btn-secondary'}`}
              onClick={() => {
                setMode('select');
                setAddType(null);
                setLinkSource(null);
              }}
            >
              选择
            </button>
            <button
              className={`btn btn-sm ${mode === 'link' ? 'btn-primary' : 'btn-secondary'}`}
              onClick={() => {
                setMode('link');
                setAddType(null);
                setLinkSource(null);
              }}
              disabled={!topology}
              title="从源设备拖拽到目标设备建立链路"
            >
              连线
            </button>
            <button
              className="btn btn-danger btn-sm"
              onClick={handleDeleteDevice}
              disabled={!selectedDeviceId}
            >
              删除设备
            </button>
            <div className="tool-sep" />
            <button className="btn btn-secondary btn-sm" onClick={() => handleZoom(0.1)}>
              +
            </button>
            <span className="zoom-label">{zoomPct}</span>
            <button className="btn btn-secondary btn-sm" onClick={() => handleZoom(-0.1)}>
              −
            </button>
            <button className="btn btn-secondary btn-sm" onClick={handleResetView}>
              重置视图
            </button>
            <div className="tool-sep" />
            <button
              className="btn btn-success btn-sm"
              onClick={handleSaveLayout}
              disabled={!topology || saving}
            >
              {saving ? '保存中…' : '保存布局'}
            </button>
            <button
              className={`btn btn-sm ${diagOpen ? 'btn-primary' : 'btn-warning'}`}
              onClick={openDiag}
              disabled={!selectedTopoId || !topology}
              title="打开网络诊断工具集（Ping / Traceroute / Telnet / ARP / DNS / 带宽 / 抓包）"
            >
              🔧 网络诊断
            </button>
            <div className="toolbar-spacer" />
            <span className="topo-status">
              {topology
                ? `设备: ${deviceCount} | 链路: ${linkCount} | 标注: ${annoCount}`
                : '未加载拓扑'}
              {linkSource && ` | 连线源: ${linkSource.deviceId}:${linkSource.port}`}
            </span>
          </div>

          <div className="topo-canvas-host" ref={canvasHostRef}>
            <TopologyCanvas
              topology={topology}
              viewport={viewport}
              onViewportChange={setViewport}
              selectedDeviceId={selectedDeviceId}
              onSelectDevice={handleSelectDevice}
              selectedLinkId={selectedLinkId}
              onSelectLink={handleSelectLink}
              onDeleteLink={handleDeleteLink}
              onDeviceMove={(id, x, y) => {
                handleDeviceMove(id, x, y);
              }}
              onLinkChange={handleLinkChange}
              onLinkPersist={handleLinkPersist}
              onPowerDevice={handlePowerDevice}
              onAddDeviceAt={handleAddDeviceAt}
              onCreateLink={(s, sp, d, dp) => {
                const explicit = linkTypeMode === 'auto' ? undefined : linkTypeMode;
                void handleCreateLink(s, sp, d, dp, explicit);
              }}
              onBackgroundClick={() => {
                if (mode === 'select') setLinkSource(null);
              }}
              onOpenDevice={(id) => openWindow(id)}
              highlightDeviceIds={diagOpen ? [diagSrc].filter(Boolean) : []}
              mode={mode}
              addType={addType}
              linkSource={linkSource}
              onLinkSourceChange={setLinkSource}
            />
            <PacketAnimator events={events} topology={topology} viewport={viewport} />
            {topology && (
              <>
                <AnnotationLayer
                  topology={topology}
                  viewport={viewport}
                  onChange={handleAnnotationChange}
                  onDelete={handleAnnotationDelete}
                />
              </>
            )}
            {diagOpen && topology && (
              <DiagnosticTools
                topologyId={selectedTopoId}
                devices={topology.devices}
                links={topology.links}
                srcDevice={diagSrc}
                onSrcChange={setDiagSrc}
                title="🔧 网络诊断工具"
                zIndex={diagMax ? 5000 : diagZ}
                initialRect={diagRect}
                minimized={diagMin}
                maximized={diagMax}
                onFocus={() => setDiagZ((z) => z + 1)}
                onClose={() => setDiagOpen(false)}
                onMinimize={() => setDiagMin((v) => !v)}
                onToggleMaximize={() => setDiagMax((v) => !v)}
                onRectChange={setDiagRect}
              />
            )}
            {showCreateModal && (
              <div
                style={{
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  right: 0,
                  bottom: 0,
                  background: 'rgba(0, 0, 0, 0.5)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  zIndex: 2000,
                }}
                onClick={() => setShowCreateModal(false)}
              >
                <div
                  style={{
                    background: '#fff',
                    borderRadius: 8,
                    padding: '24px',
                    minWidth: 320,
                    boxShadow: '0 4px 20px rgba(0,0,0,0.2)',
                  }}
                  onClick={(e) => e.stopPropagation()}
                >
                  <h3 style={{ margin: 0, marginBottom: 16, fontSize: 16, fontWeight: 600 }}>新建拓扑</h3>
                  <input
                    type="text"
                    value={createTopoName}
                    onChange={(e) => setCreateTopoName(e.target.value)}
                    placeholder="输入拓扑名称"
                    style={{
                      width: '100%',
                      padding: '8px 12px',
                      border: '1px solid #ddd',
                      borderRadius: 4,
                      fontSize: 14,
                      boxSizing: 'border-box',
                      marginBottom: 16,
                    }}
                    autoFocus
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') handleConfirmCreateTopology();
                      if (e.key === 'Escape') setShowCreateModal(false);
                    }}
                  />
                  <textarea
                    value={createTopoDesc}
                    onChange={(e) => setCreateTopoDesc(e.target.value)}
                    placeholder="拓扑说明（可选）"
                    rows={3}
                    style={{
                      width: '100%',
                      padding: '8px 12px',
                      border: '1px solid #ddd',
                      borderRadius: 4,
                      fontSize: 14,
                      boxSizing: 'border-box',
                      marginBottom: 16,
                      resize: 'vertical',
                      fontFamily: 'inherit',
                    }}
                  />
                  <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                    <button
                      onClick={() => setShowCreateModal(false)}
                      style={{
                        padding: '6px 16px',
                        border: '1px solid #ddd',
                        borderRadius: 4,
                        background: '#fff',
                        cursor: 'pointer',
                        fontSize: 14,
                      }}
                    >
                      取消
                    </button>
                    <button
                      onClick={handleConfirmCreateTopology}
                      disabled={!createTopoName.trim()}
                      style={{
                        padding: '6px 16px',
                        border: 'none',
                        borderRadius: 4,
                        background: createTopoName.trim() ? '#007bff' : '#ccc',
                        color: '#fff',
                        cursor: createTopoName.trim() ? 'pointer' : 'not-allowed',
                        fontSize: 14,
                      }}
                    >
                      创建
                    </button>
                  </div>
                </div>
              </div>
            )}
            {showEditDescModal && (
              <div
                style={{
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  right: 0,
                  bottom: 0,
                  background: 'rgba(0, 0, 0, 0.5)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  zIndex: 2000,
                }}
                onClick={() => setShowEditDescModal(false)}
              >
                <div
                  style={{
                    background: '#fff',
                    borderRadius: 8,
                    padding: '24px',
                    minWidth: 360,
                    boxShadow: '0 4px 20px rgba(0,0,0,0.2)',
                  }}
                  onClick={(e) => e.stopPropagation()}
                >
                  <h3 style={{ margin: 0, marginBottom: 16, fontSize: 16, fontWeight: 600 }}>
                    拓扑说明
                  </h3>
                  <textarea
                    value={editDescText}
                    onChange={(e) => setEditDescText(e.target.value)}
                    placeholder="输入拓扑说明（可选）"
                    rows={4}
                    autoFocus
                    style={{
                      width: '100%',
                      padding: '8px 12px',
                      border: '1px solid #ddd',
                      borderRadius: 4,
                      fontSize: 14,
                      boxSizing: 'border-box',
                      marginBottom: 16,
                      resize: 'vertical',
                      fontFamily: 'inherit',
                    }}
                    onKeyDown={(e) => {
                      if (e.key === 'Escape') setShowEditDescModal(false);
                      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) handleSaveDescription();
                    }}
                  />
                  <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                    <button
                      onClick={() => setShowEditDescModal(false)}
                      style={{
                        padding: '6px 16px',
                        border: '1px solid #ddd',
                        borderRadius: 4,
                        background: '#fff',
                        cursor: 'pointer',
                        fontSize: 14,
                      }}
                    >
                      取消
                    </button>
                    <button
                      onClick={handleSaveDescription}
                      style={{
                        padding: '6px 16px',
                        border: 'none',
                        borderRadius: 4,
                        background: '#007bff',
                        color: '#fff',
                        cursor: 'pointer',
                        fontSize: 14,
                      }}
                    >
                      保存
                    </button>
                  </div>
                </div>
              </div>
            )}
            {!topology && (
              <div className="empty-state">
                请选择或创建一个拓扑开始使用 eNSP Web Lab
              </div>
            )}
          </div>

          {/* 已打开的设备浮动窗口：每个设备独立，可同时打开多个 */}
          {topology &&
            Object.entries(windows).map(([devId, w]) => {
              const dev = topology.devices[devId];
              if (!dev) return null;
              const port = Object.keys(dev.interfaces || {})[0] || dev.id;
              return (
                <FloatWindow
                  key={devId}
                  title={dev.name}
                  subtitle={`(${port})`}
                  zIndex={w.maximized ? 5000 : (zMap[devId] || 10)}
                  initialRect={w.rect}
                  minimized={w.minimized}
                  maximized={w.maximized}
                  headerStatus={<span className="fw-check" title="已打开">✓</span>}
                  onFocus={() => bringToFront(devId)}
                  onClose={() => closeWindow(devId)}
                  onMinimize={() => minimizeWindow(devId)}
                  onToggleMaximize={() => toggleMaxWindow(devId)}
                  onRectChange={(r) => updateWindowRect(devId, r)}
                >
                  <DeviceDetail
                    topologyId={selectedTopoId}
                    selectedDevice={dev}
                    onConfigApplied={() => {
                      if (selectedTopoId) void loadTopology(selectedTopoId);
                    }}
                    onTargetDevice={(t) => openWindow(t)}
                  />
                </FloatWindow>
              );
            })}

          {/* 浮动窗口任务栏：右上角显示已打开窗口状态（spine-4 ✓），点击聚焦/还原 */}
          {topology && Object.keys(windows).length > 0 && (
            <div className="win-taskbar">
              {Object.keys(windows).map((devId) => {
                const dev = topology.devices[devId];
                if (!dev) return null;
                const w = windows[devId];
                return (
                  <button
                    type="button"
                    key={devId}
                    className={`win-task${w.minimized ? ' win-task-min' : ''}`}
                    title={w.minimized ? `还原 ${dev.name}` : `聚焦 ${dev.name}`}
                    onClick={() => {
                      bringToFront(devId);
                      if (w.minimized) minimizeWindow(devId);
                    }}
                  >
                    <span className="win-task-name">{dev.name}</span>
                    <span className="win-task-check">✓</span>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
