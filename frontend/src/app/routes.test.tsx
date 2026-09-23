import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { routes } from "./routes";

function renderAt(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

describe("应用路由", () => {
  it("登录页不套用管理布局", async () => {
    renderAt("/login");
    expect(await screen.findByRole("heading", { name: "登录 ByteMuse" })).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "主导航" })).not.toBeInTheDocument();
  });

  it.each([
    ["/dashboard", "仪表盘"],
    ["/films", "影片"],
    ["/subscriptions", "订阅"],
    ["/subscribe", "订阅"],
    ["/downloads", "下载任务"],
    ["/status", "系统状态"],
    ["/settings", "设置"],
    ["/config", "设置"],
  ])("%s 渲染 %s 页面", async (path, title) => {
    renderAt(path);
    expect(await screen.findByRole("heading", { name: title })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "主导航" })).toBeInTheDocument();
  });

  it("完整菜单入口显示能力边界", async () => {
    renderAt("/actor");
    expect(await screen.findByRole("heading", { name: "演员" })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "主导航" })).toBeInTheDocument();
    for (const label of ["看板", "订阅", "演员", "标签", "上新", "推荐", "榜单", "厂牌", "搜索", "账户", "设置", "任务", "日志", "注意"]) {
      expect(screen.getByRole("menuitem", { name: label })).toBeInTheDocument();
    }
  });

  it("侧栏使用 Arco 菜单文字，不展示旧版编号标签", async () => {
    renderAt("/dashboard");
    expect(await screen.findByRole("heading", { name: "仪表盘" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "看板" })).toBeInTheDocument();
    for (const label of ["看板", "订阅", "演员", "设置", "系统状态"]) {
      expect(screen.getByRole("menuitem", { name: label }).querySelector("svg")).toBeInTheDocument();
    }
    expect(screen.queryByText("01")).not.toBeInTheDocument();
  });

  it("管理布局收到未授权事件后跳转登录页", async () => {
    renderAt("/dashboard");
    expect(await screen.findByRole("heading", { name: "仪表盘" })).toBeInTheDocument();
    window.dispatchEvent(new Event("bytemuse:unauthorized"));
    expect(await screen.findByRole("heading", { name: "登录 ByteMuse" })).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "主导航" })).not.toBeInTheDocument();
  });
});
