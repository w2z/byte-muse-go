import { Card } from "@arco-design/web-react";
import type { ReactNode } from "react";

type InfoCardProps = {
  /** 卡片主名称，例如标签名或厂牌名。 */
  title: string;
  /** 名称右侧的补充信息，例如分类或类型。 */
  badge?: ReactNode;
  /** 底部元信息行，例如影片数量。 */
  meta?: ReactNode;
  /** 卡片底部操作区。 */
  actions?: ReactNode;
};

/**
 * 键值型信息卡（标签、厂牌等）。
 *
 * 直接复用 Arco Card，规格对齐 Arco Pro /list/card：4px 圆角、1px 边框、无阴影，
 * Card body 四周 16px；内容纵向撑满整行高度，保证同一行底部对齐。
 */
export function InfoCard({ title, badge, meta, actions }: InfoCardProps) {
  return (
    <Card className="info-card" role="article" size="small" bordered>
      <div className="info-card-body">
        <div className="info-card-head">
          <div className="info-card-title">{title}</div>
          {badge}
        </div>
        {meta}
        {actions}
      </div>
    </Card>
  );
}
