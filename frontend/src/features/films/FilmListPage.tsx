import { Descriptions, Modal, Tag } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { LibraryStatus, Media } from "../../shared/api/types";
import { DEFAULT_PAGE_SIZE } from "../../shared/ui/ListPagination";
import { MediaCardGrid } from "../../shared/ui/MediaCardGrid";
import { PageHeader } from "../../shared/ui/PageHeader";
import { PageState } from "../../shared/ui/PageState";
import "./FilmListPage.css";

/** 媒体库状态的中文文案。 */
const libraryText: Record<LibraryStatus, string> = {
  unknown: "未知",
  absent: "未入库",
  present: "已入库",
};

/** 媒体库状态对应的标签色，已入库用强调色，未知用警示色。 */
const libraryStateClass: Record<LibraryStatus, string> = {
  unknown: "warn",
  absent: "idle",
  present: "active",
};

/**
 * 影片（媒体库）列表。
 *
 * 读取 GET /media 并按对标站番号卡片网格排布：网格、卡片和分页条统一由公共 MediaCardGrid 提供，
 * 页面不自行实现卡片样式。订阅状态由 CodeCard 自带的标签展示，媒体库状态通过 renderMeta 补充；
 * 进入详情由网格的 onSelect 打开 Modal + GET /media/{mediaId} 展示。
 * 订阅仍走 POST /subscriptions 并带幂等键，成功后失效 media 与 subscriptions 查询。
 */
export function FilmListPage() {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const detail = useQuery({ queryKey: ["media-detail", selectedId], queryFn: () => apiRequest<Media>(`/media/${encodeURIComponent(selectedId!)}`), enabled: selectedId !== null });
  const query = useQuery({
    queryKey: ["media", page, pageSize],
    queryFn: () => apiRequest<Page<Media>>(`/media?page=${page}&page_size=${pageSize}`),
  });
  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;

  /** 切换每页条数：页长变了，页码必须回到第一页，否则会请求到越界页。 */
  function changePageSize(next: number) {
    setPageSize(next);
    setPage(1);
  }

  return (
    <section>
      <PageHeader title="影片" />
      <MediaCardGrid
        items={items}
        query={query}
        emptyText="暂无影片"
        total={total}
        page={page}
        pageSize={pageSize}
        onPageChange={setPage}
        onPageSizeChange={changePageSize}
        onSelect={(media) => setSelectedId(media.id)}
        renderMeta={(media) => (
          <div className="film-card-library">
            <span className="code-card-meta">媒体库</span>
            <Tag className={`state-tag ${libraryStateClass[media.library_status]}`}>{libraryText[media.library_status]}</Tag>
          </div>
        )}
      />
      <Modal title="影片详情" visible={selectedId !== null} footer={null} onCancel={() => setSelectedId(null)}>
        <PageState isLoading={detail.isLoading} error={detail.error} isEmpty={false} emptyText="" onRetry={() => void detail.refetch()}>
          <Descriptions data={[{ label: "番号", value: detail.data?.code ?? "" }, { label: "标题", value: detail.data?.title ?? "" }, { label: "订阅", value: detail.data?.subscription_status ?? "" }, { label: "媒体库", value: detail.data?.library_status ?? "" }]} column={1} />
        </PageState>
      </Modal>
    </section>
  );
}
