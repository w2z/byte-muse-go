type PageHeaderProps = {
  /** 页面主标题。只作为无障碍名称保留，不渲染任何可见内容。 */
  title: string;
  /** 需要让无障碍名称与可见文案不同时才传（例如看板显示口号但读作「仪表盘」）。 */
  titleLabel?: string;
};

/**
 * 页面标题（仅无障碍）。
 *
 * 内容区不渲染任何可见标题或工具栏：页面名由顶层面包屑 .app-breadcrumb 给出，
 * 原先挂在这里的计数与更新时间已按用户要求去掉。
 * 只保留一个 .sr-only h1，用于页面级无障碍名称和路由断言，不占布局高度。
 */
export function PageHeader({ title, titleLabel }: PageHeaderProps) {
  return (
    <h1 className="sr-only" aria-label={titleLabel}>
      {title}
    </h1>
  );
}
