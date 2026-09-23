import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { SettingsPage } from "./SettingsPage";

describe("系统设置能力边界", () => {
  it("以只读表单展示真实配置并标记未接入分组", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ database_driver: "sqlite", demo_seed_enabled: false }), { status: 200, headers: { "Content-Type": "application/json" } })));
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><SettingsPage /></QueryClientProvider>);
    expect(await screen.findByLabelText("数据库引擎")).toHaveValue("sqlite");
    expect(screen.getByRole("tab", { name: "基础运行环境" })).toBeInTheDocument();
    for (const group of ["资源源站", "媒体库", "通知与消息", "下载器", "存储", "定时任务", "翻译与 AI", "网络与爬虫"]) {
      expect(screen.getByRole("tab", { name: group })).toBeInTheDocument();
    }
    expect(screen.getByLabelText("数据库引擎")).toHaveValue("sqlite");
    expect(screen.getByLabelText("演示数据")).toBeDisabled();
    expect(screen.getByRole("button", { name: "保存设置" })).toBeDisabled();
    await userEvent.click(screen.getByRole("tab", { name: "资源源站" }));
    expect(screen.getByLabelText("MTEAM API 密钥")).toBeDisabled();
    expect(screen.getAllByPlaceholderText("后端接口未接入").length).toBeGreaterThan(0);
  });
});
