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

test("所有菜单读取当前筛选范围，空结果不发送订阅", async () => {
  vi.mocked(apiRequest).mockResolvedValue({ items: [], page: 1, page_size: 100, total: 0 });
  render(<QueryClientProvider client={new QueryClient()}><CatalogSubscriptionButton items={[]} total={2} listPath="/ranks?type=weekly&subscription=none" disabled={false} /></QueryClientProvider>);
  fireEvent.click(screen.getByRole("button", { name: "一键订阅" }));
  fireEvent.click(await screen.findByText("订阅所有"));
  expect(await screen.findByText("订阅完成：成功 0，跳过 0，失败 0")).toBeInTheDocument();
  expect(apiRequest).toHaveBeenCalledTimes(1);
  expect(vi.mocked(apiRequest).mock.calls[0][0]).toContain("type=weekly&subscription=none");
});
