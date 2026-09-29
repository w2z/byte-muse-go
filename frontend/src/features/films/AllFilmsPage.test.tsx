// @vitest-environment jsdom
// jsdom 不提供媒体查询；Arco Grid 使用此接口订阅响应式断点。
Object.defineProperty(window, "matchMedia", { writable: true, value: (query: string) => ({ matches: false, media: query, addListener() {}, removeListener() {} }) });
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { apiRequest } from "../../shared/api/client";
import { AllFilmsPage } from "./AllFilmsPage";

vi.mock("../../shared/api/client", () => ({ apiRequest: vi.fn(async () => ({ items: [], total: 0, page: 1, page_size: 15 })) }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

it("空条件和相同条件提交均重新查询第一页", async () => {
  const user = userEvent.setup();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><AllFilmsPage /></QueryClientProvider>);
  await waitFor(() => expect(apiRequest).toHaveBeenCalledTimes(1));
  await screen.findByText("暂无影片");
  await user.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(apiRequest).toHaveBeenCalledTimes(2));
  expect(apiRequest).toHaveBeenLastCalledWith("/media?page=1&page_size=15");
  await user.type(screen.getByLabelText("搜索影片"), "TEST-001{Enter}");
  await waitFor(() => expect(apiRequest).toHaveBeenCalledTimes(3));
  await user.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(apiRequest).toHaveBeenCalledTimes(4));
  expect(apiRequest).toHaveBeenLastCalledWith("/media?page=1&page_size=15&search=TEST-001");
});

it("影片类型交给服务端筛选并可恢复全部类型", async () => {
 const user = userEvent.setup();
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
 render(<QueryClientProvider client={client}><AllFilmsPage /></QueryClientProvider>);
 await user.click(screen.getByLabelText("影片类型筛选"));
 await waitFor(async () => { await user.click(screen.getByText("无码破解")); });
 await waitFor(() => expect(apiRequest).toHaveBeenLastCalledWith("/media?page=1&page_size=15&video_type=uncensored_cracked"));
 await user.click(screen.getByLabelText("影片类型筛选"));
 await waitFor(async () => { await user.click(screen.getByText("全部类型")); });
 await waitFor(() => expect(apiRequest).toHaveBeenLastCalledWith("/media?page=1&page_size=15"));
});
