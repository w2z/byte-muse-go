import { ApiError, toApiError } from "./errors";

/** 生产客户端固定请求 /api/v1，并携带会话 Cookie。 */
export const API_BASE = "/api/v1";
export const UNAUTHORIZED_EVENT = "bytemuse:unauthorized";

/** 认证入口自身不参与“401 后自动续签重试”，否则登录失败会变成无限续签。 */
const AUTH_ENTRY_PATHS = new Set(["/auth/login", "/auth/refresh"]);

/**
 * 把影片封面地址改写成服务端缓存入口：服务端按番号把封面持久化到 /data/cover，
 * 命中时直接返回本地文件，未命中下载后落盘，源站不可用时回退到原地址。
 * externalDomain 是设置页的「外网访问地址」；已配置时用它拼绝对地址，页面显示与微信封面推送使用同一个地址，
 * 未配置时用跟随页面来源的相对地址。
 * 没有封面地址时返回 undefined，由调用方显示占位图；没有番号时保留原地址。
 */
export function coverCacheURL(
  code: string | null | undefined,
  source: string | null | undefined,
  externalDomain?: string | null,
): string | undefined {
  const url = source?.trim();
  if (!url) return undefined;
  const normalized = code?.trim();
  if (!normalized) return url;
  const path = `${API_BASE}/covers/${encodeURIComponent(normalized)}?source=${encodeURIComponent(url)}`;
  const domain = externalDomain?.trim().replace(/\/+$/, "");
  return domain ? `${domain}${path}` : path;
}

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
 * onProgress 用于请求 NDJSON 扫描进度；旧后端返回 JSON 时仍兼容读取结果。
 */
export async function apiRequest<T>(path: string, init?: RequestInit, onProgress?: (progress: ScanProgress) => void): Promise<T> {
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
  if (onProgress && response.headers.get("content-type")?.includes("application/x-ndjson")) {
    return readProgressStream<T>(response, onProgress);
  }
  return (await response.json()) as T;
}

/** 扫描请求的动态计数；total 随发现的媒体文件增加，percent 由服务端按当前总数计算。 */
export type ScanProgress = {
  phase: "waiting" | "discovering" | "processing" | "finalizing" | "completed";
  processed: number;
  total: number;
  percent: number;
  current: string;
};

/** 逐行解析进度响应，兼容 UTF-8 与 JSON 跨网络分块；只有 result 事件才表示任务成功返回。 */
async function readProgressStream<T>(response: Response, onProgress: (progress: ScanProgress) => void): Promise<T> {
  const reader = response.body?.getReader();
  if (!reader) throw new ApiError("无法读取处理进度");
  const decoder = new TextDecoder();
  let buffer = "";
  type Event = { type: "progress"; progress: ScanProgress } | { type: "result"; result: T } | { type: "error"; error: { message: string } };
  try {
    while (true) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value, { stream: !done });
      const lines = buffer.split("\n");
      buffer = lines.pop() ?? "";
      if (done && buffer.trim()) { lines.push(buffer); buffer = ""; }
      for (const line of lines) {
        if (!line.trim()) continue;
        const event = JSON.parse(line) as Event;
        if (event.type === "progress") onProgress(event.progress);
        else if (event.type === "result") return event.result;
        else if (event.type === "error") throw new ApiError(event.error.message);
      }
      if (done) throw new ApiError("进度连接已中断，未收到处理结果，请检查任务结果后重试");
    }
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}
