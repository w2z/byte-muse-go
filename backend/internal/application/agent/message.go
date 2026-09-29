package agent

// chatMessage 是发往 chat/completions 的一条消息，字段与 OpenAI 契约一一对应。
type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// toolCall 是模型请求执行的一次函数调用。
type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

// toolFunction 是函数调用的名称与参数；参数为 JSON 字符串，由调用方解析。
type toolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
