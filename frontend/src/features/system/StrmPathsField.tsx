import {
  Button,
  Input,
  Modal,
  Select,
  Tag,
  Tooltip,
} from "@arco-design/web-react";
import { IconDelete, IconFolder } from "@arco-design/web-react/icon";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useRef, useState, type ComponentRef } from "react";
import { apiRequest } from "../../shared/api/client";
import { DirectoryPicker, type DirectoryPickerCrumb } from "../../shared/ui/DirectoryPicker";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";
import { Pan115DirectoryPicker } from "./Pan115DirectoryPicker";
import { ScanProgressDisplay, ScanTaskControls, useScanProgress } from "./ScanProgress";

/** 网盘类型；取值与后端 domain.StrmKind* 一致，同时用作播放地址 /files/play/{kind}/ 的路径段。 */
export type StrmKind = "115" | "cd2";

/** 排除关键字的匹配方式；取值与后端 domain.StrmExcludeMode* 一致。 */
export type StrmExcludeMode = "equals" | "prefix" | "suffix" | "contains";

/** 一条排除规则：mode 决定 value 的匹配方式，匹配一律不区分大小写。 */
export type StrmExcludeKeyword = { mode: StrmExcludeMode; value: string };

/** 一条「网盘目录 → 本地 strm 目录」映射，字段与后端 domain.StrmMapping 一一对应。 */
export type StrmMapping = {
  kind: StrmKind;
  id: string;
  path: string;
  local_path: string;
  formats: string[];
  /** 最小视频体积（MB）；0 表示不限制。 */
  min_size_mb: number;
  /** 排除规则：文件名或文件夹名命中任一规则即跳过；命中文件夹时整棵子树跳过。 */
  exclude: StrmExcludeKeyword[];
};

/** STRM 映射默认媒体格式；用户可在 Select 中移除、恢复或手动添加格式。 */
export const DEFAULT_STRM_FORMATS = [
  "mp4", "avi", "rmvb", "wmv", "mov", "mkv", "webm", "iso",
  "mpg", "m4v", "ts", "flv", "strm", "vob", "m2ts",
] as const;

/** 规范化用户输入的媒体格式，去掉扩展名前的点并统一为小写。 */
export function normalizeStrmFormats(formats: unknown): string[] {
  if (!Array.isArray(formats)) return [...DEFAULT_STRM_FORMATS];
  const result: string[] = [];
  for (const raw of formats) {
    if (typeof raw !== "string") continue;
    const format = raw.trim().toLowerCase().replace(/^\.+/, "");
    if (format !== "" && !result.includes(format)) result.push(format);
  }
  return result;
}

/** 排除规则的匹配方式选项；顺序即下拉顺序，label 与标签文案的写法一一对应。 */
const strmExcludeModes: { value: StrmExcludeMode; label: string }[] = [
  { value: "equals", label: "等于" },
  { value: "prefix", label: "前缀" },
  { value: "suffix", label: "后缀" },
  { value: "contains", label: "包含" },
];

/** 判断取值是否为受支持的匹配方式。 */
function isStrmExcludeMode(value: string): value is StrmExcludeMode {
  return strmExcludeModes.some((item) => item.value === value);
}

/**
 * 规范化排除规则：统一匹配方式、去空白、丢弃空关键字，并按「方式 + 小写关键字」去重；
 * 与后端 normalizeStrmExcludes 保持一致。关键字保留用户输入的大小写用于回显，只有判重忽略大小写。
 */
export function normalizeStrmExcludes(excludes: unknown): StrmExcludeKeyword[] {
  if (!Array.isArray(excludes)) return [];
  const result: StrmExcludeKeyword[] = [];
  const seen = new Set<string>();
  for (const raw of excludes) {
    if (raw === null || typeof raw !== "object") continue;
    const candidate = raw as { mode?: unknown; value?: unknown };
    const mode = typeof candidate.mode === "string" ? candidate.mode.trim().toLowerCase() : "";
    if (!isStrmExcludeMode(mode)) continue;
    const value = typeof candidate.value === "string" ? candidate.value.trim() : "";
    if (value === "") continue;
    const key = `${mode}\u0000${value.toLowerCase()}`;
    if (seen.has(key)) continue;
    seen.add(key);
    result.push({ mode, value });
  }
  return result;
}

/** 把一条排除规则渲染成标签文案：等于:xxx / 前缀:xxx* / 后缀:*xxx / 包含:*xxx*。 */
export function formatStrmExclude(rule: StrmExcludeKeyword): string {
  switch (rule.mode) {
    case "equals":
      return `等于:${rule.value}`;
    case "prefix":
      return `前缀:${rule.value}*`;
    case "suffix":
      return `后缀:*${rule.value}`;
    default:
      return `包含:*${rule.value}*`;
  }
}

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
    /** 全量生成清理掉的本地 strm 文件数；增量为 0。 */
    deleted: number;
    created: number;
    unchanged: number;
    failed: number;
    message: string;
  }[];
  files: number;
  /** 本次全量生成清理的本地 strm 文件总数；增量为 0。 */
  deleted: number;
  created: number;
  /** 未改写磁盘的 strm 文件总数：全量为内容一致，增量为本地已存在而跳过。 */
  unchanged: number;
  failed: number;
  emby: { attempted: boolean; refreshed: boolean; message: string };
  /** 独立统计真实文件下载；旧任务快照缺失时按零展示。 */
  downloaded?: number;
  download_skipped?: number;
  download_failed?: number;
};

/** strm 生成方式；取值与后端 domain.StrmGenerateMode 一致，决定生成接口的 incremental 参数。 */
type StrmGenerateMode = "full" | "incremental";

/** 映射是否已选齐网盘目录与本地目录；本地路径必须落在 /strm 根目录内，即以 / 开头。 */
function isStrmMappingComplete(item: StrmMapping): boolean {
  return item.id.trim() !== "" && item.path.trim() !== "" && item.local_path.startsWith("/") && item.formats.length > 0;
}

/** 未选齐网盘目录、本地目录或媒体格式的映射条数；保存时会跳过这些映射。 */
function countIncompleteMappings(value: StrmMapping[]): number {
  return value.filter((item) => !isStrmMappingComplete(item)).length;
}

/** 本地 strm 目录被多条映射重复占用的条数；重复写入同一目录会产生互相覆盖的 strm 文件。 */
function countDuplicateLocalPaths(value: StrmMapping[]): number {
  return value.filter((item, index) =>
    item.local_path.trim() !== "" &&
    value.findIndex((candidate) => candidate.local_path.trim() === item.local_path.trim()) !== index,
  ).length;
}

/** 本地 strm 路径的面包屑；根目录名为生效的 strm 目录，不能回到更上层。 */
function strmCrumbs(path: string, rootLabel: string): DirectoryPickerCrumb[] {
  const crumbs = [{ name: rootLabel, key: strmRootPath }];
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
 * 每条映射依次展示远程网盘目录与本地 strm 目录；目录选择按钮作为输入框后置按钮，删除按钮位于映射卡片外侧。
 * 网盘目录来自 115 或 CloudDrive2 的实时目录列表；本地目录浏览以系统固定的 /strm
 * 根目录为界，只能向下展开，外部新建的目录点击「刷新」即可看到。
 * 草稿由设置页统一持有并随「保存设置」提交。
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
  // 排除关键字的输入草稿；点「添加」选择匹配方式后才写入映射，输入框本身不参与保存。
  const [excludeDraft, setExcludeDraft] = useState("");
  // 组合输入框左侧的匹配方式；点「添加」直接按当前方式生成规则。
  const [excludeMode, setExcludeMode] = useState<StrmExcludeMode>("equals");
  const excludeInputRef = useRef<ComponentRef<typeof Input>>(null);
  const [message, messageHolder] = useFeedbackMessage();

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
  const incomplete = countIncompleteMappings(value);
  const duplicateLocalPaths = countDuplicateLocalPaths(value);
  const selectedFormats = value[0]?.formats ?? [...DEFAULT_STRM_FORMATS];
  const selectedMinSizeMB = value[0]?.min_size_mb ?? 0;
  const selectedExcludes = value[0]?.exclude ?? [];

  const updateFormats = (formats: unknown) => {
    const normalized = normalizeStrmFormats(formats);
    onChange(value.map((item) => ({ ...item, formats: normalized })));
  };
  // 媒体格式、最小体积与排除关键字对所有映射统一生效，与「媒体格式」共用同一份草稿传播方式。
  const updateMinSizeMB = (size: unknown) => {
    const normalized = Math.max(0, Math.trunc(Number(size) || 0));
    onChange(value.map((item) => ({ ...item, min_size_mb: normalized })));
  };
  // 排除规则对所有映射统一生效，与「媒体格式」共用同一份草稿传播方式。
  const updateExcludes = (excludes: StrmExcludeKeyword[]) => {
    onChange(value.map((item) => ({ ...item, exclude: excludes })));
  };
  // 「添加」始终可点：输入框为空时提示并聚焦输入框，避免出现点不动的禁用按钮。
  const addExclude = () => {
    const keyword = excludeDraft.trim();
    if (keyword === "") {
      message.error("请先输入文件或文件夹名称");
      excludeInputRef.current?.focus();
      return;
    }
    setExcludeDraft("");
    updateExcludes(normalizeStrmExcludes([...selectedExcludes, { mode: excludeMode, value: keyword }]));
  };
  const removeExclude = (rule: StrmExcludeKeyword) => {
    updateExcludes(selectedExcludes.filter((item) => !(item.mode === rule.mode && item.value === rule.value)));
  };

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

  return (
    <div className="settings-strm-paths">
      {messageHolder}
      {value.length === 0 ? (
        <span className="settings-field-placeholder">尚未添加 strm 映射</span>
      ) : (
        <ul className="settings-strm-path-list">
          {value.map((item, index) => (
            <li className="settings-strm-path-row" key={`${item.kind}-${item.id}-${index}`}>
              <div className="settings-strm-path-card">
                <div className="settings-strm-path-line">
                  <span className="settings-strm-path-label">网盘路径</span>
                  <div className="settings-strm-field settings-strm-remote-field">
                    <Input
                      readOnly
                      value={item.path}
                      placeholder="尚未选择远程网盘目录"
                      aria-label={`网盘路径 ${index + 1}`}
                      afterStyle={{ padding: 0, border: 0 }}
                      addAfter={(
                        <Tooltip content="选择网盘目录">
                          <Button type="primary" icon={<IconFolder />} aria-label="选择网盘目录" onClick={() => openPicker(index, "netdisk")} />
                        </Tooltip>
                      )}
                      addBefore={(
                        <Select
                          value={item.kind}
                          options={strmKindOptions}
                          className="settings-strm-kind-select"
                          aria-label={`网盘类型 ${index + 1}`}
                          onChange={(kind) => update(index, {
                            kind: kind === "cd2" ? "cd2" : "115",
                            id: "",
                            path: "",
                          })}
                        />
                      )}
                    />
                  </div>
                </div>
                <div className="settings-strm-path-line">
                  <span className="settings-strm-path-label">本地路径</span>
                  <div className="settings-strm-field">
                    <Input
                      value={item.local_path}
                      placeholder="尚未选择本地 strm 目录"
                      aria-label={`本地路径 ${index + 1}`}
                      onChange={(localPath) => update(index, { local_path: localPath })}
                      afterStyle={{ padding: 0, border: 0 }}
                      addAfter={(
                        <Tooltip content="选择本地目录">
                          <Button type="primary" icon={<IconFolder />} aria-label="选择本地目录" onClick={() => openPicker(index, "local")} />
                        </Tooltip>
                      )}
                    />
                  </div>
                </div>
              </div>
              <Tooltip content="删除映射">
                <Button
                  className="settings-strm-delete-button"
                  type="secondary"
                  status="danger"
                  icon={<IconDelete />}
                  aria-label={`删除映射 ${index + 1}`}
                  onClick={() => onChange(value.filter((_, position) => position !== index))}
                >
                  <span className="settings-strm-delete-label">删除</span>
                </Button>
              </Tooltip>
            </li>
          ))}
        </ul>
      )}
      <div className="settings-pan115-actions">
        <Button
          type="primary"
          onClick={() =>
            onChange([
              ...value,
              {
                kind: "115",
                id: "",
                path: "",
                local_path: "",
                formats: [...selectedFormats],
                min_size_mb: selectedMinSizeMB,
                exclude: [...selectedExcludes],
              },
            ])
          }
        >
          添加映射
        </Button>
      </div>
      <div className="settings-field settings-strm-format-line">
        <span className="settings-field-label">媒体格式</span>
        <Select
          mode="multiple"
          allowCreate={{
            formatter: (inputValue) => {
              const format = normalizeStrmFormats([inputValue])[0] ?? "";
              return { value: format, label: format };
            },
          }}
          value={selectedFormats}
          options={Array.from(new Set([...DEFAULT_STRM_FORMATS, ...selectedFormats])).map((format) => ({
            value: format,
            label: format,
          }))}
          placeholder="选择或输入格式"
          aria-label="媒体格式"
          onChange={updateFormats}
        />
      </div>
      <div className="settings-field settings-strm-format-line">
        <span className="settings-field-label">最小视频大小</span>
        <div className="settings-input-with-unit">
          <Input type="number"
            min={0}
            addAfter="MB"
            value={selectedMinSizeMB > 0 ? String(selectedMinSizeMB) : ""}
            placeholder="0"
            aria-label="最小视频大小 MB"
            onChange={updateMinSizeMB}
          />
        </div>
        <span className="settings-field-description">
          小于该体积的视频不生成 strm，单位 MB；留空或 0 表示不限制，网盘未返回体积的文件同样不受限制。
        </span>
      </div>
      <div className="settings-field settings-strm-format-line">
        <span className="settings-field-label">排除文件/夹</span>
        <div className="settings-strm-exclude">
          <Input
            ref={excludeInputRef}
            allowClear
            value={excludeDraft}
            placeholder="输入文件或文件夹名称"
            aria-label="排除关键字"
            addBefore={
              <Select
                value={excludeMode}
                options={strmExcludeModes}
                className="settings-strm-exclude-mode"
                aria-label="排除匹配方式"
                onChange={(mode) => {
                  const next = String(mode);
                  if (isStrmExcludeMode(next)) setExcludeMode(next);
                }}
              />
            }
            onChange={(next) => setExcludeDraft(next)}
          />
          <Button type="secondary" onClick={addExclude}>
            添加
          </Button>
        </div>
        {selectedExcludes.length > 0 ? (
          <div className="settings-strm-exclude-tags">
            {selectedExcludes.map((rule) => (
              <Tag
                key={`${rule.mode}-${rule.value}`}
                closable
                aria-label={`删除排除规则 ${formatStrmExclude(rule)}`}
                onClose={() => removeExclude(rule)}
              >
                {formatStrmExclude(rule)}
              </Tag>
            ))}
          </div>
        ) : null}
        <span className="settings-field-description">
          先选匹配方式再输入名称，点「添加」即生成规则；标签中的 * 表示通配位置。匹配不区分大小写，命中文件夹时整个文件夹都不生成 strm。
        </span>
      </div>
      {incomplete > 0 ? (
        <span className="settings-field-description">
          有 {incomplete} 条映射尚未选齐网盘目录、本地目录或媒体格式，保存时会跳过这些映射。
        </span>
      ) : null}
      {duplicateLocalPaths > 0 ? (
        <span className="settings-field-description">本地 strm 目录不能被多条映射重复使用，请修改后再保存。</span>
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
            crumbs={(path) => strmCrumbs(path, "strm")}
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
 * strm 生成操作。
 *
 * 与映射字段分开渲染，放在「STRM 生成」分组字段之后（即「生成后刷新 Emby 媒体库」下方），
 * 让「配置映射 → 配置播放地址与刷新开关 → 生成」的顺序与页面自上而下的阅读顺序一致。
 * 全量与增量共用同一个生成接口：全量先清理映射本地目录下的 strm 再重建，增量只补齐本地缺失的文件；
 * 两者都使用已保存的映射，映射未选齐或本地目录重复时禁用，避免生成无效结果。
 */
export function StrmGenerateAction({ value }: { value: StrmMapping[] }) {
  const incomplete = countIncompleteMappings(value);
  const duplicateLocalPaths = countDuplicateLocalPaths(value);
  const { scan, progress, task, control, controlsPending, unavailable, queryError } = useScanProgress<StrmScanResult, StrmGenerateMode>((mode) => `/strm/scan?incremental=${mode === "incremental"}`);
  const failedMappings = scan.data?.mappings.filter((item) => item.message !== "") ?? [];
  const disabled = scan.isPending || unavailable || value.length === 0 || incomplete > 0 || duplicateLocalPaths > 0;
  const incremental = scan.variables === "incremental";

  return (
    <div className="settings-strm-generate">
      <div className="settings-strm-scan">
        <Button
          type="secondary"
          loading={scan.isPending && incremental}
          disabled={disabled}
          onClick={() => scan.mutate("incremental")}
        >
          增量生成 strm
        </Button>
        <Button
          type="secondary"
          loading={scan.isPending && !incremental}
          disabled={disabled}
          onClick={() => scan.mutate("full")}
        >
          全量生成 strm
        </Button>
        <ScanTaskControls task={task} pending={controlsPending} onAction={control.mutate} error={control.error ?? queryError} />
      </div>
      <ScanProgressDisplay state={task?.state} canRetry={task?.can_retry} taskId={task?.id} label="生成" processingText="生成中，边扫描边写入" progress={progress} />
      {scan.isError ? (
        <span className="settings-field-description">生成失败：{scan.error.message}</span>
      ) : scan.data ? (
        <span className="settings-field-description">
          {incremental
            ? `共 ${scan.data.files} 个媒体文件，新增 ${scan.data.created} 个 strm，跳过本地已有 ${scan.data.unchanged} 个，失败 ${scan.data.failed} 个。`
            : `共 ${scan.data.files} 个媒体文件，清理本地 strm ${scan.data.deleted} 个，新增 ${scan.data.created} 个，失败 ${scan.data.failed} 个。`}
          {failedMappings.map((item) => ` ${item.path}：${item.message}`).join("")}
          {(scan.data.downloaded || scan.data.download_skipped || scan.data.download_failed)
            ? ` 媒体下载成功 ${scan.data.downloaded ?? 0} 个，跳过 ${scan.data.download_skipped ?? 0} 个，失败 ${scan.data.download_failed ?? 0} 个。` : ""}
          {scan.data.emby.refreshed
            ? " 已刷新 Emby 媒体库。"
            : scan.data.emby.message
              ? ` Emby：${scan.data.emby.message}`
              : ""}
        </span>
      ) : null}
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
export function StrmDirectoryPicker({
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
