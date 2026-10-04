import { Button, DatePicker, Divider, Drawer, Dropdown, Menu, Modal, Select, Space, Table, Tag, Grid } from "@arco-design/web-react";
import { IconClose, IconDown } from "@arco-design/web-react/icon";
import dayjs from "dayjs";
import { getDateTimeShortcuts, getDisabledDateTime, isFutureDate, serializeDateTimeRange } from "../../shared/dateTimeRange";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { DownloadAction, DownloadTask, Media } from "../../shared/api/types";
import { CodeCard } from "../../shared/ui/CodeCard";
import { ContentCard } from "../../shared/ui/ContentCard";
import { DEFAULT_PAGE_SIZE, ListPagination } from "../../shared/ui/ListPagination";
import { PageHeader } from "../../shared/ui/PageHeader";
import { PageState } from "../../shared/ui/PageState";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";

/** 下载器状态与内部阶段共用标签色板：完成为正常态，失败告警，其余为中性态。 */
function statusTone(status: string): string {
  if (status === "completed") return "active";
  if (status === "failed") return "warn";
  return "idle";
}

const transferLabels: Record<string, string> = {
  downloading: "下载中", paused: "暂停", stopped: "停止", failed: "下载失败", completed: "下载完成",
};

const actionLabels: Record<DownloadAction, string> = { pause: "暂停", stop: "停止", resume: "继续", retry: "重试", delete: "删除任务", delete_files: "删除任务+文件" };
const statusLabels: Record<DownloadTask["status"], string> = { queued: "排队中", searching: "搜索中", submitted: "已提交", downloading: "下载中", completed: "已完成", failed: "失败", unknown: "待核实" };

/** 优先展示服务端同步的下载器状态；尚无回查结果时保留内部阶段，不推断下载是否完成。 */
function DownloadStatus({ task }: { task: DownloadTask }) {
  const status = task.transfer_status || task.status;
  const label = task.transfer_status ? (transferLabels[status] ?? status) : statusLabels[task.status];
  return <Tag className={`state-tag ${statusTone(status)}`}>{label}</Tag>;
}

/** 操作只消费后端能力，删除模式明确确认；同一行请求期间禁用全部按钮。 */
function DownloadActions({ task, onChanged }: { task: DownloadTask; onChanged: (deleted?: boolean) => void }) {
  const [message, messageHolder] = useFeedbackMessage();
  const [deleteAction, setDeleteAction] = useState<"delete" | "delete_files" | null>(null);
  const actions = task.available_actions ?? [];
  const mutation = useMutation({
    mutationFn: (action: DownloadAction) => apiRequest<void>(action === "delete" || action === "delete_files"
      ? "/downloads/" + encodeURIComponent(task.id) + "?delete_files=" + String(action === "delete_files")
      : "/downloads/" + encodeURIComponent(task.id) + "/" + action,
      { method: action === "delete" || action === "delete_files" ? "DELETE" : "POST" }),
    onSuccess: (_, action) => { setDeleteAction(null); message.success("操作成功"); onChanged(action === "delete" || action === "delete_files"); },
    onError: (error: Error) => { message.error(error.message); onChanged(); },
  });
  /** 删除文件不可恢复，确认弹窗明确展示番号和删除范围。 */
  function confirmDelete(action: "delete" | "delete_files") {
    setDeleteAction(action);
  }
  if (!actions.length) return <span>—</span>;
  return <>{messageHolder}<Space size={4} wrap>
    {actions.filter((action) => action !== "delete" && action !== "delete_files").map((action) => <Button key={action} type="text" disabled={mutation.isPending} loading={mutation.isPending && mutation.variables === action} onClick={() => mutation.mutate(action)}>{actionLabels[action]}</Button>)}
    {actions.includes("delete") && (actions.includes("delete_files") ? <Dropdown trigger="click" disabled={mutation.isPending} droplist={<Menu onClickMenuItem={(key) => confirmDelete(key as "delete" | "delete_files")}><Menu.Item key="delete">删除任务</Menu.Item><Menu.Item key="delete_files">删除任务+文件</Menu.Item></Menu>}><Button type="text" status="danger" disabled={mutation.isPending}>删除<IconDown /></Button></Dropdown>
      : <Button type="text" status="danger" disabled={mutation.isPending} onClick={() => confirmDelete("delete")}>删除</Button>)}
  </Space><Modal title={(deleteAction ? actionLabels[deleteAction] : "删除任务") + "：" + (task.code ?? "此任务")} visible={deleteAction !== null}
    onCancel={() => { if (!mutation.isPending) setDeleteAction(null); }} onOk={() => { if (deleteAction) return mutation.mutateAsync(deleteAction); }}
    okText="确认删除" cancelText="取消" okButtonProps={{ status: "danger" }} confirmLoading={mutation.isPending} maskClosable={!mutation.isPending}>
    {deleteAction === "delete_files" ? "将删除下载任务及下载器中的文件，文件删除后无法恢复。" : "仅删除下载任务，保留已经下载的文件。"}
  </Modal></>;
}

type DownloadFilters = { status: string; added: string[]; completed: string[] };

/** 按媒体 ID 加载封面及分组资料；抽屉关闭或切换影片时取消未完成请求。 */
function DownloadMediaDrawer({ mediaId, onClose }: { mediaId: string | null; onClose: () => void }) {
  const detail = useQuery({
    queryKey: ["media-detail", mediaId],
    queryFn: ({ signal }) => apiRequest<Media>("/media/" + encodeURIComponent(mediaId!), { signal }),
    enabled: mediaId !== null,
  });
  return <Drawer {...{ role: "dialog", "aria-modal": true, "aria-label": "影片信息" }} title="影片信息" visible={mediaId !== null} onCancel={onClose} placement="right" width="min(860px, 100vw)" footer={null} unmountOnExit
    closeIcon={<Button type="text" aria-label="关闭影片信息" icon={<IconClose />} />}>
    <PageState isLoading={detail.isLoading} error={detail.error} onRetry={() => void detail.refetch()}>
      {detail.data ? <CodeCard key={mediaId} media={detail.data} variant="detail" /> : null}
    </PageState>
  </Drawer>;
}

/**
 * 下载任务列表。
 *
 * 下载任务是队列明细，属于表格型数据，所以按照对标站的任务页保持表格：
 * 上方一行页面标题，下方是一块圆角边框卡片（公共 .table-shell）包住表格，表格使用与
 * 定时任务页共用的 .data-table 样式（默认密度 + 深色表头 + 行分隔线）。分页使用
 * 公共 ListPagination，页码边界由服务端返回的总数换算，保留服务端分页。
 *
 * 空数据时不再整页替换为空态，而是在表格内通过 noDataElement 展示空态，
 * 与定时任务页行为一致。只展示服务端状态，不把它等同于订阅或媒体库状态；
 * 数据来源 GET /downloads。
 */
export function DownloadListPage() {
  const queryClient = useQueryClient();
  const [selectedMediaId, setSelectedMediaId] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [draft, setDraft] = useState<DownloadFilters>({ status: "", added: [], completed: [] });
  const [applied, setApplied] = useState<DownloadFilters>({ status: "", added: [], completed: [] });
  const query = useQuery({
    refetchInterval: 5000,
    queryKey: ["downloads", page, pageSize, applied.status, applied.added, applied.completed],
    queryFn: () => {
      const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
      if (applied.status) params.set("transfer_status", applied.status);
      if (applied.added.length === 2) { params.set("added_from", applied.added[0]); params.set("added_to", applied.added[1]); }
      if (applied.completed.length === 2) { params.set("completed_from", applied.completed[0]); params.set("completed_to", applied.completed[1]); }
      return apiRequest<Page<DownloadTask>>("/downloads?" + params.toString());
    },
  });
  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;
  /** 删除页末任务后退到有效页，并刷新全部已缓存的下载筛选。 */
  function refreshAfterAction(deleted = false) {
    if (deleted && items.length === 1 && page > 1) setPage(page - 1);
    void queryClient.invalidateQueries({ queryKey: ["downloads"] });
  }

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
          <Grid.Row gutter={[12, 12]} justify="start" align="center">
            <Grid.Col xs={24} sm={12} md={8} xl={4}>
              <div className="download-filter-field filter-field"><span className="download-filter-label filter-label">下载状态</span><Select className="filter-control" aria-label="下载状态筛选" value={draft.status} onChange={(value) => setDraft((current) => ({ ...current, status: value }))} options={[
                { label: "全部状态", value: "" }, { label: "下载中", value: "downloading" }, { label: "暂停", value: "paused" },
                { label: "停止", value: "stopped" },
                { label: "下载失败", value: "failed" }, { label: "下载完成", value: "completed" },
              ]} /></div>
            </Grid.Col>
            <Grid.Col xs={24} sm={12} md={8} xl={4}>
              <div className="download-filter-field filter-field filter-field--range"><span className="download-filter-label filter-label">加入时间</span><DatePicker.RangePicker className="filter-control" aria-label="加入时间筛选"
                showTime={{ format: "HH:mm:ss" }} format="YYYY-MM-DD HH:mm:ss" shortcuts={getDateTimeShortcuts()}
                value={draft.added.length === 2 ? [dayjs(draft.added[0]), dayjs(draft.added[1])] : undefined}
                disabledDate={(current) => isFutureDate(current)} disabledTime={(current) => getDisabledDateTime()(current)}
                onChange={(_dateStrings, values) => setDraft((current) => ({ ...current, added: serializeDateTimeRange(values) ?? [] }))} placeholder={["加入开始", "加入结束"]} />
              </div>
            </Grid.Col>
            <Grid.Col xs={24} sm={12} md={8} xl={4}>
              <div className="download-filter-field filter-field filter-field--range"><span className="download-filter-label filter-label">下载完成时间</span><DatePicker.RangePicker className="filter-control" aria-label="下载完成时间筛选"
                showTime={{ format: "HH:mm:ss" }} format="YYYY-MM-DD HH:mm:ss" shortcuts={getDateTimeShortcuts()}
                value={draft.completed.length === 2 ? [dayjs(draft.completed[0]), dayjs(draft.completed[1])] : undefined}
                disabledDate={(current) => isFutureDate(current)} disabledTime={(current) => getDisabledDateTime()(current)}
                onChange={(_dateStrings, values) => setDraft((current) => ({ ...current, completed: serializeDateTimeRange(values) ?? [] }))} placeholder={["完成开始", "完成结束"]} /></div>
            </Grid.Col>
            <Grid.Col xs={24} sm={12} md={8} xl={4}>
              <div className="download-filter-actions filter-actions"><Button type="primary" htmlType="submit">搜索</Button><Button onClick={reset}>重置</Button></div>
            </Grid.Col>
          </Grid.Row>
        </form>
        <Divider />
        <PageState isLoading={query.isLoading} error={query.error} onRetry={() => void query.refetch()}>
          <Table
            className="data-table"
            border={false}
            rowKey="id"
            data={items}
            loading={query.isLoading}
            noDataElement={<div className="data-table-empty" role="status">暂无下载任务</div>}
            pagination={false}
            scroll={{ x: 1400 }}
            columns={[
              { title: "影片", dataIndex: "code", width: 130, render: (value: string | null, task: DownloadTask) => value && task.media_id
                ? <Button type="text" className="code-cell" onClick={() => setSelectedMediaId(task.media_id)}>{value}</Button>
                : <span className="code-cell">{value || "—"}</span> },
              { title: "资源站", dataIndex: "source_site", render: (value: string | null) => value || "—" },
              { title: "下载器", dataIndex: "downloader", render: (value: string | null) => value || "—" },
              { title: "下载状态", key: "download_status", render: (_: unknown, task: DownloadTask) => <DownloadStatus task={task} /> },
              { title: "加入时间", dataIndex: "added_at", render: (value: string | null) => value ? dayjs(value).format("YYYY-MM-DD HH:mm") : "—" },
              { title: "完成时间", dataIndex: "completed_at", render: (value: string | null) => value ? dayjs(value).format("YYYY-MM-DD HH:mm") : "—" },
              {
                title: "外部任务",
                dataIndex: "external_id",
                render: (value: string | null | undefined) => (value ? <span className="code-cell">{value}</span> : null),
              },
              { title: "错误", dataIndex: "error_message" },
              { title: "操作", key: "actions", width: 200, fixed: "right", render: (_: unknown, task: DownloadTask) => <DownloadActions task={task} onChanged={refreshAfterAction} /> },
            ]}
          />
          <ListPagination page={page} total={total} pageSize={pageSize} onChange={setPage} onPageSizeChange={changePageSize} />
        </PageState>
      </ContentCard>
      <DownloadMediaDrawer mediaId={selectedMediaId} onClose={() => setSelectedMediaId(null)} />
    </section>
  );
}
