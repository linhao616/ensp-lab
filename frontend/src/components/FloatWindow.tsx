// FloatWindow - 通用可拖动浮动小窗口（类 eNSP 设备配置弹出窗口）
//
// 特性：
//   - 按住标题栏可在视口内任意拖动（限制在浏览器视口内，松手停留）
//   - 右下角拖拽手柄调整大小（最小 360×220）
//   - 标题栏右侧：最小化 / 最大化(还原) / 关闭
//   - 扁平化设计：暗色标题栏 + 白色内容区（eNSP 风格）
//   - 位置/大小由父组件通过 initialRect 初始化；拖拽、缩放结束时通过
//     onRectChange 回传父组件做持久化（拖拽过程中仅本地状态变化，避免频繁重渲染）
//   - 最大化时通过 --fw-ctrls-width CSS 自属性通知任务栏左移避让
//
// 该组件为"纯容器"，具体内容由 children 注入（如 DeviceDetail）。
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';

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

/**
 * 控制按钮条宽度兜底值（3 个 24px 按钮 + 2×2px gap = 76px）。
 * 仅在 ref 未挂载 / 尚未布局（宽度为 0）时使用；正常路径一律实测。
 */
const FW_CTRLS_WIDTH_FALLBACK = 76;

/**
 * 实测控制按钮条宽度。向上取整避免亚像素宽度导致任务栏少让 1px 而重新压住按钮。
 */
function measureCtrlsWidth(el: HTMLElement | null): number {
  if (!el) return FW_CTRLS_WIDTH_FALLBACK;
  const w = el.getBoundingClientRect().width;
  return w > 0 ? Math.ceil(w) : FW_CTRLS_WIDTH_FALLBACK;
}

// ---------------------------------------------------------------------------
// --fw-ctrls-width 引用计数管理（模块作用域，跨所有 FloatWindow 实例共享）
//
// 背景：任务栏 z-index=100000 高于窗口(5000)，最大化时会物理压住 .fw-ctrls，
// 导致还原按钮点不到。解决办法是最大化期间设置 --fw-ctrls-width，任务栏据此左移。
//
// 为什么需要引用计数：同时最大化 A、B 两个窗口时，还原 A 会触发 A 的 effect
// cleanup。若无条件 removeProperty，B 仍处于最大化状态但任务栏已经移回，
// B 的还原按钮再次被压住 —— 缺陷复现。
//
// 这里用 Map(token -> width) 而非单个整数计数器，是计数器的严格超集：
//   - map.size 即引用计数，减到 0 才 removeProperty；
//   - delete 幂等，cleanup 意外重入也不会把计数减成负数；
//   - 多窗口宽度不一致时取 max，保证让位量始终够用。
// StrictMode 下 effect 会「执行→cleanup→再执行」，acquire/release 严格配对，
// 最终 size 仍为 1、属性正确留存。
// ---------------------------------------------------------------------------

/** 当前处于最大化状态的窗口 token -> 其控制条实测宽度。size 即引用计数。 */
const activeCtrlsWidths = new Map<number, number>();
let nextCtrlsToken = 1;

/** 把当前所有最大化窗口所需的最大让位宽度同步到 CSS 自属性。 */
function syncCtrlsWidthVar(): void {
  const root = document.documentElement;
  if (activeCtrlsWidths.size === 0) {
    root.style.removeProperty('--fw-ctrls-width');
    return;
  }
  let max = 0;
  activeCtrlsWidths.forEach((w) => {
    if (w > max) max = w;
  });
  root.style.setProperty('--fw-ctrls-width', `${max}px`);
}

/** 占用一个引用，返回释放时需要的 token。 */
function acquireCtrlsWidth(width: number): number {
  const token = nextCtrlsToken++;
  activeCtrlsWidths.set(token, width);
  syncCtrlsWidthVar();
  return token;
}

/** 释放引用；仅当再无最大化窗口时才真正清除 CSS 自属性。 */
function releaseCtrlsWidth(token: number): void {
  activeCtrlsWidths.delete(token);
  syncCtrlsWidthVar();
}

/**
 * 返回视口可用尺寸（不含滚动条）。
 * body 已设 overflow:hidden → innerWidth/clientWidth 通常一致，
 * 但使用 clientWidth 是语义正确的做法（position:fixed 元素的可视区域）。
 */
function viewportSize(): { w: number; h: number } {
  return {
    w: document.documentElement.clientWidth,
    h: document.documentElement.clientHeight,
  };
}

/**
 * 测量 .app-header 的实际渲染高度（含 padding）。
 * 避免硬编码 HEADER_H 与实际 header 高度不符导致偏移。
 */
function measureHeaderHeight(): number {
  const el = document.querySelector<HTMLElement>('.app-header');
  return el ? Math.round(el.getBoundingClientRect().height) : 48;
}

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
  /** 控制按钮条 DOM 引用，最大化时用于实测宽度（替代硬编码常量）。 */
  const ctrlsRef = useRef<HTMLDivElement | null>(null);

  // 外部 initialRect 变化（如重新打开已持久化布局）时同步，但拖拽中不覆盖本地位置
  useEffect(() => {
    if (!draggingRef.current) setRect(initialRect);
  }, [initialRect]);

  // 最大化时通过 CSS 自属性通知全局：控制按钮条宽度（px），
  // 任务栏据此左移避让，避免 z-index=100000 的任务栏覆盖控制按钮。
  // 宽度实测自 .fw-ctrls（effect 在 DOM commit 后执行，此时 ref 必然已挂载）。
  useEffect(() => {
    if (!maximized) return;
    const token = acquireCtrlsWidth(measureCtrlsWidth(ctrlsRef.current));
    return () => releaseCtrlsWidth(token);
  }, [maximized]);

  // resize 监听器：确保最大化时 eff 尺寸随浏览器窗口实时更新
  const updateOnResize = useCallback(() => {
    if (maximized) {
      const vp = viewportSize();
      const hh = measureHeaderHeight();
      setRect((r: Rect) => ({ ...r, x: 0, y: hh, w: vp.w, h: vp.h - hh }));
    }
  }, [maximized]);

  useEffect(() => {
    if (!maximized) return;
    window.addEventListener('resize', updateOnResize);
    return () => window.removeEventListener('resize', updateOnResize);
  }, [maximized, updateOnResize]);

  const eff = maximized
    ? (() => {
        const vp = viewportSize();
        const hh = measureHeaderHeight();
        return { x: 0, y: hh, w: vp.w, h: vp.h - hh };
      })()
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
      const vp = viewportSize();
      let nx = orig.x + (ev.clientX - startX);
      let ny = orig.y + (ev.clientY - startY);
      // 限制在视口内（至少保留标题栏可见）
      nx = Math.max(0, Math.min(vp.w - 80, nx));
      ny = Math.max(0, Math.min(vp.h - TITLE_H, ny));
      setRect((r: Rect) => ({ ...r, x: nx, y: ny }));
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
      const vp = viewportSize();
      const nw = Math.max(MIN_W, Math.min(vp.w - orig.x, orig.w + (ev.clientX - startX)));
      const nh = Math.max(MIN_H, Math.min(vp.h - orig.y, orig.h + (ev.clientY - startY)));
      setRect((r: Rect) => ({ ...r, w: nw, h: nh }));
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
        <div className="fw-ctrls" ref={ctrlsRef}>
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
