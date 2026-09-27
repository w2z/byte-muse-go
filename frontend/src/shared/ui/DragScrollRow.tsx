import { useEffect, useRef, type DragEvent, type MouseEvent, type PointerEvent, type ReactNode } from "react";

/** 移动超过这个距离才判定为拖动，避免手抖把点击吃掉。 */
const DRAG_THRESHOLD = 4;

type DragScrollRowProps = {
  children: ReactNode;
  /** 外观类名；滚动方向、大小和背景由调用方决定。 */
  className?: string;
};

/**
 * 横向滚动行：按住鼠标左右拖动即可滚动，鼠标不松开时持续跟随，滚轮也能滚动。
 *
 * 只接管鼠标（pointerType === "mouse"）与左键；触摸交给浏览器原生横向滚动，
 * 免得自定义拖动和滚动惯性打架。内容不需要滚动时完全不介入，子元素点击照常。
 *
 * 拖动结束后紧跟的那一次 click 是拖动的余波，会被吞掉 —— 否则拖完松手会顺带
 * 点中松手位置下的子按钮（缩略图条上就是误切图片）。
 */
export function DragScrollRow({ children, className }: DragScrollRowProps) {
  const rowRef = useRef<HTMLDivElement | null>(null);
  const dragState = useRef<{ pointerId: number; startX: number; startScrollLeft: number; moved: boolean } | null>(null);
  const suppressClick = useRef(false);

  useEffect(() => {
    const row = rowRef.current;
    if (!row) return;
    /**
     * 垂直滚轮转成横向滚动（触控板的横向 deltaX 优先）；滚到两端后放行，
     * 让滚轮事件继续交给外层，不会把外层滚动锁死。
     */
    function handleWheel(event: WheelEvent) {
      if (!row || row.scrollWidth <= row.clientWidth) return;
      const delta = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
      if (delta === 0) return;
      const before = row.scrollLeft;
      row.scrollLeft = before + delta;
      if (row.scrollLeft === before) return;
      // React 的 onWheel 是被动监听，preventDefault 无效，所以这里用原生监听。
      event.preventDefault();
    }
    row.addEventListener("wheel", handleWheel, { passive: false });
    return () => row.removeEventListener("wheel", handleWheel);
  }, []);

  function handlePointerDown(event: PointerEvent<HTMLDivElement>) {
    if (event.pointerType !== "mouse" || event.button !== 0) return;
    const row = rowRef.current;
    if (!row || row.scrollWidth <= row.clientWidth) return;
    // 只记起点，不在这里捕获指针：一旦捕获，后继 click 的 target 会判给本行而不是被点的按钮，
    // 子按钮的 onClick 就再也不会触发（缩略图点不动就是这么来的）。捕获推迟到真的拖动起来之后。
    dragState.current = { pointerId: event.pointerId, startX: event.clientX, startScrollLeft: row.scrollLeft, moved: false };
    row.classList.add("is-dragging");
  }

  function handlePointerMove(event: PointerEvent<HTMLDivElement>) {
    const state = dragState.current;
    const row = rowRef.current;
    if (!state || !row || state.pointerId !== event.pointerId) return;
    const delta = event.clientX - state.startX;
    if (!state.moved) {
      if (Math.abs(delta) <= DRAG_THRESHOLD) return;
      state.moved = true;
      try {
        // 确认是拖动后才捕获指针，这样移出这一行甚至移出窗口也能继续收到 move。
        row.setPointerCapture(event.pointerId);
      } catch {
        // 合成事件或指针已失效时忽略，拖动逻辑本身不依赖捕获成功。
      }
    }
    // 往左拖内容右移：滚动量取反，方向与拖动一致。
    row.scrollLeft = state.startScrollLeft - delta;
  }

  function endDrag(event: PointerEvent<HTMLDivElement>) {
    const state = dragState.current;
    const row = rowRef.current;
    if (!state || !row || state.pointerId !== event.pointerId) return;
    suppressClick.current = state.moved;
    dragState.current = null;
    row.classList.remove("is-dragging");
    if (row.hasPointerCapture(event.pointerId)) row.releasePointerCapture(event.pointerId);
    if (state.moved) {
      // click 紧跟 pointerup 之后派发；万一这次没派发（指针停在行外），下一轮事件循环解除抑制，
      // 免得抑制标记残留下来，把之后每一次正常点击都吞掉。
      window.setTimeout(() => { suppressClick.current = false; }, 0);
    }
  }

  /** 禁用浏览器原生的图片/链接拖拽，否则按住拖动时会拖出一个半透明的图片幽灵。 */
  function handleDragStart(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
  }

  function handleClickCapture(event: MouseEvent<HTMLDivElement>) {
    if (!suppressClick.current) return;
    suppressClick.current = false;
    event.preventDefault();
    event.stopPropagation();
  }

  return (
    <div
      ref={rowRef}
      className={className === undefined ? "drag-scroll-row" : "drag-scroll-row " + className}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onDragStart={handleDragStart}
      onClickCapture={handleClickCapture}
    >
      {children}
    </div>
  );
}
