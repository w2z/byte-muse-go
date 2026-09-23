import { ApiError, toApiError } from "./errors";

/** 生产客户端固定请求 /api/v1，并携带会话 Cookie。 */
export const API_BASE = "/api/v1";
export const UNAUTHORIZED_EVENT = "bytemuse:unauthorized";

/** 认证入口自身不参与“401 后自动续签重试”，否则登录失败会变成无限续签。 */
const AUTH_ENTRY_PATHS = new Set(["/auth/login", "/auth/refresh"]);

export type Page<T> = {
  page: number;
  page_size: number;
  total: number;
  items: T[];
};

let refreshInFlight: Promise<boolean> | null = null;

/**
 * 用当前会话 Cookie 换取新会话，成功返回 true。
 * 会话过期后由后端在 1 天宽限窗口内续签；并发请求共用同一次续签，避免过期瞬间打出多次刷新。
 */
function refreshSession(): Promise<boolean> {
  refreshInFlight ??= fetch(`${API_BASE}/auth/refresh`, {
    method: "POST",
    credentials: "include",
    headers: { Accept: "application/json" },
  })
    .then((response) => response.ok)
    .catch(() => false)
    .finally(() => {
      refreshInFlight = null;
    });
  return refreshInFlight;
}

/** 只有字符串请求体可以安全重放，流式请求体重放会失败，直接跳过续签重试。 */
function isReplayable(init?: RequestInit): boolean {
  return init?.body == null || typeof init.body === "string";
}

async function sendRequest(path: string, init?: RequestInit): Promise<Response> {
  try {
    return await fetch(`${API_BASE}${path}`, {
      ...init,
      credentials: "include",
      headers: {
        Accept: "application/json",
        ...(init?.body ? { "Content-Type": "application/json" } : {}),
        ...init?.headers,
      },
    });
  } catch (error) {
    throw toApiError(error instanceof Error ? error : new TypeError("Failed to fetch"));
  }
}

/**
 * 统一请求入口。非 2xx 转换为 ApiError；2xx 直接返回契约 JSON。
 * 不解析旧版 success 信封。
 *
 * 收到 401 时先静默续签并重放一次：会话在有效期内会滑动续期，过期后 1 天内也能续上，
 * 因此使用中的会话不会被打断；只有超出宽限窗口才派发未授权事件，由布局跳转登录页。
 */
export async function apiRequest<T>(path: string, init?: RequestInit): Promise<T> {
  let response = await sendRequest(path, init);

  if (response.status === 401 && !AUTH_ENTRY_PATHS.has(path) && isReplayable(init) && (await refreshSession())) {
    response = await sendRequest(path, init);
  }

  if (!response.ok) {
    if (response.status === 401 && typeof window !== "undefined") {
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    }
    throw await toApiError(response);
  }

  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}
