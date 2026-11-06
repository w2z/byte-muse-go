import { Button, Input, Modal, Select, Tooltip } from "@arco-design/web-react";
import { IconDelete, IconFolder, IconUpload } from "@arco-design/web-react/icon";
import { useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
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
        {uploadPhaseLabel(status)}{" "}
        · 已处理 {status.processed}/{status.total}（{percent}%），上传{" "}
        {status.uploaded}，跳过 {status.skipped}，异常任务 {status.failed}
      </div>
      {status.current ? <div>当前文件：{status.current}</div> : null}
      {status.error ? <div>最近错误：{status.error}</div> : null}
    </div>
  );
}

/** 上传阶段文案。手动扫描在监控关闭时仍显示扫描或上传，不能写成监控已关闭。 */
function uploadPhaseLabel(status: { enabled: boolean; state: string }) {
  if (status.state === "paused") return "任务已暂停";
  if (status.state === "stopped") return "当前批次已停止";
  if (status.state === "failed") return "监控启动失败";
  if (status.state === "uploading") return "正在上传";
  if (status.state === "watching") return status.enabled ? "正在监控" : "正在扫描";
  return "监控已关闭";
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
  hasUnsavedUploadSettings = false,
  savedValue = [],
  onUpload,
}: {
  value: UploadMapping[];
  onChange: (next: UploadMapping[]) => void;
  /** 目录或同名策略相对已保存配置有改动；点击上传只提示保存，不提交扫描。 */
  hasUnsavedUploadSettings?: boolean;
  /** 最近一次已保存的映射。草稿改动后仍用它判断正在上传的原目录。 */
  savedValue?: UploadMapping[];
  /** 按已保存映射立即扫描并上传。调用方负责成功、失败提示。 */
  onUpload?: () => Promise<void>;
}) {
  const onUploadRef = useRef(onUpload);
  onUploadRef.current = onUpload;
  const actions = useQuery({
    queryKey: ["cloud-upload-status"],
    queryFn: () => apiRequest<{ actions?: { scan?: boolean } }>("/cloud-upload/status"),
    refetchInterval: 2000,
  });
  const runningDirectories = useQuery({
    queryKey: ["upload-directory-progress", "running"],
    queryFn: () => apiRequest<{ items: Array<{ kind: string; local_path: string; remote_path: string }> }>("/cloud-upload/directories/progress?status=running"),
    refetchInterval: 2000,
  });
  const upload = useMutation({ mutationFn: () => onUploadRef.current?.() ?? Promise.resolve() });
  const canScan = actions.data?.actions?.scan === true;
  const sameMapping = (mapping: UploadMapping, directory: { kind: string; local_path: string; remote_path: string }) =>
    mapping.kind === directory.kind && mapping.local_path === directory.local_path && mapping.path === directory.remote_path;
  const matchesRunning = (mapping?: UploadMapping) =>
    Boolean(mapping && runningDirectories.data?.items.some((directory) => sameMapping(mapping, directory)));
  /** 当前行或其未保存前的原映射正在传输时，不能从目录选择中删除。 */
  const mappingBusy = (row: UploadMapping, index: number) => {
    if (matchesRunning(row)) return true;
    const saved = savedValue?.[index];
    if (!saved || sameMapping(saved, { kind: row.kind, local_path: row.local_path, remote_path: row.path })) return false;
    const shifted = savedValue?.slice(index + 1).some((item) => item.kind === row.kind && item.local_path === row.local_path && item.path === row.path);
    if (shifted) return false;
    const savedKept = value.some((item, itemIndex) => itemIndex !== index && item.kind === saved.kind && item.local_path === saved.local_path && item.path === saved.path);
    return !savedKept && matchesRunning(saved);
  };
  const uploadButton = (name: string) => (
    <Tooltip content="上传文件">
      <span className="settings-upload-trigger">
        <Button
          aria-label={name}
          htmlType="button"
          icon={<IconUpload />}
          loading={upload.isPending}
          disabled={upload.isPending || (!hasUnsavedUploadSettings && !canScan)}
          onClick={() => upload.mutate()}
        />
      </span>
    </Tooltip>
  );
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
            className="settings-upload-local"
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
            className="settings-upload-kind"
            aria-label={`网盘类型 ${index + 1}`}
            value={item.kind}
            options={[
              { label: "115网盘", value: "115" },
              { label: "CloudDrive2", value: "cd2" },
            ]}
            onChange={(kind) => update(index, { kind, id: "", path: "" })}
          />
          <Input
            className="settings-upload-remote"
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
          <div className="settings-upload-mapping-actions">
            {uploadButton(`上传文件 ${index + 1}`)}
            <Tooltip content={mappingBusy(item, index) ? "目录正在上传，不能删除" : ""} disabled={!mappingBusy(item, index)}>
              <span>
            <Button
              aria-label={`删除目录映射 ${index + 1}`}
              icon={<IconDelete />}
              status="danger"
              disabled={mappingBusy(item, index)}
              onClick={() => onChange(value.filter((_, i) => i !== index))}
            />
              </span>
            </Tooltip>
          </div>
        </div>
      ))}
      <div className="settings-pan115-actions">
        {value.length === 0 ? uploadButton("上传文件") : null}
        <Button
          htmlType="button"
          onClick={() =>
            onChange([
              ...value,
              { kind: "115", id: "", path: "", local_path: "" },
            ])
          }
        >
          添加目录
        </Button>
      </div>
      <span className="settings-field-description">
        本地目录指服务所在主机或容器内的目录。上传图标按已保存映射立即扫描并上传，不改变监控开关；开启监控后还会持续处理新增及变化的文件。未保存的修改不会参与本次上传。
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
