// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { MCPClientConfigs, MCPPermissionsField, MCPTokenInput } from "./MCPSettings";

Object.defineProperty(window,"matchMedia",{writable:true,value:(query:string)=>({matches:false,media:query,addListener(){},removeListener(){}})});

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

/** 权限草稿沿用设置页的字符串存储口径，通过受控勾选验证全选和撤销。 */
function PermissionEditor() {
  const [value,setValue] = useState("[]");
  return <MCPPermissionsField value={value} onChange={setValue} tools={[
    {name:"get_version",description:"查询当前运行版本"},
    {name:"add_subscribe",description:"创建影片订阅并登记资源搜索"},
  ]} />;
}

it("权限默认不勾选，逐项选择与全选均由明确工具名组成", async () => {
  const user = userEvent.setup();
  render(<PermissionEditor />);
  const version = screen.getByRole("checkbox",{name:"查询当前运行版本"});
  const subscribe = screen.getByRole("checkbox",{name:"创建影片订阅并登记资源搜索"});
  const all = screen.getByRole("checkbox",{name:"全选权限"});
  expect(version).not.toBeChecked(); expect(subscribe).not.toBeChecked();
  await user.click(version);
  expect(version).toBeChecked(); expect(subscribe).not.toBeChecked();
  expect(screen.getByText("已选 1 / 2 项")).toBeInTheDocument();
  await user.click(all);
  expect(version).toBeChecked(); expect(subscribe).toBeChecked();
  await user.click(all);
  expect(version).not.toBeChecked(); expect(subscribe).not.toBeChecked();
});

it("权限目录缺失时不清空已保存权限，也不提供凭空构造的选项", () => {
  const change=vi.fn();
  render(<MCPPermissionsField value='["get_version"]' onChange={change} />);
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  expect(screen.getByText(/工具目录不可用/)).toBeInTheDocument();
  expect(change).not.toHaveBeenCalled();
});

/** 使用真实受控输入，验证按钮生成的草稿、复制内容和手动编辑。 */
function TokenEditor({ initial = "saved-token-for-tests-only" }: { initial?: string }) {
  const [value, setValue] = useState(initial);
  return <MCPTokenInput id="test-token" value={value} onChange={setValue} />;
}

it("更换按钮生成安全随机草稿，右侧相连的复制按钮复制当前草稿，仍可手动编辑", async () => {
  const user = userEvent.setup();
  const write = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
  render(<TokenEditor />);
  const input = screen.getByLabelText("MCP 访问令牌");
  const copy = screen.getByRole("button", { name: "复制 MCP 访问令牌" });
  expect(input.closest(".arco-input-group")).toContainElement(copy);
  await user.click(screen.getByRole("button", { name: "更换令牌" }));
  const first = (input as HTMLInputElement).value;
  expect(first).toMatch(/^[0-9a-f]{64}$/);
  expect(await screen.findByRole("status")).toHaveTextContent("保存后生效");
  await user.click(screen.getByRole("button", { name: "更换令牌" }));
  expect(input).not.toHaveValue(first);
  await user.click(copy);
  expect(write).toHaveBeenLastCalledWith((input as HTMLInputElement).value);
  await user.clear(input);
  await user.type(input, "manually-edited-token");
  await user.click(copy);
  expect(write).toHaveBeenLastCalledWith("manually-edited-token");
});

it("安全随机数不可用时保留原令牌并提示，不使用不安全的随机数", async () => {
  const user = userEvent.setup();
  vi.spyOn(globalThis.crypto, "getRandomValues").mockImplementation(() => { throw new Error("unavailable"); });
  render(<TokenEditor />);
  await user.click(screen.getByRole("button", { name: "更换令牌" }));
  expect(screen.getByLabelText("MCP 访问令牌")).toHaveValue("saved-token-for-tests-only");
  expect(await screen.findByRole("status")).toHaveTextContent("无法生成安全令牌");
});

it("空令牌不可复制，复制失败不会误报成功", async () => {
  const user = userEvent.setup();
  const write = vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("denied"));
  const view = render(<TokenEditor initial="" />);
  expect(screen.getByRole("button", { name: "复制 MCP 访问令牌" })).toBeDisabled();
  view.unmount();
  render(<TokenEditor />);
  await user.click(screen.getByRole("button", { name: "复制 MCP 访问令牌" }));
  expect(write).toHaveBeenCalled();
  expect(await screen.findByRole("status")).toHaveTextContent("复制失败");
});

it("配置预览隐藏令牌，复制使用已保存令牌，切换客户端输出对应格式", async () => {
  const user = userEvent.setup();
  const write = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
  const view = render(<MCPClientConfigs endpoint="https://bytemuse.example/api/v1/mcp" savedToken="saved-token" />);
  const preview = screen.getByRole("textbox", { name: "Claude Code 配置" });
  expect((preview as HTMLTextAreaElement).value).not.toContain("saved-token");
  expect(JSON.parse((preview as HTMLTextAreaElement).value).mcpServers.bytemuse.type).toBe("http");
  await user.click(screen.getByRole("button", { name: "复制配置" }));
  const claude = JSON.parse(write.mock.calls.at(-1)![0]);
  expect(claude.mcpServers.bytemuse).toEqual({ type: "http", url: "https://bytemuse.example/api/v1/mcp", headers: { Authorization: "Bearer saved-token" } });
  await user.click(screen.getByRole("tab", { name: "Codex" }));
  await user.click(screen.getByRole("button", { name: "复制配置" }));
  expect(write).toHaveBeenLastCalledWith('[mcp_servers.bytemuse]\nurl = "https://bytemuse.example/api/v1/mcp"\nhttp_headers = { "Authorization" = "Bearer saved-token" }');
  await user.click(screen.getByRole("tab", { name: "Cursor" }));
  view.rerender(<MCPClientConfigs endpoint="http://bytemuse.example:3750/api/v1/mcp" savedToken="new-saved-token" />);
  await user.click(screen.getByRole("button", { name: "复制配置" }));
  const cursor = JSON.parse(write.mock.calls.at(-1)![0]);
  expect(cursor.mcpServers.bytemuse).toEqual({ url: "http://bytemuse.example:3750/api/v1/mcp", headers: { Authorization: "Bearer new-saved-token" } });
});

it("令牌复制兼容局域网 HTTP，并清理临时节点及恢复焦点", async () => {
  const user = userEvent.setup();
  const clipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  const command = Object.getOwnPropertyDescriptor(document, "execCommand");
  const copy = vi.fn(() => {
    expect((document.activeElement as HTMLTextAreaElement).value).toBe("saved-token-for-tests-only");
    return true;
  });
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
  Object.defineProperty(document, "execCommand", { configurable: true, value: copy });
  try {
    const { container } = render(<TokenEditor />);
    const button = screen.getByRole("button", { name: "复制 MCP 访问令牌" });
    await user.click(button);
    await waitFor(() => expect(button).toHaveFocus());
    expect(copy).toHaveBeenCalledWith("copy");
    expect(container.querySelector("textarea")).toBeNull();
    expect(await screen.findByRole("status")).toHaveTextContent("已复制");
  } finally {
    if (clipboard) Object.defineProperty(navigator, "clipboard", clipboard);
    if (command) Object.defineProperty(document, "execCommand", command);
    else Reflect.deleteProperty(document, "execCommand");
  }
});
