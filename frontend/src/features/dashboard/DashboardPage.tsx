import { Progress } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import {
  IconDownload,
  IconSettings,
  IconStar,
  IconThunderbolt,
  IconUser,
  IconVideoCamera,
} from "@arco-design/web-react/icon";
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { apiRequest } from "../../shared/api/client";
import type { Dashboard } from "../../shared/api/types";
import { useSession } from "../../shared/auth/session";
import { ContentCard } from "../../shared/ui/ContentCard";
import { PageHeader } from "../../shared/ui/PageHeader";
import { PageState } from "../../shared/ui/PageState";
import "./DashboardPage.css";

type ShortcutItem = { to: string; label: string; icon: ReactNode };

/** 快捷入口：只做站内跳转，不依赖后端数据。 */
const shortcuts: ShortcutItem[] = [
  { to: "/subscribe", label: "订阅管理", icon: <IconStar /> },
  { to: "/films", label: "媒体库", icon: <IconVideoCamera /> },
  { to: "/downloads", label: "下载任务", icon: <IconDownload /> },
  { to: "/actor", label: "演员", icon: <IconUser /> },
];

const recentShortcuts: ShortcutItem[] = [
  { to: "/task", label: "任务", icon: <IconThunderbolt /> },
  { to: "/settings", label: "设置", icon: <IconSettings /> },
];

const snapshotMetrics: Array<{ key: keyof Dashboard; label: string; unit: string }> = [
  { key: "media_count", label: "媒体总量", unit: "条" },
  { key: "active_subscriptions", label: "活跃订阅", unit: "个" },
  { key: "completed_downloads", label: "完成下载", unit: "次" },
];

/** Overview 卡里的单个统计项：54px 圆形图标 + 12px 标题 + 22px 数值 + 单位。 */
function StatisticItem({
  icon,
  title,
  count,
  unit,
}: {
  icon: ReactNode;
  title: string;
  count: number;
  unit: string;
}) {
  return (
    <div className="wp-stat">
      <div className="wp-stat-icon">{icon}</div>
      <div>
        <div className="wp-stat-title">{title}</div>
        <div className="wp-stat-count">
          {count}
          <span className="wp-stat-unit">{unit}</span>
        </div>
      </div>
    </div>
  );
}

/**
 * 看板页（侧栏菜单名「看板」，无障碍名称为「仪表盘」）。
 *
 * 排版对齐 Arco Design Pro 的工作台看板（react-pro.arco.design/dashboard/workplace）：
 * 内容区是「左主区 flex:1 + 右辅助区 280px」双栏，卡间距 16px；
 * 主区首块展示数据库汇总统计，下方是基于服务端数据库统计值的当前快照条形图；
 * 右栏只保留指向数据库业务页面的快捷入口。
 */
export function DashboardPage() {
  const user = useSession((state) => state.user);
  const query = useQuery({
    queryKey: ["dashboard"],
    queryFn: () => apiRequest<Dashboard>("/dashboard"),
  });

  const snapshot = snapshotMetrics.map((metric) => ({
    ...metric,
    value: query.data?.[metric.key] ?? 0,
  }));
  const snapshotMaximum = Math.max(0, ...snapshot.map((metric) => metric.value));

  return (
    <section>
      <PageHeader title="看板" titleLabel="仪表盘" />
      <PageState
        isLoading={query.isLoading}
        error={query.error}
        isEmpty={false}
        emptyText=""
        onRetry={() => void query.refetch()}
      >
        <div className="wp-layout">
          <div className="wp-main">
            <ContentCard className="wp-card">
              <div className="wp-card-title">欢迎回来，{user?.username ?? "访客"}</div>
              <hr className="wp-divider" />
              <div className="wp-stats wp-stats--three">
                <StatisticItem icon={<IconVideoCamera />} title="媒体总量" count={query.data?.media_count ?? 0} unit="条" />
                <span className="wp-stats-divider" aria-hidden="true" />
                <StatisticItem icon={<IconStar />} title="活跃订阅" count={query.data?.active_subscriptions ?? 0} unit="个" />
                <span className="wp-stats-divider" aria-hidden="true" />
                <StatisticItem icon={<IconDownload />} title="完成下载" count={query.data?.completed_downloads ?? 0} unit="次" />
              </div>
            </ContentCard>

            <ContentCard className="wp-card">
              <div className="wp-card-head wp-snapshot-head">
                <div>
                  <div className="wp-card-title">当前快照</div>
                  <div className="wp-snapshot-note">条形长度相对本组最大值，非趋势、非占比</div>
                </div>
              </div>
              <div className="wp-snapshot" aria-label="当前快照条形图">
                {snapshot.map((metric) => {
                  const normalizedValue = snapshotMaximum > 0 ? (metric.value / snapshotMaximum) * 100 : 0;
                  return (
                    <div className="wp-snapshot-row" key={metric.key}>
                      <div className="wp-snapshot-meta">
                        <span>{metric.label}</span>
                        <strong>
                          {metric.value}<small>{metric.unit}</small>
                        </strong>
                      </div>
                      <Progress
                        className="wp-snapshot-progress"
                        percent={normalizedValue}
                        showText={false}
                        strokeWidth={10}
                        aria-label={`${metric.label} ${metric.value}${metric.unit}`}
                      />
                    </div>
                  );
                })}
              </div>
            </ContentCard>
          </div>

          <div className="wp-side">
            <ContentCard className="wp-card">
              <div className="wp-card-head">
                <div className="wp-card-title">快捷入口</div>
                <Link className="wp-more" to="/settings">
                  自定义
                </Link>
              </div>
              <div className="wp-shortcuts">
                {shortcuts.map((shortcut) => (
                  <Link className="wp-shortcut" key={shortcut.to} to={shortcut.to}>
                    <span className="wp-shortcut-icon">{shortcut.icon}</span>
                    <span className="wp-shortcut-title">{shortcut.label}</span>
                  </Link>
                ))}
              </div>
              <hr className="wp-divider" />
              <div className="wp-recent">最近访问</div>
              <div className="wp-shortcuts">
                {recentShortcuts.map((shortcut) => (
                  <Link className="wp-shortcut" key={shortcut.to} to={shortcut.to}>
                    <span className="wp-shortcut-icon">{shortcut.icon}</span>
                    <span className="wp-shortcut-title">{shortcut.label}</span>
                  </Link>
                ))}
              </div>
            </ContentCard>

          </div>
        </div>
      </PageState>
    </section>
  );
}
