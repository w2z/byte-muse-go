import { Button, Modal, Progress, Table, Tag, Tooltip } from "@arco-design/web-react";
import { IconDown, IconFile, IconFolder, IconInfoCircle, IconRight } from "@arco-design/web-react/icon";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest } from "../../shared/api/client";
import { DEFAULT_PAGE_SIZE, ListPagination } from "../../shared/ui/ListPagination";

type FileRow = {
  id: string; name: string; kind: "directory" | "file";
  operation: "" | "generate" | "download"; state: string;
  total: number; processed: number; failed: number; percent: number; bytes: number; size: number;
};
type FilePage = { items: FileRow[]; total: number; available: boolean };
const states: Record<string, string> = {
  scanning: "扫描中", processing: "处理中", completed: "已完成",
  skipped: "已跳过", failed: "失败", interrupted: "已中断",
};

/** 将下载字节换算成二进制单位，未知大小由调用方单独表达。 */
function bytes(value: number) {
  const unit = value > 0 ? Math.min(4, Math.floor(Math.log(value) / Math.log(1024))) : 0;
  return `${(value / 1024 ** unit).toFixed(unit ? 1 : 0)} ${["B", "KiB", "MiB", "GiB", "TiB"][unit]}`;
}

/** 下载总大小未知时只显示已传输字节，不推算百分比。 */
function FileProgress({ row }: { row: FileRow }) {
  const measurable = row.kind === "directory" || row.operation === "generate" || row.size > 0 || ["completed", "skipped"].includes(row.state);
  const failed = row.failed > 0 || ["failed", "interrupted"].includes(row.state);
  return <div>
    {measurable ? <Progress percent={row.percent} status={failed ? "error" : undefined} /> : <div>大小未知</div>}
    {row.kind === "directory"
      ? <span>已处理 {row.processed} / 已发现 {row.total}{row.state === "scanning" ? "（扫描中）" : ""}</span>
      : row.operation === "download" ? <span>{bytes(row.bytes)} / {row.size > 0 ? bytes(row.size) : "未知大小"}</span> : null}
  </div>;
}

/** 每个展开目录独立查询直属子项，收起后卸载查询；已展开目录的分页互不影响。 */
function DirectoryFiles({ taskId, parent = "", paused }: { taskId: string; parent?: string; paused: boolean }) {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [expanded, setExpanded] = useState<(string | number)[]>([]);
  const query = useQuery({
    queryKey: ["strm-task-files", taskId, parent, page, pageSize],
    queryFn: ({ signal }) => apiRequest<FilePage>(
      "/strm/scan/tasks/" + taskId + "/files?parent=" + encodeURIComponent(parent) + "&page=" + page + "&page_size=" + pageSize,
      { signal },
    ),
    refetchInterval: 750, retry: false,
  });
  return <div className="strm-file-table">
    {query.error ? <span role="alert">加载文件进度失败：{query.error.message}</span> : null}
    <Table<FileRow>
      rowKey="id" data={query.data?.items ?? []} loading={query.isPending} pagination={false} tableLayoutFixed
      expandedRowKeys={expanded} onExpandedRowsChange={setExpanded}
      expandedRowRender={row => expanded.includes(row.id) ? <DirectoryFiles taskId={taskId} parent={row.id} paused={paused} /> : null}
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
      noDataElement={query.data?.available === false ? "明细等待任务恢复后重建" : "尚未发现符合规则的文件"}
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
      scroll={{ x: 600 }}
    />
    {query.data && query.data.total > DEFAULT_PAGE_SIZE ? <ListPagination
      page={page} total={query.data.total} pageSize={pageSize}
      onChange={next => { setPage(next); setExpanded([]); }}
      onPageSizeChange={size => { setPageSize(size); setPage(1); setExpanded([]); }}
    /> : null}
  </div>;
}

/** 信息入口仅属于 STRM 任务；弹窗展示实时文件及目录进度，不触发扫描或下载。 */
export function StrmFileProgress({ taskId, paused }: { taskId: string; paused: boolean }) {
  const [visible, setVisible] = useState(false);
  return <>
    <Tooltip content="查看生成与下载文件进度">
      <Button type="text" shape="circle" icon={<IconInfoCircle />} aria-label="查看 STRM 文件进度" onClick={() => setVisible(true)} />
    </Tooltip>
    <Modal title="STRM 生成与下载进度" visible={visible} onCancel={() => setVisible(false)} footer={null} unmountOnExit style={{ width: "min(960px, calc(100vw - 24px))" }}>
      <p>展开目录查看子目录和文件。目录总数随扫描增加；文件进度按已处理项或下载字节计算。</p>
      {visible ? <DirectoryFiles key={taskId} taskId={taskId} paused={paused} /> : null}
    </Modal>
  </>;
}
