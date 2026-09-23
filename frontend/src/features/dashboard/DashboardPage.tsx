import { useQuery } from "@tanstack/react-query";
import { API_BASE, apiRequest } from "../../shared/api/client";
import { useEffect, useState } from "react";
import type { Dashboard } from "../../shared/api/types";
import { PageState } from "../../shared/ui/PageState";

/** 仪表盘展示服务端汇总，不在前端计算订阅或下载状态。 */
export function DashboardPage() {
  const [eventState, setEventState] = useState<"connecting" | "connected" | "unavailable">("connecting");
  const query = useQuery({
    queryKey: ["dashboard"],
    queryFn: () => apiRequest<Dashboard>("/dashboard"),
  });
  useEffect(() => {
    if (typeof EventSource === "undefined") {
      setEventState("unavailable");
      return;
    }
    const source = new EventSource(API_BASE + "/events");
    const handleReady = () => setEventState("connected");
    const handleError = () => setEventState("unavailable");
    source.addEventListener("ready", handleReady);
    source.addEventListener("error", handleError);
    return () => {
      source.removeEventListener("ready", handleReady);
      source.removeEventListener("error", handleError);
      source.close();
    };
  }, []);

  return (
    <section>
      <div className="page-heading">
        <div><div className="eyebrow">总览 / OVERVIEW</div><h1 aria-label="仪表盘">你的媒体脉搏。</h1><p className="page-description">订阅、下载与媒体库都在这里保持同步。今天也有新的内容在路上。</p></div>
        <div className="toolbar-note">最后同步 / 刚刚</div>
      </div>
      <PageState
        isLoading={query.isLoading}
        error={query.error}
        isEmpty={false}
        emptyText=""
        onRetry={() => void query.refetch()}
      >
        <>
          <div className="stat-grid">
            <div className="panel stat-card"><div className="stat-label">媒体总量</div><div className="stat-value">{query.data?.media_count ?? 0}</div><div className="stat-meta active">库中已编目</div></div>
            <div className="panel stat-card"><div className="stat-label">活跃订阅</div><div className="stat-value">{query.data?.active_subscriptions ?? 0}</div><div className="stat-meta active">自动追踪中</div></div>
            <div className="panel stat-card"><div className="stat-label">完成下载</div><div className="stat-value">{query.data?.completed_downloads ?? 0}</div><div className="stat-meta">累计任务</div></div>
            <div className="panel stat-card"><div className="stat-label">健康集成</div><div className="stat-value">{query.data?.healthy_integrations ?? 0}</div><div className="stat-meta active">服务在线</div></div>
          </div>
          <div className="section-label">运行摘要</div>
          <div className="dashboard-grid">
          <div className="panel content-panel"><div className="panel-title">最近活动 <small>EVENTS</small></div><div className="empty-signal">{eventState === "connected" ? "事件流已连接，暂无新事件" : eventState === "connecting" ? "正在连接事件流" : "暂无新事件"}<span>{eventState === "unavailable" ? "当前环境未提供事件流连接。" : "服务端有新事件时会显示在这里。"}</span></div></div>
            <div className="panel content-panel"><div className="panel-title">集成摘要 <small>INTEGRATIONS</small></div><div className="health-score"><div className="score-ring">{query.data?.healthy_integrations ?? 0}</div><div className="health-copy"><strong>健康集成</strong><span>服务端返回的当前健康集成数量</span></div></div><div className="quick-link" onClick={() => window.location.assign("/status")}>查看系统状态 <span>↗</span></div><div className="quick-link" onClick={() => window.location.assign("/downloads")}>查看下载队列 <span>↗</span></div></div>
          </div>
        </>
      </PageState>
    </section>
  );
}
