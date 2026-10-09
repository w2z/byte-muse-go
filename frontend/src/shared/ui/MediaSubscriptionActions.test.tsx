// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { apiRequest } from "../api/client";
import type { Media } from "../api/types";
import { MediaSubscriptionActions } from "./MediaSubscriptionActions";
vi.mock("../api/client", async (original) => ({ ...(await original<typeof import("../api/client")>()), apiRequest: vi.fn() }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });
const media: Media = { id: "a", code: "TEST", title: "测试", translated_title: null, video_type: null, subscription_status: "none", library_status: "unknown", created_at: "", updated_at: "" };
function show(item = media) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(["system-settings"], { values: { DEFAULT_FILTER: JSON.stringify({ min_size: "1" }) } });
  return render(<QueryClientProvider client={client}><MediaSubscriptionActions media={item} /></QueryClientProvider>);
}
test("每次新建读取最新默认规则，兼容字符串体积并完整提交", async () => {
  let min = "1024";
  vi.mocked(apiRequest).mockImplementation(async (path) => path === "/system/settings" ? { values: { DEFAULT_FILTER: JSON.stringify({ min_size: min, max_size: 8192, only_chinese: true, only_free: "true", custom_rule: "keep" }) } } as never : {} as never);
  show();
  fireEvent.click(screen.getByRole("button", { name: "订阅" }));
  await screen.findByRole("dialog");
  expect(screen.getByLabelText("最小体积 MB")).toHaveValue("1024");
  expect(screen.getByLabelText("最大体积 MB")).toHaveValue("8192");
  expect(screen.getByRole("switch", { name: "仅中文" })).toBeChecked();
  expect(screen.getByRole("switch", { name: "仅免费" })).toBeChecked();
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.some(([path]) => path === "/subscriptions")).toBe(true));
  const write = vi.mocked(apiRequest).mock.calls.find(([path]) => path === "/subscriptions")!;
  expect(JSON.parse(String(write[1]?.body)).filter).toMatchObject({ min_size: 1024, max_size: 8192, only_chinese: true, only_free: true, custom_rule: "keep" });
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  min = "2048";
  fireEvent.click(screen.getByRole("button", { name: "订阅" }));
  await screen.findByRole("dialog");
  expect(screen.getByLabelText("最小体积 MB")).toHaveValue("2048");
});
test("读取失败不打开空规则弹窗或提交订阅", async () => {
  vi.mocked(apiRequest).mockRejectedValue(new Error("设置读取失败"));
  show(); fireEvent.click(screen.getByRole("button", { name: "订阅" }));
  expect(await screen.findByText("设置读取失败")).toBeInTheDocument();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(vi.mocked(apiRequest).mock.calls.every(([path]) => path === "/system/settings")).toBe(true);
});
test("编辑已保存规则不被全局设置覆盖，保留零值与额外字段", async () => {
  vi.mocked(apiRequest).mockResolvedValue({});
  show({ ...media, display_status: "subscribed", active_subscription: { id: "s", media_id: "a", status: "active", mode: "preload", filter: { min_size: "0", max_size: "500", only_chinese: false, custom_rule: "saved" }, created_at: "", updated_at: "", version: 1 } });
  fireEvent.click(screen.getByRole("button", { name: "编辑" }));
  await screen.findByRole("dialog");
  expect(screen.getByLabelText("最小体积 MB")).toHaveValue("0");
  expect(screen.getByLabelText("最大体积 MB")).toHaveValue("500");
  expect(apiRequest).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  await waitFor(() => expect(apiRequest).toHaveBeenCalled());
  expect(JSON.parse(String(vi.mocked(apiRequest).mock.calls[0][1]?.body))).toMatchObject({ mode: "preload", filter: { min_size: 0, max_size: 500, custom_rule: "saved" } });
});

test("编辑空规则订阅时展示当前继承的全局默认值", async () => {
  vi.mocked(apiRequest).mockResolvedValue({ values: { DEFAULT_FILTER: JSON.stringify({ min_size: "128", max_size: "", exclude_uc: true }) } });
  show({ ...media, display_status: "subscribed", active_subscription: { id: "s", media_id: "a", status: "active", mode: "preload", filter: {}, created_at: "", updated_at: "", version: 1 } });
  fireEvent.click(screen.getByRole("button", { name: "编辑" }));
  await screen.findByRole("dialog");
  expect(screen.getByLabelText("最小体积 MB")).toHaveValue("128");
  expect(screen.getByLabelText("最大体积 MB")).toHaveValue("");
  expect(screen.getByRole("radio", { name: "预下载模式" })).toBeChecked();
});
test.each(["", "{}"])("未配置默认规则时保留空体积和关闭开关 (%s)", async (raw) => {
  vi.mocked(apiRequest).mockResolvedValue({ values: { DEFAULT_FILTER: raw } });
  show(); fireEvent.click(screen.getByRole("button", { name: "订阅" }));
  await screen.findByRole("dialog");
  expect(screen.getByLabelText("最小体积 MB")).toHaveValue("");
  expect(screen.getByRole("switch", { name: "仅中文" })).not.toBeChecked();
});
test.each(["{broken", "[]", "null"])("无效默认配置不静默以空规则打开 (%s)", async (raw) => {
  vi.mocked(apiRequest).mockResolvedValue({ values: { DEFAULT_FILTER: raw } });
  show(); fireEvent.click(screen.getByRole("button", { name: "订阅" }));
  expect(await screen.findByText("默认过滤规则格式无效，请检查设置")).toBeInTheDocument();
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("读取继承设置期间目标订阅消失时丢弃迟到的编辑弹窗", async () => {
  let resolve!: (value: unknown) => void;
  vi.mocked(apiRequest).mockImplementation(() => new Promise((done) => { resolve = done; }));
  const client = new QueryClient();
  const subscribed: Media = { ...media, display_status: "subscribed", active_subscription: { id: "s", media_id: "a", status: "active", mode: "strict", filter: {}, created_at: "", updated_at: "", version: 1 } };
  const view = render(<QueryClientProvider client={client}><MediaSubscriptionActions media={subscribed} /></QueryClientProvider>);
  fireEvent.click(screen.getByRole("button", { name: "编辑" }));
  await waitFor(() => expect(apiRequest).toHaveBeenCalled());
  expect(screen.getByRole("button", { name: "取消订阅" })).toBeDisabled();
  view.rerender(<QueryClientProvider client={client}><MediaSubscriptionActions media={media} /></QueryClientProvider>);
  await act(async () => resolve({ values: { DEFAULT_FILTER: "{}" } }));
  expect(screen.queryByRole("dialog")).toBeNull();
});
