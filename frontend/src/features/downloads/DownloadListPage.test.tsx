// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, test, vi } from "vitest";
import { DownloadListPage } from "./DownloadListPage";

const requests: string[] = [];
let responseItems: Record<string, unknown>[] = [];
vi.mock("../../shared/api/client", () => ({
  apiRequest: (path: string) => { requests.push(path); return Promise.resolve({ items: responseItems, total: responseItems.length, page: 1, page_size: 15 }); },
}));
afterEach(() => { cleanup(); requests.length = 0; responseItems = []; });

test("空筛选条件下点击搜索仍重新查询全部下载任务", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  await waitFor(() => expect(requests).toHaveLength(1));
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(requests).toHaveLength(2));
  expect(requests[1]).toBe("/downloads?page=1&page_size=15");
});

test("筛选项显示标题，点击搜索后才向服务端提交条件", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  await waitFor(() => expect(requests.length).toBeGreaterThan(0));
  const filters = container.querySelector(".download-filters")!;
  expect(filters.textContent).toContain("下载状态");
  expect(filters.textContent).toContain("加入时间");
  expect(filters.textContent).toContain("下载完成时间");
  const control = filters.querySelector('[aria-label="下载状态筛选"]')!;
  fireEvent.click(control);
  fireEvent.click(screen.getByText("暂停"));
  expect(requests.some((path) => path.includes("transfer_status=paused"))).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(requests.some((path) => path.includes("transfer_status=paused"))).toBe(true));
});

test("重置清空草稿和已应用条件并查询全部任务", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  await waitFor(() => expect(requests.length).toBeGreaterThan(0));
  const control = container.querySelector('[aria-label="下载状态筛选"]')!;
  fireEvent.click(control);
  fireEvent.click(screen.getByText("暂停"));
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(requests.at(-1)).toContain("transfer_status=paused"));
  fireEvent.click(screen.getByRole("button", { name: "重置" }));
  await waitFor(() => expect(requests.at(-1)).toBe("/downloads?page=1&page_size=15"));
  expect(control.textContent).toContain("全部状态");
});

test("传输状态以中文显示", async () => {
  responseItems = [{ id: "d1", media_id: "m1", status: "submitted", transfer_status: "paused" }];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  expect(await screen.findByRole("cell", { name: "暂停" })).not.toBeNull();
});

test("筛选与表格共用一张卡片并由分隔条隔开", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  const filters = container.querySelector(".download-filters");
  await waitFor(() => expect(container.querySelector(".data-table table")).not.toBeNull());
  const table = container.querySelector(".data-table table")!;
  const card = filters?.closest(".content-card");
  const divider = card?.querySelector(".arco-divider-horizontal");
  expect(card).not.toBeNull();
  expect(table?.closest(".content-card")).toBe(card);
  expect(container.querySelectorAll(".content-card")).toHaveLength(1);
  expect(divider).not.toBeNull();
  expect(filters!.compareDocumentPosition(divider!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(divider!.compareDocumentPosition(table) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});
