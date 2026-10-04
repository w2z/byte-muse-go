// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { apiRequest } from "../../shared/api/client";
import { ReleaseTodayPage } from "../releases/ReleaseTodayPage";
import { RecommendPage } from "../recommend/RecommendPage";
import { RankPage } from "../rank/RankPage";

Object.defineProperty(window, "matchMedia", { writable: true, value: (query: string) => ({ matches: false, media: query, addListener() {}, removeListener() {} }) });
vi.mock("../../shared/api/client", () => ({ apiRequest: vi.fn(async () => ({ items: [], total: 40, page: 1, page_size: 15 })) }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

test.each([["上新", ReleaseTodayPage, "/codes/release_today"], ["推荐", RecommendPage, "/codes/recommend"], ["榜单", RankPage, "/ranks?type=daily"]] as const)("%s 的组合筛选交给服务端并重置页码", async (_name, Component, endpoint) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><Component /></QueryClientProvider>);
  await waitFor(() => expect(apiRequest).toHaveBeenCalled());
  fireEvent.click(await screen.findByText("2", { exact: true }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toContain("page=2"));
  fireEvent.click(screen.getByLabelText("订阅状态筛选"));
  fireEvent.click(screen.getByText("已订阅", { exact: true }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toContain("subscription=active"));
  fireEvent.click(screen.getByLabelText("影片类型筛选"));
  fireEvent.click(screen.getByText("未分类", { exact: true }));
  await waitFor(() => {
    const url = vi.mocked(apiRequest).mock.calls.at(-1)?.[0];
    expect(url).toContain(endpoint); expect(url).toContain("page=1");
    expect(url).toContain("subscription=active&video_type=unknown");
  });
  const beforeSearch = vi.mocked(apiRequest).mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.length).toBeGreaterThan(beforeSearch));
  fireEvent.click(screen.getByRole("button", { name: "重置" }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).not.toContain("subscription="));
  expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).not.toContain("video_type=");
});
