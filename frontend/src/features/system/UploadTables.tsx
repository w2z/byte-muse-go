import { Button, Message, Progress, Select, Space, Table, Tabs, Tag, Tooltip } from "@arco-design/web-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { apiRequest } from "../../shared/api/client";
import { ListPagination } from "../../shared/ui/ListPagination";
import { UploadMonitorStatus } from "./UploadPathsField";

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
type DirectoryPage = { items: DirectoryRow[]; total?: number; completed?: number };
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
type FilePage = { items: FileRow[]; total: number; task_total?: number; completed?: number };
const states: Record<string, string> = {
  paused: "已暂停",
  uploaded: "等待替换",
  backing_up: "保留旧文件",
  committing: "正在替换",
  restoring: "恢复旧文件",
  cleanup: "已上传，待清理",
  discarding: "清理临时文件",
  discarded: "失败",
  attention: "需处理",
  blocked: "等待账号或权限恢复",
  stopped: "已停止",
  waiting: "等待中",
  transferring: "传送至 CD2",
  uploading: "上传到网盘",
  verifying: "校验中",
  completed: "成功",
  skipped: "成功",
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
/** 成功使用组件成功色，失败使用错误色；百分比文字与进度条保持同一状态色。 */
function uploadProgress(value: number, status?: "success" | "error") {
  const percent = Math.min(100, Math.max(0, Math.round(value * 10) / 10));
  return (
    <div className={status ? `settings-upload-progress settings-upload-progress-${status}` : undefined}>
      <Progress percent={percent} {...(status ? { status } : {})} formatText={(current) => `${current}%`} />
    </div>
  );
}
function fileProgressStatus(state: string): "success" | "error" | undefined {
  if (state === "failed" || state === "discarded") return "error";
  if (state === "completed" || state === "skipped") return "success";
  return undefined;
}
/** 校验阶段单独显示已读取字节，避免进度条停留在 0 时无法区分是否仍在计算。 */
function fileProgress(row: FileRow) {
  return (
    <div>
      {uploadProgress(row.progress, fileProgressStatus(row.state))}
      {row.state === "verifying" ? (
        <div>已校验 {bytes(row.uploaded_bytes ?? 0)} / {bytes(row.size)}</div>
      ) : null}
    </div>
  );
}
/** 首次失败即展示原因，重试次数由服务端计数驱动。 */
function fileState(row: FileRow) {
  if (row.state === "retrying") {
    const current = row.retries ?? 0;
    const total = row.max_retries || 3;
    const retryError = row.error && row.error.startsWith("正在重试 (")
      ? row.error
      : row.error
        ? `正在重试 (${current}/${total})；${row.error}`
        : "";
    return (
      <span title={row.error}>
        <Tooltip content={`当前重试次数:${current}次，总共${total}次`}>
          <Tag>{`正在重试 (${current}/${total})`}</Tag>
        </Tooltip>
        {retryError ? <div className="settings-upload-error">{retryError}</div> : null}
      </span>
    );
  }
  if (row.state === "verifying") {
    return (
      <span>
        <Tag>{states[row.state]}</Tag>
        <div className="settings-upload-verifying">正在校验文件 ({Math.round(row.progress)}%)</div>
        {row.error ? <div className="settings-upload-error">{row.error}</div> : null}
      </span>
    );
  }
  return (
    <span title={row.error}>
      <Tag color={fileProgressStatus(row.state) === "error" ? "red" : fileProgressStatus(row.state) === "success" ? "green" : undefined}>
        {states[row.state] ?? row.state}
      </Tag>
      {row.state === "skipped" ? <div className="settings-upload-skipped"><Tooltip content="已存在相同的文件，跳过上传"><span tabIndex={0}>(跳过)</span></Tooltip></div> : null}
      {row.error ? <div className="settings-upload-error">{row.error}</div> : null}
      {row.state === "discarded" && !row.error ? <div className="settings-upload-error">源文件已变化</div> : null}
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
  { label: "成功", value: "completed" },
];

/** 目录与文件分别展示；标签右侧显示实况，下方操作栏随当前列表切换，后台每两秒刷新。 */
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
  const actions = <div className="settings-upload-actions" aria-label="上传任务操作">{([
    ["pause","暂停","pause"],["stop","停止","stop"],["resume","继续","resume"],["retry_all","全部重试","retry_all"],
  ] as const).map(([action,label])=><Button key={action} htmlType="button" disabled={status.isError || !headerEnabled(action) || control.isPending} loading={control.isPending && control.variables===action} onClick={()=>control.mutate(action)}>{label}</Button>)}
    {([
      ["clear_completed","清空已完成"],["clear_failed","清空错误"],
    ] as const).map(([action,label])=><Button key={`${action}-${listScope}`} htmlType="button" disabled={status.isError || !headerEnabled({action,scope:listScope}) || control.isPending} loading={control.isPending && typeof control.variables==="object" && control.variables?.action===action && control.variables.scope===listScope} onClick={()=>control.mutate({action,scope:listScope})}>{label}</Button>)}
    <Button htmlType="button" status="danger" disabled={status.isError || !headerEnabled("delete_all") || control.isPending} loading={control.isPending && control.variables==="delete_all"} onClick={()=>control.mutate("delete_all")}>删除所有任务</Button>
  </div>;
  const directories = useQuery({
    queryKey: ["upload-directory-progress", directoryStatus],
    queryFn: () =>
      apiRequest<DirectoryPage>(
        `/cloud-upload/directories/progress?status=${encodeURIComponent(directoryStatus)}`,
      ),
    refetchInterval: 2000,
  });
  const files = useQuery({
    queryKey: ["upload-file-progress", page, pageSize, fileStatus],
    queryFn: () =>
      apiRequest<FilePage>(
        `/cloud-upload/files?page=${page}&page_size=${pageSize}&status=${encodeURIComponent(fileStatus)}`,
      ),
    refetchInterval: 2000,
  });
  const directoryTotal = directories.data?.total ?? 0;
  const fileTotal = files.data?.task_total ?? files.data?.total ?? 0;
  const directoryLabel = directoryTotal > 0 ? `目录列表 (${directories.data?.completed ?? 0}/${directoryTotal})` : "目录列表";
  const fileLabel = fileTotal > 0 ? `文件列表 (${files.data?.completed ?? 0}/${fileTotal})` : "文件列表";
  const filter = tab === "files" ? (
    <Select className="settings-upload-status-select" aria-label="文件状态" value={fileStatus} options={statusOptions} onChange={(value) => { setFileStatus(String(value)); setPage(1); }} />
  ) : (
    <Select className="settings-upload-status-select" aria-label="目录状态" value={directoryStatus} options={statusOptions} onChange={(value) => setDirectoryStatus(String(value))} />
  );
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
        extra={<div className="settings-upload-summary"><UploadMonitorStatus /></div>}
        renderTabHeader={(props, DefaultTabHeader) => <>
          <div className="settings-upload-header">
            <DefaultTabHeader {...props} />
          </div>
          <div className="settings-upload-toolbar"><span>状态</span>{filter}{actions}</div>
        </>}
      >
        <Tabs.TabPane key="directories" title={directoryLabel}>
          {directories.error ? (
            <p role="alert">{directories.error.message}</p>
          ) : null}
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
                render: (_, row) => uploadProgress(row.progress, row.completed_at ? "success" : undefined),
              },
              {
                title: "上传速度",
                dataIndex: "speed",
                width: 120,
                render: (value: number) => `${bytes(value)}/s`,
              },
              {
                title: "成功/总个数",
                width: 130,
                render: (_, row) => `${row.uploaded + row.skipped}/${row.total}`,
              },
              {
                title: "失败",
                width: 110,
                render: (_, row) => row.failed,
              },
              { title: "添加时间", dataIndex: "queued_at", width: 180, render: formatTime },
              { title: "完成时间", dataIndex: "completed_at", width: 180, render: formatTime },
              { title: "操作", width: 300, render: (_, row) => rowActions("directory", row.local_path, row) },
            ]}
          />
        </Tabs.TabPane>
        <Tabs.TabPane key="files" title={fileLabel}>
          {files.error ? <p role="alert">{files.error.message}</p> : null}
          <Table<FileRow>
            rowKey="key"
            loading={files.isPending}
            data={files.data?.items ?? []}
            pagination={false}
            scroll={{ x: 1960 }}
            columns={[
              {
                title: "路径",
                width: 420,
                render: (_, row) => (
                  <div className="settings-upload-paths">
                    <div>本地路径: {row.path}</div>
                    <div>网盘路径: {row.remote_path || "—"}</div>
                  </div>
                ),
              },
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
