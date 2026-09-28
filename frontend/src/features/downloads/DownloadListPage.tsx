import { Button, DatePicker, Divider, Select, Table, Tag } from "@arco-design/web-react";
import dayjs from "dayjs";
import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
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

const transferLabels: Record<string, string> = {
  downloading: "下载中", paused: "暂停", failed: "下载失败", completed: "下载完成",
};

type DownloadFilters = { status: string; added: string[]; completed: string[] };

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
  const [draft, setDraft] = useState<DownloadFilters>({ status: "", added: [], completed: [] });
  const [applied, setApplied] = useState<DownloadFilters>({ status: "", added: [], completed: [] });
  const query = useQuery({
    queryKey: ["downloads", page, pageSize, applied.status, applied.added, applied.completed],
    queryFn: () => {
      const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
      if (applied.status) params.set("transfer_status", applied.status);
      if (applied.added.length === 2) { params.set("added_from", dayjs(applied.added[0]).startOf("day").toISOString()); params.set("added_to", dayjs(applied.added[1]).add(1, "day").startOf("day").toISOString()); }
      if (applied.completed.length === 2) { params.set("completed_from", dayjs(applied.completed[0]).startOf("day").toISOString()); params.set("completed_to", dayjs(applied.completed[1]).add(1, "day").startOf("day").toISOString()); }
      return apiRequest<Page<DownloadTask>>("/downloads?" + params.toString());
    },
  });
  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;

  /** 切换每页条数：页长变了，页码必须回到第一页，否则会请求到越界页。 */
  function changePageSize(next: number) {
    setPageSize(next);
    setPage(1);
  }

  /** 提交完整筛选快照，避免日期和状态每次编辑时提前刷新列表。 */
  function search(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (page === 1 && draft.status === applied.status &&
      draft.added.join(",") === applied.added.join(",") &&
      draft.completed.join(",") === applied.completed.join(",")) {
      void query.refetch();
      return;
    }
    setPage(1);
    setApplied({ ...draft });
  }

  /** 同时清空控件和已应用条件，恢复第一页的完整列表。 */
  function reset() {
    const empty = { status: "", added: [], completed: [] };
    setDraft(empty);
    setApplied(empty);
    setPage(1);
  }

  return (
    <section>
      <PageHeader title="下载任务" />
      <ContentCard className="table-shell data-table-shell">
        <form className="download-filters" role="search" onSubmit={search}>
          <div className="download-filter-field"><span className="download-filter-label">下载状态</span><Select aria-label="下载状态筛选" value={draft.status} onChange={(value) => setDraft((current) => ({ ...current, status: value }))} options={[
            { label: "全部状态", value: "" }, { label: "下载中", value: "downloading" }, { label: "暂停", value: "paused" },
            { label: "下载失败", value: "failed" }, { label: "下载完成", value: "completed" },
          ]} /></div>
          <div className="download-filter-field"><span className="download-filter-label">加入时间</span><DatePicker.RangePicker aria-label="加入时间筛选" value={draft.added.length === 2 ? [dayjs(draft.added[0]), dayjs(draft.added[1])] : undefined} onChange={(value) => setDraft((current) => ({ ...current, added: value ?? [] }))} placeholder={["加入开始", "加入结束"]} /></div>
          <div className="download-filter-field"><span className="download-filter-label">下载完成时间</span><DatePicker.RangePicker aria-label="下载完成时间筛选" value={draft.completed.length === 2 ? [dayjs(draft.completed[0]), dayjs(draft.completed[1])] : undefined} onChange={(value) => setDraft((current) => ({ ...current, completed: value ?? [] }))} placeholder={["完成开始", "完成结束"]} /></div>
          <div className="download-filter-actions"><Button type="primary" htmlType="submit">搜索</Button><Button onClick={reset}>重置</Button></div>
        </form>
        <Divider />
        <PageState isLoading={query.isLoading} error={query.error} onRetry={() => void query.refetch()}>
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
              { title: "资源站", dataIndex: "source_site", render: (value: string | null) => value || "—" },
              { title: "下载器", dataIndex: "downloader", render: (value: string | null) => value || "—" },
              { title: "状态", dataIndex: "status", render: (value: DownloadTask["status"]) => <Tag className={`state-tag ${statusTone(value)}`}>{value}</Tag> },
              { title: "传输状态", dataIndex: "transfer_status", render: (value: string | null) => value ? (transferLabels[value] ?? value) : "—" },
              { title: "加入时间", dataIndex: "added_at", render: (value: string | null) => value ? dayjs(value).format("YYYY-MM-DD HH:mm") : "—" },
              { title: "完成时间", dataIndex: "completed_at", render: (value: string | null) => value ? dayjs(value).format("YYYY-MM-DD HH:mm") : "—" },
              {
                title: "外部任务",
                dataIndex: "external_id",
                render: (value: string | null | undefined) => (value ? <span className="code-cell">{value}</span> : null),
              },
              { title: "错误", dataIndex: "error_message" },
            ]}
          />
          <ListPagination page={page} total={total} pageSize={pageSize} onChange={setPage} onPageSizeChange={changePageSize} />
        </PageState>
      </ContentCard>
    </section>
  );
}
