import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { Actor } from "../../shared/api/types";
import { ActorCard } from "../../shared/ui/ActorCard";
import { ActorSubscriptionActions } from "../../shared/ui/ActorSubscriptionActions";
import { DEFAULT_PAGE_SIZE, ListPagination } from "../../shared/ui/ListPagination";
import { PageHeader } from "../../shared/ui/PageHeader";
import { PageState } from "../../shared/ui/PageState";

/**
 * 演员列表。
 *
 * 布局对齐对标站 对标站 的演员页：标题一行 + 响应式卡片网格
 * （grid-cols-1 / sm:2 / md:3 / lg:4，1280px 封顶 4 列），列表页不使用表格；
 * 卡片规格由公共组件 ActorCard 提供。
 *
 * 数据来源 GET /actors?subscription=all，同时展示已订阅和可订阅演员。
 */
export function ActorListPage() {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const query = useQuery({
    queryKey: ["actors", page, pageSize],
    queryFn: () => apiRequest<Page<Actor>>("/actors?page=" + page + "&page_size=" + pageSize + "&subscription=all"),
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
      <PageHeader title="演员" />
      <PageState
        isLoading={query.isLoading}
        error={query.error}
        isEmpty={!query.isLoading && !query.error && items.length === 0}
        emptyText="暂无演员"
        onRetry={() => void query.refetch()}
        emptyExtra={<ListPagination page={page} total={total} pageSize={pageSize} onChange={setPage} onPageSizeChange={changePageSize} plain />}
      >
        <div className="card-grid card-grid--4">
          {items.map((actor) => (
            <ActorCard key={actor.name} actor={actor} actions={<ActorSubscriptionActions actor={actor} />} />
          ))}
        </div>
        <ListPagination page={page} total={total} pageSize={pageSize} onChange={setPage} onPageSizeChange={changePageSize} plain />
      </PageState>
    </section>
  );
}
