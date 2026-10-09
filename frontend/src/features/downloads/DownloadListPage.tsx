import { Alert, Badge, Button, Drawer, Dropdown, Menu, Modal, Space, Table, Tag, Tooltip, type BadgeProps, type TableColumnProps } from "@arco-design/web-react";
import { IconClose, IconDown, IconFilter } from "@arco-design/web-react/icon";
import { DownloadColumnFilter, type DownloadFilterKind } from "./DownloadColumnFilter";
import dayjs from "dayjs";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState, type ThHTMLAttributes } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { DownloadAction, DownloadTask, Media } from "../../shared/api/types";
import { CodeCard } from "../../shared/ui/CodeCard";
import { ContentCard } from "../../shared/ui/ContentCard";
import { DEFAULT_PAGE_SIZE, ListPagination } from "../../shared/ui/ListPagination";
import { PageHeader } from "../../shared/ui/PageHeader";
import { PageState } from "../../shared/ui/PageState";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";

/** 列表、筛选选项及选中值共用状态点语义，颜色跟随 Arco 主题。 */
function DownloadStatusBadge({ status, label }: { status: string; label: string }) {
  let tone: BadgeProps["status"] = "default";
  if (status === "completed") tone = "success";
  else if (status === "failed") tone = "error";
  else if (["downloading", "searching", "submitted", "checking", "metadata", "moving"].includes(status)) tone = "processing";
  else if (status === "paused" || status === "unknown" || status === "stalled") tone = "warning";
  return <Badge status={tone} text={label} dotClassName={tone === "processing" ? "status-dot-processing" : undefined} />;
}

const transferLabels: Record<string, string> = {
  queued: "排队中", stalled: "等待连接", checking: "校验中", metadata: "获取元数据", moving: "移动中", unknown: "待核实",
  downloading: "下载中", paused: "暂停", stopped: "停止", failed: "下载失败", completed: "下载完成",
};
const seedingOptions = [
  { value: "completed", label: "已达标（估算）" }, { value: "pending", label: "未达标" }, { value: "unknown", label: "待确认" },
  { value: "not_required", label: "无做种要求" }, { value: "not_applicable", label: "不适用" },
];
const downloaderOptions = [
  { value: "qbittorrent", label: "qBittorrent" }, { value: "transmission", label: "Transmission" },
  { value: "aria2", label: "aria2" }, { value: "thunder", label: "迅雷" },
  { value: "pan115", label: "115 网盘" }, { value: "clouddrive2", label: "CloudDrive2" },
];

const columnWidthStorageKey = "bytemuse.downloads.column-widths.v1";
const defaultColumnWidths: Record<string, number> = {
  code: 150, source_site: 150, downloader: 150, transfer_status: 150, size_bytes: 110, remaining_bytes: 110, downloaded_bytes: 110,
  download_speed: 120, upload_speed: 120, download_url: 280, save_path: 220, share_ratio: 130, seeding_seconds: 140, seeding: 190,
  added_at: 180, completed_at: 180, actions: 200,
};

/** 只恢复已知列的有效宽度；存储不可用或内容损坏时使用默认布局。 */
function readColumnWidths(): Record<string, number> {
  const widths = { ...defaultColumnWidths };
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(columnWidthStorageKey) ?? "{}");
    if (saved && typeof saved === "object") for (const [field, width] of Object.entries(saved)) {
      if (Object.hasOwn(widths, field) && typeof width === "number" && Number.isFinite(width) && width >= 80 && width <= 1200) widths[field] = width;
    }
  } catch { /* 浏览器禁用存储时仍可调整本次页面宽度。 */ }
  return widths;
}

/** 使用 Arco 表头扩展点调整列宽；拖动不触发排序，结束后保存，方向键每次微调10px。 */
function ResizableDownloadHeader({ children, resizeWidth, resizeLabel, onResizeWidth, ...props }: ThHTMLAttributes<HTMLTableCellElement> & {
  resizeWidth?: number; resizeLabel?: string; onResizeWidth?: (width: number, persist: boolean) => void;
}) {
  const drag = useRef<{ x: number; width: number; current: number } | null>(null);
  return <th {...props} style={{ ...props.style, position: props.style?.position ?? (props.style?.left != null || props.style?.right != null ? "sticky" : "relative") }}>{children}
    {resizeWidth != null && <span className="download-column-resizer" role="separator" tabIndex={0} aria-orientation="vertical"
      aria-label={resizeLabel + "列宽"} aria-valuenow={resizeWidth} aria-valuemin={80} aria-valuemax={1200}
      onClick={(event) => event.stopPropagation()}
      onPointerDown={(event) => { if (event.button !== 0) return; event.preventDefault(); event.stopPropagation(); drag.current = { x: event.clientX, width: resizeWidth, current: resizeWidth }; event.currentTarget.setPointerCapture(event.pointerId); }}
      onPointerMove={(event) => { if (!drag.current) return; const width = Math.round(Math.min(1200, Math.max(80, drag.current.width + event.clientX - drag.current.x))); drag.current.current = width; onResizeWidth?.(width, false); }}
      onLostPointerCapture={() => { if (drag.current) { onResizeWidth?.(drag.current.current, true); drag.current = null; } }}
      onKeyDown={(event) => { if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return; event.preventDefault(); event.stopPropagation(); onResizeWidth?.(Math.min(1200, Math.max(80, resizeWidth + (event.key === "ArrowRight" ? 10 : -10))), true); }} />}
  </th>;
}
const downloadTableComponents = { header: { th: ResizableDownloadHeader } };

/** 字节数按 1024 自动换算，速度追加 /s；未知值保留占位，零不视为缺失。 */
function formatBytes(value: number | null | undefined, speed = false): string {
  if (value == null || !Number.isFinite(value) || value < 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = value > 0 ? Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1) : 0;
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 2)} ${units[index]}${speed ? "/s" : ""}`;
}

/** 累计做种时长来自下载器，不以加入时间或下载完成时间相减估算。 */
function formatSeedingTime(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value) || value < 0) return "—";
  const seconds = Math.floor(value);
  if (seconds < 60) return `${seconds} 秒`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟`;
  const hours = Math.floor(minutes / 60);
  return hours < 24 ? `${hours} 小时 ${minutes % 60} 分钟` : `${Math.floor(hours / 24)} 天 ${hours % 24} 小时`;
}

/** 只展示后端规则结论；提示中保留估算依据，非 PT 使用 Tag 默认色。 */
function SeedingTag({ task }: { task: DownloadTask }) {
  const status = task.source_kind === "pt" ? task.seeding?.status ?? "unknown" : "not_applicable";
  const labels = { completed: "已达标（估算）", pending: "未达标", unknown: "待确认", not_required: "无做种要求", not_applicable: "不适用" };
  const colors = { completed: "green", pending: "orange", unknown: "red", not_required: "green", not_applicable: undefined };
  return <Tooltip content={task.seeding?.rule || (task.source_kind === "pt" ? "暂无做种数据或站点规则" : "非 PT 任务")}><Tag color={colors[status]}>{labels[status]}</Tag></Tooltip>;
}

const actionLabels: Record<DownloadAction, string> = { pause: "暂停", stop: "停止", resume: "继续", retry: "重试", delete: "删除任务", delete_files: "删除任务+文件" };
const statusLabels: Record<DownloadTask["status"], string> = { queued: "排队中", searching: "搜索中", submitted: "已提交", downloading: "下载中", completed: "已完成", failed: "失败", unknown: "待核实" };

/** 优先展示服务端同步的下载器状态；尚无回查结果时保留内部阶段，不推断下载是否完成。 */
function DownloadStatus({ task }: { task: DownloadTask }) {
  const status = task.transfer_status || task.status;
  const label = task.transfer_status ? (transferLabels[status] ?? status) : statusLabels[task.status];
  return <DownloadStatusBadge status={status} label={label} />;
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
  const [columnWidths, setColumnWidths] = useState(readColumnWidths);
  const columnWidthsRef = useRef(columnWidths);
  /** 拖动期间立即更新，结束时才写入本地，避免每次刷新列表重置。 */
  function resizeColumn(field: string, width: number, persist: boolean) {
    const next = { ...columnWidthsRef.current, [field]: width };
    columnWidthsRef.current = next; setColumnWidths(next);
    if (persist) try { localStorage.setItem(columnWidthStorageKey, JSON.stringify(next)); } catch { /* 本地存储不可用时保留当前会话布局。 */ }
  }
  const queryClient = useQueryClient();
  const [selectedMediaId, setSelectedMediaId] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [columnFilters, setColumnFilters] = useState<Record<string, string[]>>({});
  const [openFilter, setOpenFilter] = useState<string | null>(null);
  const [filterDrafts, setFilterDrafts] = useState<Record<string, string[]>>({});
  const [filterUnits, setFilterUnits] = useState<Record<string, number>>({});
  const [sorting, setSorting] = useState<{ field: string; direction?: "ascend" | "descend" }>({ field: "" });
  // 分类单独读取全量已存任务，避免分页或其他条件截断选项和数量。
  const sourceSites = useQuery({
    queryKey: ["downloads", "source-sites"],
    queryFn: ({ signal }) => apiRequest<{ items: { value: string; label: string; count: number }[] }>("/downloads/source-sites", { signal }),
    enabled: openFilter === "source_site",
    refetchInterval: 5000,
  });
  const query = useQuery({
    refetchInterval: 5000,
    queryKey: ["downloads", page, pageSize, columnFilters, sorting],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
      if (Object.keys(columnFilters).length) params.set("column_filters", JSON.stringify(columnFilters));
      if (sorting.direction) { params.set("sort_by", sorting.field); params.set("sort_order", sorting.direction === "ascend" ? "asc" : "desc"); }
      return apiRequest<Page<DownloadTask>>("/downloads?" + params.toString(), { signal });
    },
  });
  const items = query.data?.items ?? [];
  const total = query.error ? 0 : query.data?.total ?? 0;
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

  /** Apply a single header filter while preserving other columns and resetting pagination. */
  function applyColumn(field: string, values: string[]) {
    const next = { ...columnFilters };
    if (values.length) next[field] = values; else delete next[field];
    setPage(1); setColumnFilters(next);
    if (page === 1 && JSON.stringify(next) === JSON.stringify(columnFilters)) void query.refetch();
  }

  /** 所有数据列在服务端筛选和排序，影片按番号关键词匹配。 */
  function column(field: string, label: string, kind?: DownloadFilterKind): Partial<TableColumnProps<DownloadTask>> {
    return {
      key: field, dataIndex: field, sorter: true, sortOrder: sorting.field === field ? sorting.direction : undefined,
      ...(kind ? { filteredValue: columnFilters[field] ?? [], filterDropdownProps: { triggerProps: { unmountOnExit: true, popupVisible: openFilter === field, onVisibleChange: (visible) => { setOpenFilter(visible ? field : null); if (visible) setFilterDrafts((current) => ({ ...current, [field]: columnFilters[field] ?? [] })); } } }, filterIcon: <span aria-label={label + "筛选"}><IconFilter /></span>,
        filterDropdown: () => <DownloadColumnFilter label={label} kind={kind} values={filterDrafts[field] ?? []}
          onDraftChange={(values) => setFilterDrafts((current) => ({ ...current, [field]: values }))}
          unitValue={filterUnits[field] ?? 1} onUnitChange={(unit) => setFilterUnits((current) => ({ ...current, [field]: unit }))}
          options={field === "transfer_status" ? Object.entries({ ...statusLabels, ...transferLabels }).map(([value, label]) => ({ value, label: <DownloadStatusBadge status={value} label={label} /> }))
            : field === "seeding" ? seedingOptions : field === "downloader" ? downloaderOptions : field === "source_site" ? sourceSites.data?.items : undefined}
          loading={field === "source_site" && sourceSites.isLoading} error={field === "source_site" ? sourceSites.error : undefined}
          onRetry={() => void sourceSites.refetch()}
          onApply={(values) => { applyColumn(field, values); setOpenFilter(null); }} /> } : {}),
    };
  }

  return (
    <section>
      <PageHeader title="下载任务" />
      <ContentCard className="table-shell data-table-shell">
        {query.error && <Alert type="error" content={query.error.message} action={<Button onClick={() => void query.refetch()}>重试</Button>} />}
          <Table
            className="data-table"
            border={false}
            rowKey="id"
            data={query.error ? [] : items}
            loading={query.isLoading}
            noDataElement={<div className="data-table-empty" role="status">暂无下载任务</div>}
            pagination={false}
            components={downloadTableComponents}
            tableLayoutFixed
            scroll={{ x: Object.values(columnWidths).reduce((sum, width) => sum + width, 0) }}
            onChange={(_, sorter, __, extra) => {
              if (extra.action !== "sort") return;
              const active = Array.isArray(sorter) ? sorter[0] : sorter;
              setSorting({ field: String(active?.field ?? ""), direction: active?.direction }); setPage(1);
            }}
            columns={([
              { ...column("code", "影片", "text"), title: "影片", width: 150, fixed: "left", render: (value: string | null, task: DownloadTask) => value && task.media_id
                ? <Button type="text" className="code-cell" onClick={() => setSelectedMediaId(task.media_id)}>{value}</Button>
                : <span className="code-cell">{value || "—"}</span> },
              { ...column("source_site", "资源站", "enum"), title: "资源站", width: 150, fixed: "left", render: (value: string | null) => value || "—" },
              { ...column("downloader", "下载器", "enum"), title: "下载器", render: (value: string | null) => value || "—" },
              { ...column("transfer_status", "下载状态", "enum"), title: "下载状态", render: (_: unknown, task: DownloadTask) => <DownloadStatus task={task} /> },
              { ...column("size_bytes", "大小", "bytes"), title: "大小", width: 110, render: (_: unknown, task: DownloadTask) => formatBytes(task.metrics?.size_bytes) },
              { ...column("remaining_bytes", "剩余", "bytes"), title: "剩余", width: 110, render: (_: unknown, task: DownloadTask) => formatBytes(task.metrics?.remaining_bytes) },
              { ...column("downloaded_bytes", "已下载", "bytes"), title: "已下载", width: 110, render: (_: unknown, task: DownloadTask) => formatBytes(task.metrics?.downloaded_bytes) },
              { ...column("download_speed", "下载速度", "bytes"), title: "下载速度", width: 120, render: (_: unknown, task: DownloadTask) => formatBytes(task.metrics?.download_speed, true) },
              { ...column("upload_speed", "上传速度", "bytes"), title: "上传速度", width: 120, render: (_: unknown, task: DownloadTask) => formatBytes(task.metrics?.upload_speed, true) },
              { ...column("download_url", "下载链接", "text"), title: "下载链接", render: (value: string | null) => <span style={{ overflowWrap: "anywhere", userSelect: "text" }}>{value || "-"}</span> },
              { ...column("save_path", "保存路径", "text"), title: "保存路径", width: 220, render: (_: unknown, task: DownloadTask) => <span style={{ overflowWrap: "anywhere" }}>{task.metrics?.save_path || "—"}</span> },
              { ...column("share_ratio", "分享率", "number"), title: <span className="download-ratio">分享率</span>, width: 130, render: (_: unknown, task: DownloadTask) => <span className="download-ratio">{task.metrics?.share_ratio == null ? "—" : task.metrics.share_ratio.toFixed(2)}</span> },
              { ...column("seeding_seconds", "做种时间", "seconds"), title: "做种时间", width: 140, render: (_: unknown, task: DownloadTask) => formatSeedingTime(task.metrics?.seeding_seconds) },
              { ...column("seeding", "完成做种（PT）", "enum"), title: "完成做种（PT）", width: 190, render: (_: unknown, task: DownloadTask) => <SeedingTag task={task} /> },
              { ...column("added_at", "加入时间", "time"), title: "加入时间", render: (value: string | null) => value ? dayjs(value).format("YYYY-MM-DD HH:mm") : "—" },
              { ...column("completed_at", "完成时间", "time"), title: "完成时间", render: (value: string | null) => value ? dayjs(value).format("YYYY-MM-DD HH:mm") : "—" },
              { title: "操作", key: "actions", width: 200, fixed: "right", render: (_: unknown, task: DownloadTask) => <DownloadActions task={task} onChanged={refreshAfterAction} /> },
            ] as TableColumnProps<DownloadTask>[]).map((definition) => {
              const field = String(definition.key);
              return { ...definition, width: columnWidths[field], headerCellStyle: { whiteSpace: "nowrap" },
                onHeaderCell: () => ({ resizeWidth: columnWidths[field], resizeLabel: field === "share_ratio" ? "分享率" : String(definition.title), onResizeWidth: (width: number, persist: boolean) => resizeColumn(field, width, persist) }),
              };
            })}
          />
          <ListPagination page={page} total={total} pageSize={pageSize} onChange={setPage} onPageSizeChange={changePageSize} />
      </ContentCard>
      <DownloadMediaDrawer mediaId={selectedMediaId} onClose={() => setSelectedMediaId(null)} />
    </section>
  );
}
