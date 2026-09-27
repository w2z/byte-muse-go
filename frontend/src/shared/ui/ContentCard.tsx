import { Card } from "@arco-design/web-react";
import type { HTMLAttributes, ReactNode } from "react";

type ContentCardProps = {
  children: ReactNode;
  className?: string;
  role?: HTMLAttributes<HTMLDivElement>["role"];
};

/**
 * 页面正文的统一白色内容块。
 *
 * 统一复用 Arco Card：4px 圆角、1px 边框、无阴影、Card body 四周 16px。
 * 业务页面只能补充布局类，不得重新定义外壳圆角或四边内边距。
 */
export function ContentCard({ children, className = "", role }: ContentCardProps) {
  const classes = ["content-card", className].filter(Boolean).join(" ");
  return (
    <Card className={classes} role={role} bordered>
      {children}
    </Card>
  );
}
