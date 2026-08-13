// AnnotationLayer - 标注层
// 迁移自 web/js/topo-canvas.js 中的 annotations 部分
//
// 功能:
//   - HTML 叠加层，使用 world→screen 坐标定位
//   - 可拖拽
//   - 可缩放（右下角拖动）
//   - 双击进入编辑模式 (in-place textarea)
//   - 设置按钮：字体大小 / 对齐 / 边框(实线·虚线·隐藏) / 背景色
//   - 删除按钮
//   - 文本框按内容自适应高度，创建后显示全部内容
import { useEffect, useRef, useState } from 'react';
import type { TextAnnotation, Topology, Viewport } from '../types';

interface AnnotationLayerProps {
  topology: Topology;
  viewport: Viewport;
  onChange: (id: string, fields: Partial<TextAnnotation>) => void;
  onDelete: (id: string) => void;
  onCreate?: (x: number, y: number) => void;
}

const FONT_SIZES = [10, 11, 12, 14, 16, 18, 20, 24];
const ALIGNS: Array<{ v: string; label: string }> = [
  { v: 'left', label: '左' },
  { v: 'center', label: '中' },
  { v: 'right', label: '右' },
];
const BORDERS: Array<{ v: string; label: string }> = [
  { v: 'solid', label: '实线' },
  { v: 'dashed', label: '虚线' },
  { v: 'hidden', label: '隐藏' },
];
const BG_SWATCHES = [
  { v: '', label: '默认' },
  { v: '#fffbe6', label: '黄' },
  { v: '#e3f2fd', label: '蓝' },
  { v: '#e8f5e9', label: '绿' },
  { v: '#fce4ec', label: '粉' },
  { v: '#f3e5f5', label: '紫' },
];

export default function AnnotationLayer(props: AnnotationLayerProps) {
  const { topology, viewport, onChange, onDelete, onCreate } = props;
  const [editingId, setEditingId] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  const [settingsId, setSettingsId] = useState<string | null>(null);
  const dragRef = useRef<{ id: string; startX: number; startY: number; origX: number; origY: number } | null>(null);
  const resizeRef = useRef<{ id: string; startX: number; startY: number; origWidth: number; origHeight: number; liveW: number; liveH: number } | null>(null);
  const [sizes, setSizes] = useState<Record<string, { width: number; height: number }>>({});
  // 当前正在拖拽调整大小的标注 id（用于实时渲染，避免回退到 auto 导致跳变）
  const [resizingId, setResizingId] = useState<string | null>(null);

  useEffect(() => {
    setEditingId(null);
    setSettingsId(null);
  }, [topology.id]);

  // 计算文本框尺寸：优先使用标注自带 width/height；否则按内容自适应（显示全部内容，不裁切）
  useEffect(() => {
    const initialSizes: Record<string, { width: number; height: number }> = {};
    topology.annotations.forEach((anno) => {
      const lines = anno.text.split('\n');
      const maxLen = Math.max(...lines.map((l) => Array.from(l).length), 1);
      const rawW = anno.width ?? 0;
      const rawH = anno.height ?? 0;
      const width = rawW > 0 ? rawW : Math.min(maxLen * 14 + 24, 640);
      const height = rawH > 0 ? rawH : lines.length * 15 + 12;
      initialSizes[anno.id] = { width: Math.max(width, 70), height: Math.max(height, 34) };
    });
    setSizes(initialSizes);
  }, [topology.id]);

  // 编辑中的 textarea 自动撑高到内容高度；超过上限则出现滚动条（解决"只显示开头几个字"）
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const TEXTAREA_MAX_H = 400;
  useEffect(() => {
    if (editingId && textareaRef.current) {
      const el = textareaRef.current;
      el.style.height = 'auto';
      const next = Math.min(el.scrollHeight, TEXTAREA_MAX_H);
      el.style.height = `${next}px`;
      el.style.overflowY = el.scrollHeight > TEXTAREA_MAX_H ? 'auto' : 'hidden';
    }
  }, [editingId, draft]);

  const startDrag = (anno: TextAnnotation, e: React.MouseEvent) => {
    if (editingId || settingsId) return;
    e.preventDefault();
    e.stopPropagation();
    dragRef.current = {
      id: anno.id,
      startX: e.clientX,
      startY: e.clientY,
      origX: anno.position_x,
      origY: anno.position_y,
    };
    const onMove = (ev: MouseEvent) => {
      const st = dragRef.current;
      if (!st) return;
      const dx = (ev.clientX - st.startX) / viewport.scale;
      const dy = (ev.clientY - st.startY) / viewport.scale;
      onChange(st.id, { position_x: st.origX + dx, position_y: st.origY + dy });
    };
    const onUp = () => {
      dragRef.current = null;
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  };

  const startResize = (annoId: string, e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    // 以当前 DOM 实际渲染尺寸为基准，避免从估算尺寸跳变
    const el = (e.currentTarget as HTMLElement).closest('.annotation') as HTMLElement | null;
    const startW = el ? el.offsetWidth : sizes[annoId]?.width ?? 200;
    const startH = el ? el.offsetHeight : sizes[annoId]?.height ?? 100;
    setSizes((prev) => ({ ...prev, [annoId]: { width: startW, height: startH } }));
    setResizingId(annoId);
    resizeRef.current = {
      id: annoId,
      startX: e.clientX,
      startY: e.clientY,
      origWidth: startW,
      origHeight: startH,
      liveW: startW,
      liveH: startH,
    };
    const onMove = (ev: MouseEvent) => {
      const st = resizeRef.current;
      if (!st) return;
      const dx = ev.clientX - st.startX;
      const dy = ev.clientY - st.startY;
      st.liveW = Math.max(70, st.origWidth + dx);
      st.liveH = Math.max(34, st.origHeight + dy);
      setSizes((prev) => ({
        ...prev,
        [st.id]: { width: st.liveW, height: st.liveH },
      }));
    };
    const onUp = () => {
      const st = resizeRef.current;
      resizeRef.current = null;
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      setResizingId(null);
      // 持久化真实尺寸，刷新后依然保持
      if (st) onChange(st.id, { width: st.liveW, height: st.liveH });
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  };

  const startEdit = (anno: TextAnnotation) => {
    setSettingsId(null);
    setEditingId(anno.id);
    setDraft(anno.text);
  };

  const commitEdit = () => {
    if (editingId) {
      onChange(editingId, { text: draft });
    }
    setEditingId(null);
  };

  const setStyle = (anno: TextAnnotation, patch: Partial<TextAnnotation>) => {
    onChange(anno.id, patch);
  };

  const borderCss = (anno: TextAnnotation): string => {
    const b = anno.border_style || 'solid';
    if (b === 'hidden') return '1px solid transparent';
    if (b === 'dashed') return '1px dashed #bbb';
    return '1px solid #ccc';
  };

  return (
    <div className="annotation-layer">
      {topology.annotations.map((anno) => {
        const screenX = anno.position_x * viewport.scale + viewport.ox;
        const screenY = anno.position_y * viewport.scale + viewport.oy;
        const isEditing = editingId === anno.id;
        const isSettings = settingsId === anno.id;
        const rawW = anno.width ?? 0;
        const rawH = anno.height ?? 0;
        const fixed = rawW > 0 || rawH > 0;
        const liveSize = sizes[anno.id];
        // 有显式尺寸 → 用保存值作为 minWidth/minHeight（内容超出时自动撑大）；
        // 否则完全自适应内容。
        // 正在拖拽调整时 → 用实时尺寸，避免松手后才跳变
        const boxW: number | string =
          fixed ? (rawW > 0 ? rawW : 'auto') : resizingId === anno.id && liveSize ? liveSize.width : 'auto';
        const boxMinH: number | undefined =
          rawH > 0 ? rawH : undefined;
        const fontSize = anno.font_size || 12;
        const fontFamily = anno.font_family || 'inherit';
        const textAlign = anno.text_align || 'left';
        const bg = anno.background || 'rgba(255,255,255,0.95)';
        return (
          <div
            key={anno.id}
            className="annotation"
            style={{
              left: screenX,
              top: screenY,
              transform: 'translate(-50%, -50%)',
              width: boxW,
              minHeight: boxMinH,
              maxWidth: 640,
              border: borderCss(anno),
              background: bg,
              overflow: 'visible',
            }}
            onMouseDown={(e) => startDrag(anno, e)}
            onDoubleClick={(e) => {
              e.stopPropagation();
              startEdit(anno);
            }}
          >
            {isEditing ? (
              <textarea
                ref={textareaRef}
                className="annotation-input"
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onBlur={commitEdit}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && e.ctrlKey) {
                    e.preventDefault();
                    commitEdit();
                  } else if (e.key === 'Escape') {
                    e.preventDefault();
                    setEditingId(null);
                  }
                }}
                autoFocus
                style={{ width: '100%', resize: 'none', fontSize, fontFamily, overflowY: 'hidden' }}
              />
            ) : (
              <div
                className="annotation-text"
                style={{
                  fontSize,
                  fontFamily,
                  textAlign: textAlign as 'left' | 'center' | 'right',
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-word',
                  overflow: 'visible',
                }}
              >
                {anno.text}
              </div>
            )}

            {!isEditing && (
              <div className="annotation-toolbar">
                <button
                  className="annotation-settings"
                  title="设置字体/对齐/边框"
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => {
                    e.stopPropagation();
                    setSettingsId(isSettings ? null : anno.id);
                  }}
                >
                  ⚙
                </button>
                <button
                  className="annotation-delete"
                  title="删除标注"
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => {
                    e.stopPropagation();
                    onDelete(anno.id);
                  }}
                >
                  ×
                </button>
              </div>
            )}

            {!isEditing && (
              <div
                className="annotation-resize"
                onMouseDown={(e) => startResize(anno.id, e)}
                title="拖动调整大小"
              />
            )}

            {isSettings && (
              <div
                className="annotation-settings-panel"
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => e.stopPropagation()}
              >
                <div className="asp-row">
                  <span>字号</span>
                  <select
                    value={fontSize}
                    onChange={(e) => setStyle(anno, { font_size: Number(e.target.value) })}
                  >
                    {FONT_SIZES.map((s) => (
                      <option key={s} value={s}>{s}</option>
                    ))}
                  </select>
                </div>
                <div className="asp-row">
                  <span>对齐</span>
                  <div className="asp-btns">
                    {ALIGNS.map((a) => (
                      <button
                        key={a.v}
                        className={textAlign === a.v ? 'asp-on' : ''}
                        onClick={() => setStyle(anno, { text_align: a.v })}
                      >
                        {a.label}
                      </button>
                    ))}
                  </div>
                </div>
                <div className="asp-row">
                  <span>边框</span>
                  <div className="asp-btns">
                    {BORDERS.map((b) => (
                      <button
                        key={b.v}
                        className={(anno.border_style || 'solid') === b.v ? 'asp-on' : ''}
                        onClick={() => setStyle(anno, { border_style: b.v })}
                      >
                        {b.label}
                      </button>
                    ))}
                  </div>
                </div>
                <div className="asp-row">
                  <span>背景</span>
                  <div className="asp-btns">
                    {BG_SWATCHES.map((c) => (
                      <button
                        key={c.label}
                        className={'asp-swatch' + ((anno.background || '') === c.v ? ' asp-on' : '')}
                        style={{ background: c.v || 'rgba(255,255,255,0.95)' }}
                        title={c.label}
                        onClick={() => setStyle(anno, { background: c.v })}
                      />
                    ))}
                  </div>
                </div>
              </div>
            )}
          </div>
        );
      })}
      {topology.annotations.length === 0 && onCreate && (
        <div className="annotation-hint">点击"添加标注"按钮在画布中央创建一个标注</div>
      )}
    </div>
  );
}
