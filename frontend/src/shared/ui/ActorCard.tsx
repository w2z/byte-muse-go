import { Card } from "@arco-design/web-react";
import type { ReactNode } from "react";
import type { Actor } from "../api/types";

type ActorCardProps = {
  /** 服务端返回的演员条目。photo 为空时用姓名首字占位，不请求外部图片。 */
  actor: Actor;
  /** 卡片底部操作区，例如取消订阅。 */
  actions?: ReactNode;
};

/**
 * 演员卡。
 *
 * 复用 Arco Card：4px 圆角、1px 边框、无阴影，Card body 四周 16px。
 * 内部为左头像、右侧上名下元信息，底部放操作。姓名与订阅截止日期来自服务端。
 */
export function ActorCard({ actor, actions }: ActorCardProps) {
  return (
    <Card className="actor-card" role="article" size="small" bordered>
      <div className="actor-card-inner">
        <div className="actor-card-photo">
          {actor.photo ? <img src={actor.photo} alt="" loading="lazy" /> : null}
        </div>
        <div className="actor-card-body">
          <div>
            <div className="actor-card-name">{actor.name}</div>
            <div className="actor-card-meta">订阅截止 {actor.limit_date ?? "未设置"}</div>
          </div>
          {actions}
        </div>
      </div>
    </Card>
  );
}
