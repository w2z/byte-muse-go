import { Button, Card, Descriptions, Divider, Image, Tag } from "@arco-design/web-react";
import dayjs from "dayjs";
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

/**
 * 图片不可显示时的统一占位：无图模式与缺少图片地址共用同一表现，
 * 只渲染图标占位，不请求真实图片，保证卡片与详情版式稳定。
 */
function ImagePlaceholder({ className, label }: { className: string; label: string }) {
  return (
    <div className={"code-card-image-placeholder " + className} role="img" aria-label={label}>
      <IconImage />
    </div>
  );
}

type CodeCardProps = {
  /** 服务端返回的媒体条目，卡片只展示不做业务状态推导。 */
  media: Media;
  /** 额外元信息行，例如榜单名次或媒体库状态。 */
  meta?: ReactNode;
  /** 页面注入的业务操作，例如订阅、取消订阅和编辑。 */
  actions?: ReactNode;
  /** 隐藏整个操作区（剧照、预告和页面注入的业务按钮），默认显示。 */
  hideActions?: boolean;
  /** 隐藏番号旁的业务状态标签，默认显示。 */
  hideStatus?: boolean;
  /** card 显示列表卡片；detail 只显示封面图及分组资料，不渲染卡片与操作。 */
  variant?: "card" | "detail";
  /** 需要打开详情时传入。 */
  onSelect?: () => void;
};

/**
 * 所有番号页共用的媒体卡，统一大图、复制、状态、剧照和预告行为。
 *
 * 内容区版式对齐对标站卡片：番号行（大号粗体番号 + 复制 + 状态标签）→ 发售日期 → 页面级 meta → 标题 → 操作按钮，
 * 底部按钮右对齐。番号只从服务端字段取，卡片不做任何业务状态推导；订阅状态由 display_status 决定。
 */
export function CodeCard({ media, meta, actions, onSelect, hideActions = false, hideStatus = false, variant = "card" }: CodeCardProps) {
  const showDetails = variant === "detail";
  const actionsHidden = hideActions || showDetails;
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
  useEffect(() => {
    if (actionsHidden) { setStillsVisible(false); setPreviewVisible(false); }
  }, [actionsHidden]);
  const title = media.translated_title || media.title;
  const coverURL = media.banner_url || media.poster_url;
  const stills = media.still_photos ?? [];
  const status: MediaDisplayStatus = media.display_status ?? (media.subscription_status === "active" ? "subscribed" : "unsubscribed");
  const statusView = displayStatus[status];
  const titleNode = onSelect
    ? <button type="button" className="code-card-title code-card-title-link" onClick={onSelect}>{title}</button>
    : <div className="code-card-title">{title}</div>;
  const details = media.details;
  const missing = "暂无";
  /** 保留零值和 false 的真实含义，不用真假判断替代缺失判断。 */
  const yesNo = (value: boolean | null | undefined) => value == null ? missing : value ? "是" : "否";
  const detailImage = (url?: string | null) => showImages && url
    ? <Image className={imageClassName} src={url} alt="影片资料图片" width={160} style={{ maxWidth: "100%" }} preview={imageMode === "VISIBLE"} />
    : <ImagePlaceholder className="code-card-detail-image-placeholder" label="影片资料占位图" />;
  const detailColumns = { xs: 1, sm: 2, md: 2, lg: 2, xl: 2, xxl: 2, xxxl: 2 };

  /** 卡片从第一张打开，详情缩略图从选中项打开，共用切换和底部缩略图条。 */
  function openStills(index = 0) {
    setCurrentStill(index);
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
      {showDetails ? (showImages && coverURL
        ? <Image className={imageClassName} src={coverURL} alt={media.code + " 封面"} width="100%" style={{ display: "block" }} preview={imageMode === "VISIBLE"} />
        : <ImagePlaceholder className="code-card-detail-cover-placeholder" label={media.code + " 封面占位图"} />) : <Card
        className="code-card"
        role="article"
        size="small"
        bordered
        cover={<div className="code-card-cover">{showImages && coverURL
          ? <img className={imageClassName} src={coverURL} alt={media.code + " 封面"} loading="lazy" />
          : <ImagePlaceholder className="code-card-cover-placeholder" label={media.code + " 封面占位图"} />}</div>}
      >
        <div className="code-card-body">
          <div className="code-card-code-row">
            <span className="code-cell">{media.code}</span>
            <Button type="text" icon={<IconCopy />} aria-label={"复制番号 " + media.code} onClick={() => void copyCode()} />
            {!hideStatus ? <Tag className={"state-tag " + statusView.className}>{statusView.text}</Tag> : null}
          </div>
          {media.release_date ? <span className="code-card-meta">发售日期: {media.release_date}</span> : null}
          {meta}
          {titleNode}
          {!actionsHidden ? <div className="code-card-actions">
            {showImages && stills.length > 0 ? <Button icon={<IconImage />} onClick={() => openStills()}>剧照</Button> : null}
            {media.preview_url ? <Button icon={<IconPlayArrow />} onClick={() => setPreviewVisible(true)}>预告</Button> : null}
            {actions}
          </div> : null}
        </div>
      </Card>}
      {showDetails ? <div style={{ marginTop: 24, overflowWrap: "anywhere" }}>
        <Descriptions title="基本信息" column={detailColumns} layout="inline-horizontal" data={[
          { label: "订阅时间", value: media.active_subscription?.created_at ? dayjs(media.active_subscription.created_at).format("YYYY-MM-DD HH:mm:ss") : missing },
          { label: "番号", value: media.code },
          { label: "发行码", value: details?.release_code || missing },
          { label: "发行日期", value: media.release_date || missing },
          { label: "时长", value: media.duration_minutes == null ? missing : media.duration_minutes + " 分钟" },
          { label: "翻译引擎", value: details?.translation_engine || missing },
        ]} />
        <Divider />
        <Descriptions title="影片内容" column={1} layout="inline-horizontal" data={[
          { label: "标题", value: title || missing },
          { label: "原标题", value: media.title || missing },
          { label: "演员", value: details?.actors?.join("、") || missing },
          { label: "简介", value: details?.plot || missing },
          { label: "标签", value: details?.tags?.join("、") || missing },
        ]} />
        <Divider />
        <Descriptions title="制作与发行" column={detailColumns} layout="inline-horizontal" data={[
          { label: "导演", value: details?.director || missing },
          { label: "制作", value: details?.producer || missing },
          { label: "发行商", value: details?.publisher || missing },
          { label: "系列", value: details?.series || missing },
        ]} />
        <Divider />
        <Descriptions title="评分与规格" column={detailColumns} layout="inline-horizontal" data={[
          { label: "评分", value: details?.rating ?? missing },
          { label: "想看人数", value: details?.want_count ?? missing },
          { label: "马赛克", value: yesNo(details?.mosaic) },
          { label: "有码", value: yesNo(details?.censored) },
          { label: "分辨率", value: details?.resolution || missing },
        ]} />
        <Divider />
        <Descriptions title="图片资料" column={detailColumns} layout="inline-horizontal" data={[
          { label: "封面", value: detailImage(media.banner_url) },
          { label: "海报", value: detailImage(media.poster_url) },
        ]} />
        {showImages && stills.length > 0 ? <>
          <Divider />
          <Descriptions title="剧照" column={1} data={[]} />
          {/* 剧照缩略图统一 16:9 裁切后向下换行；点某一张即从该张进入受控预览组。
              模糊模式只保留缩略图展示，不提供放大入口，避免绕过图片设置看到原图。 */}
          <div className="code-card-still-grid">
            {stills.map((src, index) => {
              const thumb = <img className={imageClassName} src={src} alt={media.code + " 剧照 " + (index + 1)} loading="lazy" />;
              return imageMode === "VISIBLE"
                ? <button key={src + "-" + index} type="button" className="code-card-still-cell" aria-label={"放大第 " + (index + 1) + " 张剧照"} onClick={() => openStills(index)}>{thumb}</button>
                : <span key={src + "-" + index} className="code-card-still-cell">{thumb}</span>;
            })}
          </div>
        </> : null}
      </div> : null}
      {/* 剧照不经过弹窗：点按钮直接把 Arco Image 预览（ImagePreviewGroup）拉起来，从第一张开始看。
          预览层底部的缩略图条用它的 extra 插槽注入，点缩略图切换 current，与左右箭头、键盘切换共用同一状态。 */}
      {(!actionsHidden || (showDetails && imageMode === "VISIBLE")) && showImages && stillsVisible ? <Image.PreviewGroup
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
                  aria-pressed={index === currentStill}
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
      <AppDialog title={media.code + " 预告"} visible={!actionsHidden && previewVisible} onClose={() => setPreviewVisible(false)}>
        {media.preview_url ? <MediaPlayer url={media.preview_url} poster={showImages && imageMode === "VISIBLE" ? coverURL : undefined} /> : null}
      </AppDialog>
    </>
  );
}
