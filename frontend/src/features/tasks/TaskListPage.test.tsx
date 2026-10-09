// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { TaskListPage } from "./TaskListPage";
import { apiRequest } from "../../shared/api/client";

vi.mock("../../shared/api/client", () => ({ apiRequest: vi.fn() }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

/** 验证用户编辑、失败保留草稿、重试成功刷新和取消不写入。 */
it("edits a schedule and preserves the draft on failure", async () => {
 let cron = "0 0 * * *";
 let fail = true;
 vi.mocked(apiRequest).mockImplementation(async (_path, options) => {
  if (options?.method === "PUT") {
   if (fail) throw new Error("执行计划无效");
   cron = JSON.parse(options.body as string).cron;
   return { name: "清理系统日志", cron, last_run: null, running: false } as never;
  }
  return { items: [{ name: "清理系统日志", cron, last_run: null, running: false }] } as never;
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 render(<QueryClientProvider client={client}><TaskListPage /></QueryClientProvider>);
 fireEvent.click(await screen.findByRole("button", { name: "编辑" }));
 const input = screen.getByLabelText("执行计划（Cron）");
 expect(input).toHaveValue("0 0 * * *");
 fireEvent.change(input, { target: { value: "15 6 * * *" } });
 fireEvent.click(screen.getByRole("button", { name: "保存" }));
 expect(await screen.findByRole("alert")).toHaveTextContent("执行计划无效");
 expect(input).toHaveValue("15 6 * * *");
 fail = false;
 fireEvent.click(screen.getByRole("button", { name: "保存" }));
 await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
 expect(await screen.findByText("每天 06:15")).toBeInTheDocument();
 expect(apiRequest).toHaveBeenCalledWith(`/tasks/` + encodeURIComponent("清理系统日志") + `/schedule`, { method: "PUT", body: JSON.stringify({cron: "15 6 * * *"}) });
 const writes = vi.mocked(apiRequest).mock.calls.filter(([, opts]) => opts?.method === "PUT").length;
 fireEvent.click(screen.getByRole("button", { name: "编辑" }));
 fireEvent.click(screen.getByRole("button", { name: "取消" }));
 expect(vi.mocked(apiRequest).mock.calls.filter(([, opts]) => opts?.method === "PUT")).toHaveLength(writes);
 client.clear();
});
