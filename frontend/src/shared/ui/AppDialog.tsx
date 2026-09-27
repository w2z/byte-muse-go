import { IconClose } from "@arco-design/web-react/icon";
import { useEffect, type MouseEvent, type ReactNode } from "react";

type AppDialogProps = {
  visible: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
};

/** React 19 兼容的公共弹窗，统一遮罩、关闭、键盘 Escape 与页脚布局。 */
export function AppDialog({ visible, title, onClose, children, footer }: AppDialogProps) {
  useEffect(() => {
    if (!visible) return;
    function handleKey(event: KeyboardEvent) { if (event.key === "Escape") onClose(); }
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [visible, onClose]);

  /**
   * 点遮罩关闭。只认「在遮罩空白处按下」：遮罩是 grid 居中容器，弹窗外的区域 target 就是遮罩自身。
   * 用 mousedown 而不是 click —— 在弹窗内拖选文字或拖滚动条、最后在遮罩上松手时，
   * click 的 target 会算成两者的共同祖先（遮罩），用 click 会误关。
   */
  function handleMaskMouseDown(event: MouseEvent<HTMLDivElement>) {
    if (event.target === event.currentTarget) onClose();
  }

  if (!visible) return null;
  return (
    <div className="app-dialog-mask" role="presentation" onMouseDown={handleMaskMouseDown}>
      <section className="app-dialog" role="dialog" aria-modal="true" aria-label={title}>
        <header className="app-dialog-header"><span>{title}</span><button type="button" aria-label="Close" onClick={onClose}><IconClose /></button></header>
        <div className="app-dialog-body">{children}</div>
        {footer === undefined ? null : <footer className="app-dialog-footer">{footer}</footer>}
      </section>
    </div>
  );
}
