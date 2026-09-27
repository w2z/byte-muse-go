import { Card } from "@arco-design/web-react";
import type { ReactNode } from "react";

type PageSurfaceProps = {
  children: ReactNode;
};

/**
 * 列表/卡片页的白色正文块。
 *
 * 直接复用 Arco Card，而不是用普通 div 模拟。外层 Card 负责 4px 圆角与白底，
 * 内部统一使用 Arco Pro 默认卡片内边距 20px。页面不得自行覆盖四边间距。
 */
export function PageSurface({ children }: PageSurfaceProps) {
  return (
    <Card className="page-surface-card" bordered={false}>
      {children}
    </Card>
  );
}
