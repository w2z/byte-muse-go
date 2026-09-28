import { Button, Input } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { CatalogTag } from "../../shared/api/types";
import { InfoCard } from "../../shared/ui/InfoCard";
import { DEFAULT_PAGE_SIZE, ListPagination } from "../../shared/ui/ListPagination";
import { PageState } from "../../shared/ui/PageState";
import { PageHeader } from "../../shared/ui/PageHeader";

/** 标签只读目录：名称、影片数、搜索及分页来自服务端，不触发采集或订阅。 */
export function TagListPage() {
  const [draft, setDraft] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const query = useQuery({
    queryKey: ["tags", search, page, pageSize],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams({page: String(page), page_size: String(pageSize)});
      if (search) params.set("search", search);
      return apiRequest<Page<CatalogTag>>("/tags?" + params.toString(), { signal });
    },
  });
  const items = query.data?.items ?? [];
  const pagination = <ListPagination page={page} pageSize={pageSize} total={query.data?.total ?? 0} onChange={setPage} onPageSizeChange={(size) => {setPageSize(size); setPage(1);}} plain />;
  return (
    <section>
      <PageHeader title="标签" />
      <form className="page-toolbar" role="search" onSubmit={(event) => {event.preventDefault(); setSearch(draft.trim()); setPage(1);}}>
        <div className="toolbar-group search-field">
          <Input aria-label="搜索标签" placeholder="标签名称" value={draft} onChange={setDraft} allowClear onClear={() => {setDraft(""); setSearch(""); setPage(1);}} />
          <Button type="primary" htmlType="submit">搜索</Button>
        </div>
      </form>
      <PageState isLoading={query.isLoading} error={query.error} isEmpty={!query.isLoading && !query.error && items.length === 0}
        emptyText={search ? "未找到匹配的标签" : "暂无标签，采集影片详情后可在此查看"} onRetry={() => void query.refetch()} emptyExtra={pagination}>
        <div className="card-grid card-grid--4">
          {items.map((tag) => <InfoCard key={tag.name} title={tag.name} meta={<span>关联影片 {tag.media_count} 部</span>} />)}
        </div>
        {pagination}
      </PageState>
    </section>
  );
}
