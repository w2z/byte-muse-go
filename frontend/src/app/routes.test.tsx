// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { useSession } from "../shared/auth/session";
import { routes } from "./routes";

// 隔离页面数据请求，保留真实路由和会话状态，验证两个方向的访问控制。
vi.mock("./AppLayout", () => ({ AppLayout: () => <Outlet /> }));
vi.mock("../features/auth/LoginPage", () => ({ LoginPage: () => <h1>登录表单</h1> }));
vi.mock("../features/dashboard/DashboardPage", () => ({ DashboardPage: () => <h1>后台看板</h1> }));

afterEach(() => {
  cleanup();
  useSession.getState().clear();
});

it("刷新恢复会话后访问登录页，替换当前历史记录并进入后台", async () => {
  useSession.getState().setUser({ id: "test-user", username: "tester" });
  const router = createMemoryRouter(routes, { initialEntries: ["/login"] });
  render(<RouterProvider router={router} />);

  expect(await screen.findByRole("heading", { name: "后台看板" })).toBeInTheDocument();
  expect(screen.queryByRole("heading", { name: "登录表单" })).not.toBeInTheDocument();
  expect(router.state.location.pathname).toBe("/dashboard");
  expect(router.state.historyAction).toBe("REPLACE");
});

it("没有有效会话时保留登录表单", () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/login"] });
  render(<RouterProvider router={router} />);

  expect(screen.getByRole("heading", { name: "登录表单" })).toBeInTheDocument();
  expect(router.state.location.pathname).toBe("/login");
});

it("登录页挂载后会话生效也自动进入后台", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/login"] });
  render(<RouterProvider router={router} />);
  act(() => useSession.getState().setUser({ id: "test-user", username: "tester" }));

  await waitFor(() => expect(router.state.location.pathname).toBe("/dashboard"));
  expect(screen.queryByRole("heading", { name: "登录表单" })).not.toBeInTheDocument();
});

it("未登录访问后台仍跳转登录页", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] });
  render(<RouterProvider router={router} />);

  expect(await screen.findByRole("heading", { name: "登录表单" })).toBeInTheDocument();
  expect(router.state.location.pathname).toBe("/login");
  expect(router.state.historyAction).toBe("REPLACE");
});
