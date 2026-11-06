import { Button, Message, Progress, Select, Space, Table, Tabs, Tag, Tooltip } from "@arco-design/web-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { apiRequest } from "../../shared/api/client";
import { ListPagination } from "../../shared/ui/ListPagination";

type DirectoryRow = {
  key: string;
  local_path: string;
  remote_path: string;
  kind: string;
  size: number;
  progress: number;
  speed: number;
  uploaded: number;
  total: number;
  skipped: number;
  failed: number;
  actions?: Record<string, boolean>;
  queued_at?: string;
  completed_at?: string;
};
type FileRow = {
  key: string;
  path: string;
  remote_path?: string;
  size: number;
  uploaded_bytes?: number;
  progress: number;
  speed: number;
  state: string;
  error: string;
  retries?: number;
  max_retries?: number;
  queued_at?: string;
  completed_at?: string;
  actions?: Record<string, boolean>;
};
const states: Record<string, string> = {
  paused: "已暂停",
  uploaded: "等待替换",
  backing_up: "保留旧文件",
  committing: "正在替换",
  restoring: "恢复旧文件",
  cleanup: "已上传，待清理",
  discarding: "清理临时文件",
  discarded: "源文件已变化",
  attention: "需处理",
  blocked: "等待账号或权限恢复",
  stopped: "已停止",
  waiting: "等待文件稳定",
  transferring: "传送至 CD2",
  uploading: "上传到网盘",
  verifying: "校验中",
  completed: "已上传",
  skipped: "已跳过",
  failed: "失败",
};
/** 将字节数格式化为二进制容量，零值也明确展示。 */
function bytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const index = Math.max(0, Math.min(4, Math.floor(Math.log(value) / Math.log(1024))));
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${["B", "KiB", "MiB", "GiB", "TiB"][index]}`;
}
/** 将服务端 UTC 时间显示为本地时间；未完成时明确留空。 */
function formatTime(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString("zh-CN", { hour12: false });
}
const progress = (value: number) => (
  <Progress percent={Math.min(100, Math.max(0, Math.round(value * 10) / 10))} />
);
/** 校验阶段单独显示已读取字节，避免进度条停留在 0 时无法区分是否仍在计算。 */
function fileProgress(row: FileRow) {
  return (
    <div>
      {progress(row.progress)}
      {row.state === "verifying" ? (
        <div>已校验 {bytes(row.uploaded_bytes ?? 0)} / {bytes(row.size)}</div>
      ) : null}
    </div>
  );
}
/** 自动重试期间显示已用次数；用尽后才显示失败和错误原因。 */
function fileState(row: FileRow) {
  if (row.state === "retrying") {
    const current = row.retries ?? 0;
    const total = row.max_retries || 3;
    return (
      <Tooltip content={`当前重试次数:${current}次，总共${total}次`}>
        <Tag>{`处理中 (${current}/${total})`}</Tag>
      </Tooltip>
    );
  }
  return (
    <span title={row.error}>
      <Tag color={row.state === "failed" ? "red" : row.state === "completed" ? "green" : undefined}>
        {states[row.state] ?? row.state}
      </Tag>
      {row.error ? <div>{row.error}</div> : null}
    </span>
  );
}

type UploadTarget = { action: "pause" | "stop" | "start" | "retry" | "delete"; scope: "directory" | "file"; key: string };
type HeaderCommand = { action: "clear_completed" | "clear_failed"; scope: "directories" | "files" };

const statusOptions = [
  { label: "全部", value: "all" },
  { label: "进行中", value: "running" },
  { label: "等待中", value: "waiting" },
  { label: "已暂停", value: "paused" },
  { label: "已停止", value: "stopped" },
  { label: "失败", value: "failed" },
  { label: "已完成", value: "completed" },
  { label: "已跳过", value: "skipped" },
  { label: "已变化", value: "changed" },
];

/** 目录与文件分别展示，文件使用服务端分页；后台进度每两秒刷新。 */
export function UploadTables() {
  const client = useQueryClient();
  const [tab, setTab] = useState("directories");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(15);
  const [directoryStatus, setDirectoryStatus] = useState("all");
  const [fileStatus, setFileStatus] = useState("all");
  const status = useQuery({queryKey:["cloud-upload-status"],queryFn:()=>apiRequest<{actions?:Record<string,boolean>}>("/cloud-upload/status"),refetchInterval:2000});
  const control = useMutation({
    mutationFn:(command:string | HeaderCommand)=>apiRequest("/cloud-upload/control",{method:"POST",body:JSON.stringify(typeof command==="string"?{action:command}:command)}),
    onSuccess:async()=>{await Promise.all([client.invalidateQueries({queryKey:["cloud-upload-status"]}),client.invalidateQueries({queryKey:["upload-directory-progress"]}),client.invalidateQueries({queryKey:["upload-file-progress"]})]);},
    onError:(error:Error)=>{Message.error(error.message);void client.invalidateQueries({queryKey:["cloud-upload-status"]});},
  });
  const rowControl = useMutation({
    mutationFn: (target: UploadTarget) => apiRequest("/cloud-upload/control", { method: "POST", body: JSON.stringify(target) }),
    onSuccess: async () => { await Promise.all([client.invalidateQueries({ queryKey: ["cloud-upload-status"] }), client.invalidateQueries({ queryKey: ["upload-directory-progress"] }), client.invalidateQueries({ queryKey: ["upload-file-progress"] })]); },
    onError: (error: Error) => { Message.error(error.message); void client.invalidateQueries({ queryKey: ["upload-directory-progress"] }); void client.invalidateQueries({ queryKey: ["upload-file-progress"] }); },
  });
  const rowActions = (scope: UploadTarget["scope"], name: string, row: { key: string; actions?: Record<string, boolean> }) => <Space wrap>{([
    ["pause", "暂停"], ["stop", "停止"], ["start", "开始"], ["retry", "重新上传"], ["delete", "删除"],
  ] as const).filter(([action]) => row.actions?.[action]).map(([action, text]) => <Button key={action} htmlType="button" status={action === "delete" ? "danger" : undefined} aria-label={`${text}${scope === "directory" ? "目录" : "文件"} ${name}`} disabled={rowControl.isPending} loading={rowControl.isPending && rowControl.variables?.key === row.key && rowControl.variables.action === action} onClick={() => rowControl.mutate({ action, scope, key: row.key })}>{text}</Button>)}</Space>;
  const listScope = tab === "files" ? "files" : "directories";
  const headerEnabled = (command: string | HeaderCommand) => {
    if (typeof command === "string") return Boolean(status.data?.actions?.[command]);
    const name = `${command.action}_${command.scope}`;
    return Boolean(status.data?.actions?.[name]);
  };
  const actions = <Space wrap className="settings-upload-actions" aria-label="上传任务操作">{([
    ["pause","暂停","pause"],["stop","停止","stop"],["clear_completed","清除已完成","clear_completed"],["resume","继续","resume"],
  ] as const).map(([action,label])=><Button key={action} htmlType="button" disabled={status.isError || !headerEnabled(action) || control.isPending} loading={control.isPending && control.variables===action} onClick={()=>control.mutate(action)}>{label}</Button>)}
    {([
      ["clear_completed","清空已完成"],["clear_failed","清空错误"],
    ] as const).map(([action,label])=><Button key={`${action}-${listScope}`} htmlType="button" disabled={status.isError || !headerEnabled({action,scope:listScope}) || control.isPending} loading={control.isPending && typeof control.variables==="object" && control.variables?.action===action && control.variables.scope===listScope} onClick={()=>control.mutate({action,scope:listScope})}>{label}</Button>)}
    <Button htmlType="button" status="danger" disabled={status.isError || !headerEnabled("delete_all") || control.isPending} loading={control.isPending && control.variables==="delete_all"} onClick={()=>control.mutate("delete_all")}>删除所有任务</Button>
  </Space>;
  const directories = useQuery({
    queryKey: ["upload-directory-progress", directoryStatus],
    queryFn: () =>
      apiRequest<{ items: DirectoryRow[] }>(
        `/cloud-upload/directories/progress?status=${encodeURIComponent(directoryStatus)}`,
      ),
    refetchInterval: 2000,
  });
  const files = useQuery({
    queryKey: ["upload-file-progress", page, pageSize, fileStatus],
    queryFn: () =>
      apiRequest<{ items: FileRow[]; total: number }>(
        `/cloud-upload/files?page=${page}&page_size=${pageSize}&status=${encodeURIComponent(fileStatus)}`,
      ),
    refetchInterval: 2000,
    enabled: tab === "files",
  });
  useEffect(() => {
    if (files.data && page > 1 && (page - 1) * pageSize >= files.data.total)
      setPage(Math.max(1, Math.ceil(files.data.total / pageSize)));
  }, [files.data, page, pageSize]);
  return (
    <div className="settings-upload-tables">
      <Tabs
        activeTab={tab}
        onChange={setTab}
        animation={false}
        aria-label="上传任务列表"
        renderTabHeader={(props, DefaultTabHeader)=><div className="settings-upload-header"><DefaultTabHeader {...props}/>{actions}</div>}
      >
        <Tabs.TabPane key="directories" title="目录列表">
          {directories.error ? (
            <p role="alert">{directories.error.message}</p>
          ) : null}
          <div className="settings-upload-filter">
            <span>状态</span>
            <Select aria-label="目录状态" value={directoryStatus} options={statusOptions} onChange={(value) => setDirectoryStatus(String(value))} />
          </div>
          <Table<DirectoryRow>
            rowKey="key"
            loading={directories.isPending}
            data={directories.data?.items ?? []}
            pagination={false}
            scroll={{ x: 1720 }}
            columns={[
              { title: "本地目录", dataIndex: "local_path", width: 230 },
              {
                title: "网盘目录",
                width: 230,
                render: (_, row) =>
                  `${row.kind === "115" ? "115" : "CD2"} · ${row.remote_path}`,
              },
              {
                title: "文件夹大小",
                dataIndex: "size",
                width: 120,
                render: bytes,
              },
              {
                title: "上传进度",
                dataIndex: "progress",
                width: 150,
                render: progress,
              },
              {
                title: "上传速度",
                dataIndex: "speed",
                width: 120,
                render: (value: number) => `${bytes(value)}/s`,
              },
              {
                title: "已上传/总个数",
                width: 130,
                render: (_, row) => `${row.uploaded}/${row.total}`,
              },
              {
                title: "跳过/失败",
                width: 110,
                render: (_, row) => `${row.skipped}/${row.failed}`,
              },
              { title: "添加时间", dataIndex: "queued_at", width: 180, render: formatTime },
              { title: "完成时间", dataIndex: "completed_at", width: 180, render: formatTime },
              { title: "操作", width: 300, render: (_, row) => rowActions("directory", row.local_path, row) },
            ]}
          />
        </Tabs.TabPane>
        <Tabs.TabPane key="files" title="文件列表">
          {files.error ? <p role="alert">{files.error.message}</p> : null}
          <div className="settings-upload-filter">
            <span>状态</span>
            <Select aria-label="文件状态" value={fileStatus} options={statusOptions} onChange={(value) => { setFileStatus(String(value)); setPage(1); }} />
          </div>
          <Table<FileRow>
            rowKey="key"
            loading={files.isPending}
            data={files.data?.items ?? []}
            pagination={false}
            scroll={{ x: 1960 }}
            columns={[
              { title: "本地路径", dataIndex: "path", width: 300 },
              { title: "网盘路径", dataIndex: "remote_path", width: 300 },
              {
                title: "文件大小",
                dataIndex: "size",
                width: 120,
                render: bytes,
              },
              {
                title: "状态",
                width: 180,
                render: (_, row) => fileState(row),
              },
              {
                title: "上传进度",
                dataIndex: "progress",
                width: 220,
                render: (_, row) => fileProgress(row),
              },
              {
                title: "上传速度",
                dataIndex: "speed",
                width: 120,
                render: (value: number) => `${bytes(value)}/s`,
              },
              { title: "添加时间", dataIndex: "queued_at", width: 180, render: formatTime },
              { title: "完成时间", dataIndex: "completed_at", width: 180, render: formatTime },
              { title: "操作", width: 300, render: (_, row) => rowActions("file", row.path, row) },
            ]}
          />
          <ListPagination
            page={page}
            total={files.data?.total ?? 0}
            pageSize={pageSize}
            pageSizeOptions={[15, 30, 50, 100]}
            onChange={setPage}
            onPageSizeChange={(size) => {
              setPageSize(size);
              setPage(1);
            }}
          />
        </Tabs.TabPane>
      </Tabs>
    </div>
  );
}
