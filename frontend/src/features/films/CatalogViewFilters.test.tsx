// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { apiRequest } from "../../shared/api/client";
import { ReleaseTodayPage } from "../releases/ReleaseTodayPage";
import { RecommendPage } from "../recommend/RecommendPage";
import { RankPage } from "../rank/RankPage";
import { AllFilmsPage } from "./AllFilmsPage";

Object.defineProperty(window, "matchMedia", { writable: true, value: (query: string) => ({ matches: false, media: query, addListener() {}, removeListener() {} }) });
vi.mock("../../shared/api/client", async (original) => ({ ...(await original<typeof import("../../shared/api/client")>()), apiRequest: vi.fn(async () => ({ items: [], total: 40, page: 1, page_size: 15 })) }));
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.mocked(apiRequest).mockImplementation(async () => ({ items: [], total: 40, page: 1, page_size: 15 }) as never); });

test.each([["所有影片", AllFilmsPage, "/media"], ["上新", ReleaseTodayPage, "/codes/release_today"], ["推荐", RecommendPage, "/codes/recommend"], ["榜单", RankPage, "/ranks?type=daily"]] as const)("%s 的组合筛选交给服务端并重置页码", async (_name, Component, endpoint) => {
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
  fireEvent.click(screen.getByLabelText("VR筛选"));
  fireEvent.click(screen.getByText("隐藏VR影片", { exact: true }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toContain("vr=hide"));
  fireEvent.click(screen.getByLabelText("VR筛选"));
  fireEvent.click(screen.getByText("只显示VR影片", { exact: true }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toContain("vr=only"));
  const beforeSearch = vi.mocked(apiRequest).mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.length).toBeGreaterThan(beforeSearch));
  fireEvent.click(screen.getByRole("button", { name: "重置" }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).not.toContain("subscription="));
  expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).not.toContain("video_type=");
  expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).not.toContain("vr=");
});

test.each([["上新", ReleaseTodayPage], ["推荐", RecommendPage], ["榜单", RankPage]] as const)("%s 批量完成后回到首页避免筛选结果缩短导致越界", async (_name, Component) => {
  vi.mocked(apiRequest).mockImplementation(async (path) => {
    if (path === "/system/settings") return { values: { IMAGE_MODE: "INVISIBLE" }, configured: {} } as never;
    return { items: [{ id: "already-active", code: "TEST", title: "测试", translated_title: null, video_type: null, subscription_status: "none", library_status: "unknown", created_at: "", updated_at: "" }], total: 30, page: path.includes("page=2") ? 2 : 1, page_size: 15 } as never;
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<MemoryRouter><QueryClientProvider client={client}><Component /></QueryClientProvider></MemoryRouter>);
  fireEvent.click(await screen.findByText("2", { exact: true }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.some(([path]) => path.includes("page=2"))).toBe(true));
  await waitFor(() => expect((screen.getByRole("button", { name: "一键订阅" }) as HTMLButtonElement).disabled).toBe(false));
  const before = vi.mocked(apiRequest).mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "一键订阅" }));
  fireEvent.click(await screen.findByText("订阅当前页"));
  await screen.findByText("订阅完成：成功 1，跳过 0，失败 0");
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.slice(before).some(([path]) => path.includes("page=1"))).toBe(true));
  vi.mocked(apiRequest).mockImplementation(async () => ({ items: [], total: 40, page: 1, page_size: 15 }) as never);
});

test.each([["上新", ReleaseTodayPage], ["推荐", RecommendPage], ["榜单", RankPage]] as const)("%s 在筛选处提供一键订阅菜单", async (_name, Component) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><Component /></QueryClientProvider>);
  await waitFor(() => expect(apiRequest).toHaveBeenCalled());
  await waitFor(() => expect((screen.getByRole("button", { name: "一键订阅" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "一键订阅" }));
  expect(await screen.findByText("订阅当前页")).toBeTruthy();
  expect(screen.getByText("订阅所有")).toBeTruthy();
});
