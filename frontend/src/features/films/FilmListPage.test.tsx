import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { FilmListPage } from "./FilmListPage";

describe("影片列表空状态", () => {
  it("接口返回空列表时展示空状态而不是表格行", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            page: 1,
            page_size: 20,
            total: 0,
            items: [],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={queryClient}>
        <FilmListPage />
      </QueryClientProvider>,
    );

    expect(await screen.findByText("暂无影片")).toBeInTheDocument();
    expect(screen.queryByRole("row")).not.toBeInTheDocument();
    expect(vi.mocked(fetch).mock.calls[0]?.[0]).toBe("/api/v1/media?page=1&page_size=20");
  });
  it("打开详情时从后端读取单条媒体", async () => {
    const user = userEvent.setup();
    const media = { id: "media-1", code: "ABC-001", title: "真实影片", translated_title: null, subscription_status: "none", library_status: "absent", created_at: "2026-09-23T00:00:00Z", updated_at: "2026-09-23T00:00:00Z" };
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => Promise.resolve(new Response(JSON.stringify(String(input).includes("/media?page") ? { page: 1, page_size: 20, total: 1, items: [media] } : media), { status: 200 })));
    vi.stubGlobal("fetch", fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><FilmListPage /></QueryClientProvider>);
    await user.click(await screen.findByRole("button", { name: "查看详情" }));
    expect(fetchMock.mock.calls.some((call) => String(call[0]) === "/api/v1/media/media-1")).toBe(true);
  });
  it("订阅影片时发送幂等键与服务端契约字段", async () => {
    const user = userEvent.setup();
    const media = { id: "media-1", code: "ABC-001", title: "真实影片", translated_title: null, subscription_status: "none", library_status: "absent", created_at: "2026-09-23T00:00:00Z", updated_at: "2026-09-23T00:00:00Z" };
    const fetchMock = vi.fn().mockImplementation((_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") return Promise.resolve(new Response(JSON.stringify({ id: "sub-1", media_id: media.id, status: "active", mode: "strict", filter: {}, created_at: media.created_at, updated_at: media.updated_at, version: 1 }), { status: 201 }));
      return Promise.resolve(new Response(JSON.stringify({ page: 1, page_size: 20, total: 1, items: [media] }), { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><FilmListPage /></QueryClientProvider>);
    await user.click(await screen.findByRole("button", { name: "订阅影片" }));
    const call = fetchMock.mock.calls.find((entry) => (entry[1] as RequestInit | undefined)?.method === "POST");
    expect(String(call?.[0])).toBe("/api/v1/subscriptions");
    expect((call?.[1]?.headers as Record<string, string>)["Idempotency-Key"]).toMatch(/.{8,}/);
    expect(JSON.parse(call?.[1]?.body as string)).toEqual({ media_id: media.id, mode: "strict", filter: {} });
    await waitFor(() => expect(screen.getByRole("button", { name: "订阅影片" })).not.toBeDisabled());
  });
  it("分页切换请求服务端第二页", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const page = String(input).includes("page=2") ? 2 : 1;
      return Promise.resolve(new Response(JSON.stringify({ page, page_size: 20, total: 21, items: [{ id: "media-" + page, code: "CODE-" + page, title: "影片" + page, translated_title: null, subscription_status: "none", library_status: "absent", created_at: "2026-09-23T00:00:00Z", updated_at: "2026-09-23T00:00:00Z" }] }), { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><FilmListPage /></QueryClientProvider>);
    await user.click(await screen.findByRole("button", { name: "下一页" }));
    await waitFor(() => expect(fetchMock.mock.calls.some((call) => String(call[0]) === "/api/v1/media?page=2&page_size=20")).toBe(true));
  });
});
