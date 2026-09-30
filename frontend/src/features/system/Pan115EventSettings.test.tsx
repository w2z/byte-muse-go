// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { SettingsPage } from "./SettingsPage";
import { apiRequest } from "../../shared/api/client";

vi.mock("../../shared/api/client", () => ({ apiRequest: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

it("115事件监听紧邻 Emby 上方，Cookie 清空关闭并禁用且保存 false", async () => {
  const user = userEvent.setup();
  let values: Record<string, string> = { PAN115_EVENT_ENABLE: "true", PAN115_COOKIE: "" };
  vi.mocked(apiRequest).mockImplementation(async (_path, options) => {
    if (options?.method === "PUT") values = { ...values, ...JSON.parse(String(options.body)).values };
    return { database_driver: "sqlite", values: { ...values }, configured: {} };
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><SettingsPage /></QueryClientProvider>);
  await user.click(await screen.findByRole("tab", { name: "网盘" }));
  await user.click(screen.getByRole("tab", { name: "STRM 生成" }));
  expect(screen.getByRole("switch", { name: "115事件监听" })).toBeDisabled();
  expect(screen.getByRole("switch", { name: "115事件监听" })).not.toBeChecked();
  // Cookie 已随扫码入口移到「115网盘」分组；分组切换会重挂载字段，因此每次切页都重新查询控件。
  await user.click(screen.getByRole("tab", { name: "115网盘" }));
  fireEvent.change(screen.getByLabelText("115 Cookie"), { target: { value: "UID=123_test; CID=test; SEID=test" } });
  await user.click(screen.getByRole("tab", { name: "STRM 生成" }));
  const control = screen.getByRole("switch", { name: "115事件监听" });
  expect(control).toBeEnabled();
  expect(control).not.toBeChecked();
  await user.click(control);
  expect(control).toBeChecked();
  const field = control.closest(".settings-field");
  expect(field?.nextElementSibling).toContainElement(screen.getByRole("switch", { name: "生成后刷新 Emby 媒体库" }));
  await user.click(screen.getByRole("tab", { name: "115网盘" }));
  fireEvent.change(screen.getByLabelText("115 Cookie"), { target: { value: "   " } });
  await user.click(screen.getByRole("tab", { name: "STRM 生成" }));
  expect(screen.getByRole("switch", { name: "115事件监听" })).toBeDisabled();
  expect(screen.getByRole("switch", { name: "115事件监听" })).not.toBeChecked();
  await user.click(screen.getByRole("button", { name: "保存设置" }));
  await waitFor(() => expect(values.PAN115_EVENT_ENABLE).toBe("false"));
  expect(values.PAN115_COOKIE).toBe("");
});
