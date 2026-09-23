import { Button, Message, Table, Tag } from "@arco-design/web-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { Subscription } from "../../shared/api/types";
import { PageState } from "../../shared/ui/PageState";

const statusText = {
  none: "未订阅",
  active: "订阅中",
  canceled: "已取消",
} as const;

/** 订阅列表与取消共用订阅服务。请求未完成时禁用同一行，避免重复提交。 */
export function SubscriptionListPage() {
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const query = useQuery({
    queryKey: ["subscriptions", page],
    queryFn: () => apiRequest<Page<Subscription>>(`/subscriptions?page=${page}&page_size=20`),
  });
  const cancel = useMutation({
    mutationFn: (subscription: Subscription) =>
      apiRequest<Subscription>(`/subscriptions/${subscription.id}/cancel`, { method: "POST" }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["subscriptions"] });
    },
    onError: (error: Error) => {
      Message.error(error.message);
    },
  });
  const items = query.data?.items ?? [];

  return (
    <section>
      <div className="page-heading"><div><div className="eyebrow">订阅中心 / WATCHLIST</div><h1>订阅</h1><p className="page-description">让系统替你盯住资源变化，匹配到合适内容时自动进入队列。</p></div><div className="toolbar-note">{query.data?.total ?? 0} 个追踪项</div></div>
      <PageState
        isLoading={query.isLoading}
        error={query.error}
        isEmpty={!query.isLoading && !query.error && items.length === 0}
        emptyText="暂无订阅"
        onRetry={() => void query.refetch()}
      >
        <div className="table-shell"><div className="toolbar"><div className="toolbar-note">自动追踪 / 状态总览</div></div><Table
          rowKey="id"
          data={items}
          pagination={false}
          columns={[
            { title: "影片", dataIndex: "media_id" },
            { title: "模式", dataIndex: "mode" },
            { title: "状态", render: (_, record) => <Tag className={`state-tag ${record.status === "active" ? "active" : "idle"}`}>{statusText[record.status]}</Tag> },
            {
              title: "操作",
              render: (_, record) => (
                <Button
                  className="danger-action"
                  disabled={record.status !== "active" || cancel.isPending}
                  loading={cancel.isPending && cancel.variables?.id === record.id}
                  onClick={() => cancel.mutate(record)}
                >
                  取消订阅
                </Button>
              ),
            },
          ]}
        /><div className="list-pagination"><Button disabled={page === 1} onClick={() => setPage(page - 1)}>上一页</Button><span>第 {page} 页 / 共 {Math.max(1, Math.ceil((query.data?.total ?? 0) / 20))} 页</span><Button disabled={page * 20 >= (query.data?.total ?? 0)} onClick={() => setPage(page + 1)}>下一页</Button></div></div>
      </PageState>
    </section>
  );
}
