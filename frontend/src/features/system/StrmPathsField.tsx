import { Button, Input, Modal, Radio } from "@arco-design/web-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest } from "../../shared/api/client";
import { DirectoryPicker, type DirectoryPickerCrumb } from "../../shared/ui/DirectoryPicker";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";
import { Pan115DirectoryPicker } from "./Pan115DirectoryPicker";

/** 网盘类型；取值与后端 domain.StrmKind* 一致，同时用作播放地址 /files/play/{kind}/ 的路径段。 */
export type StrmKind = "115" | "cd2";

/** 一条「网盘目录 → 本地 strm 目录」映射，字段与后端 domain.StrmMapping 一一对应。 */
export type StrmMapping = { kind: StrmKind; id: string; path: string; local_path: string };

/** 网盘类型选项；顺序与设置页「网盘」分类的页签一致。 */
const strmKindOptions: { value: StrmKind; label: string }[] = [
  { value: "115", label: "115网盘" },
  { value: "cd2", label: "CloudDrive2" },
];

/** 本地 strm 根目录；目录浏览只能在该根目录以下进行，不能向上查询。 */
const strmRootPath = "/";

/** 目录浏览结果；本地 strm 与 CloudDrive2 两个选择器共用同一结构。 */
type StrmDirectoryPage = { path: string; directories: { name: string; path: string }[] };

/** 一次生成的汇总结果，字段与后端 domain.StrmScanResult 一一对应。 */
type StrmScanResult = {
  mappings: {
    kind: string;
    path: string;
    local_path: string;
    files: number;
    created: number;
    unchanged: number;
    failed: number;
    message: string;
  }[];
  files: number;
  created: number;
  failed: number;
  emby: { attempted: boolean; refreshed: boolean; message: string };
};

/** 映射是否已选齐网盘目录与本地目录；本地路径必须落在 /strm 根目录内，即以 / 开头。 */
function isStrmMappingComplete(item: StrmMapping): boolean {
  return item.id.trim() !== "" && item.path.trim() !== "" && item.local_path.startsWith("/");
}

/** 本地 strm 路径的面包屑；根目录固定为 strm，不能回到更上层。 */
function strmCrumbs(path: string): DirectoryPickerCrumb[] {
  const crumbs = [{ name: "strm", key: strmRootPath }];
  let current = "";
  for (const segment of path.split("/").filter((item) => item !== "")) {
    current += `/${segment}`;
    crumbs.push({ name: segment, key: current });
  }
  return crumbs;
}

/** 网盘路径的面包屑；网盘目录是绝对路径，逐级向上回退。 */
function cloudCrumbs(path: string): DirectoryPickerCrumb[] {
  const crumbs = [{ name: "网盘根目录", key: "/" }];
  let current = "";
  for (const segment of path.split("/").filter((item) => item !== "")) {
    current += `/${segment}`;
    crumbs.push({ name: segment, key: current });
  }
  return crumbs;
}

/**
 * strm 网盘映射字段。
 *
 * 每行一条映射：先选网盘类型，再分别选择网盘目录与本地 strm 目录，支持添加多个映射。
 * 网盘目录来自 115 或 CloudDrive2 的实时目录列表；本地目录浏览固定以 /strm 为根，
 * 只能向下展开，外部新建的目录点击「刷新」即可看到。草稿由设置页统一持有并随「保存设置」提交。
 */
export function StrmPathsField({
  value,
  onChange,
}: {
  value: StrmMapping[];
  onChange: (next: StrmMapping[]) => void;
}) {
  // index 指向正在编辑的行，target 决定打开网盘目录还是本地目录选择器。
  const [picking, setPicking] = useState<{ index: number; target: "netdisk" | "local" } | null>(null);
  // 每次打开都换 key 重建选择器：弹窗关闭后 DOM 可能被保留，靠 key 保证每次都从根目录开始。
  const [pickerRun, setPickerRun] = useState(0);

  const openPicker = (index: number, target: "netdisk" | "local") => {
    setPickerRun((previous) => previous + 1);
    setPicking({ index, target });
  };
  const closePicker = () => setPicking(null);
  const update = (index: number, patch: Partial<StrmMapping>) =>
    onChange(value.map((item, position) => (position === index ? { ...item, ...patch } : item)));

  const current = picking ? value[picking.index] : undefined;
  // 同一网盘类型下已选目录不允许重复；正在编辑的行排除在外，允许原样保留。
  const selectedNetdiskIDs = value
    .filter((item, position) => position !== picking?.index && item.kind === current?.kind)
    .map((item) => item.id);
  // 本地 strm 目录同样不允许两条映射写入同一目录。
  const selectedLocalPaths = value
    .filter((_, position) => position !== picking?.index)
    .map((item) => item.local_path);
  const incomplete = value.filter((item) => !isStrmMappingComplete(item)).length;

  const chooseNetdisk = (picked: { id: string; path: string }) => {
    if (picking === null) return;
    update(picking.index, { id: picked.id, path: picked.path });
    closePicker();
  };
  const chooseLocal = (picked: { id: string; path: string }) => {
    if (picking === null) return;
    update(picking.index, { local_path: picked.path });
    closePicker();
  };

  const scan = useMutation({
    mutationFn: () => apiRequest<StrmScanResult>("/strm/scan", { method: "POST" }),
  });
  const failedMappings = scan.data?.mappings.filter((item) => item.message !== "") ?? [];

  return (
    <div className="settings-strm-paths">
      {value.length === 0 ? (
        <span className="settings-field-placeholder">尚未添加 strm 映射</span>
      ) : (
        <ul className="settings-strm-path-list">
          {value.map((item, index) => (
            <li className="settings-strm-path-row" key={`${item.kind}-${item.id}-${index}`}>
              <div className="settings-strm-path-line">
                <span className="settings-strm-path-label">网盘目录</span>
                <div className="settings-strm-kind" role="group" aria-label={`网盘类型 ${index + 1}`}>
                  <Radio.Group
                    value={item.kind}
                    onChange={(kind) => update(index, { kind: kind === "cd2" ? "cd2" : "115", id: "", path: "" })}
                  >
                    {strmKindOptions.map((option) => (
                      <Radio key={option.value} value={option.value}>{option.label}</Radio>
                    ))}
                  </Radio.Group>
                </div>
                <Input
                  readOnly
                  value={item.path}
                  placeholder="尚未选择网盘目录"
                  aria-label={`网盘路径 ${index + 1}`}
                />
                <Button type="secondary" onClick={() => openPicker(index, "netdisk")}>选择网盘目录</Button>
              </div>
              <div className="settings-strm-path-line">
                <span className="settings-strm-path-label">本地 strm</span>
                <Input
                  readOnly
                  value={item.local_path}
                  placeholder="尚未选择本地 strm 目录"
                  aria-label={`本地路径 ${index + 1}`}
                />
                <Button type="secondary" onClick={() => openPicker(index, "local")}>选择本地目录</Button>
                <Button
                  type="secondary"
                  status="danger"
                  aria-label={`删除映射 ${index + 1}`}
                  onClick={() => onChange(value.filter((_, position) => position !== index))}
                >
                  删除
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      <div className="settings-pan115-actions">
        <Button
          type="primary"
          onClick={() => onChange([...value, { kind: "115", id: "", path: "", local_path: "" }])}
        >
          添加映射
        </Button>
      </div>
      {incomplete > 0 ? (
        <span className="settings-field-description">
          有 {incomplete} 条映射尚未选齐网盘目录与本地目录，保存时会跳过这些映射。
        </span>
      ) : null}
      <div className="settings-strm-scan">
        <Button
          type="secondary"
          loading={scan.isPending}
          disabled={scan.isPending || incomplete > 0}
          onClick={() => scan.mutate()}
        >
          生成 strm
        </Button>
        <span className="settings-field-description">
          生成使用已保存的映射；修改后请先保存设置再生成。
        </span>
      </div>
      {scan.isError ? (
        <span className="settings-field-description">生成失败：{scan.error.message}</span>
      ) : scan.data ? (
        <span className="settings-field-description">
          共 {scan.data.files} 个媒体文件，新增 {scan.data.created} 个 strm，失败 {scan.data.failed} 个。
          {failedMappings.map((item) => ` ${item.path}：${item.message}`).join("")}
          {scan.data.emby.refreshed
            ? " 已刷新 Emby 媒体库。"
            : scan.data.emby.message
              ? ` Emby：${scan.data.emby.message}`
              : ""}
        </span>
      ) : null}
      <Modal
        title="选择网盘目录"
        className="settings-directory-picker-modal"
        visible={picking?.target === "netdisk"}
        footer={null}
        unmountOnExit
        maskClosable={false}
        onCancel={closePicker}
      >
        {current ? (
          current.kind === "115" ? (
            <Pan115DirectoryPicker
              key={pickerRun}
              selectedIDs={selectedNetdiskIDs}
              onCancel={closePicker}
              onSelect={chooseNetdisk}
            />
          ) : (
            <StrmDirectoryPicker
              key={pickerRun}
              endpoint="/strm/clouddrive/directories"
              queryKeyPrefix="strm-clouddrive-directories"
              crumbs={cloudCrumbs}
              selectedIDs={selectedNetdiskIDs}
              onCancel={closePicker}
              onSelect={chooseNetdisk}
            />
          )
        ) : null}
      </Modal>
      <Modal
        title="选择本地 strm 目录"
        className="settings-directory-picker-modal"
        visible={picking?.target === "local"}
        footer={null}
        unmountOnExit
        maskClosable={false}
        onCancel={closePicker}
      >
        {current ? (
          <StrmDirectoryPicker
            key={pickerRun}
            endpoint="/strm/directories"
            queryKeyPrefix="strm-directories"
            crumbs={strmCrumbs}
            selectedIDs={selectedLocalPaths}
            creatable
            onCancel={closePicker}
            onSelect={chooseLocal}
          />
        ) : null}
      </Modal>
    </div>
  );
}

/**
 * 按路径浏览的目录选择器：把 {path, directories} 浏览接口适配到公共目录选择组件。
 *
 * 本地 strm 目录与 CloudDrive2 网盘目录共用这一份实现：两者的浏览接口都返回
 * {path, directories}，只有根目录语义与是否允许新建目录不同。
 * creatable 只在本地 strm 目录开启：网盘目录的新建不在本页能力范围内。
 */
function StrmDirectoryPicker({
  endpoint,
  queryKeyPrefix,
  crumbs: buildCrumbs,
  selectedIDs,
  creatable = false,
  onCancel,
  onSelect,
}: {
  endpoint: string;
  queryKeyPrefix: string;
  crumbs: (path: string) => DirectoryPickerCrumb[];
  selectedIDs: string[];
  creatable?: boolean;
  onCancel: () => void;
  onSelect: (target: { id: string; path: string }) => void;
}) {
  const [path, setPath] = useState(strmRootPath);
  const [newName, setNewName] = useState("");
  const [message, messageHolder] = useFeedbackMessage();
  // 每次请求都实时读取目录，外部新建的目录刷新后即可看到。
  const listing = useQuery({
    queryKey: [queryKeyPrefix, path],
    queryFn: () => apiRequest<StrmDirectoryPage>(`${endpoint}?path=${encodeURIComponent(path)}`),
    retry: false,
  });
  const create = useMutation({
    mutationFn: (name: string) =>
      apiRequest<{ name: string; path: string }>(endpoint, {
        method: "POST",
        body: JSON.stringify({ path, name }),
      }),
    onSuccess: () => {
      setNewName("");
      void listing.refetch();
    },
    onError: (error: Error) => message.error(error.message),
  });

  const directories = listing.data?.directories ?? [];

  return (
    <>
      {messageHolder}
      <DirectoryPicker
        crumbs={buildCrumbs(path)}
        onNavigate={(crumb) => setPath(crumb.key)}
        onEnter={(entry) => setPath(entry.key)}
        entries={directories.map((item) => ({
          key: item.path,
          name: item.name,
          path: item.path,
          disabled: selectedIDs.includes(item.path),
        }))}
        status={listing.isLoading ? "loading" : listing.isError ? "error" : "ready"}
        errorText={listing.error?.message}
        currentOccupied={selectedIDs.includes(path)}
        currentPath={path}
        onConfirm={(entry) => onSelect({ id: entry.key, path: entry.key })}
        onCancel={onCancel}
        toolbar={
          <Button type="secondary" size="mini" loading={listing.isFetching} onClick={() => void listing.refetch()}>
            刷新
          </Button>
        }
        extra={
          creatable ? (
            <div className="settings-strm-picker-create">
              <Input
                aria-label="新目录名称"
                value={newName}
                placeholder="新目录名称"
                onChange={(value) => setNewName(value)}
              />
              <Button
                type="secondary"
                loading={create.isPending}
                disabled={create.isPending || newName.trim() === ""}
                onClick={() => create.mutate(newName.trim())}
              >
                新建目录
              </Button>
            </div>
          ) : null
        }
      />
    </>
  );
}
