import type { ReactNode } from "react";
import type { Media } from "../api/types";
import { CodeCard } from "./CodeCard";
import { ListPagination } from "./ListPagination";
import { MediaSubscriptionActions } from "./MediaSubscriptionActions";
import { PageState } from "./PageState";

/** 列表查询状态：只取页面 useQuery 结果里与本组件有关的三项，页面直接传 query 即可。 */
export type MediaCardGridQuery = {
  isLoading: boolean;
  error: Error | null;
  refetch: () => unknown;
};

type MediaCardGridProps<T> = {
  /** 当前页列表项，顺序即服务端返回顺序（名次按它换算）。 */
  items: T[];
  query: MediaCardGridQuery;
  /** 空数据提示文案。 */
  emptyText: string;
  total: number;
  page: number;
  pageSize: number;
  onPageChange: (page: number) => void;
  /** 必须同时把页码重置为 1，否则会请求到越界页。 */
  onPageSizeChange: (pageSize: number) => void;
  /** 网格列数规格：默认 1/2/3/4/5 列；wide 在 1024px 提前到 5 列（榜单页）。 */
  columns?: "default" | "wide";
  /** 列表项主键，默认取 item.id。 */
  itemKey?: (item: T, index: number) => string;
  /** 把列表项投影成卡片需要的媒体对象；返回 null 时该项不渲染（例如缺少媒体投影）。 */
  toMedia?: (item: T, index: number) => Media | null;
  /** 卡片 meta 槽：名次、订阅模式、媒体库状态等页面级补充信息。 */
  renderMeta?: (item: T, index: number) => ReactNode;
  /** 需要打开详情时传入，回调参数里的 index 是列表项在 items 中的原始下标。 */
  onSelect?: (media: Media, item: T, index: number) => void;
  /** 覆盖“空列表”的判定，例如搜索页尚未提交关键字时传 true，只显示空态。 */
  empty?: boolean;
  /** 为 false 时不渲染分页条（例如尚未发起查询的搜索页）。 */
  paginated?: boolean;
};

function defaultItemKey<T>(item: T, index: number): string {
  const id = (item as { id?: unknown }).id;
  return id === undefined || id === null ? String(index) : String(id);
}

function defaultToMedia<T>(item: T): Media | null {
  return item as unknown as Media;
}

/**
 * 番号卡片网格。
 *
 * 所有番号页（订阅、上新、推荐、榜单、影片、搜索）的列表正文只有数据源、空态文案和卡片补充信息不同，
 * 卡片、响应式列数、加载/失败/空态、分页条行为完全一致，因此统一收在这里：
 * 页面只负责请求、分页状态和 meta，网格、状态位和分页条不再各自实现。
 *
 * 列数规格来自公共样式 .card-grid（1/2/3/4/5 列，间距 16px），卡片复用公共 CodeCard，
 * 订阅操作复用 MediaSubscriptionActions，分页条复用 ListPagination，本组件不重复定义它们的样式与行为。
 * 组件不推导任何业务语义：订阅状态由服务端 display_status 决定，名次、模式等由页面通过 renderMeta 注入。
 */
export function MediaCardGrid<T>({
  items,
  query,
  emptyText,
  total,
  page,
  pageSize,
  onPageChange,
  onPageSizeChange,
  columns = "default",
  itemKey = defaultItemKey,
  toMedia = defaultToMedia,
  renderMeta,
  onSelect,
  empty,
  paginated = true,
}: MediaCardGridProps<T>) {
  const cards: { item: T; index: number; media: Media }[] = [];
  items.forEach((item, index) => {
    const media = toMedia(item, index);
    if (media) cards.push({ item, index, media });
  });

  // index 保留在 items 里的原始下标，弹窗与名次换算都基于服务端返回顺序，不受投影过滤影响。
  const isEmpty = empty ?? (!query.isLoading && !query.error && cards.length === 0);
  const pager = paginated ? (
    <ListPagination plain page={page} total={total} pageSize={pageSize} onChange={onPageChange} onPageSizeChange={onPageSizeChange} />
  ) : null;

  return (
    <PageState
      isLoading={query.isLoading}
      error={query.error}
      isEmpty={isEmpty}
      emptyText={emptyText}
      onRetry={() => void query.refetch()}
      emptyExtra={pager}
    >
      <div className={columns === "wide" ? "card-grid card-grid--wide" : "card-grid"}>
        {cards.map(({ item, index, media }) => (
          <CodeCard
            key={itemKey(item, index)}
            media={media}
            meta={renderMeta?.(item, index)}
            onSelect={onSelect ? () => onSelect(media, item, index) : undefined}
            actions={<MediaSubscriptionActions media={media} />}
          />
        ))}
      </div>
      {pager}
    </PageState>
  );
}
