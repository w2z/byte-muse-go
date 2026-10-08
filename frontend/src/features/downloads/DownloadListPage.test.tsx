// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, test, vi } from "vitest";
import dayjs from "dayjs";
import { DownloadListPage } from "./DownloadListPage";

// jsdom 未实现媒体查询，提供 Arco 响应式描述列表所需的浏览器接口。
Object.defineProperty(window, "matchMedia", { writable: true, value: (query: string) => ({ matches: false, media: query, addListener() {}, removeListener() {} }) });

const requests: string[] = [];
let responseItems: Record<string, unknown>[] = [];
let detailError = false;
// 只替换网络请求：封面地址拼接沿用真实实现，抽屉里的封面才会走缓存入口。
vi.mock("../../shared/api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../shared/api/client")>()),
  apiRequest: (path: string) => {
    requests.push(path);
    if (path.startsWith("/media/")) return detailError ? Promise.reject(new Error("影片加载失败")) : Promise.resolve({ id: "m1", code: "TEST-001", title: "影片详情标题", release_date: "2026-09-28", subscription_status: "active", display_status: "subscribed", preview_url: "https://example.test/trailer.mp4" });
    if (path === "/system/settings") return Promise.resolve({ values: { IMAGE_MODE: "INVISIBLE" } });
    return Promise.resolve({ items: responseItems, total: responseItems.length, page: 1, page_size: 15 });
  },
}));
afterEach(() => { cleanup(); requests.length = 0; responseItems = []; detailError = false; });

test("展示实时指标、PT 规则标签和未知值，移除外部任务列", async () => {
  responseItems = [{ id: "metrics", media_id: "m1", code: "TEST-001", status: "completed", source_kind: "pt",
    metrics: { size_bytes: 1024 ** 4, remaining_bytes: 0, downloaded_bytes: 1024 ** 3, download_speed: 1024 ** 2, upload_speed: 1024, save_path: "/downloads/test", share_ratio: 2.5, seeding_seconds: 3 * 86400 },
    seeding: { status: "completed", rule: "规则测试" } },
    { id: "unknown", media_id: "m2", status: "submitted", source_kind: "bt", metrics: null }];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  const row = (await screen.findByText("/downloads/test")).closest("tr")!;
  for (const text of ["1.00 TB", "0 B", "1.00 GB", "1.00 MB/s", "1.00 KB/s", "2.50", "3 天 0 小时", "已达标（估算）"]) expect(within(row).getByText(text)).not.toBeNull();
  expect(screen.getByText("不适用")).not.toBeNull();
  expect(screen.queryByText("外部任务")).toBeNull();
});

test.each([["加入开始", "added"], ["完成开始", "completed"]])("%s支持时间选择和四个快捷范围，查询保留所选时刻", async (placeholder, prefix) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  await waitFor(() => expect(requests.length).toBeGreaterThan(0));
  fireEvent.click(screen.getByPlaceholderText(placeholder));
  for (const name of ["今天", "昨天", "本周", "本月"]) expect(await screen.findByText(name, { exact: true })).not.toBeNull();
  expect(screen.getByText("选择时间", { exact: true })).not.toBeNull();
  const before = dayjs();
  fireEvent.click(screen.getByText("今天", { exact: true }));
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(requests.at(-1)).toContain(prefix + "_from="));
  const params = new URLSearchParams(requests.at(-1)!.split("?")[1]);
  const end = dayjs(params.get(prefix + "_to")!);
  expect(dayjs(params.get(prefix + "_from")!).valueOf()).toBe(before.startOf("day").valueOf());
  expect(end.valueOf()).toBeGreaterThanOrEqual(before.startOf("second").valueOf());
  expect(end.valueOf()).toBeLessThanOrEqual(Date.now());
  const appliedEnd = params.get(prefix + "_to");
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(requests.length).toBeGreaterThan(2));
  expect(new URLSearchParams(requests.at(-1)!.split("?")[1]).get(prefix + "_to")).toBe(appliedEnd);
  const exactStart = before.subtract(1, "day").hour(10).minute(11).second(12).millisecond(0);
  fireEvent.click(screen.getByPlaceholderText(placeholder));
  fireEvent.change(screen.getByPlaceholderText(placeholder), { target: { value: exactStart.format("YYYY-MM-DD HH:mm:ss") } });
  fireEvent.click(screen.getByRole("button", { name: "确定" }));
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(new URLSearchParams(requests.at(-1)!.split("?")[1]).get(prefix + "_from")).toBe(exactStart.toISOString()));
  fireEvent.click(screen.getByRole("button", { name: "重置" }));
  await waitFor(() => expect(requests.at(-1)).toBe("/downloads?page=1&page_size=15"));
});

test("点击番号加载封面和资料，详情无卡片与操作并可关闭重开", async () => {
  responseItems = [{ id: "d1", media_id: "media/id", code: "TEST-001", status: "failed" }];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "TEST-001" }));
  const dialog = await screen.findByRole("dialog", { name: "影片信息" });
  expect((await within(dialog).findAllByText("影片详情标题")).length).toBeGreaterThan(0);
  expect(requests).toContain("/media/media%2Fid");
  expect(within(dialog).getByText("2026-09-28")).not.toBeNull();
  expect(within(dialog).queryByRole("article")).toBeNull();
  expect(within(dialog).getByRole("button", { name: "复制番号 TEST-001" })).not.toBeNull();
  expect(within(dialog).queryByText("已订阅")).toBeNull();
  expect(within(dialog).queryByRole("button", { name: "预告" })).toBeNull();
  fireEvent.click(within(dialog).getByRole("button", { name: "关闭影片信息" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  fireEvent.click(screen.getByRole("button", { name: "TEST-001" }));
  expect(await screen.findByRole("dialog", { name: "影片信息" })).not.toBeNull();
  fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape", keyCode: 27 });
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

test("影片加载失败可在抽屉内重试", async () => {
  detailError = true;
  responseItems = [{ id: "d1", media_id: "m1", code: "TEST-001", status: "failed" }];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "TEST-001" }));
  const dialog = await screen.findByRole("dialog", { name: "影片信息" });
  expect(await within(dialog).findByText("影片加载失败")).not.toBeNull();
  detailError = false;
  fireEvent.click(within(dialog).getByRole("button", { name: "重试" }));
  expect((await within(dialog).findAllByText("影片详情标题")).length).toBeGreaterThan(0);
});

test("空筛选条件下点击搜索仍重新查询全部下载任务", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  await waitFor(() => expect(requests).toHaveLength(1));
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(requests).toHaveLength(2));
  expect(requests[1]).toBe("/downloads?page=1&page_size=15");
});

test.each([["paused", "暂停"], ["queued", "排队"]])("筛选项 %s 点击搜索后才向服务端提交条件", async (value, label) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  await waitFor(() => expect(requests.length).toBeGreaterThan(0));
  const filters = container.querySelector(".download-filters")!;
  expect(filters.textContent).toContain("下载状态");
  expect(filters.textContent).toContain("加入时间");
  expect(filters.textContent).toContain("下载完成时间");
  const control = filters.querySelector('[aria-label="下载状态筛选"]')!;
  fireEvent.click(control);
  fireEvent.click(screen.getByText(label));
  expect(requests.some((path) => path.includes(`transfer_status=${value}`))).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(requests.some((path) => path.includes(`transfer_status=${value}`))).toBe(true));
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

test.each([["queued", "排队"], ["stalled", "等待连接"], ["checking", "校验中"], ["metadata", "获取元数据"], ["moving", "移动中"], ["unknown", "待核实"], ["paused", "暂停"], ["stopped", "停止"], ["downloading", "下载中"], ["completed", "下载完成"], ["failed", "下载失败"]])("下载状态以下载器的 %s 为准，不显示内部已提交阶段", async (transferStatus, label) => {
  responseItems = [{ id: "d1", media_id: "m1", status: "submitted", transfer_status: transferStatus }];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  expect(await screen.findByRole("cell", { name: label })).not.toBeNull();
  expect(screen.queryByRole("cell", { name: "已提交" })).toBeNull();
  expect(screen.queryByText("传输状态", { exact: true })).toBeNull();
});

test("尚无下载器状态时保留搜索阶段", async () => {
  responseItems = [{ id: "d1", media_id: "m1", status: "searching", transfer_status: null }];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  expect(await screen.findByRole("cell", { name: "搜索中" })).not.toBeNull();
});

test("影片显示番号而不是内部媒体ID，并展示失败操作", async () => {
  responseItems = [{ id: "d1", media_id: "internal-media-id", code: "TEST-001", status: "failed", available_actions: ["retry", "delete"] }];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  expect(await screen.findByRole("cell", { name: "TEST-001" })).not.toBeNull();
  expect(screen.queryByText("internal-media-id")).toBeNull();
  expect(screen.getByRole("button", { name: "重试" })).not.toBeNull();
  expect(screen.getByRole("button", { name: "删除" })).not.toBeNull();
});

test("删除下拉区分保留文件与删除文件，并显示对应确认提示", async () => {
  responseItems = [{ id: "d1", media_id: "m1", code: "TEST-001", status: "submitted", transfer_status: "downloading", available_actions: ["stop", "delete", "delete_files"] }];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "删除" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "删除任务+文件" }));
  expect(await screen.findByText("将删除下载任务及下载器中的文件，文件删除后无法恢复。")).not.toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
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
