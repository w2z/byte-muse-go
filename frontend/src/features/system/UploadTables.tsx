import { Button, Message, Progress, Space, Table, Tabs, Tag } from "@arco-design/web-react";
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
};
type FileRow = {
  key: string;
  path: string;
  size: number;
  progress: number;
  speed: number;
  state: string;
  error: string;
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
  failed: "失败，等待重试",
};
/** 将字节数格式化为二进制容量，零值也明确展示。 */
function bytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const index = Math.max(0, Math.min(4, Math.floor(Math.log(value) / Math.log(1024))));
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${["B", "KiB", "MiB", "GiB", "TiB"][index]}`;
}
const progress = (value: number) => (
  <Progress percent={Math.min(100, Math.max(0, Math.round(value * 10) / 10))} />
);

/** 目录与文件分别展示，文件使用服务端分页；后台进度每两秒刷新。 */
export function UploadTables() {
  const client = useQueryClient();
  const status = useQuery({queryKey:["cloud-upload-status"],queryFn:()=>apiRequest<{actions?:Record<string,boolean>}>("/cloud-upload/status"),refetchInterval:2000});
  const control = useMutation({
    mutationFn:(action:string)=>apiRequest("/cloud-upload/control",{method:"POST",body:JSON.stringify({action})}),
    onSuccess:async()=>{await Promise.all([client.invalidateQueries({queryKey:["cloud-upload-status"]}),client.invalidateQueries({queryKey:["upload-directory-progress"]}),client.invalidateQueries({queryKey:["upload-file-progress"]})]);},
    onError:(error:Error)=>{Message.error(error.message);void client.invalidateQueries({queryKey:["cloud-upload-status"]});},
  });
  const actions = <Space wrap className="settings-upload-actions" aria-label="上传任务操作">{[
    ["pause","暂停"],["stop","停止"],["clear_completed","清除已完成"],["resume","继续"],["delete_all","删除所有任务"],
  ].map(([action,label])=><Button key={action} htmlType="button" status={action==="delete_all"?"danger":undefined} disabled={status.isError || !status.data?.actions?.[action] || control.isPending} loading={control.isPending && control.variables===action} onClick={()=>control.mutate(action)}>{label}</Button>)}</Space>;
  const [tab, setTab] = useState("directories");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(15);
  const directories = useQuery({
    queryKey: ["upload-directory-progress"],
    queryFn: () =>
      apiRequest<{ items: DirectoryRow[] }>(
        "/cloud-upload/directories/progress",
      ),
    refetchInterval: 2000,
  });
  const files = useQuery({
    queryKey: ["upload-file-progress", page, pageSize],
    queryFn: () =>
      apiRequest<{ items: FileRow[]; total: number }>(
        `/cloud-upload/files?page=${page}&page_size=${pageSize}`,
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
          <Table<DirectoryRow>
            rowKey="key"
            loading={directories.isPending}
            data={directories.data?.items ?? []}
            pagination={false}
            scroll={{ x: 1050 }}
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
            ]}
          />
        </Tabs.TabPane>
        <Tabs.TabPane key="files" title="文件列表">
          {files.error ? <p role="alert">{files.error.message}</p> : null}
          <Table<FileRow>
            rowKey="key"
            loading={files.isPending}
            data={files.data?.items ?? []}
            pagination={false}
            scroll={{ x: 900 }}
            columns={[
              { title: "文件路径", dataIndex: "path", width: 300 },
              {
                title: "文件大小",
                dataIndex: "size",
                width: 120,
                render: bytes,
              },
              {
                title: "状态",
                width: 160,
                render: (_, row) => (
                  <span title={row.error}>
                    <Tag
                      color={
                        row.state === "failed"
                          ? "red"
                          : row.state === "completed"
                            ? "green"
                            : undefined
                      }
                    >
                      {states[row.state] ?? row.state}
                    </Tag>
                    {row.error ? <div>{row.error}</div> : null}
                  </span>
                ),
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
