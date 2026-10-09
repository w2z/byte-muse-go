import { afterEach, expect, test, vi } from "vitest";
import { apiRequest } from "./client";
import { ApiError } from "./errors";
import type { Media } from "./types";
import { subscribeCatalog } from "./catalogSubscriptions";

vi.mock("./client", async (original) => ({ ...(await original<typeof import("./client")>()), apiRequest: vi.fn() }));
afterEach(() => vi.resetAllMocks());
const film = (id: string, active = false): Media => ({ id, code: id, title: id, translated_title: null, video_type: null, subscription_status: active ? "active" : "none", library_status: "unknown", created_at: "", updated_at: "" });

test("当前页只逐条订阅快照中的唯一影片，跳过已订阅和冲突，单项失败继续", async () => {
  const writes: string[] = [];
  vi.mocked(apiRequest).mockImplementation(async (_path, init) => {
    const body = JSON.parse(String(init?.body));
    writes.push(body.media_id);
    expect(body.mode).toBe("strict");
    expect(init?.headers).toHaveProperty("Idempotency-Key");
    if (body.media_id === "conflict") throw new ApiError("已经订阅", 409);
    if (body.media_id === "failed") throw new ApiError("服务失败", 500);
    return {} as never;
  });
  const result = await subscribeCatalog({ scope: "page", items: [film("a"), film("a"), film("active", true), film("conflict"), film("failed"), film("b")], listPath: "/codes/recommend", signal: new AbortController().signal });
  expect(writes).toEqual(["a", "conflict", "failed", "b"]);
  expect(result).toMatchObject({ total: 5, succeeded: 2, skipped: 2, failed: 1, processed: 5 });
});

test("所有先按服务端页长读完筛选快照再写入，避免未订阅列表收缩漏项", async () => {
  const events: string[] = [];
  vi.mocked(apiRequest).mockImplementation(async (path, init) => {
    if (init?.method === "POST") { events.push(JSON.parse(String(init.body)).media_id); return {} as never; }
    const params = new URL("https://example.test" + path).searchParams;
    expect(params.get("type")).toBe("weekly");
    expect(params.get("subscription")).toBe("none");
    expect(params.get("video_type")).toBe("unknown");
    const page = Number(params.get("page"));
    events.push("read" + page);
    return { items: page === 1 ? [film("a"), film("b")] : [film("c")], page, page_size: 2, total: 3 } as never;
  });
  const result = await subscribeCatalog({ scope: "all", items: [film("other")], listPath: "/ranks?type=weekly&subscription=none&video_type=unknown", signal: new AbortController().signal });
  expect(events).toEqual(["read1", "read2", "a", "b", "c"]);
  expect(result.succeeded).toBe(3);
});

test("读取所有中途失败不提交部分订阅", async () => {
  vi.mocked(apiRequest).mockResolvedValueOnce({ items: [film("a")], page: 1, page_size: 1, total: 2 }).mockRejectedValueOnce(new Error("读取失败"));
  await expect(subscribeCatalog({ scope: "all", items: [], listPath: "/codes/recommend", signal: new AbortController().signal })).rejects.toThrow("读取失败");
  expect(vi.mocked(apiRequest).mock.calls.every(([, init]) => init?.method !== "POST")).toBe(true);
});

test("离开页面后等待当前写入结束，不再提交下一部", async () => {
  const controller = new AbortController();
  vi.mocked(apiRequest).mockImplementation(async () => { controller.abort(); return {} as never; });
  const result = await subscribeCatalog({ scope: "page", items: [film("a"), film("b")], listPath: "/codes/recommend", signal: controller.signal });
  expect(result).toMatchObject({ total: 2, processed: 1, succeeded: 1 });
  expect(apiRequest).toHaveBeenCalledTimes(1);
});

test("权限失效停止剩余写入并保留部分执行结果", async () => {
  vi.mocked(apiRequest).mockResolvedValueOnce({}).mockRejectedValueOnce(new ApiError("没有权限", 403));
  const result = await subscribeCatalog({ scope: "page", items: [film("a"), film("b"), film("c")], listPath: "/codes/recommend", signal: new AbortController().signal });
  expect(result).toMatchObject({ total: 3, processed: 2, succeeded: 1, failed: 1, stoppedReason: "没有权限" });
  expect(apiRequest).toHaveBeenCalledTimes(2);
});
