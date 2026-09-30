import { Progress } from "@arco-design/web-react";
import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { apiRequest, type ScanProgress } from "../../shared/api/client";

/** 管理一次扫描流：重试清空旧进度，卸载中止请求，禁止自动重放有副作用的任务。 */
export function useScanProgress<T extends { files: number }>(path: string) {
  const [progress, setProgress] = useState<ScanProgress>();
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  const scan = useMutation({
    retry: false,
    mutationFn: async () => {
      controller.current?.abort();
      const current = new AbortController();
      controller.current = current;
      setProgress({ phase: "waiting", processed: 0, total: 0, percent: 0, current: "" });
      let streamed = false;
      const result = await apiRequest<T>(path, { method: "POST", signal: current.signal, headers: { Accept: "application/x-ndjson" } }, (next) => {
        streamed = true;
        if (!current.signal.aborted) setProgress(next);
      });
      // 兼容尚未更新的后端：仅在结果返回后补齐终态，等待期间不模拟进度。
      if (!streamed && !current.signal.aborted) setProgress({ phase: "completed", processed: result.files, total: result.files, percent: 100, current: "" });
      return result;
    },
  });
  return { scan, progress };
}

/** 扫描与生成共用的进度展示；完成、部分失败以最终响应为准，100% 不单独表示业务成功。 */
export function ScanProgressDisplay({ progress, pending, error, warning, label }: {
  progress?: ScanProgress; pending: boolean; error: boolean; warning: boolean; label: string;
}) {
  if (!progress) return null;
  const status = error ? "error" : !pending && warning ? "warning" : !pending ? "success" : "normal";
  const phase = error ? "处理失败" : !pending ? warning ? "处理结束，部分失败" : "处理完成"
    : progress.phase === "waiting" ? "等待处理" : progress.phase === "discovering" ? "扫描中，总数持续更新"
      : progress.phase === "finalizing" || progress.phase === "completed" ? "正在汇总结果" : "处理中";
  return (
    <div className="settings-scan-progress" aria-label={`${label}处理进度`}>
      <div className="settings-scan-progress-summary" role="status">
        <span>{phase}</span>
        <span>已处理 / 总数：{progress.processed} / {progress.total}</span>
        <span>百分比：{progress.percent}%</span>
      </div>
      <Progress percent={progress.percent} status={status} animation={pending} showText={false} aria-label={`${label}进度条`} />
      {pending && progress.current ? <span className="settings-scan-progress-current settings-field-description">{progress.current}</span> : null}
    </div>
  );
}
