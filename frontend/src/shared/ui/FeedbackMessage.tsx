import { IconCheckCircle, IconCloseCircle } from "@arco-design/web-react/icon";
import { useEffect, useRef, useState, type ReactNode } from "react";

type FeedbackKind = "success" | "error";
type FeedbackPhase = "entering" | "visible" | "leaving";
type Feedback = { kind: FeedbackKind; text: string; phase: FeedbackPhase } | null;

const ENTER_DURATION = 180;
const EXIT_DURATION = 160;

/** 提供兼容 React 19 的轻量操作反馈，避免旧版 Arco Message 对 ReactDOM.render 的依赖。 */
export function useFeedbackMessage(): [{ success: (text: string) => void; error: (text: string) => void }, ReactNode] {
  const [feedback, setFeedback] = useState<Feedback>(null);
  const enterTimer = useRef<number | null>(null);
  const closeTimer = useRef<number | null>(null);
  const exitTimer = useRef<number | null>(null);

  function clearTimers() {
    if (enterTimer.current !== null) window.clearTimeout(enterTimer.current);
    if (closeTimer.current !== null) window.clearTimeout(closeTimer.current);
    if (exitTimer.current !== null) window.clearTimeout(exitTimer.current);
    enterTimer.current = null;
    closeTimer.current = null;
    exitTimer.current = null;
  }

  useEffect(() => { return () => clearTimers(); }, []);

  function show(kind: FeedbackKind, text: string) {
    clearTimers();
    setFeedback({ kind, text, phase: "entering" });
    enterTimer.current = window.setTimeout(() => {
      setFeedback((current) => current ? { ...current, phase: "visible" } : null);
      enterTimer.current = null;
    }, ENTER_DURATION);
    closeTimer.current = window.setTimeout(() => {
      setFeedback((current) => current ? { ...current, phase: "leaving" } : null);
      closeTimer.current = null;
      exitTimer.current = window.setTimeout(() => {
        setFeedback(null);
        exitTimer.current = null;
      }, EXIT_DURATION);
    }, 3000);
  }

  return [
    { success: (text) => show("success", text), error: (text) => show("error", text) },
    feedback ? (
      <div className={`feedback-message ${feedback.kind} feedback-message--${feedback.phase}`} role="status">
        {feedback.kind === "success" ? <IconCheckCircle aria-hidden="true" /> : <IconCloseCircle aria-hidden="true" />}
        <span>{feedback.text}</span>
      </div>
    ) : null,
  ];
}
