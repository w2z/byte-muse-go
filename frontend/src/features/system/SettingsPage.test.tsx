// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
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
    await user.click(bypassControl);
    await waitFor(() => expect(getComputedStyle(screen.getByRole("option", { name: "FlareSolverr" })).pointerEvents).not.toBe("none"));
    await user.click(screen.getByRole("option", { name: "FlareSolverr" }));
    expect(bypassURL).toBeEnabled();
    await user.type(bypassURL, "http://localhost:8191/v1");
    await user.click(bypassControl);
    await waitFor(() => expect(getComputedStyle(screen.getByRole("option", { name: "不使用" })).pointerEvents).not.toBe("none"));
    await user.click(screen.getByRole("option", { name: "不使用" }));
    expect(bypassURL).toBeDisabled();
    expect(bypassURL).toHaveValue("http://localhost:8191/v1");
    const bypassNote = screen.getByText(/选择增强类型后填写服务地址/);
    expect(bypassControl.compareDocumentPosition(bypassNote) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    for (const [name, url] of [
      ["CloudflareBypassForScraping", "https://github.com/sarperavci/CloudflareBypassForScraping"],
      ["FlareSolverr", "https://github.com/FlareSolverr/FlareSolverr"],
      ["Scrapling", "https://github.com/D4Vinci/Scrapling"],
    ]) {
      expect(screen.getByRole("link", { name })).toHaveAttribute("href", url);
    }
  });
});
