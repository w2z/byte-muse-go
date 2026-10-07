import { Button, Input, Modal, Select } from "@arco-design/web-react";
import { IconDelete, IconFolder } from "@arco-design/web-react/icon";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../../shared/api/client";
import { Pan115DirectoryPicker } from "./Pan115DirectoryPicker";
import { StrmDirectoryPicker } from "./StrmPathsField";

/** 上传映射：本地路径是服务所在主机可访问的绝对路径；115 使用目录 ID，CD2 使用完整路径。 */
export type UploadMapping = {
  kind: "115" | "cd2";
  id: string;
  path: string;
  local_path: string;
};

/** 展示后台监控实况，计数以服务为准；查询失败不会伪装成监控已停止。 */
export function UploadMonitorStatus() {
  const query = useQuery({
    queryKey: ["cloud-upload-status"],
    queryFn: () =>
      apiRequest<{
        enabled: boolean;
        state: string;
        total: number;
        processed: number;
        uploaded: number;
        skipped: number;
        failed: number;
        current: string;
        error: string;
      }>("/cloud-upload/status"),
    refetchInterval: 2000,
  });
  const status = query.data;
  if (query.error)
    return (
      <span className="settings-field-description">
        读取监控状态失败：{query.error.message}
      </span>
    );
  if (!status)
    return (
      <span className="settings-field-description">正在读取监控状态…</span>
    );
  const percent =
    status.total > 0 ? Math.round((status.processed / status.total) * 100) : 0;
  return (
    <div className="settings-field-description" role="status">
      <div>
        {status.state === "paused" ? "任务已暂停" : status.state === "stopped" ? "当前批次已停止" : status.state === "failed" ? "监控启动失败" : status.enabled
          ? status.state === "uploading"
            ? "正在上传"
            : "正在监控"
          : "监控已关闭"}{" "}
        · 已处理 {status.processed}/{status.total}（{percent}%），上传{" "}
        {status.uploaded}，跳过 {status.skipped}，异常任务 {status.failed}
      </div>
      {status.current ? <div>当前文件：{status.current}</div> : null}
      {status.error ? <div>最近错误：{status.error}</div> : null}
    </div>
  );
}

/** 构造服务器目录面包屑，兼容 Linux 根目录及 Windows 盘符。 */
function uploadCrumbs(path: string) {
  const parts = path.split("/").filter(Boolean);
  const windows = /^[A-Za-z]:/.test(path);
  return [
    { key: "/", name: "根目录" },
    ...parts.map((name, index) => ({
      name,
      key: `${windows ? "" : "/"}${parts.slice(0, index + 1).join("/")}${windows && index === 0 ? "/" : ""}`,
    })),
  ];
}

/** 复用公共目录选择器维护多组本地到网盘映射；草稿随设置页统一保存。 */
export function UploadPathsField({
  value,
  onChange,
}: {
  value: UploadMapping[];
  onChange: (next: UploadMapping[]) => void;
}) {
  const [picking, setPicking] = useState<{
    index: number;
    local: boolean;
  } | null>(null);
  const current = picking ? value[picking.index] : undefined;
  const close = () => setPicking(null);
  const update = (index: number, patch: Partial<UploadMapping>) =>
    onChange(
      value.map((item, i) => (i === index ? { ...item, ...patch } : item)),
    );
  const choose = (target: { id: string; path: string }) => {
    if (picking)
      update(
        picking.index,
        picking.local
          ? { local_path: target.path }
          : { id: target.id, path: target.path },
      );
    close();
  };
  return (
    <div className="settings-strm-paths">
      {value.map((item, index) => (
        <div className="settings-upload-mapping" key={index}>
          <Input
            aria-label={`本地目录 ${index + 1}`}
            readOnly
            value={item.local_path}
            placeholder="选择本地目录"
            addAfter={
              <Button
                aria-label={`选择本地目录 ${index + 1}`}
                icon={<IconFolder />}
                onClick={() => setPicking({ index, local: true })}
              />
            }
          />
          <Select
            aria-label={`网盘类型 ${index + 1}`}
            value={item.kind}
            options={[
              { label: "115网盘", value: "115" },
              { label: "CloudDrive2", value: "cd2" },
            ]}
            onChange={(kind) => update(index, { kind, id: "", path: "" })}
          />
          <Input
            aria-label={`网盘目录 ${index + 1}`}
            readOnly
            value={item.path}
            placeholder="选择网盘目录"
            addAfter={
              <Button
                aria-label={`选择网盘目录 ${index + 1}`}
                icon={<IconFolder />}
                onClick={() => setPicking({ index, local: false })}
              />
            }
          />
          <Button
            aria-label={`删除目录映射 ${index + 1}`}
            icon={<IconDelete />}
            status="danger"
            onClick={() => onChange(value.filter((_, i) => i !== index))}
          />
        </div>
      ))}
      <Button
        onClick={() =>
          onChange([
            ...value,
            { kind: "115", id: "", path: "", local_path: "" },
          ])
        }
      >
        添加目录
      </Button>
      <span className="settings-field-description">
        本地目录指服务所在主机或容器内的目录。首次开启会上传已有文件，后续自动处理新增及变化的文件，并保留子目录结构。
      </span>
      <Modal
        title={picking?.local ? "选择本地目录" : "选择网盘目录"}
        className="settings-directory-picker-modal"
        visible={picking !== null}
        footer={null}
        unmountOnExit
        maskClosable={false}
        onCancel={close}
      >
        {current && picking ? (
          !picking.local && current.kind === "115" ? (
            <Pan115DirectoryPicker
              selectedIDs={[]}
              onCancel={close}
              onSelect={choose}
            />
          ) : (
            <StrmDirectoryPicker
              endpoint={
                picking.local
                  ? "/cloud-upload/directories"
                  : "/strm/clouddrive/directories"
              }
              queryKeyPrefix={
                picking.local
                  ? "upload-local-directories"
                  : "strm-clouddrive-directories"
              }
              crumbs={uploadCrumbs}
              selectedIDs={[]}
              onCancel={close}
              onSelect={choose}
            />
          )
        ) : null}
      </Modal>
    </div>
  );
}
