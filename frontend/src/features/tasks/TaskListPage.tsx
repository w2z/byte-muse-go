import { Alert, Button, Input, Space, Table } from "@arco-design/web-react";
import { IconEdit, IconPlayArrow } from "@arco-design/web-react/icon";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { ScheduledTask } from "../../shared/api/types";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";
import { ContentCard } from "../../shared/ui/ContentCard";
import { PageHeader } from "../../shared/ui/PageHeader";
import { PageState } from "../../shared/ui/PageState";
import { AppDialog } from "../../shared/ui/AppDialog";
import "./TaskListPage.css";

const CRON_FIELD_LABELS = ["分钟", "小时", "日期", "月份", "星期"] as const;
const WEEKDAYS = ["日", "一", "二", "三", "四", "五", "六"] as const;
const MONTHS = ["一月", "二月", "三月", "四月", "五月", "六月", "七月", "八月", "九月", "十月", "十一月", "十二月"] as const;

function describeCronPart(value: string, index: number): string {
  const values = value.split(",").map((item) => item.trim());
  if (values.length > 1) return `${CRON_FIELD_LABELS[index]}为${values.map((item) => describeCronValue(item, index)).join("、")}`;
  return `${CRON_FIELD_LABELS[index]}为${describeCronValue(value, index)}`;
}

function describeCronValue(value: string, index: number): string {
  if (value.includes("/")) {
    const [base, step] = value.split("/");
    const unit = ["分钟", "小时", "天", "个月", "天"][index];
    if (base === "*") return "每" + step + unit;
    return (base.includes("-") ? describeCronValue(base, index) : "从" + formatCronNumber(base, index) + "开始") + "，每" + step + unit;
  }
  if (value === "*") return ["每分钟", "每小时", "每天", "每月", "每天"][index];
  const range = value.match(/^(\d+)-(\d+)$/);
  if (range) return `${formatCronNumber(range[1], index)}至${formatCronNumber(range[2], index)}`;
  return formatCronNumber(value, index);
}

function formatCronNumber(value: string, index: number): string {
  const number = Number(value);
  if (!Number.isInteger(number)) return value;
  if (index === 4 && number >= 0 && number <= 7) return `星期${WEEKDAYS[number === 7 ? 0 : number]}`;
  if (index === 3 && number >= 1 && number <= 12) return MONTHS[number - 1];
  return String(number);
}

/** 将标准五段式 Cron 转成可读中文；遇到非标准表达式保留原文，避免误导。 */
function describeValidCron(cron: string): string {
  if (!cron.trim()) return "已暂停定时执行";
  const fields = cron.trim().split(/\s+/);
  if (fields.length !== 5) return `Cron：${cron}`;
  const [minute, hour, day, month, weekday] = fields;
  if (minute !== "*" && hour !== "*" && /^\d+$/.test(minute) && /^\d+$/.test(hour) && day === "*" && month === "*" && weekday === "*") {
    return `每天 ${hour.padStart(2, "0")}:${minute.padStart(2, "0")}`;
  }
  if (/^\*\/\d+$/.test(minute) && hour === "*" && day === "*" && month === "*" && weekday === "*") return `每${minute.slice(2)}分钟`;
  return [minute, hour, day, month, weekday].map((value, index) => describeCronPart(value, index)).join("，");
}

const CRON_BOUNDS = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 6]] as const;
const CRON_NAMES = [[], [], [], ["JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"], ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"]];
const CRON_MACROS: Record<string, string> = {
  "@yearly": "每年 1 月 1 日 00:00", "@annually": "每年 1 月 1 日 00:00",
  "@monthly": "每月 1 日 00:00", "@weekly": "每周日 00:00",
  "@daily": "每天 00:00", "@midnight": "每天 00:00", "@hourly": "每小时整点",
};

/** 在编辑时解析服务端支持的五段 Cron、名称、步长及宏；空值沿用暂停语义，保存仍由后端校验。 */
export function parseCron(value: string): { valid: boolean; description: string } {
  let cron = value.trim();
  const invalid = (description: string) => ({ valid: false, description });
  if (!cron) return { valid: true, description: "已暂停定时执行" };
  let timezone = "";
  if (/^(?:CRON_TZ|TZ)=/.test(cron)) {
    const match = cron.match(/^(?:CRON_TZ|TZ)=([^ ]+) +(.+)$/);
    if (!match) return invalid("时区后需要填写执行计划");
    timezone = match[1];
    try { if (timezone !== "Local") new Intl.DateTimeFormat("zh-CN", { timeZone: timezone }); }
    catch { return invalid("无法识别执行时区"); }
    cron = match[2].trim();
  }
  const valid = (description: string) => ({ valid: true, description: description + (timezone ? "（时区：" + timezone + "）" : "") });
  if (Object.hasOwn(CRON_MACROS, cron)) return valid(CRON_MACROS[cron]);
  if (cron.startsWith("@every ")) {
    const duration = cron.slice(7);
    const parts = [...duration.matchAll(/(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h)/g)];
    if (parts.map((part) => part[0]).join("") !== duration || !parts.length) return invalid("间隔格式无效，例如 @every 1h30m");
    const units: Record<string, number> = { ns: 1e-9, us: 1e-6, "µs": 1e-6, "μs": 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 };
    const seconds = parts.reduce((total, part) => total + Number(part[1]) * units[part[2]], 0);
    if (!Number.isFinite(seconds) || seconds > 9223372036) return invalid("执行间隔超出支持范围");
    return valid("每" + Math.max(1, Math.floor(seconds)) + "秒");
  }
  const fields = cron.split(/\s+/);
  if (fields.length !== 5) return invalid("请填写五段 Cron：分钟 小时 日期 月份 星期");
  const normalized: string[] = [];
  for (const [index, field] of fields.entries()) {
    const [min, max] = CRON_BOUNDS[index];
    const number = (part: string) => {
      const named = CRON_NAMES[index].indexOf(part.toUpperCase());
      return named >= 0 ? named + min : /^\+?\d+$/.test(part) ? Number(part) : NaN;
    };
    const parts: string[] = [];
    for (const item of field.split(",")) {
      const split = item.split("/");
      const range = split[0].split("-");
      const star = range[0] === "*" || range[0] === "?";
      const start = star ? min : number(range[0]);
      const end = star ? max : range.length === 2 ? number(range[1]) : split.length === 2 ? max : start;
      const step = split.length === 2 && /^\+?\d+$/.test(split[1]) ? Number(split[1]) : split.length === 1 ? 1 : NaN;
      if (split.length > 2 || range.length > 2 || (star && range.length !== 1) || !Number.isSafeInteger(start) || !Number.isSafeInteger(end) || !Number.isSafeInteger(step) || step <= 0 || start < min || end > max || start > end) {
        return invalid(CRON_FIELD_LABELS[index] + "格式无效：范围 " + min + "–" + max + "，步长必须为正整数");
      }
      parts.push((star ? "*" : String(start) + (range.length === 2 ? "-" + end : "")) + (split.length === 2 ? "/" + step : ""));
    }
    normalized.push(parts.join(","));
  }
  return valid(describeValidCron(normalized.join(" ")));
}

/** 列表和编辑预览共用解析结果，避免同一计划出现两种中文说明。 */
export function describeCron(cron: string): string {
  return parseCron(cron).description;
}

function formatLastRun(value: string | null): string {
  if (!value) return "尚未执行";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "时间不可用";
  return date.toLocaleString("zh-CN", { hour12: false });
}

/** 展示定时任务，支持手动执行及持久化编辑计划；保存计划不会触发任务。 */
export function TaskListPage() {
  const queryClient = useQueryClient();
  const [message, messageHolder] = useFeedbackMessage();
  const [editing, setEditing] = useState<ScheduledTask | null>(null);
  const [cron, setCron] = useState("");
  const preview = useMemo(() => parseCron(cron), [cron]);
  const scheduleMutation = useMutation({
    mutationFn: (value: { name: string; cron: string }) => apiRequest<ScheduledTask>(
      "/tasks/" + encodeURIComponent(value.name) + "/schedule",
      { method: "PUT", body: JSON.stringify({ cron: value.cron.trim() }) },
    ),
    onSuccess: () => {
      setEditing(null);
      message.success("执行计划已保存并生效");
      void queryClient.invalidateQueries({ queryKey: ["scheduled-tasks"] });
      void queryClient.invalidateQueries({ queryKey: ["system-settings"] });
    },
  });
  const query = useQuery({
    queryKey: ["scheduled-tasks"],
    queryFn: () => apiRequest<Page<ScheduledTask>>("/tasks?all=true"),
    refetchInterval: 5000,
  });
  const runMutation = useMutation({
    mutationFn: (taskName: string) => apiRequest<void>(`/tasks/${encodeURIComponent(taskName)}/run`, { method: "POST" }),
    onSuccess: () => {
      message.success("任务已开始执行");
      void queryClient.invalidateQueries({ queryKey: ["scheduled-tasks"] });
    },
    onError: (error: Error) => message.error(error.message),
  });
  const items = useMemo(() => query.data?.items ?? [], [query.data?.items]);

  return (
    <section>
      <PageHeader title="定时任务" />
      <PageState isLoading={query.isLoading} error={query.error} isEmpty={false} emptyText="暂无定时任务" onRetry={() => void query.refetch()}>
        <ContentCard className="table-shell data-table-shell">
          <Table
            className="data-table task-table"
            border={false}
            rowKey="name"
            data={items}
            loading={query.isLoading || query.isFetching}
            noDataElement={<div className="data-table-empty" role="status">暂无定时任务</div>}
            pagination={false}
            columns={[
              {
                title: "任务名称",
                dataIndex: "name",
                width: "22%",
                render: (name: string, task: ScheduledTask) => (
                  <div className="task-name-cell">
                    <strong>{name}</strong>
                    <span>{task.running ? "正在执行" : "已就绪"}</span>
                  </div>
                ),
              },
              {
                title: "执行计划",
                dataIndex: "cron",
                width: "30%",
                render: (value: string) => (
                  <div className="task-cron-cell">
                    <span>{describeCron(value)}</span>
                    <code>{value}</code>
                  </div>
                ),
              },
              {
                title: "上次执行",
                dataIndex: "last_run",
                width: "23%",
                render: (value: string | null) => <span className={value ? "task-last-run" : "task-last-run task-last-run--empty"}>{formatLastRun(value)}</span>,
              },
              {
                title: "操作",
                width: "25%",
                render: (_: unknown, task: ScheduledTask) => (
                  <Space wrap>
                  <Button icon={<IconEdit />} onClick={() => {
                    scheduleMutation.reset();
                    setCron(task.cron);
                    setEditing(task);
                  }}>编辑</Button>
                  <Button
                    type="primary"
                    icon={<IconPlayArrow />}
                    loading={runMutation.isPending && runMutation.variables === task.name}
                    disabled={task.running || runMutation.isPending}
                    onClick={() => runMutation.mutate(task.name)}
                  >
                    立即执行
                  </Button>
                  </Space>
                ),
              },
            ]}
          />
        </ContentCard>
      </PageState>
      <AppDialog visible={editing !== null} title={"编辑 [" + (editing?.name ?? "") + "] 执行计划"} onClose={() => {
        if (!scheduleMutation.isPending) setEditing(null);
      }} footer={<>
        <Button disabled={scheduleMutation.isPending} onClick={() => setEditing(null)}>取消</Button>
        <Button type="primary" disabled={!preview.valid} loading={scheduleMutation.isPending} onClick={() => {
          if (editing && preview.valid && !scheduleMutation.isPending) scheduleMutation.mutate({ name: editing.name, cron });
        }}>保存</Button>
      </>}>
        <div className="task-schedule-preview" aria-live="polite">
          <Alert type={preview.valid ? "success" : "error"} content={preview.description} />
        </div>
        <label htmlFor="task-schedule-cron">执行计划（Cron）</label>
        <Input id="task-schedule-cron" value={cron} disabled={scheduleMutation.isPending}
          onChange={(value) => { setCron(value); scheduleMutation.reset(); }} placeholder="例如：0 0 * * *" />
        <div className="task-schedule-notes">
        <p>按服务端时区执行，依次填写：分钟 小时 日期 月份 星期。例如 0 0 * * * 表示每天零点。</p>
        <p>清空后暂停定时执行，仍可手动执行。保存立即生效，不影响当前正在执行的任务。</p>
        <p>服务启动时，除清理系统日志外的任务仍按原有规则补跑一次。</p>
        </div>
        {scheduleMutation.error && <p role="alert">{scheduleMutation.error.message}</p>}
      </AppDialog>
      {messageHolder}
    </section>
  );
}
