import { Button, Descriptions, Modal, Table, Tag } from "@arco-design/web-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { Media } from "../../shared/api/types";
import { PageState } from "../../shared/ui/PageState";

const statusText = {
  none: "未订阅",
  active: "订阅中",
  canceled: "已取消",
} as const;

/** 影片列表读取 GET /media，订阅状态与媒体库状态分开展示。 */
export function FilmListPage() {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const queryClient = useQueryClient();
  const subscribe = useMutation({
    mutationFn: (mediaId: string) => apiRequest("/subscriptions", { method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() }, body: JSON.stringify({ media_id: mediaId, mode: "strict", filter: {} }) }),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ["media"] }); await queryClient.invalidateQueries({ queryKey: ["subscriptions"] }); },
  });
  const detail = useQuery({ queryKey: ["media-detail", selectedId], queryFn: () => apiRequest<Media>(`/media/${encodeURIComponent(selectedId!)}`), enabled: selectedId !== null });
  const query = useQuery({
    queryKey: ["media", page],
    queryFn: () => apiRequest<Page<Media>>(`/media?page=${page}&page_size=20`),
  });
  const items = query.data?.items ?? [];

  return (
    <section>
      <div className="page-heading"><div><div className="eyebrow">媒体库 / LIBRARY</div><h1>影片</h1><p className="page-description">所有被发现、订阅和整理过的内容都会在这里汇合。</p></div><div className="toolbar-note">{query.data?.total ?? 0} 项记录</div></div>
      {subscribe.isSuccess && <p role="status" className="inline-success">订阅已创建</p>}
      {subscribe.isError && <p role="alert" className="inline-error">{subscribe.error.message}</p>}
      <PageState
        isLoading={query.isLoading}
        error={query.error}
        isEmpty={!query.isLoading && !query.error && items.length === 0}
        emptyText="暂无影片"
        onRetry={() => void query.refetch()}
      >
        <div className="table-shell"><div className="toolbar"><div className="toolbar-note">资源目录 / 当前页</div></div><Table
          rowKey="id"
          data={items}
          pagination={false}
          columns={[
            { title: "编号", dataIndex: "code", render: (value) => <span className="code-cell">{value}</span> },
            { title: "标题", dataIndex: "title", render: (value) => <span className="title-cell">{value}</span> },
            { title: "操作", render: (_, record) => <><Button type="text" onClick={() => setSelectedId(record.id)}>查看详情</Button><Button type="text" disabled={record.subscription_status === "active" || subscribe.isPending} loading={subscribe.isPending && subscribe.variables === record.id} onClick={() => subscribe.mutate(record.id)}>订阅影片</Button></> },
            {
              title: "订阅",
                render: (_, record) => <Tag className={`state-tag ${record.subscription_status === "active" ? "active" : "idle"}`}>{statusText[record.subscription_status]}</Tag>,
            },
            {
              title: "媒体库",
                render: (_, record) => <Tag className={`state-tag ${record.library_status === "present" ? "active" : "idle"}`}>{record.library_status}</Tag>,
            },
          ]}
        /><div className="list-pagination"><Button disabled={page === 1} onClick={() => setPage(page - 1)}>上一页</Button><span>第 {page} 页 / 共 {Math.max(1, Math.ceil((query.data?.total ?? 0) / 20))} 页</span><Button disabled={page * 20 >= (query.data?.total ?? 0)} onClick={() => setPage(page + 1)}>下一页</Button></div></div>
      </PageState>
      <Modal title="影片详情" visible={selectedId !== null} footer={null} onCancel={() => setSelectedId(null)}>
        <PageState isLoading={detail.isLoading} error={detail.error} isEmpty={false} emptyText="" onRetry={() => void detail.refetch()}>
          <Descriptions data={[{ label: "番号", value: detail.data?.code ?? "" }, { label: "标题", value: detail.data?.title ?? "" }, { label: "订阅", value: detail.data?.subscription_status ?? "" }, { label: "媒体库", value: detail.data?.library_status ?? "" }]} column={1} />
        </PageState>
      </Modal>
    </section>
  );
}
