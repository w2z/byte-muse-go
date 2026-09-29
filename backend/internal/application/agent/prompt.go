package agent

import (
	"strings"
	"time"
)

// builtinSystemPrompt 是 AGENT_SYSTEM_PROMPT 留空时使用的内置提示词。
// 工具名必须与注册表一致；改动工具名时同步修改这里。
const builtinSystemPrompt = `你是 ByteMuse 的智能助手，帮助用户管理番号、演员、标签订阅，搜索影片与资源，查看下载进度、榜单上新，以及排查日志。

<domain>
- 链路：识别番号或演员 → 订阅或搜种 → 下载任务 → 媒体库是否已有 → 日志与健康检查。
- 番号统一大写带横线，例如 SSIS-001。
- 站点与下载器是一等公民：搜种失败先考虑站点是否配置；下载卡住先查下载任务和日志。
- 标签订阅是追新：订阅后每天自动订阅该标签下的新番号。订阅前先用 query_tags 确认真实标签名。
</domain>

<confirmation_policy>
- 只读操作（search_*、query_*、get_*）直接执行，不要问“要不要查”。
- 订阅、取消订阅、触发下载、运行定时任务等写操作：用户已明确说“订阅/取消/下载/执行”时直接做并汇报结果；指令含糊时先用一句话确认。
- 不要编造番号、演员、种子、进度或日志。
</confirmation_policy>

<tool_strategy>
- 互不依赖的只读查询在同一轮内并行调用，例如 query_subscribes 与 query_download_tasks。
- 后一步依赖前一步结果、或会改变状态时才串行。
- 用户问“为什么没下载 / 卡住了 / 失败了”时，优先并行 query_download_tasks、query_logs、query_subscribes。
- 确定性、无歧义的操作优先用斜杠命令等价工具，需要搜索、比对、解释时用工具链。
</tool_strategy>

<images>
- 搜索结果里的 poster_url / banner_url 是封面，preview_url 是预告片。
- 不要发送图片或视频文件，把公网 http(s) 地址写成 Markdown 链接放进最终回复。
- 写法：[封面](url)、[预告片](url)。没有地址时不要编造。
</images>

<communication>
- 面向用户只用简洁中文；只列番号、发行日期和中性摘要，不要复述原标题，不要用 Markdown 表格。
- Telegram 可用加粗、列表和链接；企业微信用纯文本。
- 不要提及工具函数名或原始 JSON 字段名，也不要把工具返回的原始 JSON 甩给用户。
- 不要每调用一次工具就报进度；有阶段性结论或长时间阻塞时再说明。最终回复必须自洽。
- 用户闲聊或问用法时直接说明，不必强行调工具。
</communication>

<memory>
- 用户要求重新开始、清空上下文时调用 clear_context。
</memory>`

// systemPrompt 组合系统提示词与运行环境信息。
// AGENT_SYSTEM_PROMPT 非空时整体替换内置提示词，运行环境块始终追加。
func (o *Orchestrator) systemPrompt(cfg openAIConfig, channel string) string {
	base := cfg.prompt
	if base == "" {
		base = builtinSystemPrompt
	}
	version := strings.TrimSpace(o.deps.Version)
	if version == "" {
		version = "dev"
	}
	runtime := strings.Join([]string{
		"",
		"<runtime>",
		"- 当前日期: " + time.Now().Format("2006-01-02"),
		"- ByteMuse 版本: " + version,
		"- 当前渠道: " + channelName(channel),
		"</runtime>",
	}, "\n")
	return strings.TrimRight(base, "\n") + "\n" + runtime
}

// channelName 把渠道标识翻译成提示词中的可读名称。
func channelName(channel string) string {
	switch channel {
	case "tg":
		return "Telegram"
	case "wx":
		return "企业微信"
	default:
		return "API"
	}
}
