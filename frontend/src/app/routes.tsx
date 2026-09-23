import { Navigate, type RouteObject } from "react-router-dom";
import { AppLayout } from "./AppLayout";
import { LoginPage } from "../features/auth/LoginPage";
import { DashboardPage } from "../features/dashboard/DashboardPage";
import { FilmListPage } from "../features/films/FilmListPage";
import { SubscriptionListPage } from "../features/subscriptions/SubscriptionListPage";
import { DownloadListPage } from "../features/downloads/DownloadListPage";
import { SystemStatusPage } from "../features/system/SystemStatusPage";
import { SettingsPage } from "../features/system/SettingsPage";
import { ActorListPage } from "../features/actors/ActorListPage";
import { CompatibilityPage } from "../features/legacy/CompatibilityPage";

/** 第一阶段路由。登录页独立，其余页面共用管理布局。 */
export const routes: RouteObject[] = [
  { path: "/login", element: <LoginPage /> },
  {
    path: "/",
    element: <AppLayout />,
    children: [
      { index: true, element: <Navigate to="/dashboard" replace /> },
      { path: "dashboard", element: <DashboardPage /> },
      { path: "films", element: <FilmListPage /> },
      { path: "subscriptions", element: <SubscriptionListPage /> },
      { path: "subscribe", element: <SubscriptionListPage /> },
      { path: "downloads", element: <DownloadListPage /> },
      { path: "status", element: <SystemStatusPage /> },
      { path: "settings", element: <SettingsPage /> },
      { path: "config", element: <SettingsPage /> },
      { path: "actor", element: <ActorListPage /> },
      { path: "tag", element: <CompatibilityPage title="标签" eyebrow="通用 / TAGS" endpoint="/tags" description="读取标签数据。" /> },
      { path: "release-today", element: <CompatibilityPage title="上新" eyebrow="通用 / RELEASES" endpoint="/codes/release_today" description="读取今日发行内容。" /> },
      { path: "recommend", element: <CompatibilityPage title="推荐" eyebrow="通用 / RECOMMENDATIONS" endpoint="/codes/recommend" description="读取推荐内容。" /> },
      { path: "rank", element: <CompatibilityPage title="榜单" eyebrow="通用 / RANKINGS" endpoint="/ranks?type=monthly" description="读取榜单内容。" /> },
      { path: "brands", element: <CompatibilityPage title="厂牌" eyebrow="通用 / BRANDS" endpoint="/brands" description="读取厂牌数据。" /> },
      { path: "search", element: <CompatibilityPage title="搜索" eyebrow="通用 / SEARCH" endpoint="/complex/search?q=" description="搜索媒体和资源。" /> },
      { path: "profile", element: <CompatibilityPage title="账户" eyebrow="系统 / ACCOUNT" endpoint="/profile" description="读取当前账户。" /> },
      { path: "task", element: <CompatibilityPage title="任务" eyebrow="系统 / TASKS" endpoint="/tasks" description="读取任务列表。" /> },
      { path: "logs", element: <CompatibilityPage title="日志" eyebrow="系统 / LOGS" endpoint="/logs" description="读取系统日志。" /> },
      { path: "notice", element: <CompatibilityPage title="注意" eyebrow="系统 / NOTICE" endpoint="/notice" description="读取通知中心。" /> },
      { path: "*", element: <Navigate to="/dashboard" replace /> },
    ],
  },
];
