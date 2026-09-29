import { Card } from "@arco-design/web-react";
import type { ReactNode } from "react";

type InfoCardProps = {
  /** 卡片主名称，例如标签名或厂牌名。 */
  title: ReactNode;
  /** 名称右侧的补充信息，例如分类或类型。 */
  badge?: ReactNode;
  /** 底部元信息行，例如影片数量。 */
  meta?: ReactNode;
  /** 卡片底部操作区。 */
  actions?: ReactNode;
  /** 操作区位置；end 将操作放在右侧并垂直居中。 */
  actionsPlacement?: "bottom" | "end";
};

/**
 * 键值型信息卡（标签、厂牌等）。
 *
 * 直接复用 Arco Card，规格对齐 Arco Pro /list/card：4px 圆角、1px 边框、无阴影，
 * Card body 四周 16px；内容纵向撑满整行高度，保证同一行底部对齐。
 */
export function InfoCard({ title, badge, meta, actions, actionsPlacement = "bottom" }: InfoCardProps) {
  return (
    <Card className="info-card" role="article" size="small" bordered>
      <div className={"info-card-body" + (actionsPlacement === "end" ? " info-card-body--end-actions" : "")}>
       <div className="info-card-content">
        <div className="info-card-head">
          <div className="info-card-title">{title}</div>
          {badge}
        </div>
        {meta}
       </div>
        {actions && <div className="info-card-actions">{actions}</div>}
      </div>
    </Card>
  );
}
