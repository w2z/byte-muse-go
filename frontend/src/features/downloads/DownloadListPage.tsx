import { Table, Tag } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { DownloadTask } from "../../shared/api/types";
import { ContentCard } from "../../shared/ui/ContentCard";
import { DEFAULT_PAGE_SIZE, ListPagination } from "../../shared/ui/ListPagination";
import { PageHeader } from "../../shared/ui/PageHeader";
import { PageState } from "../../shared/ui/PageState";

/** 下载任务状态到公共标签色板的映射：完成是正常态，失败告警，其余（排队/搜索/提交/下载中）都是进行中。 */
function statusTone(status: DownloadTask["status"]): string {
  if (status === "completed") return "active";
  if (status === "failed") return "warn";
  return "idle";
}

/**
 * 下载任务列表。
 *
 * 下载任务是队列明细，属于表格型数据，所以按照对标站 对标站 的任务页保持表格：
 * 上方一行页面标题，下方是一块圆角边框卡片（公共 .table-shell）包住表格，表格使用与
 * 定时任务页共用的 .data-table 样式（默认密度 + 深色表头 + 行分隔线）。分页使用
 * 公共 ListPagination，页码边界由服务端返回的总数换算，保留服务端分页。
 *
 * 空数据时不再整页替换为空态，而是在表格内通过 noDataElement 展示空态，
 * 与定时任务页行为一致。只展示服务端状态，不把它等同于订阅或媒体库状态；
 * 数据来源 GET /downloads。
 */
export function DownloadListPage() {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const query = useQuery({
    queryKey: ["downloads", page, pageSize],
    queryFn: () => apiRequest<Page<DownloadTask>>(`/downloads?page=${page}&page_size=${pageSize}`),
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
      <PageHeader title="下载任务" />
      <PageState isLoading={query.isLoading} error={query.error} onRetry={() => void query.refetch()}>
        <ContentCard className="table-shell data-table-shell">
          <Table
            className="data-table"
            border={false}
            rowKey="id"
            data={items}
            loading={query.isLoading || query.isFetching}
            noDataElement={<div className="data-table-empty" role="status">暂无下载任务</div>}
            pagination={false}
            columns={[
              { title: "影片", dataIndex: "media_id", render: (value: string) => <span className="code-cell">{value}</span> },
              { title: "状态", dataIndex: "status", render: (value: DownloadTask["status"]) => <Tag className={`state-tag ${statusTone(value)}`}>{value}</Tag> },
              {
                title: "外部任务",
                dataIndex: "external_id",
                render: (value: string | null | undefined) => (value ? <span className="code-cell">{value}</span> : null),
              },
              { title: "错误", dataIndex: "error_message" },
            ]}
          />
          <ListPagination page={page} total={total} pageSize={pageSize} onChange={setPage} onPageSizeChange={changePageSize} />
        </ContentCard>
      </PageState>
    </section>
  );
}
