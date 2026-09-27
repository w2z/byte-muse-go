const ATTRIBUTE_LABELS: Record<string, string> = {
  actor: "演员",
  media_id: "媒体编号",
  subscription_id: "订阅编号",
  rank_type: "类型",
  task: "任务",
  error: "原因",
  retry: "重试次数",
};

function formatValue(value: unknown): string {
  if (typeof value === "boolean") return value ? "是" : "否";
  if (value === null || value === undefined) return "-";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function formatAttribute(key: string, value: unknown): string {
  const label = ATTRIBUTE_LABELS[key] ?? key;
  return label + "：" + formatValue(value);
}

/** 将结构化日志属性压缩成单行中文文案，保留未知属性以便排查问题。 */
export function formatLogMessage(message: string, attrs?: Record<string, unknown>): string {
  if (message === "定时清理日志完成" && Number(attrs?.deleted ?? 0) > 0) {
    return "清理系统日志（" + formatValue(attrs?.deleted) + "条）";
  }
  if (message === "管理员登录成功" && attrs?.username !== undefined) {
    return "管理员登录成功：" + formatValue(attrs.username);
  }
  if (message === "管理员登录失败") {
    const reason = attrs?.error === undefined ? "用户名或密码错误" : formatValue(attrs.error);
    const ip = attrs?.ip === undefined ? "未知" : formatValue(attrs.ip);
    return "管理员登录失败：" + reason + "，IP：" + ip;
  }
  if (!attrs || Object.keys(attrs).length === 0) return message;
  const parts: string[] = [];
  for (const [key, value] of Object.entries(attrs)) {
    if (key === "count" || key === "deleted" || key === "retention_days" || key === "username" || key === "ip") continue;
    parts.push(formatAttribute(key, value));
  }
  if (Object.prototype.hasOwnProperty.call(attrs, "count")) {
    parts.push("获取到 " + formatValue(attrs.count) + " 条数据");
  }
  return parts.length > 0 ? message + "，" + parts.join("，") : message;
}

/** 没有实际删除日志的清理记录不应占用日志列表空间。 */
export function shouldDisplayLog(message: string, attrs?: Record<string, unknown>): boolean {
  return !(message === "定时清理日志完成" && Number(attrs?.deleted ?? 0) <= 0);
}
