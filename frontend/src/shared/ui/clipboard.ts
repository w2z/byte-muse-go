/** 复制文本；兼容局域网 HTTP 下缺少 Clipboard API 的浏览器，并恢复原焦点及清理临时节点。 */
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const input = document.createElement("textarea");
  input.value = text;
  input.readOnly = true;
  input.style.cssText = "position:fixed;opacity:0;pointer-events:none;";
  (focused?.closest('[role="dialog"]') || document.body).appendChild(input);
  try {
    input.focus({ preventScroll: true });
    input.select();
    if (!document.execCommand("copy")) throw new Error("Copy failed");
  } finally {
    input.remove();
    focused?.focus({ preventScroll: true });
  }
}
