import { Button } from "@arco-design/web-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { Media, Subscription } from "../../shared/api/types";
import { DEFAULT_PAGE_SIZE } from "../../shared/ui/ListPagination";
import { MediaCardGrid } from "../../shared/ui/MediaCardGrid";
import { PageHeader } from "../../shared/ui/PageHeader";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";
import "./SubscriptionListPage.css";

const modeText = { strict: "严格", preload: "预下载" } as const;

/** 订阅列表直接使用服务端内嵌的媒体投影，保证与其他番号页面共用同一张大图卡。 */
export function SubscriptionListPage() {
  const [message, messageHolder] = useFeedbackMessage();
  const queryClient = useQueryClient();
  const enqueue = useMutation({
    mutationFn: (id: string) => apiRequest<{ task_id: string }>("/subscriptions/" + encodeURIComponent(id) + "/download", { method: "POST" }),
    onSuccess: async () => { message.success?.("已登记资源搜索，有资源才会建立下载任务"); await queryClient.invalidateQueries(); },
    onError: (error: Error) => message.error?.(error.message),
  });
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const query = useQuery({
    queryKey: ["subscriptions", page, pageSize],
    queryFn: () => apiRequest<Page<Subscription>>(`/subscriptions?page=` + page + `&page_size=` + pageSize + `&status=active`),
  });
  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;

  /** 切换每页条数：页长变了，页码必须回到第一页，否则会请求到越界页。 */
  function changePageSize(next: number) {
    setPageSize(next);
    setPage(1);
  }

  /** 订阅行没有媒体投影时该项不可渲染，交给网格组件按 null 跳过。 */
  function mediaFor(item: Subscription): Media | null {
    if (!item.media) return null;
    return { ...item.media, active_subscription: item.status === "active" ? item : null };
  }

  return (
    <section>
      {messageHolder}
      <PageHeader title="订阅" />
      <MediaCardGrid
        items={items}
        query={query}
        emptyText="暂无订阅"
        total={total}
        page={page}
        pageSize={pageSize}
        onPageChange={setPage}
        onPageSizeChange={changePageSize}
        itemKey={(item) => item.id}
        toMedia={mediaFor}
        renderMeta={(item) => <div className="code-card-meta">{modeText[item.mode]}模式 <Button type="text" loading={enqueue.isPending && enqueue.variables === item.id} onClick={() => enqueue.mutate(item.id)}>搜索下载</Button></div>}
      />
    </section>
  );
}
