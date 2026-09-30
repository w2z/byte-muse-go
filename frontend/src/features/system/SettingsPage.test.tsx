// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SettingsPage } from "./SettingsPage";
import { apiRequest } from "../../shared/api/client";

vi.mock("../../shared/api/client", () => ({
  apiRequest: vi.fn(async () => ({
    database_driver: "sqlite",
    values: {
      PT_DEFAULT_DOWNLOADER: "qbittorrent",
      BT_DEFAULT_DOWNLOADER: "qbittorrent",
      BYPASS_ENGINE: "",
      BYPASS_URL: "",
    },
    configured: {},
  })),
}));

function renderSettings() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <SettingsPage />
    </QueryClientProvider>,
  );
}

describe("设置页字段布局", () => {
  it("点击布尔选项文字可反复切换草稿，重置恢复原值", async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "消息渠道" }));
    const control = screen.getByRole("switch", { name: "订阅成功" });
    await user.click(screen.getByText("订阅成功"));
    expect(control).toBeChecked();
    await user.click(screen.getByText("订阅成功"));
    expect(control).not.toBeChecked();
    await user.click(screen.getByText("订阅成功"));
    await user.click(screen.getByRole("button", { name: "重置" }));
    expect(control).not.toBeChecked();
    expect(vi.mocked(apiRequest).mock.calls.some(([, options]) => options?.method === "PUT")).toBe(false);
  });

  it("点击过滤选项文字与单选文字更新草稿，禁用开关文字不触发切换", async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(await screen.findByRole("tab", { name: /^过滤$/ }));
    const filter = screen.getByRole("switch", { name: "仅中文" });
    await user.click(screen.getByText("仅中文"));
    expect(filter).toBeChecked();
    await user.click(screen.getByText("仅中文"));
    expect(filter).not.toBeChecked();
    await user.click(screen.getByRole("tab", { name: /^站点$/ }));
    const auth = screen.getByRole("group", { name: "PTFans 鉴权方式" });
    await user.click(within(auth).getByText("密钥"));
    expect(within(auth).getByRole("radio", { name: "密钥" })).toBeChecked();
    await user.click(within(auth).getByText("Cookie"));
    expect(within(auth).getByRole("radio", { name: "Cookie" })).toBeChecked();
    await user.click(screen.getByRole("tab", { name: "其他" }));
    await user.click(screen.getByText("爬虫增强是否使用代理"));
    expect(screen.getByRole("switch", { name: "爬虫增强是否使用代理" })).toBeDisabled();
    expect(screen.getByRole("switch", { name: "爬虫增强是否使用代理" })).not.toBeChecked();
  });

  it("代理开关保存后重新加载保持选择，关闭增强保存为 false", async () => {
    const user = userEvent.setup();
    let values: Record<string, string> = { BYPASS_ENGINE: "flaresolverr", BYPASS_USE_PROXY: "false" };
    vi.mocked(apiRequest).mockImplementation(async (_path, options) => {
      if (options?.method === "PUT") values = { ...values, ...JSON.parse(String(options.body)).values };
      return { database_driver: "sqlite", values: { ...values }, configured: {} };
    });
    const view = renderSettings();
    await user.click(await screen.findByRole("tab", { name: "其他" }));
    await user.click(screen.getByRole("switch", { name: "爬虫增强是否使用代理" }));
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "重置" })).toBeDisabled());
    view.unmount();
    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "其他" }));
    expect(screen.getByRole("switch", { name: "爬虫增强是否使用代理" })).toBeChecked();
    await user.click(screen.getByLabelText("爬虫增强类型"));
    await waitFor(() => expect(getComputedStyle(screen.getByRole("option", { name: "不使用" })).pointerEvents).not.toBe("none"));
    await user.click(screen.getByRole("option", { name: "不使用" }));
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(values.BYPASS_USE_PROXY).toBe("false"));
  });

  it.each([
    ["对话 Agent", "自定义 System Prompt（留空使用内置提示词）"],
    ["翻译模型", "翻译 Prompt"],
  ])("%s 提示词框内计数并截断超限字符，保存完整 emoji", async (tab, label) => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "AI 模型" }));
    await user.click(screen.getByRole("tab", { name: tab }));
    const input = screen.getByLabelText(label);
    expect(input).toHaveAccessibleDescription("0/15360");
    fireEvent.change(input, { target: { value: "中😀a\n" } });
    expect(input).toHaveAccessibleDescription("4/15360");
    const tooLong = "中".repeat(15360) + "a";
    fireEvent.change(input, { target: { value: tooLong } });
    expect(input).toHaveValue("中".repeat(15360));
    expect(input).toHaveAccessibleDescription("15360/15360");
    expect(screen.getByText("15360/15360")).toHaveClass("settings-prompt-count-limit");
    expect(screen.queryByText(/UTF-8|字节|不会自动截断/)).not.toBeInTheDocument();
    const boundary = "😀".repeat(15360);
    fireEvent.change(input, { target: { value: boundary + "😀" } });
    expect(input).toHaveValue(boundary);
    fireEvent.keyDown(input, { key: "s", ctrlKey: true });
    await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.filter(([, options]) => options?.method === "PUT")).toHaveLength(1));
    const call = vi.mocked(apiRequest).mock.calls.find(([, options]) => options?.method === "PUT");
    expect(Object.values(JSON.parse(String(call?.[1]?.body)).values)).toContain(boundary);
  });

  it("Agent 使用未保存草稿测试 OpenAI，阻止重复点击并通过 Message 显示结果", async () => {
    const user = userEvent.setup();
    let finish!: (value: unknown) => void;
    vi.mocked(apiRequest).mockImplementation(async (_path, options) => options?.method === "POST"
      ? new Promise((resolve) => { finish = resolve; })
      : { database_driver: "sqlite", values: {}, configured: {} });
    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "AI 模型" }));
    await user.click(screen.getByRole("tab", { name: "对话 Agent" }));
    await user.type(screen.getByLabelText("模型名称"), "draft-model");
    await user.type(screen.getByLabelText("接口地址（OpenAI 兼容）"), "https://example.com/v1");
    await user.type(screen.getByLabelText("API Key"), "test-key");
    const button = screen.getByRole("button", { name: "测试 OpenAI" });
    await user.dblClick(button);
    expect(button).toBeDisabled();
    const posts = vi.mocked(apiRequest).mock.calls.filter(([, options]) => options?.method === "POST");
    expect(posts).toHaveLength(1);
    expect(posts[0][0]).toBe("/system/settings/openai/test");
    expect(JSON.parse(String(posts[0][1]?.body))).toEqual({ url: "https://example.com/v1", model: "draft-model", api_key: "test-key" });
    expect(vi.mocked(apiRequest).mock.calls.some(([, options]) => options?.method === "PUT")).toBe(false);
    finish({ message: "OpenAI 连接成功 (892ms)" });
    expect(await screen.findByRole("status")).toHaveTextContent("OpenAI 连接成功 (892ms)");
    await waitFor(() => expect(button).toBeEnabled());
    vi.mocked(apiRequest).mockRejectedValueOnce(new Error("OpenAI 连接失败 (892ms)：鉴权失败（HTTP 401），请检查 API Key 和模型权限"));
    await user.click(button);
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("OpenAI 连接失败 (892ms)：鉴权失败（HTTP 401），请检查 API Key 和模型权限"));
    expect(button).toBeEnabled();
    vi.mocked(apiRequest).mockRejectedValueOnce(new Error("网络连接失败"));
    await user.click(button);
    await waitFor(() => expect(screen.getByRole("status").textContent).toMatch(/^OpenAI 连接失败 [(][0-9]+ms[)]：网络连接失败$/));
  });

  it("翻译模型页签用未保存草稿独立测试，不复用对话配置", async () => {
    const user = userEvent.setup();
    vi.mocked(apiRequest).mockImplementation(async (_path, options) => options?.method === "POST"
      ? { message: "OpenAI 连接成功 (120ms)" }
      : {
        database_driver: "sqlite",
        values: { OPENAI_URL: "https://agent.example/v1", OPENAI_MODEL: "agent-model", OPENAI_API_KEY: "agent-key" },
        configured: {},
      });
    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "AI 模型" }));
    await user.click(screen.getByRole("tab", { name: "翻译模型" }));
    await user.type(screen.getByLabelText("模型名称"), "translate-model");
    await user.type(screen.getByLabelText("接口地址（OpenAI 兼容）"), "https://translate.example/v1");
    await user.type(screen.getByLabelText("API Key"), "translate-key");
    await user.click(screen.getByRole("button", { name: "测试翻译模型" }));
    const posts = vi.mocked(apiRequest).mock.calls.filter(([, options]) => options?.method === "POST");
    expect(posts).toHaveLength(1);
    expect(JSON.parse(String(posts[0][1]?.body))).toEqual({
      url: "https://translate.example/v1",
      model: "translate-model",
      api_key: "translate-key",
    });
    expect(vi.mocked(apiRequest).mock.calls.some(([, options]) => options?.method === "PUT")).toBe(false);
    expect(await screen.findByRole("status")).toHaveTextContent("OpenAI 连接成功 (120ms)");
  });

  it("保存 AI 模型分组会同时提交对话与翻译两套配置", async () => {
    const user = userEvent.setup();
    let values: Record<string, string> = {};
    vi.mocked(apiRequest).mockImplementation(async (_path, options) => {
      if (options?.method === "PUT") values = { ...values, ...JSON.parse(String(options.body)).values };
      return { database_driver: "sqlite", values: { ...values }, configured: {} };
    });
    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "AI 模型" }));
    await user.type(screen.getByLabelText("接口地址（OpenAI 兼容）"), "https://agent.example/v1");
    await user.click(screen.getByRole("tab", { name: "翻译模型" }));
    await user.type(screen.getByLabelText("接口地址（OpenAI 兼容）"), "https://translate.example/v1");
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(values.OPENAI_URL).toBe("https://agent.example/v1"));
    expect(values.TRANSLATION_OPENAI_URL).toBe("https://translate.example/v1");
  });

  it("翻译模型配置不全时禁用 OpenAI 翻译引擎，删除配置后回落默认引擎并随分组保存提交", async () => {
    const user = userEvent.setup();
    let values: Record<string, string> = {
      TRANSLATION_ENGINE: "openai",
      TRANSLATION_OPENAI_URL: "https://translate.example/v1",
      TRANSLATION_OPENAI_MODEL: "translate-model",
      TRANSLATION_OPENAI_API_KEY: "translate-key",
    };
    vi.mocked(apiRequest).mockImplementation(async (_path, options) => {
      if (options?.method === "PUT") values = { ...values, ...JSON.parse(String(options.body)).values };
      return { database_driver: "sqlite", values: { ...values }, configured: {} };
    });
    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "翻译" }));
    expect(screen.getByRole("radio", { name: "OpenAI" })).toBeEnabled();
    expect(screen.getByRole("radio", { name: "OpenAI" })).toBeChecked();
    // 在「AI 模型 → 翻译模型」删除模型名称后，OpenAI 选项不可选，当前选择立即回落默认引擎。
    await user.click(screen.getByRole("tab", { name: "AI 模型" }));
    await user.click(screen.getByRole("tab", { name: "翻译模型" }));
    await user.clear(screen.getByLabelText("模型名称"));
    await user.click(screen.getByRole("tab", { name: "翻译" }));
    expect(screen.getByRole("radio", { name: "OpenAI" })).toBeDisabled();
    expect(screen.getByRole("radio", { name: "关闭" })).toBeChecked();
    // 保存「AI 模型」分组也要把回落结果一并提交，避免设置值仍停留在 openai。
    await user.click(screen.getByRole("tab", { name: "AI 模型" }));
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(values.TRANSLATION_ENGINE).toBe("none"));
    expect(values.TRANSLATION_OPENAI_MODEL).toBe("");
  });

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(apiRequest).mockResolvedValue({
      database_driver: "sqlite",
      values: { PT_DEFAULT_DOWNLOADER: "qbittorrent", BT_DEFAULT_DOWNLOADER: "qbittorrent", BYPASS_ENGINE: "", BYPASS_URL: "", PTT_AUTH_TYPE: "cookie", PTFANS_AUTH_TYPE: "cookie", ROUSIPRO_AUTH_TYPE: "cookie", NICEPT_AUTH_TYPE: "cookie" },
      configured: {},
    });
  });
  afterEach(cleanup);

  it("在每个 PT 站点凭据输入框下显示用户确认的 H&R 或做种规则", async () => {
    const user = userEvent.setup();
    renderSettings();
    await waitFor(() => expect(screen.getByText("下载器")).toBeInTheDocument());
    await user.click(screen.getByRole("tab", { name: "站点设置" }));

    for (const [label, detail] of [
      ["馒头", /^无 H&R 要求。$/],
      ["PTTime Cookie", /^无 H&R 要求。$/],
      ["PTFans COOKIE", /^H&R：35 天内累计做种 7 天。$/],
      ["RousiPro COOKIE", /^做种要求：做种时间 ≥24 小时或单种分享率 ≥1.0。$/],
      ["NicePT COOKIE", /^H&R：12天内做种3天 或 单种分享率 ≥2\.0$/],
    ] as const) {
      const control = screen.getByRole("textbox", { name: label });
      const note = control.closest(".settings-field")?.querySelector(".settings-field-description");
      expect(note).toHaveTextContent(detail);
      expect(control.compareDocumentPosition(note!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }
    expect(screen.queryByText(/新手考核|新人考核|发布者|账号考核/)).not.toBeInTheDocument();
  });

  it("馒头令牌为单行输入，其他站点 COOKIE 保持多行", async () => {
    const user = userEvent.setup();
    renderSettings();
    await waitFor(() => expect(screen.getByText("下载器")).toBeInTheDocument());
    await user.click(screen.getByRole("tab", { name: "站点设置" }));

    const token = screen.getByRole("textbox", { name: "馒头" });
    expect(token.tagName).toBe("INPUT");
    expect(token).toHaveAttribute("placeholder", "请输入 存取令牌 (实验室->存取令牌)");
    await user.type(token, "example-token");
    expect(token).toHaveValue("example-token");
    expect(screen.getByRole("textbox", { name: "PTTime Cookie" }).tagName).toBe("TEXTAREA");
    expect(screen.getByRole("textbox", { name: "PTFans COOKIE" }).tagName).toBe("TEXTAREA");
  });

  it("支持完整密钥接口的三站可切换凭据，PTTime 仅提供 Cookie", async () => {
    const user = userEvent.setup();
    let values: Record<string, string> = { PTFANS_COOKIE: "session=example" };
    vi.mocked(apiRequest).mockImplementation(async (_path, options) => {
      if (options?.method === "PUT") {
        values = { ...values, ...JSON.parse(String(options.body)).values };
      }
      return { database_driver: "sqlite", values: { ...values }, configured: {} };
    });
    const view = renderSettings();
    await screen.findByRole("group", { name: "PTFans 鉴权方式" });
    for (const site of ["PTFans", "RousiPro", "NicePT"]) {
      const auth = screen.getByRole("group", { name: `${site} 鉴权方式` });
      expect(within(auth).getByRole("radio", { name: "密钥" })).toBeChecked();
      const title = auth.closest(".settings-field")?.querySelector(".settings-field-label");
      expect(title?.textContent).toBe(site);
      expect(title!.compareDocumentPosition(auth) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      await user.click(within(auth).getByRole("radio", { name: "密钥" }));
      expect(screen.getByRole("textbox", { name: `${site} 密钥` }).tagName).toBe("INPUT");
      expect(screen.queryByRole("textbox", { name: new RegExp(`^${site} (COOKIE|Cookie)$`) })).not.toBeInTheDocument();
    }
    expect(screen.queryByRole("group", { name: "PTTime 鉴权方式" })).not.toBeInTheDocument();
    expect(screen.queryByRole("spinbutton", { name: "PTTime UID" })).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "PTTime Cookie" }).tagName).toBe("TEXTAREA");
    expect(screen.queryByRole("group", { name: /馒头.*鉴权/ })).not.toBeInTheDocument();
    const ptfans = screen.getByRole("group", { name: "PTFans 鉴权方式" });
    await user.type(screen.getByRole("textbox", { name: "PTFans 密钥" }), "example-token");
    await user.click(within(ptfans).getByRole("radio", { name: "Cookie" }));
    expect(screen.getByRole("textbox", { name: "PTFans COOKIE" })).toHaveValue("session=example");
    await user.click(within(ptfans).getByRole("radio", { name: "密钥" }));
    expect(screen.getByRole("textbox", { name: "PTFans 密钥" })).toHaveValue("example-token");
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "重置" })).toBeDisabled());
    view.unmount();
    renderSettings();
    expect(await screen.findByRole("textbox", { name: "PTFans 密钥" })).toHaveValue("example-token");
    expect(within(screen.getByRole("group", { name: "PTFans 鉴权方式" })).getByRole("radio", { name: "密钥" })).toBeChecked();
    await user.click(within(screen.getByRole("group", { name: "PTFans 鉴权方式" })).getByRole("radio", { name: "Cookie" }));
    expect(screen.getByRole("textbox", { name: "PTFans COOKIE" })).toHaveValue("session=example");
    await user.click(screen.getByRole("button", { name: "重置" }));
    expect(screen.getByRole("textbox", { name: "PTFans 密钥" })).toHaveValue("example-token");
  });

  it("主站选择位于站点分组并在切换分组后保留所选值", async () => {
    const user = userEvent.setup();
    renderSettings();
    await waitFor(() => expect(screen.getByText("下载器")).toBeInTheDocument());
    await user.click(screen.getByRole("tab", { name: "站点设置" }));
    expect(screen.getByLabelText("主站选择（配合排序器使用）")).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "馒头" }));
    await user.click(screen.getByRole("tab", { name: /^排序$/ }));
    expect(screen.queryByLabelText("主站选择（配合排序器使用）")).not.toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: /^站点$/ }));
    expect(screen.getByRole("radio", { name: "馒头" })).toBeChecked();
  });

  it("空值即有效选项的枚举不提示“请选择”，未选择的枚举仍提示", async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "定时任务" }));
    const rankType = screen.getByLabelText("JAVDB榜单自动订阅");
    expect(within(rankType).getByRole("radio", { name: "不订阅" })).toBeChecked();
    expect(within(rankType).queryByText("请选择")).not.toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /^站点$/ }));
    const mainSite = screen.getByLabelText("主站选择（配合排序器使用）");
    expect(within(mainSite).getByText("请选择")).toBeInTheDocument();
  });

  it("下载器默认设置独立显示，爬虫增强备注位于控件后并展示三个项目链接", async () => {
    const user = userEvent.setup();
    renderSettings();

    await waitFor(() => expect(screen.getByText("下载器")).toBeInTheDocument());
    await user.click(screen.getByText("下载器"));
    expect(screen.getByText("默认设置")).toBeInTheDocument();
    expect(screen.getByText("PT默认下载器")).toBeInTheDocument();
    expect(screen.getByText("BT默认下载器")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "站点设置" }));
    await user.click(screen.getByText("其他"));
    const bypassControl = screen.getByLabelText("爬虫增强类型");
    expect(screen.getByText("不使用")).toBeInTheDocument();
    const bypassURL = screen.getByLabelText("爬虫增强");
    expect(bypassURL).toBeDisabled();
    const proxySwitch = screen.getByRole("switch", { name: "爬虫增强是否使用代理" });
    expect(proxySwitch).toBeDisabled();
    expect(proxySwitch).not.toBeChecked();
    await user.click(bypassControl);
    await waitFor(() => expect(getComputedStyle(screen.getByRole("option", { name: "FlareSolverr" })).pointerEvents).not.toBe("none"));
    await user.click(screen.getByRole("option", { name: "FlareSolverr" }));
    expect(bypassURL).toBeEnabled();
    expect(proxySwitch).toBeEnabled();
    await user.click(proxySwitch);
    expect(proxySwitch).toBeChecked();
    await user.type(bypassURL, "http://localhost:8191/v1");
    await user.click(bypassControl);
    await waitFor(() => expect(getComputedStyle(screen.getByRole("option", { name: "不使用" })).pointerEvents).not.toBe("none"));
    await user.click(screen.getByRole("option", { name: "不使用" }));
    expect(bypassURL).toBeDisabled();
    expect(proxySwitch).toBeDisabled();
    expect(proxySwitch).not.toBeChecked();
    expect(bypassURL).toHaveValue("http://localhost:8191/v1");
    const bypassNote = screen.getByText(/选择增强类型后填写服务地址/);
    expect(bypassControl.compareDocumentPosition(bypassNote) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    for (const [name, url] of [
      ["ByPass", "https://github.com/sarperavci/CloudflareBypassForScraping"],
      ["FlareSolverr", "https://github.com/FlareSolverr/FlareSolverr"],
      ["Scrapling", "https://github.com/D4Vinci/Scrapling"],
    ]) {
      expect(screen.getByRole("link", { name })).toHaveAttribute("href", url);
    }
    await user.click(bypassControl);
    await waitFor(() => expect(getComputedStyle(screen.getByRole("option", { name: "ByPass" })).pointerEvents).not.toBe("none"));
    await user.click(screen.getByRole("option", { name: "ByPass" }));
    expect(proxySwitch).toBeEnabled();
    expect(proxySwitch).not.toBeChecked();
    await user.click(proxySwitch);
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.some(([, options]) => options?.method === "PUT")).toBe(true));
    const save = vi.mocked(apiRequest).mock.calls.find(([, options]) => options?.method === "PUT");
    expect(JSON.parse(String(save?.[1]?.body)).values).toMatchObject({ BYPASS_ENGINE: "cloudflare_bypass_for_scraping", BYPASS_USE_PROXY: "true" });
  });

  it("微信与 Telegram 各自独立控制消息通知开关，底部备注说明适用范围", async () => {
    const user = userEvent.setup();
    let values: Record<string, string> = {};
    vi.mocked(apiRequest).mockImplementation(async (_path, options) => {
      if (options?.method === "PUT") values = { ...values, ...JSON.parse(String(options.body)).values };
      return { database_driver: "sqlite", values: { ...values }, configured: {} };
    });
    const notifyLabels = ["订阅成功", "订阅失败", "开始下载", "下载完成", "下载失败", "Agent对话"];

    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "消息渠道" }));
    expect(screen.getByRole("heading", { name: "消息" })).toBeInTheDocument();
    expect(screen.getByText("该设置只针对于微信、TG")).toBeInTheDocument();
    // 6 个开关必须在同一个横向行容器内，而不是各占一行。
    expect(document.querySelector(".settings-inline-row")?.querySelectorAll('[role="switch"]')).toHaveLength(
      notifyLabels.length,
    );
    for (const label of notifyLabels) expect(screen.getByRole("switch", { name: label })).not.toBeChecked();

    await user.click(screen.getByRole("switch", { name: "订阅成功" }));
    await user.click(screen.getByRole("switch", { name: "下载完成" }));
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(values.WECHAT_NOTIFY_SUBSCRIBE).toBe("true"));
    expect(values.WECHAT_NOTIFY_DOWNLOAD_COMPLETE).toBe("true");
    expect(values.WECHAT_NOTIFY_DOWNLOAD_FAILED).toBe("false");

    // 切到 Telegram：同一批开关必须保持各自独立的取值。
    await user.click(screen.getByRole("tab", { name: "Telegram" }));
    expect(screen.getByText("该设置只针对于微信、TG")).toBeInTheDocument();
    for (const label of notifyLabels) expect(screen.getByRole("switch", { name: label })).not.toBeChecked();
    await user.click(screen.getByRole("switch", { name: "Agent对话" }));
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(values.TELEGRAM_NOTIFY_AGENT_CHAT).toBe("true"));
    expect(values.TELEGRAM_NOTIFY_SUBSCRIBE).toBe("false");
    expect(values.WECHAT_NOTIFY_SUBSCRIBE).toBe("true");
  });
});

describe("网盘设置", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(cleanup);

  const boundAccount = {
    id: "1",
    name: "115 用户",
    avatar: "",
    level: "VIP",
    space: {
      total: { size: 5000, formatted: "5TB" },
      used: { size: 1000, formatted: "1TB" },
      remaining: { size: 4000, formatted: "4TB" },
    },
    quota: { total: 1500, used: 30, remaining: 1470 },
  };

  it("网盘分类用二级页签展示 115 网盘与 CloudDrive2，CloudDrive2 不再属于下载器", async () => {
    const user = userEvent.setup();
    renderSettings();

    await user.click(await screen.findByRole("tab", { name: "下载器" }));
    expect(screen.queryByRole("tab", { name: "CloudDrive2" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "网盘" }));
    expect(screen.getByRole("tab", { name: "115网盘" })).toBeInTheDocument();
    expect(screen.getByLabelText("离线下载保存目录")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "CloudDrive2" }));
    expect(screen.getByLabelText("CD2地址")).toBeInTheDocument();
    expect(screen.getByLabelText("CD2保存路径")).toBeInTheDocument();
    expect(screen.queryByLabelText("离线下载保存目录")).not.toBeInTheDocument();
  });

  it("BT 默认下载器提供 115 网盘选项，PT 默认下载器不提供", async () => {
    const user = userEvent.setup();
    renderSettings();

    await user.click(await screen.findByRole("tab", { name: "下载器" }));
    // 115 只接受磁力与直链，只属于 BT 默认下载器；PT 组仍只有 qBittorrent 与 Transmission。
    expect(screen.getAllByRole("radio", { name: "115网盘" })).toHaveLength(1);
    expect(screen.getByText(/选择 115 网盘前需先在「网盘」分类扫码绑定账号/)).toBeInTheDocument();
  });

  it("扫码登录展示二维码，授权后展示账号并可解除绑定", async () => {
    const user = userEvent.setup();
    let linked = false;
    vi.mocked(apiRequest).mockImplementation(async (path, options) => {
      if (path === "/system/settings") return { database_driver: "sqlite", values: {}, configured: {} };
      if (path === "/pan115/account" && options?.method === "DELETE") {
        linked = false;
        return undefined;
      }
      if (path === "/pan115/account") return { linked, account: linked ? boundAccount : null };
      if (path === "/pan115/login/sessions" && options?.method === "POST") {
        return { session_id: "session-1", qr_code: "data:image/png;base64,AAA", expires_at: "2026-09-29T10:00:00Z" };
      }
      if (path === "/pan115/login/sessions/session-1" && options?.method === "DELETE") return undefined;
      if (path === "/pan115/login/sessions/session-1") {
        linked = true;
        return { status: "authorized", account: boundAccount };
      }
      throw new Error(`未处理的请求 ${path}`);
    });

    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "网盘" }));
    await user.click(await screen.findByRole("button", { name: "扫码登录" }));

    expect(await screen.findByAltText("115 登录二维码")).toHaveAttribute("src", "data:image/png;base64,AAA");
    // 二维码改为弹窗展示，不再内联展开在标题下方。
    expect(screen.getByText("115 扫码登录")).toBeInTheDocument();
    // 授权成功后结束轮询、刷新账号快照并展示绑定信息。
    expect(await screen.findByText("115 用户")).toBeInTheDocument();
    expect(screen.queryByAltText("115 登录二维码")).not.toBeInTheDocument();
    // 等级、已用、云下载配额各占一行，两行数值下方各有一条进度条。
    expect(screen.getByText("等级 VIP")).toBeInTheDocument();
    expect(screen.getByText("已用 1TB / 5TB")).toBeInTheDocument();
    expect(screen.getByText("已用 30 / 1500")).toBeInTheDocument();
    expect(screen.getByLabelText("空间容量 已用 1TB / 5TB")).toBeInTheDocument();
    expect(screen.getByLabelText("云下载配额 已用 30 / 1500")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "解除绑定" }));
    expect(await screen.findByRole("button", { name: "扫码登录" })).toBeInTheDocument();
  });

  it("115 未返回云下载配额时只提示配额不可用，账号与容量照常展示", async () => {
    const user = userEvent.setup();
    vi.mocked(apiRequest).mockImplementation(async (path) => {
      if (path === "/system/settings") return { database_driver: "sqlite", values: {}, configured: {} };
      if (path === "/pan115/account") return { linked: true, account: { ...boundAccount, quota: null } };
      throw new Error(`未处理的请求 ${path}`);
    });

    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "网盘" }));

    expect(await screen.findByText("115 用户")).toBeInTheDocument();
    expect(screen.getByText("已用 1TB / 5TB")).toBeInTheDocument();
    expect(screen.getByText("云下载配额暂不可用")).toBeInTheDocument();
    expect(screen.queryByLabelText(/云下载配额/)).not.toBeInTheDocument();
  });

  it("二维码过期后提示失效并允许重新获取", async () => {
    const user = userEvent.setup();
    vi.mocked(apiRequest).mockImplementation(async (path, options) => {
      if (path === "/system/settings") return { database_driver: "sqlite", values: {}, configured: {} };
      if (path === "/pan115/account") return { linked: false, account: null };
      if (path === "/pan115/login/sessions" && options?.method === "POST") {
        return { session_id: "session-1", qr_code: "data:image/png;base64,AAA", expires_at: "2026-09-29T10:00:00Z" };
      }
      if (path === "/pan115/login/sessions/session-1" && options?.method === "DELETE") return undefined;
      if (path === "/pan115/login/sessions/session-1") return { status: "expired", account: null };
      throw new Error(`未处理的请求 ${path}`);
    });

    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "网盘" }));
    await user.click(await screen.findByRole("button", { name: "扫码登录" }));

    expect(await screen.findByText("二维码已过期，请重新获取二维码")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重新获取二维码" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "取消" })).not.toBeInTheDocument();
  });

  it("扫描目录可添加多个 115 目录，重复目录不可再添加，保存为 JSON 数组", async () => {
    const user = userEvent.setup();
    const values: Record<string, string> = {};
    const directories: Record<string, { id: string; name: string }[]> = {
      "0": [{ id: "10", name: "电影" }],
      "10": [{ id: "11", name: "日韩" }],
    };
    vi.mocked(apiRequest).mockImplementation(async (path, options) => {
      if (path === "/system/settings" && options?.method === "PUT") {
        const body = JSON.parse(String(options.body)) as { values: Record<string, string> };
        Object.assign(values, body.values);
        return { database_driver: "sqlite", values: { ...body.values }, configured: {} };
      }
      if (path === "/system/settings") return { database_driver: "sqlite", values: {}, configured: {} };
      if (path === "/pan115/account") return { linked: true, account: boundAccount };
      if (path.startsWith("/pan115/files")) {
        const directoryID = new URL(path, "http://localhost").searchParams.get("directory_id") ?? "0";
        const crumbs = directoryID === "0"
          ? [{ id: "0", name: "" }]
          : [{ id: "0", name: "" }, { id: "10", name: "电影" }];
        const entries = directories[directoryID] ?? [];
        return {
          directory_id: directoryID,
          path: crumbs,
          files: entries.map((item) => ({
            id: item.id, parent_id: directoryID, name: item.name, is_directory: true, size: 0, pick_code: "",
          })),
          total: entries.length,
          has_more: false,
        };
      }
      throw new Error(`未处理的请求 ${path}`);
    });

    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "网盘" }));
    expect(screen.getByText("尚未添加扫描目录")).toBeInTheDocument();

    // 第一次：根目录下选中「电影」后确认；未选中任何目录时确认按钮不可点。
    await user.click(screen.getByRole("button", { name: "添加目录" }));
    const firstConfirm = await screen.findByRole("button", { name: "确认" });
    expect(firstConfirm).toBeDisabled();
    await user.click(await screen.findByRole("button", { name: "电影" }));
    expect(firstConfirm).toBeEnabled();
    await user.click(firstConfirm);
    await waitForDialogClosed();
    expect(await screen.findByLabelText("扫描目录 1")).toHaveValue("/电影");

    // 已添加的目录在弹窗内标记为已添加，不能再次选中。
    await user.click(screen.getByRole("button", { name: "添加目录" }));
    expect(await screen.findByRole("button", { name: "电影" })).toBeDisabled();
    expect(screen.getByText("已添加")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "取消" }));
    await waitForDialogClosed();

    // 第二次：点击末级蓝色路径选中根目录，共两个地址。
    await user.click(screen.getByRole("button", { name: "添加目录" }));
    await user.click(await screen.findByRole("button", { name: "根目录" }));
    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitForDialogClosed();
    expect(await screen.findByLabelText("扫描目录 2")).toHaveValue("/");

    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(values.PAN115_SCAN_PATHS).toBe(JSON.stringify([
      { id: "10", path: "/电影" },
      { id: "0", path: "/" },
    ])));

    // 删除第二个目录后只剩一个地址。
    await user.click(screen.getAllByRole("button", { name: "删除" })[1]);
    expect(screen.queryByLabelText("扫描目录 2")).not.toBeInTheDocument();
  });

  /** 等待弹窗完全卸载：退出动画在 jsdom 里靠定时器收尾，立刻打开下一个弹窗会同时命中两个。 */
  async function waitForDialogClosed() {
    await waitFor(() => expect(screen.queryAllByRole("dialog")).toHaveLength(0));
  }

  it("STRM 生成页签可添加多条网盘映射，分别选择网盘目录与本地 strm 目录后保存为 JSON 数组", async () => {
    const user = userEvent.setup();
    const values: Record<string, string> = {};
    const cloudDirectories: Record<string, { name: string; path: string }[]> = {
      "/": [{ name: "115", path: "/115" }],
      "/115": [],
    };
    const localDirectories: Record<string, { name: string; path: string }[]> = {
      "/": [{ name: "movies", path: "/movies" }],
      "/movies": [],
    };
    vi.mocked(apiRequest).mockImplementation(async (path, options) => {
      if (path === "/system/settings" && options?.method === "PUT") {
        const body = JSON.parse(String(options.body)) as { values: Record<string, string> };
        Object.assign(values, body.values);
        return { database_driver: "sqlite", values: { ...body.values }, configured: {} };
      }
      if (path === "/system/settings") return { database_driver: "sqlite", values: {}, configured: {} };
      if (path === "/pan115/account") return { linked: true, account: boundAccount };
      if (path.startsWith("/pan115/files")) {
        const directoryID = new URL(path, "http://localhost").searchParams.get("directory_id") ?? "0";
        const entries = directoryID === "0" ? [{ id: "10", name: "电影" }] : [];
        return {
          directory_id: directoryID,
          path: directoryID === "0" ? [{ id: "0", name: "" }] : [{ id: "0", name: "" }, { id: "10", name: "电影" }],
          files: entries.map((item) => ({
            id: item.id, parent_id: directoryID, name: item.name, is_directory: true, size: 0, pick_code: "",
          })),
          total: entries.length,
          has_more: false,
        };
      }
      if (path.startsWith("/strm/clouddrive/directories")) {
        const current = new URL(path, "http://localhost").searchParams.get("path") ?? "/";
        return { path: current, directories: cloudDirectories[current] ?? [] };
      }
      if (path.startsWith("/strm/directories")) {
        const current = new URL(path, "http://localhost").searchParams.get("path") ?? "/";
        return { path: current, directories: localDirectories[current] ?? [] };
      }
      throw new Error(`未处理的请求 ${path}`);
    });

    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "网盘" }));
    await user.click(screen.getByRole("tab", { name: "STRM 生成" }));
    expect(screen.getByText("尚未添加 strm 映射")).toBeInTheDocument();

    // 第一条映射：115 网盘目录 + 本地 strm 目录，类型默认 115。
    await user.click(screen.getByRole("button", { name: "添加映射" }));
    await user.click(screen.getByRole("button", { name: "选择网盘目录" }));
    await user.click(await screen.findByRole("button", { name: "电影" }));
    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitForDialogClosed();
    expect(await screen.findByLabelText("网盘路径 1")).toHaveValue("/电影");

    await user.click(screen.getByRole("button", { name: "选择本地目录" }));
    await user.click(await screen.findByRole("button", { name: "movies" }));
    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitForDialogClosed();
    expect(await screen.findByLabelText("本地路径 1")).toHaveValue("/movies");

    // 第二条映射：切到 CloudDrive2 后清空已选网盘目录，重新选择网盘与本地根目录。
    await user.click(screen.getByRole("button", { name: "添加映射" }));
    await user.click(
      within(screen.getByRole("group", { name: "网盘类型 2" })).getByRole("radio", { name: "CloudDrive2" }),
    );
    await user.click(screen.getAllByRole("button", { name: "选择网盘目录" })[1]);
    await user.click(await screen.findByRole("button", { name: "115" }));
    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitForDialogClosed();
    expect(await screen.findByLabelText("网盘路径 2")).toHaveValue("/115");

    // 本地根目录只能通过末级蓝色路径选中：根目录下没有可选的子目录。
    await user.click(screen.getAllByRole("button", { name: "选择本地目录" })[1]);
    await user.click(await screen.findByRole("button", { name: "strm" }));
    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitForDialogClosed();
    expect(await screen.findByLabelText("本地路径 2")).toHaveValue("/");

    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(values.STRM_PATHS).toBe(JSON.stringify([
      { kind: "115", id: "10", path: "/电影", local_path: "/movies", formats: ["mp4", "avi", "rmvb", "wmv", "mov", "mkv", "webm", "iso", "mpg", "m4v", "ts", "flv", "strm", "vob", "m2ts"] },
      { kind: "cd2", id: "/115", path: "/115", local_path: "/", formats: ["mp4", "avi", "rmvb", "wmv", "mov", "mkv", "webm", "iso", "mpg", "m4v", "ts", "flv", "strm", "vob", "m2ts"] },
    ])));

    // 删除第一条映射后只剩一条，并且删除后不再显示未选齐提示。
    await user.click(screen.getByRole("button", { name: "删除映射 1" }));
    expect(screen.getByLabelText("网盘路径 1")).toHaveValue("/115");
    expect(screen.queryByLabelText("网盘路径 2")).not.toBeInTheDocument();
  });

  it("本地 strm 目录选择器固定以 /strm 为根，可新建目录并刷新出外部创建的目录", async () => {
    const user = userEvent.setup();
    const created: { path: string; name: string }[] = [];
    let directories = [{ name: "movies", path: "/movies" }];
    vi.mocked(apiRequest).mockImplementation(async (path, options) => {
      if (path === "/system/settings") return { database_driver: "sqlite", values: {}, configured: {} };
      if (path === "/pan115/account") return { linked: true, account: boundAccount };
      if (path === "/strm/directories" && options?.method === "POST") {
        const body = JSON.parse(String(options.body)) as { path: string; name: string };
        created.push(body);
        const prefix = body.path === "/" ? "" : body.path;
        directories = [...directories, { name: body.name, path: `${prefix}/${body.name}` }];
        return { name: body.name, path: `${prefix}/${body.name}` };
      }
      if (path.startsWith("/strm/directories")) {
        const current = new URL(path, "http://localhost").searchParams.get("path") ?? "/";
        return { path: current, directories: current === "/" ? directories : [] };
      }
      throw new Error(`未处理的请求 ${path}`);
    });

    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "网盘" }));
    await user.click(screen.getByRole("tab", { name: "STRM 生成" }));
    await user.click(screen.getByRole("button", { name: "添加映射" }));
    await user.click(screen.getByRole("button", { name: "选择本地目录" }));

    // 根目录固定为 strm：面包屑只有一项，没有回到 /strm 以上的入口。
    const dialog = await screen.findByRole("dialog");
    const breadcrumbs = within(dialog).getByRole("navigation", { name: "目录路径" });
    expect(within(breadcrumbs).getAllByRole("button")).toHaveLength(1);
    expect(within(breadcrumbs).getByRole("button", { name: "strm" })).toBeInTheDocument();
    expect(await within(dialog).findByRole("button", { name: "movies" })).toBeInTheDocument();
    // 未选中任何目录时确认按钮不可点。
    expect(within(dialog).getByRole("button", { name: "确认" })).toBeDisabled();

    // 新建目录后列表立即刷新出新目录。
    await user.type(within(dialog).getByLabelText("新目录名称"), "tv");
    await user.click(within(dialog).getByRole("button", { name: "新建目录" }));
    expect(await within(dialog).findByRole("button", { name: "tv" })).toBeInTheDocument();
    expect(created).toEqual([{ path: "/", name: "tv" }]);

    // 其他位置新建的目录点击刷新后可见。
    directories = [...directories, { name: "external", path: "/external" }];
    await user.click(within(dialog).getByRole("button", { name: "刷新" }));
    expect(await within(dialog).findByRole("button", { name: "external" })).toBeInTheDocument();

    // 根目录通过末级蓝色路径选中，确认后回填 /。
    await user.click(within(dialog).getByRole("button", { name: "strm" }));
    await user.click(within(dialog).getByRole("button", { name: "确认" }));
    expect(await screen.findByLabelText("本地路径 1")).toHaveValue("/");
  });

  it("STRM 目录设置决定本地目录选择器的根名称，并随保存提交", async () => {
    const user = userEvent.setup();
    const values: Record<string, string> = {};
    vi.mocked(apiRequest).mockImplementation(async (path, options) => {
      if (path === "/system/settings" && options?.method === "PUT") {
        const body = JSON.parse(String(options.body)) as { values: Record<string, string> };
        Object.assign(values, body.values);
        return { database_driver: "sqlite", values: { ...body.values }, configured: {} };
      }
      if (path === "/system/settings") {
        return { database_driver: "sqlite", values: { STRM_ROOT: "/media/library/strm" }, configured: {} };
      }
      if (path === "/pan115/account") return { linked: true, account: boundAccount };
      if (path.startsWith("/strm/directories")) {
        const current = new URL(path, "http://localhost").searchParams.get("path") ?? "/";
        return { path: current, directories: [] };
      }
      throw new Error(`未处理的请求 ${path}`);
    });

    renderSettings();
    await user.click(await screen.findByRole("tab", { name: "网盘" }));
    await user.click(screen.getByRole("tab", { name: "STRM 生成" }));

    // 已保存的根目录回显在设置项里，并成为本地目录选择器的根名称。
    expect(await screen.findByLabelText("STRM 目录")).toHaveValue("/media/library/strm");
    await user.click(screen.getByRole("button", { name: "添加映射" }));
    await user.click(screen.getByRole("button", { name: "选择本地目录" }));
    const dialog = await screen.findByRole("dialog");
    const breadcrumbs = within(dialog).getByRole("navigation", { name: "目录路径" });
    expect(within(breadcrumbs).getAllByRole("button")).toHaveLength(1);
    expect(within(breadcrumbs).getByRole("button", { name: "strm" })).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitForDialogClosed();

    // 修改后随保存提交；留空表示沿用部署默认值。
    await user.clear(screen.getByLabelText("STRM 目录"));
    await user.type(screen.getByLabelText("STRM 目录"), "/mnt/strm");
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(values.STRM_ROOT).toBe("/mnt/strm"));
  });
});
