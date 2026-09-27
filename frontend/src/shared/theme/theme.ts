import { create } from "zustand";

export type ThemeMode = "light" | "dark";

/**
 * Arco 组件靠 body 上的 arco-theme 属性切换整套设计 token，
 * 自定义样式（styles.css）复用同一属性做深色覆盖，所以只用这一个来源。
 */
const THEME_ATTRIBUTE = "arco-theme";
const STORAGE_KEY = "bytemuse:theme";

function systemPrefersDark(): boolean {
  return typeof window !== "undefined" && window.matchMedia?.("(prefers-color-scheme: dark)").matches === true;
}

function readStoredMode(): ThemeMode | null {
  try {
    const value = window.localStorage.getItem(STORAGE_KEY);
    return value === "light" || value === "dark" ? value : null;
  } catch {
    // 隐私模式等场景下 localStorage 可能不可用，按“用户未选择”处理。
    return null;
  }
}

/** 把主题写到 body；Arco 组件与自定义样式都读这个属性。 */
export function applyTheme(mode: ThemeMode): void {
  if (typeof document === "undefined") {
    return;
  }
  document.body.setAttribute(THEME_ATTRIBUTE, mode);
}

/** 首次访问跟随系统偏好，之后以用户的选择为准。 */
export function resolveInitialMode(): ThemeMode {
  return readStoredMode() ?? (systemPrefersDark() ? "dark" : "light");
}

type ThemeState = {
  mode: ThemeMode;
  setMode: (mode: ThemeMode) => void;
  toggle: () => void;
};

// 模块加载时就落到 body 上：样式表导入早于首屏渲染，避免先画亮色再切深色的闪白。
const initialMode = resolveInitialMode();
applyTheme(initialMode);

export const useTheme = create<ThemeState>((set, get) => ({
  mode: initialMode,
  setMode: (mode) => {
    applyTheme(mode);
    try {
      window.localStorage.setItem(STORAGE_KEY, mode);
    } catch {
      // 存不下就只在本次会话生效。
    }
    set({ mode });
  },
  toggle: () => get().setMode(get().mode === "dark" ? "light" : "dark"),
}));
