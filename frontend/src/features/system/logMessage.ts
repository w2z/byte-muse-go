const ATTRIBUTE_LABELS: Record<string, string> = {
  actor: "演员",
  code: "番号",
  filename: "文件名",
  created: "新建",
  channel: "渠道",
  event: "事件",
  with_cover: "带封面",
  kind: "消息类型",
  spoiler: "防剧透",
  reason: "原因",
  rank_type: "类型",
  task: "任务",
  error: "原因",
  retry: "重试次数",
  task_id: "任务 ID",
  processed: "已处理",
  total: "总数",
  success: "成功",
  skipped: "跳过",
  failed: "失败",
  workers: "并发数",
};

/** 查询类日志携带的番号列表：压缩为「（A、B）」附在数量之后，空列表不占位。 */
function formatCodes(value: unknown): string {
  if (!Array.isArray(value)) return "";
  const codes = value.map(formatValue).filter((item) => item !== "" && item !== "-");
  return codes.length > 0 ? "（" + codes.join("、") + "）" : "";
}

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
  if (message === "定时任务执行完成" && attrs?.task === "清理系统日志") {
    const deleted = Number(attrs.deleted ?? 0);
    return deleted > 0
      ? "清理系统日志（" + formatValue(attrs.deleted) + "条）"
      : message + "，任务：" + formatValue(attrs.task);
  }
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
    // 订阅编号与媒体编号都是不对用户暴露的内部标识。旧格式日志仍带这两个字段，界面不再展示；
    // 新日志一律用番号标识对象，这里只是让历史记录不露出原始字段名。
    if (key === "subscription_id" || key === "media_id") continue;
    if (key === "count" || key === "codes" || key === "deleted" || key === "retention_days" || key === "username" || key === "ip") continue;
    parts.push(formatAttribute(key, value));
  }
  if (Object.prototype.hasOwnProperty.call(attrs, "count")) {
    parts.push("获取到 " + formatValue(attrs.count) + " 条数据");
  }
  const codes = formatCodes(attrs.codes);
  if (codes) parts.push(codes);
  return parts.length > 0 ? message + "，" + parts.join("，") : message;
}

/** 没有实际删除日志的清理记录不应占用日志列表空间。 */
export function shouldDisplayLog(message: string, attrs?: Record<string, unknown>): boolean {
  if (message === "定时任务执行完成" && attrs?.task === "清理系统日志") {
    return Number(attrs.deleted ?? 0) > 0;
  }
  return !(message === "定时清理日志完成" && Number(attrs?.deleted ?? 0) <= 0);
}
