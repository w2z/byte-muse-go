// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { TaskListPage, parseCron } from "./TaskListPage";
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
 expect(await screen.findByText("执行计划无效")).toBeInTheDocument();
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

it.each(["99 0 * * *", "0 24 * * *", "0 0 0 * *", "0 0 * 13 *", "0 0 * * 7", "*/0 * * * *", "0 0 * * 5-1", "0 0 * * MON#2", "0 0 * *", "@unknown"])("rejects invalid schedule %s", (cron) => {
 expect(parseCron(cron).valid).toBe(false);
});

it.each([
 ["0 3 * * *", "每天 03:00"],
 ["*/5 * * * *", "每5分钟"],
 ["@daily", "每天 00:00"],
 ["", "已暂停定时执行"],
 ["0 9-18/2 * JAN-MAR MON-FRI", "星期一至星期五"],
 ["@every 1h30m", "每5400秒"],
])("describes valid schedule %s", (cron, description) => {
 expect(parseCron(cron)).toMatchObject({ valid: true });
 expect(parseCron(cron).description).toContain(description);
});

/** 解析错误必须阻止保存，修正后恢复；预览与列表共用同一说明。 */
it("previews the draft and blocks invalid saves", async () => {
 vi.mocked(apiRequest).mockResolvedValue({ items: [{ name: "同步上新", cron: "0 3 * * *", last_run: null, running: false }] });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<QueryClientProvider client={client}><TaskListPage /></QueryClientProvider>);
 fireEvent.click(await screen.findByRole("button", { name: "编辑" }));
 const dialog = within(screen.getByRole("dialog", { name: "编辑 [同步上新] 执行计划" }));
 expect(dialog.getByText("每天 03:00").closest(".arco-alert")).toHaveClass("arco-alert-success");
 const input = dialog.getByLabelText("执行计划（Cron）");
 fireEvent.change(input, { target: { value: "99 3 * * *" } });
 expect(dialog.getByRole("button", { name: "保存" })).toBeDisabled();
 expect(dialog.getByText(/分钟格式无效/).closest(".arco-alert")).toHaveClass("arco-alert-error");
 fireEvent.click(dialog.getByRole("button", { name: "保存" }));
 expect(vi.mocked(apiRequest).mock.calls.some(([, opts]) => opts?.method === "PUT")).toBe(false);
 fireEvent.change(input, { target: { value: "0 6 * * *" } });
 expect(dialog.getByText("每天 06:00").closest(".arco-alert")).toHaveClass("arco-alert-success");
 expect(dialog.getByRole("button", { name: "保存" })).toBeEnabled();
 fireEvent.change(input, { target: { value: "" } });
 expect(dialog.getByText("已暂停定时执行")).toBeInTheDocument();
 expect(dialog.getByRole("button", { name: "保存" })).toBeEnabled();
 client.clear();
});
