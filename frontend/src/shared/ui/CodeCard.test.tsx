// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { apiRequest } from "../api/client";
import type { Media, SystemSettings } from "../api/types";
import { CodeCard } from "./CodeCard";

vi.mock("../api/client", () => ({ apiRequest: vi.fn() }));
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

function renderCard(mode?: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  if (mode !== undefined) client.setQueryData(["system-settings"], settings(mode));
  const view = render(<QueryClientProvider client={client}><CodeCard media={media} /></QueryClientProvider>);
  return { client, ...view };
}

it("无图模式不渲染封面或剧照，手动打开预告也不传封面", async () => {
  const user = userEvent.setup();
  const { container } = renderCard("INVISIBLE");
  expect(container.querySelector("img")).toBeNull();
  expect(container.querySelector(".code-card-cover")).toBeNull();
  expect(screen.queryByRole("button", { name: "剧照" })).toBeNull();
  expect(screen.getByText("测试影片")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "预告" }));
  expect(screen.getByLabelText("预告播放器")).not.toHaveAttribute("poster");
});

it("图片模式保存到共享缓存后，已有卡片立即隐藏封面", async () => {
  const { client } = renderCard("VISIBLE");
  expect(screen.getByRole("img", { name: "TEST-001 封面" })).toHaveAttribute("src", media.banner_url);
  await act(async () => { client.setQueryData(["system-settings"], settings("INVISIBLE")); });
  await waitFor(() => expect(screen.queryByRole("img", { name: "TEST-001 封面" })).toBeNull());
  await act(async () => { client.setQueryData(["system-settings"], settings("VISIBLE")); });
  expect(await screen.findByRole("img", { name: "TEST-001 封面" })).toBeInTheDocument();
});

it("首次加载设置之前不加载图片，加载无图设置后保持隐藏", async () => {
  let resolve!: (value: SystemSettings) => void;
  vi.mocked(apiRequest).mockReturnValue(new Promise<SystemSettings>((done) => { resolve = done; }));
  const { container, client } = renderCard();
  expect(container.querySelector("img")).toBeNull();
  await act(async () => { resolve(settings("INVISIBLE")); });
  await waitFor(() => expect(client.getQueryData(["system-settings"])).toEqual(settings("INVISIBLE")));
  expect(container.querySelector("img")).toBeNull();
});

it("设置读取失败时不露出图片", async () => {
  vi.mocked(apiRequest).mockRejectedValue(new Error("设置读取失败"));
  const { container, client } = renderCard();
  await waitFor(() => expect(client.getQueryState(["system-settings"])?.status).toBe("error"));
  expect(container.querySelector("img")).toBeNull();
});

it("模糊模式使用模糊封面，未知模式不加载图片", async () => {
  const { client } = renderCard("BLUR");
  expect(screen.getByRole("img", { name: "TEST-001 封面" })).toHaveClass("code-card-image-blurred");
  await act(async () => { client.setQueryData(["system-settings"], settings("unknown")); });
  await waitFor(() => expect(screen.queryByRole("img", { name: "TEST-001 封面" })).toBeNull());
});
