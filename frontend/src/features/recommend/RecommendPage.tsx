import { Button, Grid } from "@arco-design/web-react";
import { CatalogFilters, catalogFilterParams } from "../../shared/ui/CatalogFilters";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { Media } from "../../shared/api/types";
import { DEFAULT_PAGE_SIZE } from "../../shared/ui/ListPagination";
import { MediaCardGrid } from "../../shared/ui/MediaCardGrid";
import { PageHeader } from "../../shared/ui/PageHeader";

/**
 * 推荐页。
 *
 * 布局对齐对标站的推荐页：主区域 = 一行 24px 粗体标题 + 响应式卡片网格，
 * 网格、卡片和分页条统一由公共 MediaCardGrid 提供，本页只负责请求、分页状态和空态文案。
 * 数据取自 GET /codes/recommend，分页由服务端裁决，这里只按页请求并展示总数。
 */
export function RecommendPage() {
  const [subscription, setSubscription] = useState("");
  const [videoType, setVideoType] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const query = useQuery({
    queryKey: ["recommend", page, pageSize, subscription, videoType],
    queryFn: () =>
      apiRequest<Page<Media>>(`/codes/recommend?page=${page}&page_size=${pageSize}${catalogFilterParams(subscription, videoType)}`),
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
      <PageHeader title="推荐" />
      <div className="filter-toolbar" role="search"><Grid.Row gutter={[12, 12]} justify="start" align="center">
          <CatalogFilters subscription={subscription} videoType={videoType} onSubscriptionChange={(value) => { setSubscription(value); setPage(1); }} onVideoTypeChange={(value) => { setVideoType(value); setPage(1); }} />
          <Grid.Col xs={24} sm={12} md={8} xl={4}><div className="filter-actions"><Button type="primary" onClick={() => { if (page === 1) void query.refetch(); else setPage(1); }}>搜索</Button><Button onClick={() => { if (!subscription && !videoType && page === 1) void query.refetch(); setSubscription(""); setVideoType(""); setPage(1); }}>重置</Button></div></Grid.Col>
      </Grid.Row></div>
      <MediaCardGrid
        items={items}
        query={query}
        emptyText="暂无推荐"
        total={total}
        page={page}
        pageSize={pageSize}
        onPageChange={setPage}
        onPageSizeChange={changePageSize}
      />
    </section>
  );
}
