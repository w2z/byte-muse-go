import { Button, Table, Tag } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { DownloadTask } from "../../shared/api/types";
import { PageState } from "../../shared/ui/PageState";

/** 下载任务只展示服务端状态，不把它等同于订阅或媒体库状态。 */
export function DownloadListPage() {
  const [page, setPage] = useState(1);
  const query = useQuery({
    queryKey: ["downloads", page],
    queryFn: () => apiRequest<Page<DownloadTask>>(`/downloads?page=${page}&page_size=20`),
  });
  const items = query.data?.items ?? [];

  return (
    <section>
      <div className="page-heading"><div><div className="eyebrow">下载队列 / PIPELINE</div><h1>下载任务</h1><p className="page-description">从资源发现到客户端完成，所有任务的状态都在这里可见。</p></div><div className="toolbar-note">实时队列</div></div>
      <PageState
        isLoading={query.isLoading}
        error={query.error}
        isEmpty={!query.isLoading && !query.error && items.length === 0}
        emptyText="暂无下载任务"
        onRetry={() => void query.refetch()}
      >
        <div className="table-shell"><div className="toolbar"><div className="toolbar-note">任务流水 / 最近更新优先</div></div><Table
          rowKey="id"
          data={items}
          pagination={false}
          columns={[
            { title: "影片", dataIndex: "media_id" },
            { title: "状态", dataIndex: "status", render: (value) => <Tag className={`state-tag ${value === "completed" ? "active" : value === "failed" ? "warn" : "idle"}`}>{value}</Tag> },
            { title: "外部任务", dataIndex: "external_id" },
            { title: "错误", dataIndex: "error_message" },
          ]}
        /><div className="list-pagination"><Button disabled={page === 1} onClick={() => setPage(page - 1)}>上一页</Button><span>第 {page} 页 / 共 {Math.max(1, Math.ceil((query.data?.total ?? 0) / 20))} 页</span><Button disabled={page * 20 >= (query.data?.total ?? 0)} onClick={() => setPage(page + 1)}>下一页</Button></div></div>
      </PageState>
    </section>
  );
}
