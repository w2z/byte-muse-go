// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { apiRequest } from "../api/client";
import type { Media } from "../api/types";
import { CatalogSubscriptionButton } from "./CatalogSubscriptionButton";

vi.mock("../api/client", async (original) => ({ ...(await original<typeof import("../api/client")>()), apiRequest: vi.fn() }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });
const media: Media = { id: "a", code: "A", title: "测试", translated_title: null, video_type: null, subscription_status: "none", library_status: "unknown", created_at: "", updated_at: "" };

test("菜单当前页提交订阅，执行中禁用重复点击，结束后刷新缓存并汇总", async () => {
  let finish!: (value: unknown) => void;
  vi.mocked(apiRequest).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
  const client = new QueryClient();
  client.setQueryData(["recommend"], { items: [media] });
  render(<QueryClientProvider client={client}><CatalogSubscriptionButton items={[media]} total={2} listPath="/codes/recommend" disabled={false} /></QueryClientProvider>);
  const button = screen.getByRole("button", { name: "一键订阅" });
  fireEvent.click(button);
  fireEvent.click(await screen.findByText("订阅当前页"));
  await waitFor(() => expect(screen.getByRole("button", { name: "一键订阅" })).toBeDisabled());
  fireEvent.click(screen.getByRole("button", { name: "一键订阅" }));
  expect(apiRequest).toHaveBeenCalledTimes(1);
  expect(apiRequest).toHaveBeenCalledWith("/subscriptions", expect.objectContaining({ method: "POST", body: JSON.stringify({ media_id: "a", mode: "strict", filter: {} }) }));
  await act(async () => finish({}));
  expect(await screen.findByText("订阅完成：成功 1，跳过 0，失败 0")).toBeInTheDocument();
  expect(client.getQueryState(["recommend"])?.isInvalidated).toBe(true);
  await waitFor(() => expect(screen.getByRole("button", { name: "一键订阅" })).toBeEnabled());
});

test("当前筛选无未订阅影片时禁用主按钮", async () => {
  vi.mocked(apiRequest).mockResolvedValue({ items: [], page: 1, page_size: 1, total: 0 });
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><CatalogSubscriptionButton items={[{ ...media, subscription_status: "active" }]} total={20} listPath="/ranks?type=weekly&vr=hide&video_type=censored" disabled={false} /></QueryClientProvider>);
  await waitFor(() => expect(apiRequest).toHaveBeenCalled());
  expect(screen.getByRole("button", { name: "一键订阅" })).toBeDisabled();
  const params = new URL("https://example.test" + vi.mocked(apiRequest).mock.calls[0][0]).searchParams;
  expect(Object.fromEntries(params)).toEqual({ type: "weekly", vr: "hide", video_type: "censored", subscription: "none", page: "1", page_size: "1" });
});

test("当前页全已订阅但其他页可订阅时仅禁用当前页菜单", async () => {
  vi.mocked(apiRequest).mockResolvedValue({ items: [media], page: 1, page_size: 1, total: 1 });
  render(<QueryClientProvider client={new QueryClient()}><CatalogSubscriptionButton items={[{ ...media, subscription_status: "active" }]} total={20} listPath="/codes/recommend" disabled={false} /></QueryClientProvider>);
  await waitFor(() => expect(screen.getByRole("button", { name: "一键订阅" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "一键订阅" }));
  expect(await screen.findByRole("menuitem", { name: "订阅当前页" })).toHaveAttribute("aria-disabled", "true");
  expect(screen.getByRole("menuitem", { name: "订阅所有" })).not.toHaveAttribute("aria-disabled", "true");
  fireEvent.click(screen.getByRole("menuitem", { name: "订阅当前页" }));
  expect(vi.mocked(apiRequest).mock.calls.every(([, init]) => init?.method !== "POST")).toBe(true);
});

test.each([0, 1])("空结果或已完整加载且全已订阅时不额外查询并禁用按钮 (%s)", (total) => {
  render(<QueryClientProvider client={new QueryClient()}><CatalogSubscriptionButton items={total ? [{ ...media, subscription_status: "active" }] : []} total={total} listPath="/codes/recommend" disabled={false} /></QueryClientProvider>);
  expect(screen.getByRole("button", { name: "一键订阅" })).toBeDisabled();
  expect(apiRequest).not.toHaveBeenCalled();
});

test("已订阅筛选不越过当前范围查询未订阅影片", () => {
  render(<QueryClientProvider client={new QueryClient()}><CatalogSubscriptionButton items={[{ ...media, subscription_status: "active" }]} total={20} listPath="/codes/recommend?subscription=active&vr=only" disabled={false} /></QueryClientProvider>);
  expect(screen.getByRole("button", { name: "一键订阅" })).toBeDisabled();
  expect(apiRequest).not.toHaveBeenCalled();
});

test("查询失败时保持禁用，切换筛选不使用之前的可订阅结果", async () => {
  vi.mocked(apiRequest).mockResolvedValueOnce({ total: 1 }).mockRejectedValue(new Error("查询失败"));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const props = { items: [{ ...media, subscription_status: "active" as const }], total: 20, disabled: false };
  const view = render(<QueryClientProvider client={client}><CatalogSubscriptionButton {...props} listPath="/ranks?vr=only" /></QueryClientProvider>);
  await waitFor(() => expect(screen.getByRole("button", { name: "一键订阅" })).toBeEnabled());
  view.rerender(<QueryClientProvider client={client}><CatalogSubscriptionButton {...props} listPath="/ranks?vr=hide" /></QueryClientProvider>);
  expect(screen.getByRole("button", { name: "一键订阅" })).toBeDisabled();
  await waitFor(() => expect(apiRequest).toHaveBeenCalledTimes(2));
  expect(screen.getByRole("button", { name: "一键订阅" })).toBeDisabled();
});
