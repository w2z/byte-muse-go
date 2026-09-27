import { IconClose } from "@arco-design/web-react/icon";
import { useEffect, type ReactNode } from "react";

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

  if (!visible) return null;
  return (
    <div className="app-dialog-mask" role="presentation">
      <section className="app-dialog" role="dialog" aria-modal="true" aria-label={title}>
        <header className="app-dialog-header"><span>{title}</span><button type="button" aria-label="Close" onClick={onClose}><IconClose /></button></header>
        <div className="app-dialog-body">{children}</div>
        {footer === undefined ? null : <footer className="app-dialog-footer">{footer}</footer>}
      </section>
    </div>
  );
}
