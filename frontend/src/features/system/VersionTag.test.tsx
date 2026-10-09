// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { apiRequest } from "../../shared/api/client";
import type { SystemVersion } from "../../shared/api/types";
import { VersionTag } from "./VersionTag";

vi.mock("../../shared/api/client", () => ({ apiRequest: vi.fn() }));
const UPGRADE_LABELS = ["开始下载文件", "开始解压文件", "正在升级", "正在重启"];

beforeEach(() => vi.stubEnv("VITE_APP_VERSION", "0.1.21"));

afterEach(() => {
  cleanup();
  vi.mocked(apiRequest).mockReset();
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

/** 渲染独立查询客户端下的版本标签，避免用例间缓存串扰。 */
function renderTag() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <VersionTag />
    </QueryClientProvider>,
  );
}

const base: SystemVersion = { current: "0.1.21", latest: "0.1.21", has_update: false, release_url: "", checked_at: "2026-09-29T12:00:00Z", check_error: "" };

it("有更新时在状态位置按编号展示跨版本说明，按纯文本渲染", async () => {
  vi.mocked(apiRequest).mockImplementation((path) => Promise.resolve(path === "/system/upgrade"
    ? { enabled: true, phase: "idle", target: "", error: "" }
    : { ...base, latest: "0.1.23", has_update: true, changes: [
      { version: "0.1.22", message: "新增升级进度" },
      { version: "0.1.23", message: "<script>修复版本检查</script>" },
    ] }));
  renderTag();
  fireEvent.click(screen.getByRole("button", { name: "v0.1.21" }));
  const list = await screen.findByRole("list", { name: "更新内容" });
  expect(list.tagName).toBe("OL");
  expect(list.querySelectorAll("li")).toHaveLength(2);
  expect(list.textContent).toContain("新增升级进度");
  expect(list.textContent).toContain("<script>修复版本检查</script>");
  expect(list.querySelector("script")).toBeNull();
  expect(screen.queryByText("当前已是最新版本")).toBeNull();
  expect(screen.queryByText("新版本 0.1.23 已发布")).toBeNull();
});

it("点击版本打开弹窗，异步检查后切换升级操作，暂不升级关闭弹窗", async () => {
  let resolve!: (value: SystemVersion) => void;
  vi.mocked(apiRequest).mockImplementation((path) => path === "/system/upgrade" ? Promise.resolve({ enabled:true, phase:"idle", target:"", error:"" }) : path.includes("refresh") ? new Promise<SystemVersion>((done) => { resolve = done; }) : Promise.resolve(base));
  renderTag();
  fireEvent.click(screen.getByRole("button", { name: "v0.1.21" }));
  expect(await screen.findByText("当前已是最新版本")).toBeInTheDocument();
  const check = screen.getByRole("button", { name: "立即检查更新" });
  fireEvent.click(check);
  await waitFor(() => expect(check).toHaveClass("arco-btn-loading"));
  expect(apiRequest).toHaveBeenLastCalledWith("/system/version?refresh=true");
  await act(async () => resolve({ ...base, latest: "0.1.22", has_update: true }));
  expect(await screen.findByRole("button", { name: "立即升级" })).toBeInTheDocument();
  expect(screen.getByText("升级至 0.1.22")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "暂不升级" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

it("手动检查失败显示原因并允许重试，不显示已是最新", async () => {
  vi.mocked(apiRequest).mockImplementation((path) => Promise.resolve(path === "/system/upgrade" ? { enabled:true, phase:"idle", target:"", error:"" } : path.includes("refresh") ? { ...base, latest: "", check_error: "检查更新超时" } : base));
  renderTag();
  fireEvent.click(screen.getByRole("button", { name: "v0.1.21" }));
  await screen.findByText("当前已是最新版本");
  fireEvent.click(screen.getByRole("button", { name: "立即检查更新" }));
  expect(await screen.findByText("检查更新超时")).toBeInTheDocument();
  expect(screen.queryByText("当前已是最新版本")).toBeNull();
  expect(screen.getByRole("button", { name: "立即检查更新" })).not.toBeDisabled();
});

it("显示带 v 前缀的当前版本，无更新时保持默认背景", async () => {
  vi.mocked(apiRequest).mockResolvedValue(base);
  const { container } = renderTag();

  expect(await screen.findByText("v0.1.21")).toBeInTheDocument();
  expect(apiRequest).toHaveBeenCalledWith("/system/version");
  const tag = container.querySelector(".arco-tag");
  expect(tag).not.toBeNull();
  expect(tag?.className).not.toContain("orangered");
  expect(tag?.className).not.toContain("checkable");
  expect(container.querySelector(".arco-icon-github")).not.toBeNull();
  expect(container.querySelector(".header-version-link")).toBeNull();
  expect(container.querySelector(".arco-badge-dot")).toBeNull();
});

it("发现新版本时只显示颜色和红点，不提供链接或文字提示", async () => {
  vi.mocked(apiRequest).mockResolvedValue({ ...base, latest: "0.1.22", has_update: true, release_url: "https://github.com/w2z/byte-muse-go" });
  const { container } = renderTag();

  expect(await screen.findByText("v0.1.21")).toBeInTheDocument();
  await waitFor(() => expect(container.querySelector(".arco-tag")?.className).toContain("arco-tag-orangered"));
  expect(screen.queryByRole("link")).toBeNull();
  expect(container.querySelector(".arco-badge-dot")).not.toBeNull();
});

it("检查更新失败时仍显示当前版本，并保留占位容器", async () => {
  vi.mocked(apiRequest).mockResolvedValue({ ...base, check_error: "无法访问 GitHub 发布仓库，请检查服务所在网络的连通性" });
  const { container } = renderTag();

  expect(await screen.findByText("v0.1.21")).toBeInTheDocument();
  expect(container.querySelector(".header-version")).not.toBeNull();
});

it("接口失败时仍显示本地版本，且不误报已是最新", async () => {
  vi.mocked(apiRequest).mockRejectedValue(new Error("请求失败"));
  const { container } = renderTag();

  expect(screen.getByText("v0.1.21")).toBeInTheDocument();
  expect(container.querySelector(".header-version")).not.toBeNull();
  expect(container.querySelector(".arco-badge-dot")).toBeNull();
  expect(screen.queryByText(/已是最新版本/)).toBeNull();
});

it("更新请求未返回时立即显示本地版本，返回后再显示更新角标", async () => {
  let resolve!: (value: SystemVersion) => void;
  vi.mocked(apiRequest).mockReturnValue(new Promise<SystemVersion>((done) => { resolve = done; }));
  const { container } = renderTag();

  expect(screen.getByText("v0.1.21")).toBeInTheDocument();
  expect(container.querySelector(".arco-badge-dot")).toBeNull();
  expect(screen.queryByText(/已是最新版本/)).toBeNull();

  await act(async () => resolve({ ...base, latest: "0.1.22", has_update: true, release_url: "https://github.com/w2z/byte-muse-go" }));
  await waitFor(() => expect(container.querySelector(".arco-badge-dot")).not.toBeNull());
  expect(screen.getByText("v0.1.21")).toBeInTheDocument();
  expect(screen.queryByRole("link")).toBeNull();
});

it.each([false, true])("历史升级成功后不残留步骤，有更新=%s 时恢复版本信息", async (hasUpdate) => {
  vi.mocked(apiRequest).mockImplementation((path) => Promise.resolve(path === "/system/upgrade"
    ? { enabled: true, phase: "success", completed_steps: 4, target: base.current, error: "" }
    : { ...base, latest: hasUpdate ? "0.1.22" : base.current, has_update: hasUpdate, changes: hasUpdate ? [{ version: "0.1.22", message: "修复版本检查" }] : [] }));
  renderTag();
  fireEvent.click(screen.getByRole("button", { name: "v0.1.21" }));
  expect(await screen.findByText(hasUpdate ? "修复版本检查" : "当前已是最新版本")).toBeInTheDocument();
  await waitFor(() => expect(apiRequest).toHaveBeenCalledWith("/system/upgrade"));
  expect(screen.queryByRole("group", { name: "升级步骤" })).toBeNull();
  expect(screen.queryByRole("progressbar", { name: "升级进度" })).toBeNull();
  expect(screen.getByRole("button", { name: hasUpdate ? "立即升级" : "立即检查更新" })).toBeInTheDocument();
});

it("升级失败后收起步骤并保留失败原因", async () => {
  vi.mocked(apiRequest).mockImplementation((path) => Promise.resolve(path === "/system/upgrade"
    ? { enabled: true, phase: "failed", completed_steps: 2, target: "0.1.22", error: "安装失败" }
    : { ...base, latest: "0.1.22", has_update: true }));
  renderTag();
  fireEvent.click(screen.getByRole("button", { name: "v0.1.21" }));
  expect(await screen.findByText("安装失败")).toBeInTheDocument();
  expect(screen.queryByRole("group", { name: "升级步骤" })).toBeNull();
  expect(screen.queryByRole("progressbar", { name: "升级进度" })).toBeNull();
});

it("升级依次展示进度，断连不误判完成，目标就绪后刷新", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const reload = vi.fn();
  vi.stubGlobal("location", { ...window.location, reload });
  const state = { enabled: true, phase: "idle", completed_steps: 0, target: "0.1.22", error: "" };
  vi.mocked(apiRequest).mockImplementation((path, init) => Promise.resolve(path !== "/system/upgrade"
    ? { ...base, latest: "0.1.22", has_update: true } : { ...state, phase: init?.method === "POST" ? "downloading" : "idle" }));
  render(<QueryClientProvider client={client}><VersionTag /></QueryClientProvider>);
  fireEvent.click(screen.getByRole("button", { name: "v0.1.21" }));
  fireEvent.click(await screen.findByRole("button", { name: "立即升级" }));
  await waitFor(() => expect(screen.getByRole("progressbar", { name: "升级进度" })).toHaveAttribute("aria-valuenow", "0"));
  const steps = screen.getByRole("group", { name: "升级步骤" });
  expect(steps.querySelector(".arco-steps")).not.toBeNull();
  expect(steps.compareDocumentPosition(screen.getByRole("progressbar", { name: "升级进度" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  for (const [index, phase] of ["extracting", "installing", "restarting"].entries()) {
    await act(async () => { client.setQueryData(["system", "upgrade"], { ...state, phase, completed_steps: index + 1 }); });
    await waitFor(() => expect(screen.getByRole("progressbar", { name: "升级进度" })).toHaveAttribute("aria-valuenow", String((index + 1) * 25)));
    expect(document.querySelectorAll(".version-upgrade-steps .is-complete")).toHaveLength(index + 1);
    expect(screen.getByText(UPGRADE_LABELS[index + 1])).toHaveAttribute("aria-current", "step");
    expect(reload).not.toHaveBeenCalled();
  }
  vi.mocked(apiRequest).mockRejectedValueOnce(new Error("服务断开"));
  await act(async () => { await client.refetchQueries({ queryKey: ["system", "upgrade"] }); });
  expect(screen.getByRole("progressbar", { name: "升级进度" })).toHaveAttribute("aria-valuenow", "75");
  expect(reload).not.toHaveBeenCalled();
  await act(async () => { client.setQueryData(["system", "upgrade"], { ...state, phase: "success", completed_steps: 4 }); });
  await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
  expect(screen.queryByRole("group", { name: "升级步骤" })).toBeNull();
  expect(screen.queryByRole("progressbar", { name: "升级进度" })).toBeNull();
  vi.unstubAllGlobals();
  client.clear();
});
