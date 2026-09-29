import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Button, Divider, Input, Tabs, Grid } from "@arco-design/web-react";
import { IconSearch } from "@arco-design/web-react/icon";
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
 * 数据来源 GET /actors；订阅中使用 active，热门使用 hot，关键词统一由服务端过滤。
 */
export function ActorListPage() {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [tab, setTab] = useState<"active" | "hot">("active");
  const [keywords, setKeywords] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const query = useQuery({
    queryKey: ["actors", tab, page, pageSize, keywords],
    queryFn: () => apiRequest<Page<Actor>>("/actors?page=" + page + "&page_size=" + pageSize + "&subscription=" + tab + (keywords ? "&keywords=" + encodeURIComponent(keywords) : "")),
  });
  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;

  /** 切换每页条数：页长变了，页码必须回到第一页，否则会请求到越界页。 */
  function changePageSize(next: number) {
    setPageSize(next);
    setPage(1);
  }

  function changeTab(next: string) {
    if (next !== "active" && next !== "hot") return;
    setTab(next);
    setPage(1);
    setKeywords("");
    setSearchInput("");
  }

  /** 搜索回到第一页，相同条件仍刷新当前结果。 */
  function submitSearch() {
    const next = searchInput.trim();
    setKeywords(next);
    setPage(1);
    if (next === keywords && page === 1) void query.refetch();
  }

  /** 清空筛选并回到当前标签的第一页。 */
  function resetFilters() {
    setSearchInput("");
    setKeywords("");
    setPage(1);
    if (!keywords && page === 1) void query.refetch();
  }

  return (
    <section>
      <PageHeader title="演员" />
      <Tabs style={{ marginBottom: 16 }} activeTab={tab} onChange={changeTab}>
          <Tabs.TabPane key="active" title="订阅中" />
          <Tabs.TabPane key="hot" title="热门" />
      </Tabs>
      <form className="filter-toolbar actor-filter-toolbar" role="search" onSubmit={(event) => { event.preventDefault(); submitSearch(); }}>
        <Grid.Row gutter={[12, 12]} justify="start" align="center">
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="actor-search-field filter-labeled">
              <span className="filter-label">演员名称</span>
              <Input
                aria-label="演员名称"
                className="actor-search"
                allowClear
                value={searchInput}
                placeholder="搜索演员名称"
                prefix={<IconSearch />}
                onChange={setSearchInput}
                onClear={resetFilters}
              />
            </div>
          </Grid.Col>
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="filter-actions">
              <Button type="primary" htmlType="submit" loading={query.isFetching}>搜索</Button>
              <Button htmlType="button" onClick={resetFilters}>重置</Button>
            </div>
          </Grid.Col>
        </Grid.Row>
      </form>
      <Divider style={{ margin: "0 0 16px" }} />
      <PageState
        isLoading={query.isLoading}
        error={query.error}
        isEmpty={!query.isLoading && !query.error && items.length === 0}
        emptyText={keywords ? "未找到匹配的演员" : tab === "active" ? "暂无已订阅演员" : "暂无热门演员"}
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
