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

/** 订阅状态和类型共用控件；由页面决定查询与回到首页，组件只展示和回传选择。 */
export function CatalogFilters({ subscription, videoType, onSubscriptionChange, onVideoTypeChange }: {
  subscription: string; videoType: string;
  onSubscriptionChange: (value: string) => void; onVideoTypeChange: (value: string) => void;
}) {
  return <>
    <Grid.Col xs={24} sm={12} md={8} xl={4}>
      <div className="filter-labeled"><span className="filter-label">订阅状态</span><Select aria-label="订阅状态筛选" value={subscription} onChange={onSubscriptionChange} options={subscriptionOptions} /></div>
    </Grid.Col>
    <Grid.Col xs={24} sm={12} md={8} xl={4}>
      <div className="filter-labeled"><span className="filter-label">影片类型</span><Select aria-label="影片类型筛选" value={videoType} onChange={onVideoTypeChange} options={videoTypeOptions} /></div>
    </Grid.Col>
  </>;
}

/** 在分页参数后附加非空筛选，供上新、推荐及榜单使用。 */
export function catalogFilterParams(subscription: string, videoType: string): string {
  const params = new URLSearchParams();
  if (subscription) params.set("subscription", subscription);
  if (videoType) params.set("video_type", videoType);
  return params.size ? "&" + params.toString() : "";
}
