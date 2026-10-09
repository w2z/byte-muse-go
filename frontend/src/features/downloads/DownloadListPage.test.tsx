// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, test, vi } from "vitest";
import { DownloadListPage } from "./DownloadListPage";
import { DownloadColumnFilter } from "./DownloadColumnFilter";

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

test("表头日期快捷范围使用 RFC3339，确认前不提交", async () => {
  const apply = vi.fn();
  render(<DownloadColumnFilter label="加入时间" kind="time" values={[]} onApply={apply} />);
  fireEvent.click(screen.getByPlaceholderText("开始时间"));
  for (const label of ["今天", "昨天", "本周", "本月"]) expect(await screen.findByText(label, { exact: true })).not.toBeNull();
  fireEvent.click(screen.getByText("今天", { exact: true }));
  expect(apply).not.toHaveBeenCalled();
  fireEvent.click(within(screen.getByLabelText("加入时间筛选条件")).getByRole("button", { name: "确定" }));
  const range = apply.mock.calls[0][0];
  expect(range).toHaveLength(2);
  expect(range[0]).toMatch(/Z$/);
  expect(Date.parse(range[0])).toBeLessThan(Date.parse(range[1]));
});

test("数值筛选拒绝反向范围且真实零值可以提交", () => {
  const apply = vi.fn();
  const { rerender } = render(<DownloadColumnFilter key="invalid" label="分享率" kind="number" values={["2", "1"]} onApply={apply} />);
  expect(screen.getByRole("alert").textContent).toContain("最小值不能大于最大值");
  expect((screen.getByRole("button", { name: "确定" }) as HTMLButtonElement).disabled).toBe(true);
  rerender(<DownloadColumnFilter key="zero" label="分享率" kind="number" values={["0", "0"]} onApply={apply} />);
  fireEvent.click(screen.getByRole("button", { name: "确定" }));
  expect(apply).toHaveBeenCalledWith(["0", "0"]);
});

test("表头筛选组合与清除保留其他列，全部数据列可远程排序", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  await waitFor(() => expect(requests).toHaveLength(1));
  expect(screen.queryByRole("search")).toBeNull();
  expect(screen.getByLabelText("影片筛选")).not.toBeNull();
  expect(container.querySelectorAll(".arco-table-sorter")).toHaveLength(16);
  fireEvent.click(screen.getByLabelText("资源站筛选"));
  fireEvent.change(screen.getByLabelText("资源站关键词"), { target: { value: "site" } });
  expect(requests).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "确定" }));
  await waitFor(() => expect(screen.queryByLabelText("资源站筛选条件")).toBeNull());
  await waitFor(() => expect(decodeURIComponent(requests.at(-1)!)).toContain("source_site"));
  fireEvent.click(screen.getByLabelText("大小筛选"));
  fireEvent.change(screen.getByLabelText("大小最小值"), { target: { value: "1024" } });
  fireEvent.blur(screen.getByLabelText("大小最小值"));
  fireEvent.click(within(screen.getByLabelText("大小筛选条件")).getByRole("button", { name: "确定" }));
  await waitFor(() => expect(screen.queryByLabelText("大小筛选条件")).toBeNull());
  await waitFor(() => expect(decodeURIComponent(requests.at(-1)!)).toContain("size_bytes"));
  expect(decodeURIComponent(requests.at(-1)!)).toContain("source_site");
  fireEvent.click(screen.getByLabelText("大小筛选"));
  fireEvent.click(screen.getByRole("button", { name: "清除" }));
  await waitFor(() => expect(decodeURIComponent(requests.at(-1)!)).not.toContain("size_bytes"));
  expect(decodeURIComponent(requests.at(-1)!)).toContain("source_site");
  fireEvent.click(container.querySelector(".arco-table-sorter .arco-table-sorter-icon")!);
  await waitFor(() => expect(requests.at(-1)).toContain("sort_by=code"));
});

test("影片输入与下载器多选组合提交，清除影片保留下载器", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  await waitFor(() => expect(requests).toHaveLength(1));
  fireEvent.click(screen.getByLabelText("影片筛选"));
  fireEvent.change(screen.getByLabelText("影片关键词"), { target: { value: "TEST-001" } });
  expect(requests).toHaveLength(1);
  fireEvent.keyDown(screen.getByLabelText("影片关键词"), { key: "Enter", keyCode: 13 });
  await waitFor(() => expect(screen.queryByLabelText("影片筛选条件")).toBeNull());
  fireEvent.click(screen.getByLabelText("下载器筛选"));
  expect(screen.queryByLabelText("下载器关键词")).toBeNull();
  fireEvent.click(screen.getByLabelText("qBittorrent"));
  fireEvent.click(screen.getByLabelText("Transmission"));
  fireEvent.click(screen.getByRole("button", { name: "确定" }));
  await waitFor(() => expect(JSON.parse(new URLSearchParams(requests.at(-1)!.split("?")[1]).get("column_filters")!)).toEqual({ code: ["TEST-001"], downloader: ["qbittorrent", "transmission"] }));
  await waitFor(() => expect(screen.queryByLabelText("下载器筛选条件")).toBeNull());
  fireEvent.click(screen.getByLabelText("影片筛选"));
  fireEvent.click(screen.getByRole("button", { name: "清除" }));
  await waitFor(() => expect(JSON.parse(new URLSearchParams(requests.at(-1)!.split("?")[1]).get("column_filters")!)).toEqual({ downloader: ["qbittorrent", "transmission"] }));
});

test("刷新结果不会丢失状态筛选草稿，确认后才提交", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  await waitFor(() => expect(requests).toHaveLength(1));
  fireEvent.click(screen.getByLabelText("下载状态筛选"));
  fireEvent.click(within(screen.getByLabelText("下载状态筛选条件")).getByText("排队中", { exact: true }));
  await client.invalidateQueries({ queryKey: ["downloads"] });
  await waitFor(() => expect(requests).toHaveLength(2));
  expect(requests.at(-1)).not.toContain("column_filters");
  expect((within(screen.getByLabelText("下载状态筛选条件")).getByRole("checkbox", { name: "排队中" }) as HTMLInputElement).checked).toBe(true);
  fireEvent.click(within(screen.getByLabelText("下载状态筛选条件")).getByRole("button", { name: "确定" }));
  await waitFor(() => expect(decodeURIComponent(requests.at(-1)!)).toContain('"transfer_status":["queued"]'));
});

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

test.each([["queued", "排队中"], ["stalled", "等待连接"], ["checking", "校验中"], ["metadata", "获取元数据"], ["moving", "移动中"], ["unknown", "待核实"], ["paused", "暂停"], ["stopped", "停止"], ["downloading", "下载中"], ["completed", "下载完成"], ["failed", "下载失败"]])("下载状态以下载器的 %s 为准，不显示内部已提交阶段", async (transferStatus, label) => {
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

test("筛选位于表头且保留单张表格卡片", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(<QueryClientProvider client={client}><DownloadListPage /></QueryClientProvider>);
  await waitFor(() => expect(container.querySelector(".data-table table")).not.toBeNull());
  const table = container.querySelector(".data-table table")!;
  const card = table.closest(".content-card");
  expect(card).not.toBeNull();
  expect(table?.closest(".content-card")).toBe(card);
  expect(container.querySelectorAll(".content-card")).toHaveLength(1);
  expect(container.querySelector(".download-filters")).toBeNull();
  expect(within(table.querySelector("thead")!).getByLabelText("下载状态筛选")).not.toBeNull();
});
