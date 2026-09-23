import { afterEach, describe, expect, it, vi } from "vitest";
import { apiRequest, isApiError, UNAUTHORIZED_EVENT } from "./client";

const trackedListeners: Array<[string, EventListener]> = [];

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function unauthorizedResponse() {
  return jsonResponse({ code: "unauthorized", message: "未登录或会话失效" }, 401);
}

/** 记录未授权事件次数，测试结束后统一解绑。 */
function trackUnauthorized() {
  const listener = vi.fn();
  window.addEventListener(UNAUTHORIZED_EVENT, listener);
  trackedListeners.push([UNAUTHORIZED_EVENT, listener]);
  return listener;
}

afterEach(() => {
  vi.unstubAllGlobals();
  while (trackedListeners.length > 0) {
    const [event, listener] = trackedListeners.pop()!;
    window.removeEventListener(event, listener);
  }
});

describe("apiRequest 会话续签", () => {
  it("401 后先续签再重放原请求，不派发未授权事件", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(unauthorizedResponse())
      .mockResolvedValueOnce(jsonResponse({ user: { id: "u1", username: "admin" } }))
      .mockResolvedValueOnce(jsonResponse({ active_subscriptions: 3 }));
    vi.stubGlobal("fetch", fetchMock);
    const unauthorized = trackUnauthorized();

    await expect(apiRequest("/dashboard")).resolves.toEqual({ active_subscriptions: 3 });

    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/dashboard", expect.objectContaining({ credentials: "include" }));
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/v1/auth/refresh", expect.objectContaining({ method: "POST", credentials: "include" }));
    expect(fetchMock).toHaveBeenNthCalledWith(3, "/api/v1/dashboard", expect.objectContaining({ credentials: "include" }));
    expect(unauthorized).not.toHaveBeenCalled();
  });

  it("续签失败时派发未授权事件并抛出原始 401", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(unauthorizedResponse())
      .mockResolvedValueOnce(unauthorizedResponse());
    vi.stubGlobal("fetch", fetchMock);
    const unauthorized = trackUnauthorized();

    const error = await apiRequest("/dashboard").catch((reason: unknown) => reason);

    expect(isApiError(error) && error.status === 401).toBe(true);
    expect(unauthorized).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("并发 401 只发起一次续签", async () => {
    let refreshCalls = 0;
    const seenPaths = new Set<string>();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/auth/refresh") {
        refreshCalls += 1;
        await Promise.resolve();
        return jsonResponse({ user: { id: "u1", username: "admin" } });
      }
      if (!seenPaths.has(url)) {
        seenPaths.add(url);
        return unauthorizedResponse();
      }
      return jsonResponse({ ok: url });
    });
    vi.stubGlobal("fetch", fetchMock);
    const unauthorized = trackUnauthorized();

    await expect(Promise.all([apiRequest("/dashboard"), apiRequest("/media")])).resolves.toEqual([
      { ok: "/api/v1/dashboard" },
      { ok: "/api/v1/media" },
    ]);
    expect(refreshCalls).toBe(1);
    expect(unauthorized).not.toHaveBeenCalled();
  });

  it.each(["/auth/login", "/auth/refresh"])("%s 自身不触发续签", async (path) => {
    const fetchMock = vi.fn().mockResolvedValueOnce(unauthorizedResponse());
    vi.stubGlobal("fetch", fetchMock);
    const unauthorized = trackUnauthorized();

    await expect(apiRequest(path, { method: "POST", body: "{}" })).rejects.toBeInstanceOf(Error);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(unauthorized).toHaveBeenCalledTimes(1);
  });
});
