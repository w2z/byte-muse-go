import { Avatar, Breadcrumb, Button, Layout, Menu, Tag, Tooltip } from "@arco-design/web-react";
import { IconApps, IconCalendar, IconDashboard, IconFile, IconFire, IconList, IconMenuFold, IconMenuUnfold, IconNotification, IconSearch, IconSettings, IconStar, IconStorage, IconTag, IconThunderbolt, IconUser, IconVideoCamera } from "@arco-design/web-react/icon";
import { useLayoutEffect, useMemo, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { UNAUTHORIZED_EVENT } from "../shared/api/client";
import { useSession } from "../shared/auth/session";

const contentItems = [
  { key: "/dashboard", label: "看板" },
  { key: "/subscribe", label: "订阅" },
  { key: "/actor", label: "演员" },
  { key: "/tag", label: "标签" },
  { key: "/release-today", label: "上新" },
  { key: "/recommend", label: "推荐" },
  { key: "/rank", label: "榜单" },
  { key: "/brands", label: "厂牌" },
  { key: "/search", label: "搜索" },
  { key: "/films", label: "媒体库" },
  { key: "/downloads", label: "下载任务" },
];
const systemItems = [
  { key: "/profile", label: "账户" },
  { key: "/settings", label: "设置" },
  { key: "/task", label: "任务" },
  { key: "/logs", label: "日志" },
  { key: "/notice", label: "注意" },
  { key: "/status", label: "系统状态" },
];
const allItems = [...contentItems, ...systemItems];
const routeAliases: Record<string, string> = { "/subscriptions": "/subscribe", "/config": "/settings" };
function menuIcon(key: string) {
  if (key === "/dashboard") return <IconDashboard />;
  if (key === "/subscribe") return <IconStar />;
  if (key === "/actor") return <IconUser />;
  if (key === "/tag") return <IconTag />;
  if (key === "/release-today") return <IconCalendar />;
  if (key === "/recommend") return <IconFire />;
  if (key === "/rank") return <IconThunderbolt />;
  if (key === "/brands") return <IconApps />;
  if (key === "/search") return <IconSearch />;
  if (key === "/films") return <IconVideoCamera />;
  if (key === "/downloads") return <IconList />;
  if (key === "/profile") return <IconUser />;
  if (key === "/settings") return <IconSettings />;
  if (key === "/task") return <IconThunderbolt />;
  if (key === "/logs") return <IconFile />;
  if (key === "/notice") return <IconNotification />;
  if (key === "/status") return <IconStorage />;
  return <IconFile />;
}

/** 登录后的管理布局，统一管理分组导航、页面层级和未授权跳转。 */
export function AppLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const clearSession = useSession((state) => state.clear);
  const user = useSession((state) => state.user);
  const [collapsed, setCollapsed] = useState(false);
  const selectedKey = routeAliases[location.pathname] ?? location.pathname;
  const currentItem = useMemo(() => allItems.find((item) => item.key === selectedKey), [selectedKey]);
  const parentLabel = contentItems.some((item) => item.key === selectedKey) ? "通用" : "系统";
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
  return (
    <Layout className="app-shell">
      <Layout.Sider className="app-sider" width={240} collapsedWidth={64} collapsed={collapsed} onCollapse={setCollapsed} breakpoint="xl" trigger={null}>
        <div className="brand-lockup" onClick={() => navigate("/dashboard")} role="button" tabIndex={0} aria-label="返回看板" onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") navigate("/dashboard"); }}><span className="brand-mark">B<span>/</span>M</span>{!collapsed && <span className="brand-name">BYTEMUSE</span>}</div>
        <nav aria-label="主导航" className="side-nav">
          {!collapsed && <p className="nav-caption">通用</p>}
          <Menu collapse={collapsed} theme="light" selectedKeys={[selectedKey]} onClickMenuItem={(key) => navigate(key)}>{renderItems(contentItems)}</Menu>
          {!collapsed && <p className="nav-caption system-caption">系统</p>}
          <Menu collapse={collapsed} selectedKeys={[selectedKey]} onClickMenuItem={(key) => navigate(key)}>{renderItems(systemItems)}</Menu>
        </nav>
        {!collapsed && <div className="sider-footer"><div className="sider-footer-version">BYTEMUSE / 0.1.0</div></div>}
      </Layout.Sider>
      <Layout className="app-main">
        <Layout.Header className="app-header">
          <div className="header-left"><Tooltip content={collapsed ? "展开菜单" : "收起菜单"}><Button type="text" className="collapse-button" aria-label={collapsed ? "展开菜单" : "收起菜单"} onClick={() => setCollapsed((value) => !value)}>{collapsed ? <IconMenuUnfold /> : <IconMenuFold />}</Button></Tooltip><div className="header-context"><span className="header-kicker">{parentLabel}</span><span className="header-divider">/</span><strong>{currentItem?.label ?? "页面未找到"}</strong></div></div>
          <div className="header-actions"><Tag className="user-pill">{user?.username ?? "访客"}</Tag><Button className="avatar-button" shape="circle" aria-label="账户" onClick={() => navigate("/profile")}><Avatar size={28}>{(user?.username ?? "访客").slice(0, 1).toUpperCase()}</Avatar></Button></div>
        </Layout.Header>
        <Layout.Content className="app-content"><div className="page-container"><div className="pro-breadcrumb"><Breadcrumb><Breadcrumb.Item key="brand">ByteMuse</Breadcrumb.Item><Breadcrumb.Item key="group">{parentLabel}</Breadcrumb.Item><Breadcrumb.Item key="page">{currentItem?.label ?? "页面未找到"}</Breadcrumb.Item></Breadcrumb></div><Outlet /></div></Layout.Content>
      </Layout>
    </Layout>
  );
}
