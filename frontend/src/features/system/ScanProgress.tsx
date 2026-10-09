import { Button, Progress } from "@arco-design/web-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiRequest, type ScanProgress } from "../../shared/api/client";
import { StrmFileProgress } from "./StrmFileProgress";

/** 服务端任务快照；刷新页面从数据库恢复，结果为空表示尚未完成。 */
export type ScanTask<T> = {
  id: string; kind: "library" | "strm"; mode: string;
  state: "running" | "pausing" | "paused" | "canceling" | "canceled" | "completed" | "failed" | "interrupted";
  progress: ScanProgress; result: T | null; error: string; created_at: string; updated_at: string; can_retry?: boolean;
};

/** 任务进行中的状态；暂停与取消中仍属进行中，只有进行中的任务才有需要展示的实时进度。 */
const scanTaskActiveStates: ScanTask<unknown>["state"][] = ["running", "pausing", "paused", "canceling"];

/** 判断任务是否仍在进行；按钮可用性与进度条显示共用同一判断。 */
export function isScanTaskActive(state?: ScanTask<unknown>["state"]) {
  return state !== undefined && scanTaskActiveStates.includes(state);
}

/** 扫描与生成分别轮询持久化任务；卸载只结束查询，不取消后台执行。 */
export function useScanProgress<T extends { files: number }, V = void>(path: string | ((variables: V) => string), taskPath?: string) {
  const base = taskPath ?? (typeof path === "string" ? path.split("?")[0] : "/strm/scan");
  const client = useQueryClient();
  const key = ["scan-task", base];
  const query = useQuery({
    queryKey: key, queryFn: ({ signal }) => apiRequest<ScanTask<T> | null>(base + "/task", { signal }),
    refetchInterval: 750, retry: false,
  });
  const update = async (next: ScanTask<T>) => {
    await client.cancelQueries({ queryKey: key });
    client.setQueryData(key, next);
  };
  const start = useMutation({
    retry: false,
    mutationFn: (variables: V) => apiRequest<ScanTask<T>>(typeof path === "function" ? path(variables) : path, { method: "POST", headers: { Prefer: "respond-async" } }),
    onSuccess: update,
    onError: () => { void query.refetch(); },
  });
  const task = query.data ?? undefined;
  const control = useMutation({
    retry: false,
    mutationFn: (action: "pause" | "resume" | "cancel" | "retry") => {
      if (!task) throw new Error("任务尚未加载");
      return apiRequest<ScanTask<T>>(base + "/tasks/" + task.id + "/control", { method: "POST", body: JSON.stringify({ action }) });
    },
    onSuccess: update,
    onError: () => { void query.refetch(); },
  });
  const active = isScanTaskActive(task?.state);
  const error = task?.state === "failed" ? new Error(task.error) : null;
  const scan = {
    mutate: start.mutate, variables: (task?.mode || start.variables) as V,
    data: task?.state === "completed" ? task.result ?? undefined : undefined,
    isPending: start.isPending || control.isPending || active, isError: Boolean(error), error: error ?? new Error(""),
  };
  return { scan, progress: task?.progress, task, control, controlsPending: start.isPending || control.isPending, unavailable: query.isPending || query.isError, queryError: start.error ?? query.error };
}

/** 暂停确认后显示继续；控制请求期间禁用按钮，错误保留可见。 */
export function ScanTaskControls({ task, pending, onAction, error }: {
  task?: ScanTask<unknown>; pending: boolean; onAction: (action: "pause" | "resume" | "cancel" | "retry") => void; error?: Error | null;
}) {
  const active = isScanTaskActive(task?.state);
  return <>
    {!active && task?.can_retry ? <Button status="danger" loading={pending} disabled={pending} onClick={() => onAction("retry")}>继续失败的任务</Button> : null}
    {active ? <>
      <Button disabled={pending || task?.state === "pausing" || task?.state === "canceling"} onClick={() => onAction(task?.state === "paused" ? "resume" : "pause")}>{task?.state === "paused" ? "继续" : "暂停"}</Button>
      <Button status="danger" disabled={pending || task?.state === "canceling"} onClick={() => onAction("cancel")}>取消</Button>
    </> : null}
    {error ? <span role="alert" className="settings-field-description">{error.message}</span> : null}
  </>;
}

/**
 * 扫描与生成共用的进度展示：只在任务进行中显示进度条，结束后的结论由调用方的结果或失败文案表达。
 * 服务重启导致的中断不是进度而是状态提示，单独保留一句可操作说明。
 */
export function ScanProgressDisplay({ progress, label, state, canRetry, taskId, processingText = "处理中" }: {
  progress?: ScanProgress; label: string; state?: ScanTask<unknown>["state"]; canRetry?: boolean; taskId?:string; processingText?: string;
}) {
  if (state === "interrupted") return <span className="settings-field-description">服务重启，任务已中断，{canRetry ? "可继续失败的任务" : "请重新启动"}</span>;
  if (!progress || !isScanTaskActive(state)) return null;
  const phase = state === "paused" ? "已暂停" : state === "pausing" ? "正在暂停" : state === "canceling" ? "正在取消"
    : progress.phase === "waiting" ? "等待处理" : progress.phase === "discovering" ? "扫描中，总数持续更新"
      : progress.phase === "cooling" ? "115 访问受限，等待恢复" : progress.phase === "finalizing" || progress.phase === "completed" ? "正在汇总结果" : processingText;
  return (
    <div className="settings-scan-progress" aria-label={`${label}处理进度`}>
      <span role="status">{phase}</span>
      <Progress
        percent={progress.percent}
        animation={state !== "paused"}
        formatText={(percent) => `${percent}% - ${progress.processed}/${progress.total}`}
        aria-label={`${label}进度条`}
      />
      {progress.current ? <div className="settings-scan-progress-location"><span className="settings-scan-progress-current settings-field-description">{progress.current}</span>{taskId ? <StrmFileProgress key={taskId} taskId={taskId} paused={state==="paused"} /> : null}</div> : null}
    </div>
  );
}
