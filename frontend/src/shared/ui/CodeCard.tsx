import { Button, Card, Image, Tag } from "@arco-design/web-react";
import { IconCopy, IconImage, IconPlayArrow } from "@arco-design/web-react/icon";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { apiRequest } from "../api/client";
import type { Media, MediaDisplayStatus, SystemSettings } from "../api/types";
import { MediaPlayer } from "./MediaPlayer";
import { DragScrollRow } from "./DragScrollRow";
import { useFeedbackMessage } from "./FeedbackMessage";
import { AppDialog } from "./AppDialog";

const displayStatus = {
  unsubscribed: { text: "未订阅", className: "idle" },
  subscribed: { text: "已订阅", className: "active" },
  downloading: { text: "下载中", className: "warn" },
  completed: { text: "已完成", className: "active" },
  failed: { text: "失败", className: "danger" },
  unknown: { text: "未知", className: "warn" },
} as const;

type CodeCardProps = {
  /** 服务端返回的媒体条目，卡片只展示不做业务状态推导。 */
  media: Media;
  /** 额外元信息行，例如榜单名次或媒体库状态。 */
  meta?: ReactNode;
  /** 页面注入的业务操作，例如订阅、取消订阅和编辑。 */
  actions?: ReactNode;
  /** 需要打开详情时传入。 */
  onSelect?: () => void;
};

/**
 * 所有番号页共用的媒体卡，统一大图、复制、状态、剧照和预告行为。
 *
 * 内容区版式对齐对标站卡片：番号行（大号粗体番号 + 复制 + 状态标签）→ 发售日期 → 页面级 meta → 标题 → 操作按钮，
 * 底部按钮右对齐。番号只从服务端字段取，卡片不做任何业务状态推导；订阅状态由 display_status 决定。
 */
export function CodeCard({ media, meta, actions, onSelect }: CodeCardProps) {
  // 与设置页共用缓存：保存即生效，请求由 React Query 合并，不为每张卡片单独请求。
  const settings = useQuery({
    queryKey: ["system-settings"],
    queryFn: () => apiRequest<SystemSettings>("/system/settings"),
  });
  const imageMode = settings.data?.values.IMAGE_MODE;
  // 未加载、读取失败或未知值均不加载图片，避免默认显示导致无图模式短暂泄露封面。
  const showImages = !settings.isError && (imageMode === "VISIBLE" || imageMode === "BLUR");
  const imageClassName = imageMode === "BLUR" ? "code-card-image-blurred" : undefined;
  const [message, messageHolder] = useFeedbackMessage();
  const [stillsVisible, setStillsVisible] = useState(false);
  const [currentStill, setCurrentStill] = useState(0);
  const [previewVisible, setPreviewVisible] = useState(false);
  useEffect(() => {
    if (!showImages) setStillsVisible(false);
  }, [showImages]);
  const title = media.translated_title || media.title;
  const coverURL = media.banner_url || media.poster_url;
  const stills = media.still_photos ?? [];
  const status: MediaDisplayStatus = media.display_status ?? (media.subscription_status === "active" ? "subscribed" : "unsubscribed");
  const statusView = displayStatus[status];
  const titleNode = onSelect
    ? <button type="button" className="code-card-title code-card-title-link" onClick={onSelect}>{title}</button>
    : <div className="code-card-title">{title}</div>;

  /** 打开剧照预览：每次从第一张开始，不记住上次看到的位置。 */
  function openStills() {
    setCurrentStill(0);
    setStillsVisible(true);
  }

  async function copyCode() {
    try {
      await navigator.clipboard.writeText(media.code);
      message.success?.("番号已复制");
    } catch {
      message.error?.("番号复制失败");
    }
  }

  return (
    <>
      {messageHolder}
      <Card
        className="code-card"
        role="article"
        size="small"
        bordered
        cover={showImages ? <div className="code-card-cover">{coverURL ? <img className={imageClassName} src={coverURL} alt={media.code + " 封面"} loading="lazy" /> : null}</div> : undefined}
      >
        <div className="code-card-body">
          <div className="code-card-code-row">
            <span className="code-cell">{media.code}</span>
            <Button type="text" icon={<IconCopy />} aria-label={"复制番号 " + media.code} onClick={() => void copyCode()} />
            <Tag className={"state-tag " + statusView.className}>{statusView.text}</Tag>
          </div>
          {media.release_date ? <span className="code-card-meta">发售日期: {media.release_date}</span> : null}
          {meta}
          {titleNode}
          <div className="code-card-actions">
            {showImages && stills.length > 0 ? <Button icon={<IconImage />} onClick={openStills}>剧照</Button> : null}
            {media.preview_url ? <Button icon={<IconPlayArrow />} onClick={() => setPreviewVisible(true)}>预告</Button> : null}
            {actions}
          </div>
        </div>
      </Card>
      {/* 剧照不经过弹窗：点按钮直接把 Arco Image 预览（ImagePreviewGroup）拉起来，从第一张开始看。
          预览层底部的缩略图条用它的 extra 插槽注入，点缩略图切换 current，与左右箭头、键盘切换共用同一状态。 */}
      {showImages && stillsVisible ? <Image.PreviewGroup
        imgAttributes={{ className: imageClassName }}
        srcList={stills}
        visible={stillsVisible}
        onVisibleChange={setStillsVisible}
        current={currentStill}
        onChange={setCurrentStill}
        infinite
        extra={
          stills.length > 1 ? (
            <DragScrollRow className="code-card-still-thumbs">
              {stills.map((src, index) => (
                <button
                  key={src + "-" + index}
                  type="button"
                  className={index === currentStill ? "code-card-still-thumb code-card-still-thumb--active" : "code-card-still-thumb"}
                  aria-label={"查看第 " + (index + 1) + " 张剧照"}
                  onClick={() => setCurrentStill(index)}
                >
                  <img className={imageClassName} src={src} alt="" />
                </button>
              ))}
            </DragScrollRow>
          ) : null
        }
      /> : null}
      {/* 预告仍用弹窗 + 播放器；剧照改由上面的 Image.PreviewGroup 直接展示。 */}
      <AppDialog title={media.code + " 预告"} visible={previewVisible} onClose={() => setPreviewVisible(false)}>
        {media.preview_url ? <MediaPlayer url={media.preview_url} poster={showImages && imageMode === "VISIBLE" ? coverURL : undefined} /> : null}
      </AppDialog>
    </>
  );
}
