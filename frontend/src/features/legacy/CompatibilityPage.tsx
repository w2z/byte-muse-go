import { Button, Table, Tag } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../../shared/api/client";
import { PageState } from "../../shared/ui/PageState";

type Item = { code?: string; title?: string; poster_url?: string | null; release_date?: string | null; level?: string; message?: string; username?: string };
type Response = { items?: Item[]; total?: number; user?: { username?: string } };

export function CompatibilityPage({ title, eyebrow, endpoint, description }: { title: string; eyebrow: string; endpoint: string; description: string }) {
  const query = useQuery({ queryKey: ["compatibility", endpoint], queryFn: () => apiRequest<Response>(endpoint) });
  const items = query.data?.items ?? [];
  return (
    <section>
      <div className="page-heading"><div><div className="eyebrow">{eyebrow}</div><h1>{title}</h1><p className="page-description">{description}</p></div><div className="toolbar-note">真实接口</div></div>
      <PageState isLoading={query.isLoading} error={query.error} isEmpty={!query.isLoading && !query.error && items.length === 0} emptyText="暂无数据" onRetry={() => void query.refetch()}>
        <div className="table-shell"><Table rowKey="code" data={items} pagination={false} columns={[{ title: "编号", dataIndex: "code", render: (value) => <span className="code-cell">{value ?? "—"}</span> }, { title: "标题", dataIndex: "title", render: (value) => <span className="title-cell">{value ?? "—"}</span> }, { title: "发行日期", dataIndex: "release_date" }, { title: "状态", render: (_, item) => <Tag className="state-tag active">{item.level ?? "已接入"}</Tag> }]} /><div className="list-pagination"><Button onClick={() => void query.refetch()}>刷新</Button><span>共 {query.data?.total ?? items.length} 项</span></div></div>
      </PageState>
    </section>
  );
}
