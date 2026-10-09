import { Breadcrumb, Button, Layout, Menu, Tooltip } from "@arco-design/web-react";
import { IconCalendar, IconDashboard, IconFile, IconFire, IconList, IconMenuFold, IconMenuUnfold, IconMoon, IconSearch, IconSettings, IconStar, IconSun, IconTags, IconThunderbolt, IconUser, IconVideoCamera } from "@arco-design/web-react/icon";
import { useLayoutEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Outlet, useLocation, useMatches, useNavigate } from "react-router-dom";
import { UNAUTHORIZED_EVENT, apiRequest } from "../shared/api/client";
import { VersionTag } from "../features/system/VersionTag";
import { useSession } from "../shared/auth/session";
import { useTheme } from "../shared/theme/theme";
import { PageSurface } from "../shared/ui/PageSurface";
import type { RouteMeta } from "./routes";

const contentItems = [
  { key: "/dashboard", label: "看板" },
  { key: "/subscribe", label: "订阅" },
  { key: "/actor", label: "演员" },
  { key: "/tag", label: "标签" },
  { key: "/release-today", label: "上新" },
  { key: "/recommend", label: "推荐" },
  { key: "/rank", label: "榜单" },
  { key: "/search", label: "搜索" },
  { key: "/films", label: "媒体库" },
  { key: "/all-films", label: "所有影片" },
  { key: "/downloads", label: "下载任务" },
];
const systemItems = [
  { key: "/settings", label: "设置" },
  { key: "/task", label: "任务" },
  { key: "/logs", label: "日志" },
];
const routeAliases: Record<string, string> = { "/subscriptions": "/subscribe", "/config": "/settings" };
function menuIcon(key: string) {
  if (key === "/dashboard") return <IconDashboard />;
  if (key === "/subscribe") return <IconStar />;
  if (key === "/actor") return <IconUser />;
  if (key === "/tag") return <IconTags />;
  if (key === "/release-today") return <IconCalendar />;
  if (key === "/recommend") return <IconFire />;
  if (key === "/rank") return <IconThunderbolt />;
  if (key === "/search") return <IconSearch />;
  if (key === "/films" || key === "/all-films") return <IconVideoCamera />;
  if (key === "/downloads") return <IconList />;
  if (key === "/settings") return <IconSettings />;
  if (key === "/task") return <IconThunderbolt />;
  if (key === "/logs") return <IconFile />;
  return <IconFile />;
}

/** 登录后的管理布局：桌面侧栏可折叠；手机端默认隐藏，由顶栏的单个按钮切换完整菜单。 */
export function AppLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const matches = useMatches();
  const clearSession = useSession((state) => state.clear);
  const themeMode = useTheme((state) => state.mode);
  const toggleTheme = useTheme((state) => state.toggle);
  const [collapsed, setCollapsed] = useState(false);
  const [isMobile, setIsMobile] = useState(() => window.matchMedia("(max-width: 760px)").matches);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const menuCollapsed = !isMobile && collapsed;
  useLayoutEffect(() => {
    const media = window.matchMedia("(max-width: 760px)");
    const updateViewport = () => {
      setIsMobile(media.matches);
      setMobileMenuOpen(false);
    };
    media.addEventListener("change", updateViewport);
    return () => media.removeEventListener("change", updateViewport);
  }, []);
  useLayoutEffect(() => {
    setMobileMenuOpen(false);
  }, [location.pathname]);
  const logout = useMutation({
    mutationFn: () => apiRequest<void>("/auth/logout", { method: "POST" }),
    onSettled: () => {
      clearSession();
      navigate("/login", { replace: true });
    },
  });
  const selectedKey = routeAliases[location.pathname] ?? location.pathname;
  // 页面标签、分组和白底内容块均由路由元数据驱动，避免布局层重复维护路径清单。
  const currentMeta = [...matches]
    .reverse()
    .map((match) => match.handle as RouteMeta | undefined)
    .find((handle) => handle?.label);
  const isWhiteSurface = currentMeta?.surface === "white";
  const contentClassName = (isWhiteSurface ? "app-content app-content--surface" : "app-content") + (currentMeta?.innerScroll ? " app-content--inner-scroll" : "");
  const isDark = themeMode === "dark";
  const themeToggleLabel = isDark ? "切换为亮色模式" : "切换为暗色模式";
  const collapseLabel = collapsed ? "展开菜单" : "折叠菜单";
  const mobileMenuLabel = mobileMenuOpen ? "关闭菜单" : "展开菜单";
  useLayoutEffect(() => {
    const handleUnauthorized = () => {
      clearSession();
      if (location.pathname !== "/login") navigate("/login", { replace: true });
    };
    window.addEventListener(UNAUTHORIZED_EVENT, handleUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, handleUnauthorized);
  }, [clearSession, location.pathname, navigate]);
  const renderItems = (items: typeof contentItems) => items.map((item) => (
    <Menu.Item key={item.key} aria-label={item.label}>
      <span className="nav-icon" aria-hidden="true">{menuIcon(item.key)}</span><span className="nav-label">{item.label}</span>
    </Menu.Item>
  ));
  const goHome = () => navigate("/dashboard");
  // 手机端点击当前菜单项也应收起，不能只依赖路由变化。
  const navigateFromMenu = (key: string) => {
    setMobileMenuOpen(false);
    navigate(key);
  };
  return (
    <Layout className="app-shell" onKeyDown={(event) => { if (event.key === "Escape") setMobileMenuOpen(false); }}>
      <Layout.Header className="app-header">
        <div className="header-brand">
          {isMobile && <Button className="icon-button" type="secondary" shape="circle" aria-label={mobileMenuLabel} aria-expanded={mobileMenuOpen} aria-controls="app-navigation" onClick={() => setMobileMenuOpen((open) => !open)}>{mobileMenuOpen ? <IconMenuFold /> : <IconMenuUnfold />}</Button>}
          <div className="brand-lockup" onClick={goHome} role="button" tabIndex={0} aria-label="返回看板" onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") goHome(); }}><span className="brand-mark">B<span>/</span>M</span><span className="brand-name">BYTEMUSE</span></div>
        </div>
        <div className="header-actions">
          <Tooltip content={themeToggleLabel}><Button className="icon-button" type="secondary" shape="circle" aria-label={themeToggleLabel} onClick={toggleTheme}>{isDark ? <IconSun /> : <IconMoon />}</Button></Tooltip>
          <Tooltip content="系统设置"><Button className="icon-button" type="secondary" shape="circle" aria-label="系统设置" onClick={() => navigate("/settings")}><IconSettings /></Button></Tooltip>
          <VersionTag />
          <Button className="logout-button" type="secondary" aria-label="退出登录" loading={logout.isPending} onClick={() => logout.mutate()}>退出登录</Button>
        </div>
      </Layout.Header>
      <Layout className="app-body">
        <Layout.Sider className={mobileMenuOpen ? "app-sider app-sider--mobile-open" : "app-sider"} width={240} collapsedWidth={64} collapsed={menuCollapsed} collapsible trigger={null} onCollapse={setCollapsed}>
          <nav id="app-navigation" aria-label="主导航" className="side-nav">
            {!menuCollapsed && <p className="nav-caption">通用</p>}
            <Menu collapse={menuCollapsed} theme="light" selectedKeys={[selectedKey]} onClickMenuItem={navigateFromMenu}>{renderItems(contentItems)}</Menu>
            {!menuCollapsed && <p className="nav-caption system-caption">系统</p>}
            <Menu collapse={menuCollapsed} selectedKeys={[selectedKey]} onClickMenuItem={navigateFromMenu}>{renderItems(systemItems)}</Menu>
          </nav>
          {!isMobile && <div className="sider-bottom">
            <Tooltip content={collapseLabel} position={collapsed ? "right" : "top"}>
              <button type="button" className="sider-collapse" aria-label={collapseLabel} aria-expanded={!collapsed} onClick={() => setCollapsed(!collapsed)}>
                {collapsed ? <IconMenuUnfold /> : <IconMenuFold />}
              </button>
            </Tooltip>
          </div>}
        </Layout.Sider>
        <Layout className="app-main">
          <Layout.Content className={contentClassName}>
            <div className="app-content-scroll">
              {currentMeta ? (
                <Breadcrumb className="app-breadcrumb" aria-label="内容导航">
                  <Breadcrumb.Item key="group">{currentMeta.group}</Breadcrumb.Item>
                  <Breadcrumb.Item key="page">{currentMeta.label}</Breadcrumb.Item>
                </Breadcrumb>
              ) : null}
              <div className="page-container">
                {isWhiteSurface ? <PageSurface fillHeight={currentMeta?.innerScroll}><Outlet /></PageSurface> : <Outlet />}
              </div>
            </div>
          </Layout.Content>
        </Layout>
      </Layout>
    </Layout>
  );
}
