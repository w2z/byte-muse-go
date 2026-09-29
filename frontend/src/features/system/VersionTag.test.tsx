// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { apiRequest } from "../../shared/api/client";
import type { SystemVersion } from "../../shared/api/types";
import { VersionTag } from "./VersionTag";

vi.mock("../../shared/api/client", () => ({ apiRequest: vi.fn() }));

afterEach(() => {
  cleanup();
  vi.mocked(apiRequest).mockReset();
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
});

it("发现新版本时使用 orangered 并链接发布仓库", async () => {
  vi.mocked(apiRequest).mockResolvedValue({ ...base, latest: "0.1.22", has_update: true, release_url: "https://github.com/w2z/byte-muse-go" });
  const { container } = renderTag();

  expect(await screen.findByText("v0.1.21")).toBeInTheDocument();
  expect(container.querySelector(".arco-tag")?.className).toContain("arco-tag-orangered");
  expect(container.querySelector(".header-version-link")).toHaveAttribute("href", "https://github.com/w2z/byte-muse-go");
});

it("检查更新失败时仍显示当前版本，并保留占位容器", async () => {
  vi.mocked(apiRequest).mockResolvedValue({ ...base, check_error: "无法访问 GitHub 发布仓库，请检查服务所在网络的连通性" });
  const { container } = renderTag();

  expect(await screen.findByText("v0.1.21")).toBeInTheDocument();
  expect(container.querySelector(".header-version")).not.toBeNull();
});

it("接口失败时不渲染标签，容器仍占位", async () => {
  vi.mocked(apiRequest).mockRejectedValue(new Error("请求失败"));
  const { container } = renderTag();

  await expect(apiRequest).toHaveBeenCalledWith("/system/version");
  expect(container.querySelector(".header-version")).not.toBeNull();
  expect(container.querySelector(".arco-tag")).toBeNull();
});
