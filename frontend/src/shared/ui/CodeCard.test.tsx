// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ComponentProps } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { apiRequest, coverCacheURL } from "../api/client";
import type { Media, SystemSettings } from "../api/types";
import { CodeCard } from "./CodeCard";
import { MemoryRouter } from "react-router-dom";

it("演员标签保留原名，仅数据库匹配项提供蓝色搜索链接", () => {
  const client = new QueryClient();
  client.setQueryData(["system-settings"], settings("INVISIBLE"));
  render(<MemoryRouter><QueryClientProvider client={client}><CodeCard media={{ ...media, actors: [
    { name: "演员别名", actor_name: "演员甲" }, { name: "未收录演员", actor_name: null },
  ] }} /></QueryClientProvider></MemoryRouter>);
  expect(screen.getByRole("link", { name: "搜索演员 演员别名 的影片" })).toHaveAttribute("href", "/search?actor=%E6%BC%94%E5%91%98%E7%94%B2");
  expect(screen.getByText("演员别名").closest(".arco-tag")).toHaveClass("arco-tag-arcoblue");
  expect(screen.getByText("未收录演员").closest("a")).toBeNull();
});

// jsdom 未实现媒体查询，提供 Arco 响应式描述列表所需的浏览器接口。
Object.defineProperty(window, "matchMedia", { writable: true, value: (query: string) => ({ matches: false, media: query, addListener() {}, removeListener() {} }) });

// 只替换网络请求：封面地址拼接沿用真实实现，断言的就是页面实际请求的地址。
vi.mock("../api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/client")>()),
  apiRequest: vi.fn(),
}));
// 播放器会创建浏览器媒体实例；保留封面入参用于检查图片模式边界。
vi.mock("./MediaPlayer", () => ({ MediaPlayer: ({ poster }: { poster?: string }) => <video aria-label="预告播放器" poster={poster} /> }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

const media: Media = {
  id: "film-1", code: "TEST-001", title: "测试影片", translated_title: null, video_type: null,
  banner_url: "https://example.test/cover.jpg", poster_url: "https://example.test/poster.jpg",
  still_photos: ["https://example.test/still.jpg"], preview_url: "https://example.test/trailer.mp4",
  subscription_status: "none", library_status: "unknown", created_at: "", updated_at: "",
};
const settings = (mode: string): SystemSettings => ({ database_driver: "sqlite", values: { IMAGE_MODE: mode }, configured: {} });

/** 主封面统一走服务端缓存入口：番号作为文件名，源站地址作为回退与下载来源。 */
const coverSrc = "/api/v1/covers/TEST-001?source=" + encodeURIComponent("https://example.test/cover.jpg");

function renderCard(mode?: string, props: Partial<ComponentProps<typeof CodeCard>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  if (mode !== undefined) client.setQueryData(["system-settings"], settings(mode));
  const view = render(<QueryClientProvider client={client}><CodeCard media={media} {...props} /></QueryClientProvider>);
  return { client, ...view };
}

it("无图模式用占位图替代封面且不加载真实图片，剧照入口隐藏，手动打开预告也不传封面", async () => {
  const user = userEvent.setup();
  const { container } = renderCard("INVISIBLE");
  expect(container.querySelector("img")).toBeNull();
  expect(screen.getByRole("img", { name: "TEST-001 封面占位图" })).toBeInTheDocument();
  expect(container.querySelector(".code-card-cover")).not.toBeNull();
  expect(screen.queryByRole("button", { name: "剧照" })).toBeNull();
  expect(screen.getByText("测试影片")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "预告" }));
  expect(screen.getByLabelText("预告播放器")).not.toHaveAttribute("poster");
});

it("支持隐藏整个操作区和状态，同时保留影片信息与复制番号", () => {
  const { container } = renderCard("VISIBLE", { hideActions: true, hideStatus: true, actions: <button>订阅</button> });
  expect(screen.getByText("测试影片")).toBeInTheDocument();
  expect(screen.getByRole("img", { name: "TEST-001 封面" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "复制番号 TEST-001" })).toBeInTheDocument();
  expect(screen.queryByText("未订阅")).toBeNull();
  expect(container.querySelector(".code-card-actions")).toBeNull();
  expect(screen.queryByRole("button", { name: "订阅" })).toBeNull();
  expect(screen.queryByRole("button", { name: "预告" })).toBeNull();
});

it("默认保留状态及操作，隐藏状态不会隐藏操作", () => {
  const { client, rerender } = renderCard("VISIBLE", { actions: <button>订阅</button> });
  expect(screen.getByText("未订阅")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "订阅" })).toBeInTheDocument();
  rerender(<QueryClientProvider client={client}><CodeCard media={media} hideStatus actions={<button>订阅</button>} /></QueryClientProvider>);
  expect(screen.queryByText("未订阅")).toBeNull();
  expect(screen.getByRole("button", { name: "订阅" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "预告" })).toBeInTheDocument();
});

it("图片模式保存到共享缓存后，已有卡片立即切换为占位图", async () => {
  const { client } = renderCard("VISIBLE");
  expect(screen.getByRole("img", { name: "TEST-001 封面" })).toHaveAttribute("src", coverSrc);
  await act(async () => { client.setQueryData(["system-settings"], settings("INVISIBLE")); });
  await waitFor(() => expect(screen.queryByRole("img", { name: "TEST-001 封面" })).toBeNull());
  expect(screen.getByRole("img", { name: "TEST-001 封面占位图" })).toBeInTheDocument();
  await act(async () => { client.setQueryData(["system-settings"], settings("VISIBLE")); });
  expect(await screen.findByRole("img", { name: "TEST-001 封面" })).toBeInTheDocument();
});

it("首次加载设置之前不加载图片，加载无图设置后仍不加载真实图片", async () => {
  let resolve!: (value: SystemSettings) => void;
  vi.mocked(apiRequest).mockReturnValue(new Promise<SystemSettings>((done) => { resolve = done; }));
  const { container, client } = renderCard();
  expect(container.querySelector("img")).toBeNull();
  expect(screen.getByRole("img", { name: "TEST-001 封面占位图" })).toBeInTheDocument();
  await act(async () => { resolve(settings("INVISIBLE")); });
  await waitFor(() => expect(client.getQueryData(["system-settings"])).toEqual(settings("INVISIBLE")));
  expect(container.querySelector("img")).toBeNull();
  expect(screen.getByRole("img", { name: "TEST-001 封面占位图" })).toBeInTheDocument();
});

it("设置读取失败时不露出真实图片，改用占位图", async () => {
  vi.mocked(apiRequest).mockRejectedValue(new Error("设置读取失败"));
  const { container, client } = renderCard();
  await waitFor(() => expect(client.getQueryState(["system-settings"])?.status).toBe("error"));
  expect(container.querySelector("img")).toBeNull();
  expect(screen.getByRole("img", { name: "TEST-001 封面占位图" })).toBeInTheDocument();
});

it("模糊模式使用模糊封面，未知模式不加载图片", async () => {
  const { client } = renderCard("BLUR");
  expect(screen.getByRole("img", { name: "TEST-001 封面" })).toHaveClass("code-card-image-blurred");
  await act(async () => { client.setQueryData(["system-settings"], settings("unknown")); });
  await waitFor(() => expect(screen.queryByRole("img", { name: "TEST-001 封面" })).toBeNull());
  expect(screen.getByRole("img", { name: "TEST-001 封面占位图" })).toBeInTheDocument();
});

it("完整资料分组显示真实零值与否值，无图模式不加载详细图片", () => {
  const { container } = renderCard("INVISIBLE", { variant: "detail", media: { ...media, details: {
    actors: ["演员甲"], tags: ["标签甲"], producer: "制作商", publisher: "发行商", series: null,
    release_code: null, plot: null, director: null, rating: 0, want_count: 0, translation_engine: null, mosaic: false, censored: false, resolution: null,
  } } });
  expect(screen.getByText("基本信息")).toBeInTheDocument();
  expect(screen.getByText("制作与发行")).toBeInTheDocument();
  expect(screen.getByText("评分与规格")).toBeInTheDocument();
  expect(screen.getByText("演员甲")).toBeInTheDocument();
  expect(screen.getByText("标签甲")).toBeInTheDocument();
  expect(screen.getAllByText("0")).toHaveLength(2);
  expect(screen.getAllByText("否")).toHaveLength(2);
  expect(screen.getAllByRole("img", { name: "影片资料占位图" })).toHaveLength(2);
  expect(container.querySelector("img")).toBeNull();
});

it("详情顶部显示完整封面，基本信息提供复制按钮", () => {
  const { container } = renderCard("VISIBLE", { variant: "detail" });
  expect(screen.getByRole("img", { name: "TEST-001 封面" })).toHaveAttribute("src", coverSrc);
  expect(screen.getByRole("img", { name: "TEST-001 封面" })).toHaveAttribute("width", "100%");
  expect(container.querySelector(".code-card")).toBeNull();
  expect(screen.getByRole("button", { name: "复制番号 TEST-001" })).toBeInTheDocument();
  expect(screen.getByText("基本信息")).toBeInTheDocument();
  expect(screen.getByRole("img", { name: "TEST-001 剧照 1" })).toHaveAttribute("src", media.still_photos![0]);
});

it("详情点击番号文字和末尾图标都复制当前番号，失败给出提示", async () => {
  const user = userEvent.setup();
  const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
  renderCard("INVISIBLE", { variant: "detail" });
  await user.click(screen.getByRole("button", { name: "TEST-001" }));
  await user.click(screen.getByRole("button", { name: "复制番号 TEST-001" }));
  expect(writeText).toHaveBeenCalledTimes(2);
  expect(writeText).toHaveBeenLastCalledWith("TEST-001");
  expect((await screen.findAllByText("番号已复制")).length).toBeGreaterThan(0);
  writeText.mockRejectedValueOnce(new Error("clipboard denied"));
  await user.click(screen.getByRole("button", { name: "复制番号 TEST-001" }));
  expect(await screen.findByText("番号复制失败")).toBeInTheDocument();
});

it("内网 HTTP 没有 Clipboard API 时仍能复制并恢复焦点", async () => {
  const user = userEvent.setup();
  const clipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  const execCommand = Object.getOwnPropertyDescriptor(document, "execCommand");
  const copy = vi.fn(() => {
    expect((document.activeElement as HTMLTextAreaElement).value).toBe("TEST-001");
    return true;
  });
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
  Object.defineProperty(document, "execCommand", { configurable: true, value: copy });
  try {
    const { container } = renderCard("INVISIBLE", { variant: "detail" });
    const button = screen.getByRole("button", { name: "复制番号 TEST-001" });
    await user.click(button);
    expect(copy).toHaveBeenCalledWith("copy");
    expect(container.querySelector("textarea")).toBeNull();
    expect(button).toHaveFocus();
    expect(await screen.findByText("番号已复制")).toBeInTheDocument();
  } finally {
    if (clipboard) Object.defineProperty(navigator, "clipboard", clipboard);
    if (execCommand) Object.defineProperty(document, "execCommand", execCommand);
    else Reflect.deleteProperty(document, "execCommand");
  }
});

it("没有剧照的详情不显示剧照区域", () => {
  renderCard("VISIBLE", { variant: "detail", media: { ...media, still_photos: [] } });
  expect(screen.queryByText("剧照")).toBeNull();
});

it("详情从选中的剧照打开预览，底部缩略图可切换且无图设置立即关闭", async () => {
  const user = userEvent.setup();
  const { client } = renderCard("VISIBLE", { variant: "detail", media: { ...media, still_photos: ["https://example.test/one.jpg", "https://example.test/two.jpg"] } });
  const second = screen.getByRole("img", { name: "TEST-001 剧照 2" });
  fireEvent.load(second);
  await user.click(second);
  expect(await screen.findByRole("button", { name: "查看第 2 张剧照" })).toHaveAttribute("aria-pressed", "true");
  await user.click(screen.getByRole("button", { name: "查看第 1 张剧照" }));
  expect(screen.getByRole("button", { name: "查看第 1 张剧照" })).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByRole("button", { name: "查看第 2 张剧照" })).toHaveAttribute("aria-pressed", "false");
  await act(async () => { client.setQueryData(["system-settings"], settings("INVISIBLE")); });
  await waitFor(() => expect(document.querySelector("img")).toBeNull());
});

it("缺少封面地址时同样显示占位图", () => {
  const { container } = renderCard("VISIBLE", { media: { ...media, banner_url: null, poster_url: null } });
  expect(container.querySelector("img")).toBeNull();
  expect(screen.getByRole("img", { name: "TEST-001 封面占位图" })).toBeInTheDocument();
});

it("详情模式无图时顶部封面与资料图片都显示占位图", () => {
  const { container } = renderCard("INVISIBLE", { variant: "detail" });
  expect(container.querySelector("img")).toBeNull();
  expect(screen.getByRole("img", { name: "TEST-001 封面占位图" })).toBeInTheDocument();
  expect(screen.getAllByRole("img", { name: "影片资料占位图" })).toHaveLength(2);
});

/** 「外网访问地址」已配置时页面封面改用绝对缓存地址，与微信封面推送取同一张图。 */
it("配置外网访问地址后封面改用绝对缓存地址", () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(["system-settings"], { ...settings("VISIBLE"), values: { IMAGE_MODE: "VISIBLE", EXTERNAL_DOMAIN: "https://muse.example.com/" } });
  render(<QueryClientProvider client={client}><CodeCard media={media} /></QueryClientProvider>);
  expect(screen.getByRole("img", { name: "TEST-001 封面" })).toHaveAttribute("src", "https://muse.example.com" + coverSrc);
});

it.each(["i0", "i1", "i3", "i4"])("%s.wp.com 图片代理直接拼接源站路径", (host) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(["system-settings"], { ...settings("VISIBLE"), values: { IMAGE_MODE: "VISIBLE", EXTERNAL_DOMAIN: `https://${host}.wp.com/` } });
  render(<QueryClientProvider client={client}><CodeCard media={{ ...media, code: "START-640", banner_url: "https://c0.jdbstatic.com/covers/p9/P98NVa.jpg" }} /></QueryClientProvider>);
  expect(screen.getByRole("img", { name: "START-640 封面" })).toHaveAttribute("src", `https://${host}.wp.com/c0.jdbstatic.com/covers/p9/P98NVa.jpg`);
});

it("图片代理保留原图编码与查询参数，普通域名继续使用缓存接口", () => {
  const source = "http://img.example/a%20b.jpg?width=640&v=2";
  expect(coverCacheURL("TEST-001", source, " https://i1.wp.com "))
    .toBe("https://i1.wp.com/img.example/a%20b.jpg?width=640&v=2");
  expect(coverCacheURL("TEST-001", source, "https://i0.wp.com.example.test/"))
    .toBe("https://i0.wp.com.example.test/api/v1/covers/TEST-001?source=" + encodeURIComponent(source));
  expect(coverCacheURL("TEST-001", null, "https://i0.wp.com/")).toBeUndefined();
});
