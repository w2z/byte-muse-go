import { Button, Input, Space, Table } from "@arco-design/web-react";
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
  if (value === "*") return index === 0 ? "每分钟" : index === 1 ? "每小时" : "每天";
  const step = value.match(/^\*\/(\d+)$/);
  if (step) return `每${step[1]}${index === 0 ? "分钟" : index === 1 ? "小时" : CRON_FIELD_LABELS[index]}`;
  const range = value.match(/^(\d+)-(\d+)$/);
  if (range) return `${formatCronNumber(range[1], index)}至${formatCronNumber(range[2], index)}`;
  const nth = value.match(/^(\d+)(?:#|L)(\d+)?$/);
  if (nth && index === 4) return nth[2] ? `每月第${nth[2]}个星期${formatCronNumber(nth[1], index)}` : `每月最后一个星期${formatCronNumber(nth[1], index)}`;
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
export function describeCron(cron: string): string {
  if (!cron.trim()) return "已暂停定时执行";
  const fields = cron.trim().split(/\s+/);
  if (fields.length !== 5) return `Cron：${cron}`;
  const [minute, hour, day, month, weekday] = fields;
  if (minute !== "*" && hour !== "*" && /^\d+$/.test(minute) && /^\d+$/.test(hour) && day === "*" && month === "*" && weekday === "*") {
    return `每天 ${hour.padStart(2, "0")}:${minute.padStart(2, "0")}`;
  }
  if (minute.startsWith("*/") && hour === "*" && day === "*" && month === "*" && weekday === "*") return `每${minute.slice(2)}分钟`;
  if (minute === "0" && hour !== "*" && day === "*" && month === "*" && weekday === "*") return `每天 ${hour.padStart(2, "0")}:00`;
  return [minute, hour, day, month, weekday].map((value, index) => describeCronPart(value, index)).join("，");
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
                    type="secondary"
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
      <AppDialog visible={editing !== null} title="编辑执行计划" onClose={() => {
        if (!scheduleMutation.isPending) setEditing(null);
      }} footer={<>
        <Button disabled={scheduleMutation.isPending} onClick={() => setEditing(null)}>取消</Button>
        <Button type="primary" loading={scheduleMutation.isPending} onClick={() => {
          if (editing && !scheduleMutation.isPending) scheduleMutation.mutate({ name: editing.name, cron });
        }}>保存</Button>
      </>}>
        <p>{editing?.name}</p>
        <label htmlFor="task-schedule-cron">执行计划（Cron）</label>
        <Input id="task-schedule-cron" value={cron} disabled={scheduleMutation.isPending}
          onChange={setCron} placeholder="例如：0 0 * * *" />
        <p>按服务端时区执行，依次填写：分钟 小时 日期 月份 星期。例如 0 0 * * * 表示每天零点。</p>
        <p>清空后暂停定时执行，仍可手动执行。保存立即生效，不影响当前正在执行的任务。</p>
        <p>服务启动时，除清理系统日志外的任务仍按原有规则补跑一次。</p>
        {scheduleMutation.error && <p role="alert">{scheduleMutation.error.message}</p>}
      </AppDialog>
      {messageHolder}
    </section>
  );
}
