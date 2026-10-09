import { Button, Grid, Modal, Popover, Progress, Switch, Table, Tag, Tooltip } from "@arco-design/web-react";
import { IconDown, IconExclamationCircleFill, IconFile, IconFolder, IconInfoCircle, IconRight } from "@arco-design/web-react/icon";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { apiRequest } from "../../shared/api/client";
import { DEFAULT_PAGE_SIZE, ListPagination } from "../../shared/ui/ListPagination";

type FileRow = {
  id: string; name: string; kind: "directory" | "file";
  operation: "" | "generate" | "download"; state: string; error?: string;
  total: number; processed: number; failed: number; percent: number; bytes: number; size: number;
};
type FilePage = { items: FileRow[]; total: number; available: boolean };
const states: Record<string, string> = {
  scanning: "扫描中", processing: "处理中", waiting: "等待处理", completed: "已完成",
  skipped: "已跳过", failed: "失败", interrupted: "已中断",
};

/** 将下载字节换算成二进制单位，未知大小由调用方单独表达。 */
function bytes(value: number) {
  const unit = value > 0 ? Math.min(4, Math.floor(Math.log(value) / Math.log(1024))) : 0;
  return `${(value / 1024 ** unit).toFixed(unit ? 1 : 0)} ${["B", "KiB", "MiB", "GiB", "TiB"][unit]}`;
}

/** 未知总大小不推算百分比；失败图标可点击查看后端错误，旧明细明确提示原因缺失。 */
function FileProgress({ row }: { row: FileRow }) {
  const measurable = row.kind === "directory" || row.operation === "generate" || row.size > 0 || ["completed", "skipped"].includes(row.state);
  const failed = row.failed > 0 || ["failed", "interrupted"].includes(row.state);
  const fileFailed = failed && row.kind === "file";
  const errorInfo = fileFailed ? <Popover trigger="click" title="错误信息" getPopupContainer={() => document.body} content={
    <div className="strm-file-error">{row.error?.trim() || "未记录具体错误，请查看任务日志。"}</div>
  }>
    <Button type="text" status="danger" shape="circle" icon={<IconExclamationCircleFill />} aria-label={`查看 ${row.name} 的错误信息`} />
  </Popover> : null;
  return <div>
    {measurable ? <Progress percent={row.percent} status={failed ? "error" : undefined} formatText={fileFailed ? () => errorInfo : undefined} /> : <div>大小未知{errorInfo}</div>}
    {row.kind === "directory"
      ? <span>已处理 {row.processed} / 已发现 {row.total}{row.state === "scanning" ? "（扫描中）" : ""}</span>
      : row.operation === "download" ? <span>{bytes(row.bytes)} / {row.size > 0 ? bytes(row.size) : "未知大小"}</span> : null}
  </div>;
}

/** 每层独立分页，过滤切换同步回第一页并保留展开状态；实时完成导致页数缩减时回到有效页。 */
function DirectoryFiles({ taskId, parent = "", paused, hideCompleted }: { taskId: string; parent?: string; paused: boolean; hideCompleted: boolean }) {
  const [pagination, setPagination] = useState({ page: 1, hideCompleted });
  const page = pagination.hideCompleted === hideCompleted ? pagination.page : 1;
  const setPage = (next: number) => setPagination({ page: next, hideCompleted });
  if (pagination.hideCompleted !== hideCompleted) setPagination({ page: 1, hideCompleted });
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [expanded, setExpanded] = useState<(string | number)[]>([]);
  const query = useQuery({
    queryKey: ["strm-task-files", taskId, parent, page, pageSize, hideCompleted],
    queryFn: ({ signal }) => apiRequest<FilePage>(
      "/strm/scan/tasks/" + taskId + "/files?parent=" + encodeURIComponent(parent) + "&page=" + page + "&page_size=" + pageSize + "&hide_completed=" + hideCompleted,
      { signal },
    ),
    refetchInterval: 750, retry: false,
  });
  const total = query.data?.total;
  useEffect(() => {
    if (total !== undefined && page > Math.max(1, Math.ceil(total / pageSize))) {
      setPagination({ page: Math.max(1, Math.ceil(total / pageSize)), hideCompleted });
    }
  }, [total, page, pageSize, hideCompleted]);
  return <div className="strm-file-table">
    {query.error ? <span role="alert">加载文件进度失败：{query.error.message}</span> : null}
    <Table<FileRow>
      rowKey="id" data={query.data?.items ?? []} loading={query.isPending} pagination={false}
      expandedRowKeys={expanded} onExpandedRowsChange={setExpanded}
      expandedRowRender={row => expanded.includes(row.id) ? <DirectoryFiles taskId={taskId} parent={row.id} paused={paused} hideCompleted={hideCompleted} /> : null}
      expandProps={{
        rowExpandable: row => row.kind === "directory",
        icon: ({ record, expanded: open }) => <span
          role="button" tabIndex={0} aria-expanded={open} aria-label={(open ? "收起" : "展开") + "目录 " + record.name}
          onKeyDown={event => {
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault();
              setExpanded(keys => open ? keys.filter(key => key !== record.id) : [...keys, record.id]);
            }
          }}
        >{open ? <IconDown /> : <IconRight />}</span>,
      }}
      noDataElement={query.data?.available === false ? "明细等待任务恢复后重建" : hideCompleted ? "暂无未完成项" : "尚未发现符合规则的文件"}
      columns={[
        { title: "目录 / 文件", dataIndex: "name", render: (_, row) => <span className="strm-file-name">{row.kind === "directory" ? <IconFolder /> : <IconFile />}{row.name}</span> },
        { title: "状态", width: 125, render: (_, row) => <div>
          {row.kind === "file" ? <div>{row.operation === "download" ? "下载附件" : "生成 STRM"}</div> : null}
          <Tag color={row.failed > 0 || ["failed", "interrupted"].includes(row.state) ? "red" : undefined}>
            {paused && ["scanning", "processing"].includes(row.state) ? "已暂停" : states[row.state] ?? row.state}
          </Tag>
          {row.failed > 0 ? <span> 失败 {row.failed}</span> : null}
        </div> },
        { title: "进度", width: 210, render: (_, row) => <FileProgress row={row} /> },
      ]}
    />
    {query.data && query.data.total > DEFAULT_PAGE_SIZE ? <ListPagination
      page={page} total={query.data.total} pageSize={pageSize}
      onChange={next => { setPage(next); setExpanded([]); }}
      onPageSizeChange={size => { setPageSize(size); setPage(1); setExpanded([]); }}
    /> : null}
  </div>;
}

/** 信息入口仅属于 STRM 任务；60% 视口弹窗统一承载各层溢出滚动，不触发扫描或下载。 */
export function StrmFileProgress({ taskId, paused }: { taskId: string; paused: boolean }) {
  const [visible, setVisible] = useState(false);
  const [hideCompleted, setHideCompleted] = useState(false);
  return <>
    <Tooltip content="查看生成与下载文件进度">
      <Button type="text" shape="circle" icon={<IconInfoCircle />} aria-label="查看 STRM 文件进度" onClick={() => { setHideCompleted(false); setVisible(true); }} />
    </Tooltip>
    <Modal title="STRM 生成与下载进度" visible={visible} onCancel={() => setVisible(false)} footer={null} unmountOnExit style={{ width: "60vw", height: "60dvh", overflow: "auto", overscrollBehavior: "contain" }}>
      <div className="strm-file-dialog-content">
        <p>展开目录查看子目录和文件。目录总数随扫描增加；文件进度按已处理项或下载字节计算。</p>
        <Grid.Row gutter={[12, 12]} justify="start" align="center" className="filter-toolbar">
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <label className="strm-file-filter">隐藏已完成 <Switch aria-label="隐藏已完成" checked={hideCompleted} onChange={setHideCompleted} /></label>
          </Grid.Col>
        </Grid.Row>
        <div role="region" aria-label="STRM 文件列表" tabIndex={0}>
          {visible ? <DirectoryFiles key={taskId} taskId={taskId} paused={paused} hideCompleted={hideCompleted} /> : null}
        </div>
      </div>
    </Modal>
  </>;
}
