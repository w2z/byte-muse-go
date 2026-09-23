import { Avatar, Button, Table, Tag } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { Actor } from "../../shared/api/types";
import { PageState } from "../../shared/ui/PageState";

/** 演员列表读取 GET /actors，只展示旧版带订阅截止日期的演员。 */
export function ActorListPage() {
  const [page, setPage] = useState(1);
  const query = useQuery({
    queryKey: ["actors", page],
    queryFn: () => apiRequest<Page<Actor>>("/actors?page=" + page + "&page_size=20"),
  });
  const items = query.data?.items ?? [];
  const totalPages = Math.max(1, Math.ceil((query.data?.total ?? 0) / 20));

  return (
    <section>
      <div className="page-heading">
        <div><div className="eyebrow">通用 / ACTORS</div><h1>演员</h1><p className="page-description">管理已订阅演员，并查看订阅截止日期。</p></div>
        <div className="toolbar-note">{query.data?.total ?? 0} 位演员</div>
      </div>
      <PageState isLoading={query.isLoading} error={query.error} isEmpty={!query.isLoading && !query.error && items.length === 0} emptyText="暂无已订阅演员" onRetry={() => void query.refetch()}>
        <div className="table-shell">
          <Table rowKey="name" data={items} pagination={false} columns={[
            { title: "演员", dataIndex: "name", render: (_: unknown, record: Actor) => <div className="title-cell"><Avatar size={32}><img src={record.photo ?? undefined} alt="" /></Avatar><span>{record.name}</span></div> },
            { title: "订阅截止", dataIndex: "limit_date", render: (value: string | null) => <Tag className="state-tag active">{value ?? "未设置"}</Tag> },
          ]} />
          <div className="list-pagination"><Button disabled={page === 1} onClick={() => setPage(page - 1)}>上一页</Button><span>第 {page} 页 / 共 {totalPages} 页</span><Button disabled={page >= totalPages} onClick={() => setPage(page + 1)}>下一页</Button></div>
        </div>
      </PageState>
    </section>
  );
}
