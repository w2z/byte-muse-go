import { Navigate, type RouteObject } from "react-router-dom";
import { AppLayout } from "./AppLayout";
import { LoginPage } from "../features/auth/LoginPage";
import { DashboardPage } from "../features/dashboard/DashboardPage";
import { FilmListPage } from "../features/films/FilmListPage";
import { AllFilmsPage } from "../features/films/AllFilmsPage";
import { SubscriptionListPage } from "../features/subscriptions/SubscriptionListPage";
import { DownloadListPage } from "../features/downloads/DownloadListPage";
import { SettingsPage } from "../features/system/SettingsPage";
import { ActorListPage } from "../features/actors/ActorListPage";
import { TagListPage } from "../features/tags/TagListPage";
import { ReleaseTodayPage } from "../features/releases/ReleaseTodayPage";
import { RecommendPage } from "../features/recommend/RecommendPage";
import { RankPage } from "../features/rank/RankPage";
import { SearchPage } from "../features/search/SearchPage";
import { TaskListPage } from "../features/tasks/TaskListPage";
import { LogsPage } from "../features/system/LogsPage";
import { useSession } from "../shared/auth/session";

/**
 * 应用路由。登录页独立，其余页面共用管理布局。
 *
 * 每个菜单入口对应一个独立页面组件，不再用参数化的通用页面代替，
 * 便于各页面按业务差异排版，也避免一处调整影响全部入口。
 *
 * `handle.surface = "white"` 标记列表/卡片页：这些页面的内容区保持灰底，页面内容整体
 * 套进一个白色内容块（对齐 Arco Pro /list/card 的两层结构），由 AppLayout 读取后
 * 给 .app-content 加修饰类，具体规格见 styles.css 的 .app-content--surface。
 */
export type RouteMeta = {
  label: string;
  group: "通用" | "系统";
  surface?: "white";
  /** 页面自行管理列表滚动，外层内容区固定。 */
  innerScroll?: boolean;
};

/** 未恢复会话时不渲染管理壳，避免刷新瞬间出现受保护页面后再跳转登录页。 */
function ProtectedLayout() {
  const user = useSession((state) => state.user);
  return user ? <AppLayout /> : <Navigate to="/login" replace />;
}

/** 会话恢复后，已登录用户访问登录页时直接进入看板，避免重复显示登录表单。 */
function GuestLoginPage() {
  const user = useSession((state) => state.user);
  return user ? <Navigate to="/dashboard" replace /> : <LoginPage />;
}

const meta = (
  label: string,
  group: RouteMeta["group"],
  surface?: RouteMeta["surface"],
): RouteMeta => ({ label, group, surface });

export const routes: RouteObject[] = [
  { path: "/login", element: <GuestLoginPage /> },
  {
    path: "/",
    element: <ProtectedLayout />,
    children: [
      { index: true, element: <Navigate to="/dashboard" replace /> },
      { path: "dashboard", element: <DashboardPage />, handle: meta("看板", "通用") },
      { path: "films", element: <FilmListPage />, handle: meta("媒体库", "通用", "white") },
      { path: "all-films", element: <AllFilmsPage />, handle: meta("所有影片", "通用", "white") },
      { path: "subscriptions", element: <SubscriptionListPage />, handle: meta("订阅", "通用", "white") },
      { path: "subscribe", element: <SubscriptionListPage />, handle: meta("订阅", "通用", "white") },
      { path: "downloads", element: <DownloadListPage />, handle: meta("下载任务", "通用") },
      { path: "settings", element: <SettingsPage />, handle: meta("设置", "系统") },
      { path: "config", element: <SettingsPage />, handle: meta("设置", "系统") },
      { path: "actor", element: <ActorListPage />, handle: meta("演员", "通用", "white") },
      { path: "tag", element: <TagListPage />, handle: { ...meta("标签", "通用", "white"), innerScroll: true } },
      { path: "release-today", element: <ReleaseTodayPage />, handle: meta("上新", "通用", "white") },
      { path: "recommend", element: <RecommendPage />, handle: meta("推荐", "通用", "white") },
      { path: "rank", element: <RankPage />, handle: meta("榜单", "通用", "white") },
      { path: "search", element: <SearchPage />, handle: meta("搜索", "通用", "white") },
      { path: "task", element: <TaskListPage />, handle: meta("任务", "系统") },
      { path: "logs", element: <LogsPage />, handle: meta("日志", "系统") },
      { path: "*", element: <Navigate to="/dashboard" replace /> },
    ],
  },
];
