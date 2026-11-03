import { Button, Input, Space, Tabs } from "@arco-design/web-react";
import { IconCopy, IconRefresh } from "@arco-design/web-react/icon";
import { useState } from "react";
import { copyText } from "../../shared/ui/clipboard";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";

/** MCP 令牌编辑器：按钮仅更换草稿；右侧相连的复制按钮复制当前输入，保存由设置页统一处理。 */
export function MCPTokenInput({ id, value, onChange, disabled = false }: {
  id: string; value: string; onChange: (value: string) => void; disabled?: boolean;
}) {
  const [message, holder] = useFeedbackMessage();

  /** 只使用浏览器安全随机数，不支持时保留原值，不降级为可预测随机数。 */
  function replaceToken() {
    try {
      const bytes = globalThis.crypto.getRandomValues(new Uint8Array(32));
      onChange(Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join(""));
      message.success("新令牌已生成，保存后生效");
    } catch {
      message.error("无法生成安全令牌，请手动填写或通过 HTTPS 访问");
    }
  }

  /** 不把令牌写入通知或日志，只报告复制结果。 */
  async function copyToken() {
    try {
      await copyText(value);
      message.success("MCP 访问令牌已复制");
    } catch {
      message.error("复制失败，请显示令牌后手动复制");
    }
  }

  return <>
    {holder}
    <Input.Password id={id} aria-label="MCP 访问令牌" value={value} onChange={onChange}
      disabled={disabled} placeholder="开启后保存自动生成" autoComplete="off" afterStyle={{ padding: 0 }}
      addAfter={<Button.Group>
        <Button disabled={disabled} icon={<IconRefresh />} onClick={replaceToken}>更换令牌</Button>
        <Button disabled={disabled || !value} icon={<IconCopy />} aria-label="复制 MCP 访问令牌" onClick={() => void copyToken()}>复制</Button>
      </Button.Group>} />
  </>;
}

const clients = [
  { code: "claude", name: "Claude Code", path: ".mcp.json（项目目录，JSON）", docs: "https://code.claude.com/docs/en/mcp" },
  { code: "codex", name: "Codex", path: "~/.codex/config.toml（TOML）", docs: "https://developers.openai.com/codex/mcp/" },
  { code: "cursor", name: "Cursor", path: ".cursor/mcp.json（项目目录，JSON）", docs: "https://cursor.com/docs/mcp" },
];

/** 按客户端原生格式生成配置；使用 JSON 字符串转义，避免地址或凭据破坏 JSON/TOML。 */
function clientConfig(client: string, endpoint: string, token: string): string {
  const authorization = `Bearer ${token}`;
  if (client === "codex") {
    return `[mcp_servers.bytemuse]\nurl = ${JSON.stringify(endpoint)}\nhttp_headers = { "Authorization" = ${JSON.stringify(authorization)} }`;
  }
  return JSON.stringify({ mcpServers: { bytemuse: {
    ...(client === "claude" ? { type: "http" } : {}),
    url: endpoint,
    headers: { Authorization: authorization },
  } } }, null, 2);
}

/** 展示客户端配置模板；预览遮蔽令牌，复制仅填入已保存的令牌，避免使用尚未生效的草稿。 */
export function MCPClientConfigs({ endpoint, savedToken }: { endpoint: string; savedToken: string }) {
  const [activeCode, setActiveCode] = useState("claude");
  const [message, holder] = useFeedbackMessage();
  const active = clients.find((client) => client.code === activeCode) ?? clients[0];

  async function copyConfig() {
    try {
      await copyText(clientConfig(active.code, endpoint, savedToken || "<MCP_TOKEN>"));
      message.success(savedToken ? "配置已复制，已填入保存的令牌" : "配置已复制，请替换 <MCP_TOKEN>");
    } catch {
      message.error("配置复制失败，请手动复制模板");
    }
  }

  return <div className="settings-field settings-field-wide">
    {holder}
    <h3 className="settings-field-section">Agent 配置</h3>
    <span className="settings-field-description">预览中的令牌以 &lt;MCP_TOKEN&gt; 代替，复制时填入已保存的令牌。更换令牌后请先保存，再复制配置。Codex 使用 TOML，Claude Code 与 Cursor 使用 JSON。</span>
    <Tabs activeTab={active.code} onChange={setActiveCode} animation={false} aria-label="MCP 客户端配置">
      {clients.map((client) => <Tabs.TabPane key={client.code} title={client.name} />)}
    </Tabs>
    <Space wrap>
      <Button icon={<IconCopy />} onClick={() => void copyConfig()}>复制配置</Button>
      <span className="settings-field-description">{active.path}</span>
      <a href={active.docs} target="_blank" rel="noreferrer">官方说明</a>
    </Space>
    <Input.TextArea aria-label={`${active.name} 配置`} readOnly value={clientConfig(active.code, endpoint, "<MCP_TOKEN>")}
      className="settings-mcp-config" autoSize={{ minRows: 4, maxRows: 12 }} spellCheck={false} />
  </div>;
}
