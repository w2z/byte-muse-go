import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { Media } from "../../shared/api/types";
import { DEFAULT_PAGE_SIZE } from "../../shared/ui/ListPagination";
import { MediaCardGrid } from "../../shared/ui/MediaCardGrid";
import { PageHeader } from "../../shared/ui/PageHeader";

/**
 * 上新页。
 *
 * 布局对齐对标站的上新页：上方一行页面标题，下方是响应式番号海报卡片网格，
 * 网格、卡片和分页条统一由公共 MediaCardGrid 提供，页面不自行实现卡片样式。
 * 数据读取 GET /codes/release_today，分页以服务端返回为准。
 */
export function ReleaseTodayPage() {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const query = useQuery({
    queryKey: ["release-today", page, pageSize],
    queryFn: () => apiRequest<Page<Media>>(`/codes/release_today?page=${page}&page_size=${pageSize}`),
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
      <PageHeader title="上新" />
      <MediaCardGrid
        items={items}
        query={query}
        emptyText="暂无上新"
        total={total}
        page={page}
        pageSize={pageSize}
        onPageChange={setPage}
        onPageSizeChange={changePageSize}
      />
    </section>
  );
}
