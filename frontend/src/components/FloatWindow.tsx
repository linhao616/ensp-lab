// FloatWindow - 通用可拖动浮动小窗口（类 eNSP 设备配置弹出窗口）
//
// 特性：
//   - 按住标题栏可在视口内任意拖动（限制在浏览器视口内，松手停留）
//   - 右下角拖拽手柄调整大小（最小 360×220）
//   - 标题栏右侧：最小化 / 最大化(还原) / 关闭
//   - 扁平化设计：暗色标题栏 + 白色内容区（eNSP 风格）
//   - 位置/大小由父组件通过 initialRect 初始化；拖拽、缩放结束时通过
//     onRectChange 回传父组件做持久化（拖拽过程中仅本地状态变化，避免频繁重渲染）
//
// 该组件为"纯容器"，具体内容由 children 注入（如 DeviceDetail）。
import { useEffect, useRef, useState, type ReactNode } from 'react';

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

interface FloatWindowProps {
  title: string;
  subtitle?: string;
  zIndex: number;
  initialRect: Rect;
  minimized: boolean;
  maximized: boolean;
  // 标题栏状态标识（如已打开的 ✓）
  headerStatus?: ReactNode;
  onFocus: () => void;
  onClose: () => void;
  onMinimize: () => void;
  onToggleMaximize: () => void;
  onRectChange: (rect: Rect) => void;
  children: ReactNode;
}

const MIN_W = 360;
const MIN_H = 220;
const TITLE_H = 34;
const HEADER_H = 48; // 顶部应用 header 高度，最大化时避让

export default function FloatWindow(props: FloatWindowProps) {
  const {
    title,
    subtitle,
    zIndex,
    initialRect,
    minimized,
    maximized,
    headerStatus,
    onFocus,
    onClose,
    onMinimize,
    onToggleMaximize,
    onRectChange,
    children,
  } = props;

  const [rect, setRect] = useState<Rect>(initialRect);
  const rectRef = useRef(rect);
  rectRef.current = rect;
  const draggingRef = useRef(false);

  // 外部 initialRect 变化（如重新打开已持久化布局）时同步，但拖拽中不覆盖本地位置
  useEffect(() => {
    if (!draggingRef.current) setRect(initialRect);
  }, [initialRect]);

  const eff = maximized
    ? { x: 0, y: HEADER_H, w: window.innerWidth, h: window.innerHeight - HEADER_H }
    : rect;

  const startDrag = (e: React.MouseEvent) => {
    if (maximized || e.button !== 0) return;
    e.preventDefault();
    onFocus();
    const startX = e.clientX;
    const startY = e.clientY;
    const orig = { ...rectRef.current };
    draggingRef.current = true;
    const onMove = (ev: MouseEvent) => {
      let nx = orig.x + (ev.clientX - startX);
      let ny = orig.y + (ev.clientY - startY);
      // 限制在视口内（至少保留标题栏可见）
      nx = Math.max(0, Math.min(window.innerWidth - 80, nx));
      ny = Math.max(0, Math.min(window.innerHeight - TITLE_H, ny));
      setRect((r) => ({ ...r, x: nx, y: ny }));
    };
    const onUp = () => {
      draggingRef.current = false;
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      document.body.classList.remove('resizing-ew');
      onRectChange(rectRef.current);
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    document.body.classList.add('resizing-ew');
  };

  const startResize = (e: React.MouseEvent) => {
    if (maximized || e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    onFocus();
    const startX = e.clientX;
    const startY = e.clientY;
    const orig = { ...rectRef.current };
    const onMove = (ev: MouseEvent) => {
      const nw = Math.max(MIN_W, Math.min(window.innerWidth - orig.x, orig.w + (ev.clientX - startX)));
      const nh = Math.max(MIN_H, Math.min(window.innerHeight - orig.y, orig.h + (ev.clientY - startY)));
      setRect((r) => ({ ...r, w: nw, h: nh }));
    };
    const onUp = () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      document.body.classList.remove('resizing-nwse');
      onRectChange(rectRef.current);
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    document.body.classList.add('resizing-nwse');
  };

  return (
    <div
      className={`float-window${minimized ? ' fw-min' : ''}${maximized ? ' fw-max' : ''}`}
      style={{ left: eff.x, top: eff.y, width: eff.w, height: minimized ? TITLE_H : eff.h, zIndex }}
      onMouseDown={onFocus}
    >
      <div className="fw-titlebar" onMouseDown={startDrag}>
        <span className="fw-dot" />
        <span className="fw-title" title={title}>
          {title}
        </span>
        {subtitle && <span className="fw-sub">{subtitle}</span>}
        {headerStatus && <span className="fw-status">{headerStatus}</span>}
        <div className="fw-ctrls">
          <button
            type="button"
            className="fw-btn"
            title="最小化"
            onMouseDown={(e) => e.stopPropagation()}
            onClick={() => {
              onFocus();
              onMinimize();
            }}
          >
            —
          </button>
          <button
            type="button"
            className="fw-btn"
            title="最大化 / 还原"
            onMouseDown={(e) => e.stopPropagation()}
            onClick={() => {
              onFocus();
              onToggleMaximize();
            }}
          >
            {maximized ? '❐' : '▢'}
          </button>
          <button
            type="button"
            className="fw-btn fw-close"
            title="关闭"
            onMouseDown={(e) => e.stopPropagation()}
            onClick={() => {
              onFocus();
              onClose();
            }}
          >
            ×
          </button>
        </div>
      </div>
      {!minimized && (
        <div className="fw-body">{children}</div>
      )}
      {!minimized && !maximized && (
        <div className="fw-resizer" title="拖拽调整大小" onMouseDown={startResize} />
      )}
    </div>
  );
}
