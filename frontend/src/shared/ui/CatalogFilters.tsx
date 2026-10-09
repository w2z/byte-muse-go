import { Grid, Select } from "@arco-design/web-react";

/** 四个影片列表共用订阅筛选值，与 /media 的查询语义一致。 */
export const subscriptionOptions = [
  { label: "全部订阅", value: "" }, { label: "已订阅", value: "active" }, { label: "未订阅", value: "none" },
];

/** 未分类以 unknown 查询数据库 NULL，不生成额外的影片类型。 */
export const videoTypeOptions = [
  { label: "全部类型", value: "" }, { label: "有码", value: "censored" },
  { label: "无码", value: "uncensored" }, { label: "无码破解", value: "uncensored_cracked" },
  { label: "流出", value: "leaked" }, { label: "未分类", value: "unknown" },
];

/** 共用订阅、类型及可选 VR 筛选；由页面负责查询和重置页码。 */
export function CatalogFilters({ subscription, videoType, vr = "", onVRChange, onSubscriptionChange, onVideoTypeChange }: {
  subscription: string; videoType: string; vr?: string; onVRChange?: (value: string) => void;
  onSubscriptionChange: (value: string) => void; onVideoTypeChange: (value: string) => void;
}) {
  return <>
    <Grid.Col xs={24} sm={12} md={8} xl={4}>
      <div className="filter-labeled"><span className="filter-label">订阅状态</span><Select aria-label="订阅状态筛选" value={subscription} onChange={onSubscriptionChange} options={subscriptionOptions} /></div>
    </Grid.Col>
    <Grid.Col xs={24} sm={12} md={8} xl={4}>
      <div className="filter-labeled"><span className="filter-label">影片类型</span><Select aria-label="影片类型筛选" value={videoType} onChange={onVideoTypeChange} options={videoTypeOptions} /></div>
    </Grid.Col>
    {onVRChange && <Grid.Col xs={24} sm={12} md={8} xl={4}>
      <div className="filter-labeled"><span className="filter-label">VR</span><Select aria-label="VR筛选" value={vr} onChange={onVRChange} options={[{ label: "全部影片", value: "" }, { label: "隐藏VR影片", value: "hide" }, { label: "只显示VR影片", value: "only" }]} /></div>
    </Grid.Col>}
  </>;
}

/** 在分页参数后附加非空筛选，供上新、推荐及榜单使用。 */
export function catalogFilterParams(subscription: string, videoType: string, vr = ""): string {
  const params = new URLSearchParams();
  if (subscription) params.set("subscription", subscription);
  if (videoType) params.set("video_type", videoType);
  if (vr) params.set("vr", vr);
  return params.size ? "&" + params.toString() : "";
}
