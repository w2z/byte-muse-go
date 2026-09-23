import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../../shared/api/client";
import type { SystemStatus } from "../../shared/api/types";
import { PageState } from "../../shared/ui/PageState";

/** 系统状态读取 GET /system/status，调度器只展示是否运行。 */
export function SystemStatusPage() {
  const query = useQuery({
    queryKey: ["system-status"],
    queryFn: () => apiRequest<SystemStatus>("/system/status"),
  });

  return (
    <section>
      <div className="page-heading"><div><div className="eyebrow">系统 / HEALTH</div><h1>系统状态</h1><p className="page-description">查看核心服务、数据库和调度器的当前运行情况。</p></div></div>
      <PageState
        isLoading={query.isLoading}
        error={query.error}
        isEmpty={false}
        emptyText=""
        onRetry={() => void query.refetch()}
      >
        <div className="settings-grid"><div className="panel content-panel"><div className="panel-title">服务概览 <small>RUNTIME</small></div><dl className="info-list"><div className="info-row"><dt>应用版本</dt><dd>{query.data?.version}</dd></div><div className="info-row"><dt>调度器</dt><dd className={query.data?.scheduler_running ? "ok" : "off"}>{query.data?.scheduler_running ? "运行中" : "已停止"}</dd></div></dl></div><div className="panel content-panel"><div className="panel-title">运行环境 <small>ENVIRONMENT</small></div><dl className="info-list"><div className="info-row"><dt>数据库引擎</dt><dd>{query.data?.database_driver}</dd></div><div className="info-row"><dt>启动时间</dt><dd>{query.data?.started_at}</dd></div></dl></div></div>
      </PageState>
    </section>
  );
}
