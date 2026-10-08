import { Button, Divider, Input, InputTag, Progress, Radio, Select, Switch, Tabs } from "@arco-design/web-react";
import { IconCheck, IconClose, IconLaunch, IconSave, IconUndo } from "@arco-design/web-react/icon";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Fragment, useEffect, useState, type ReactNode } from "react";
import { apiRequest } from "../../shared/api/client";
import type { SystemSettings } from "../../shared/api/types";
import { ContentCard } from "../../shared/ui/ContentCard";
import { PageState } from "../../shared/ui/PageState";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";
import { Pan115LoginPanel } from "./Pan115LoginPanel";
import { UploadPathsField, UploadMonitorStatus, type UploadMapping } from "./UploadPathsField";
import { UploadTables } from "./UploadTables";
import { Pan115LibraryScanAction, Pan115ScanPathsField, type Pan115ScanPath } from "./Pan115ScanPathsField";
import {
  DEFAULT_STRM_FORMATS,
  normalizeStrmExcludes,
  normalizeStrmFormats,
  StrmGenerateAction,
  StrmPathsField,
  type StrmMapping,
} from "./StrmPathsField";

type FieldKind = "text" | "textarea" | "bool" | "int" | "enum" | "json" | "sort" | "paths" | "strm-paths" | "upload-paths";
type SettingOption = {
  value: string;
  label: string;
  /** 依赖的配置键；任一为空时该选项不可选（如 OpenAI 翻译需要完整的翻译模型配置）。 */
  requires?: string[];
};
type SettingField = {
  key: string;
  label: string;
  /** 站点标题链接，在新标签页打开对应站点。 */
  siteURL?: string;
  /** 标签下方的补充说明，避免把长文案塞进字段标题。 */
  description?: string;
  kind: FieldKind;
  /** 兼容历史配置；当前设置表单统一单列。 */
  wide?: boolean;
  /** 标记后端加密存储的敏感值；前端仍按普通文本框回显和编辑。 */
  secret?: boolean;
  placeholder?: string;
  unit?: string;
  options?: SettingOption[];
  /** 支持两种凭据的站点；模式和凭据独立保存，切换时不清空隐藏字段。 */
  siteAuth?: { modeKey: string; keyField: string; keyPlaceholder: string };
  /** 字段上方的分节标题；同一分组里成组的字段（如消息通知开关）用它归类。 */
  section?: string;
  /** 与相邻同标记字段排成一行；用于同一分节内的多个布尔开关。 */
  inline?: boolean;
};
/** 一个分组内的页签；同一业务对象下有多套互不影响的配置时使用。 */
type SettingTab = {
  code: string;
  title: string;
  fields: SettingField[];
  /** 该页签的 OpenAI 兼容连接测试；使用当前草稿，不保存设置也不依赖启用开关。 */
  test?: { label: string; urlKey: string; modelKey: string; apiKeyKey: string };
};
type SettingGroup = {
  code: string;
  title: string;
  fields: SettingField[];
  tabs?: SettingTab[];
  /** 该分组顶部展示 115 扫码绑定面板；面板自身不参与设置草稿与保存。 */
  pan115Login?: boolean;
  /** 该分组字段之后展示 strm 生成操作；它不是设置项，只读取当前映射草稿判断可用性。 */
  strmGenerate?: boolean;
  /** 该分组字段之后展示 115 扫描入库操作；它不是设置项，只读取当前扫描目录草稿判断可用性。 */
  pan115Scan?: boolean;
  /** 分组底部的备注，用于说明该组配置的适用范围。 */
  note?: string;
};
type SettingCategory = { code: string; title: string; groupCodes: string[] };
type SettingsUpdate = { values: Record<string, string> };
const TabPane = Tabs.TabPane;
type EmbyMediaTask = { id: string; state: "queued" | "running" | "pausing" | "paused" | "canceling" | "canceled" | "completed" | "failed" | "interrupted"; phase?: "scanning" | "refreshing"; processed: number; total: number; success: number; skipped: number; failed: number; error?: string; can_retry?: boolean };
/** 刷新任务的状态与续跑资格由服务端提供；续跑复用原任务断点。 */
function EmbyMediaInfoAction() {
  const client = useQueryClient();
  const query = useQuery({ queryKey: ["emby-media-task"], queryFn: ({ signal }) => apiRequest<EmbyMediaTask | null>("/strm/emby/media-info/task", { signal }), refetchInterval: 1000 });
  const task = query.data;
  const update = async (next: EmbyMediaTask) => {
    await client.cancelQueries({ queryKey: ["emby-media-task"] });
    client.setQueryData(["emby-media-task"], next);
  };
  const refresh = useMutation({ mutationFn: () => apiRequest<{ task: EmbyMediaTask }>("/strm/emby/media-info/refresh", { method: "POST", headers: { Prefer: "respond-async" } }), onSuccess: (result) => update(result.task) });
  const control = useMutation({ mutationFn: (action: "pause" | "resume" | "cancel" | "retry") => {
    if (!task) throw new Error("任务尚未加载");
    return apiRequest<EmbyMediaTask>(`/strm/emby/media-info/tasks/${task.id}/control`, { method: "POST", body: JSON.stringify({ action }) });
  }, onSuccess: update });
  const active = task?.state === "queued" || task?.state === "running" || task?.state === "pausing" || task?.state === "paused" || task?.state === "canceling";
  const percent = task?.phase === "scanning" ? 0 : task && task.total > 0
    ? Math.min(100, Math.round((task.processed / task.total) * 100))
    : task?.state === "completed" ? 100 : 0;
  return (
    <div className="settings-strm-generate">
      <div className="settings-strm-scan">
      <Button type="secondary" loading={refresh.isPending} disabled={refresh.isPending || control.isPending || active} onClick={() => refresh.mutate()}>
        立即刷新strm 媒体库信息
      </Button>
      {!active && task?.can_retry ? <Button status="danger" loading={control.isPending} disabled={control.isPending || refresh.isPending} onClick={() => control.mutate("retry")}>继续失败的任务</Button> : null}
      {active ? <span className="settings-strm-generate-actions">
        <Button disabled={control.isPending || task?.state === "pausing" || task?.state === "canceling"} onClick={() => control.mutate(task?.state === "paused" ? "resume" : "pause")}>{task?.state === "paused" ? "继续" : "暂停"}</Button>
        <Button status="danger" disabled={control.isPending || task?.state === "canceling"} onClick={() => control.mutate("cancel")}>停止</Button>
      </span> : null}
      </div>
      {active ? (
        <div className="settings-scan-progress" aria-label="STRM 媒体信息预热进度">
          <Progress percent={percent} animation={task?.state === "running"} formatText={(value) => `${value}% - ${task?.processed ?? 0}/${task?.total ?? 0}`} />
          <span className="settings-field-description">
            {task?.state === "queued" ? "等待处理" : task?.state === "pausing" ? "正在暂停" : task?.state === "paused" ? "已暂停" : task?.state === "canceling" ? "正在停止" : task?.phase === "scanning" ? "正在扫描媒体库，统计数量中" : "正在刷新媒体信息"}：成功 {task?.success ?? 0}，跳过 {task?.skipped ?? 0}，失败 {task?.failed ?? 0}
          </span>
        </div>
      ) : null}
      {task?.state === "completed" ? (
        <span className="settings-field-description">
          预热完成：共 {task.total} 个，成功 {task.success}，跳过 {task.skipped}，失败 {task.failed}。
        </span>
      ) : null}
      {task?.state === "failed" ? <span role="alert" className="settings-field-description">刷新失败：{task.error || "未知错误"}</span> : null}
      {task?.state === "canceled" ? <span className="settings-field-description">刷新已停止：已处理 {task.processed}/{task.total}。</span> : null}
      {control.isError ? <span role="alert" className="settings-field-description">任务控制失败，请刷新后重试。</span> : null}
    </div>
  );
}
// 每个 Unicode 码点最多 4 个 UTF-8 字节，确保输入不超过后端 60 KiB 容量。
const PROMPT_MAX_CHARS = 15360;

/** 仅提示词采用长文本容量，Cookie 等普通配置不共享此限制。 */
function isPromptSetting(key: string): boolean {
  return key === "TRANSLATION_PROMPT" || key === "AGENT_SYSTEM_PROMPT";
}

/** 按完整 Unicode 码点截断，避免把 emoji 的代理对切开。 */
function limitPrompt(value: string): string {
  return Array.from(value).slice(0, PROMPT_MAX_CHARS).join("");
}

const bypassProjects = [
  { name: "ByPass", url: "https://github.com/sarperavci/CloudflareBypassForScraping" },
  { name: "FlareSolverr", url: "https://github.com/FlareSolverr/FlareSolverr" },
  { name: "Scrapling", url: "https://github.com/D4Vinci/Scrapling" },
] as const;

/** 判断设置页是否应响应 Ctrl+S，忽略浏览器默认的网页保存快捷键。 */
export function isSettingsSaveShortcut(event: Pick<KeyboardEvent, "key" | "ctrlKey">): boolean {
  return event.ctrlKey && event.key.toLowerCase() === "s";
}
/** 表单草稿：文本类存字符串，开关类存布尔。 */
type Draft = Record<string, string | boolean>;

const mainSiteOptions: SettingOption[] = [
  { value: "ALL", label: "自动" },
  { value: "馒头", label: "馒头" },
  { value: "BT", label: "BT" },
  { value: "PTT", label: "PTT" },
  { value: "NicePT", label: "NicePT" },
  { value: "PTFans", label: "PTFans" },
  { value: "RousiPro", label: "RousiPro" },
];

const imageModeOptions: SettingOption[] = [
  { value: "INVISIBLE", label: "默认" },
  { value: "VISIBLE", label: "有图" },
  { value: "BLUR", label: "模糊" },
];

const rankTypeOptions: SettingOption[] = [
  { value: "", label: "不订阅" },
  { value: "daily", label: "每日" },
  { value: "weekly", label: "周" },
  { value: "monthly", label: "每月" },
];

const sortTags: SettingOption[] = [
  { value: "uc", label: "无码" },
  { value: "!uc", label: "排除无码" },
  { value: "seeders", label: "做种" },
  { value: "chinese", label: "中文" },
  { value: "uhd", label: "UHD" },
  { value: "!uhd", label: "排除UHD" },
  { value: "site", label: "站点" },
  { value: "free", label: "免费" },
];

/** 过滤项草稿；json 中本页面未展示的键通过 unknown 原样保留，避免保存时丢配置。 */
type FilterSwitchKey =
  | "only_chinese"
  | "only_uc"
  | "exclude_uc"
  | "only_free"
  | "only_uhd"
  | "exclude_uhd";
type FilterDraft = Record<FilterSwitchKey, boolean> & {
  min_size: string;
  max_size: string;
};

const emptyFilterDraft: FilterDraft = {
  only_chinese: false,
  only_uc: false,
  exclude_uc: false,
  only_free: false,
  only_uhd: false,
  exclude_uhd: false,
  min_size: "",
  max_size: "",
};

const filterSwitches: { key: FilterSwitchKey; label: string }[] = [
  { key: "only_chinese", label: "仅中文" },
  { key: "only_uc", label: "仅无码" },
  { key: "exclude_uc", label: "排除无码" },
  { key: "only_free", label: "仅免费" },
  { key: "only_uhd", label: "仅UHD" },
  { key: "exclude_uhd", label: "排除UHD" },
];

/** OpenAI 翻译引擎依赖的「翻译模型」配置键；三者任一为空即视为未配置，Prompt 不参与判定。 */
const translationOpenAIKeys = ["TRANSLATION_OPENAI_URL", "TRANSLATION_OPENAI_MODEL", "TRANSLATION_OPENAI_API_KEY"];
/** 翻译引擎默认值；OpenAI 翻译未配置齐全时回落此值，与后端 none（关闭）语义一致。 */
const defaultTranslationEngine = "none";

/** 扫描目录配置键；字段定义、解析与序列化共用同一处定义，避免键名漂移。 */
const scanPathsKey = "PAN115_SCAN_PATHS";
/** strm 配置键：网盘映射、strm 内容使用的对外基址、生成后是否刷新 Emby 媒体库；本地根目录固定为 /strm。 */
const strmPathsKey = "STRM_PATHS";
const strmPlayBaseKey = "STRM_PLAY_BASE";
const strmEmbyRefreshKey = "STRM_EMBY_REFRESH";
const strmEmbyMediaEnableKey = "STRM_EMBY_MEDIA_ENABLE";
const strmEmbyMediaIntervalKey = "STRM_EMBY_MEDIA_INTERVAL_MINUTES";
const strmEmbyMediaAfterRefreshKey = "STRM_EMBY_MEDIA_AFTER_REFRESH";
const strmDownloadEnableKey = "STRM_DOWNLOAD_ENABLE";
const strmDownloadExtensionsKey = "STRM_DOWNLOAD_EXTENSIONS";
const defaultDownloadExtensions = '["srt","ssa","ass","nfo","jpg","png"]';

/** 后缀草稿保持为 JSON 字符串，空串沿用后端默认，显式 [] 保留为空列表。 */
function downloadExtensionTags(raw: string): string[] {
  try { return JSON.parse(raw || defaultDownloadExtensions) as string[]; }
  catch { return []; }
}

/** 分组、顺序与字段命名对齐对标站的 /config。 */
const groups: SettingGroup[] = [
  {
    code: "downloader-defaults",
    title: "默认设置",
    fields: [
      {
        key: "PT_DEFAULT_DOWNLOADER",
        label: "PT默认下载器",
        kind: "enum",
        options: [
          { value: "qbittorrent", label: "qBittorrent" },
          { value: "transmission", label: "Transmission" },
        ],
      },
      {
        key: "BT_DEFAULT_DOWNLOADER",
        label: "BT默认下载器",
        kind: "enum",
        description: "选择 115 网盘前需先在「网盘」分类扫码绑定账号，未绑定时订阅会以「默认下载器未配置」失败",
        options: [
          { value: "qbittorrent", label: "qBittorrent" },
          { value: "transmission", label: "Transmission" },
          { value: "aria2", label: "aria2" },
          { value: "thunder", label: "迅雷" },
          { value: "pan115", label: "115网盘" },
        ],
      },
    ],
  },
  {
    code: "site",
    title: "站点",
    fields: [
      {
        key: "MTEAM_API_KEY", label: "馒头", kind: "text", secret: true,
        siteURL: "https://kp.m-team.cc/",
        placeholder: "请输入 存取令牌 (实验室->存取令牌)",
        description: "无 H&R 要求。",
      },
      {
        key: "PTFANS_COOKIE", label: "PTFans", kind: "text", secret: true,
        siteURL: "https://ptfans.cc/special.php",
        siteAuth: { modeKey: "PTFANS_AUTH_TYPE", keyField: "PTFANS_API_KEY", keyPlaceholder: "请输入访问令牌" },
        description: "H&R：35 天内累计做种 7 天。",
      },
      {
        key: "ROUSIPRO_COOKIE", label: "RousiPro", kind: "text", secret: true,
        siteURL: "https://rousi.pro/",
        siteAuth: { modeKey: "ROUSIPRO_AUTH_TYPE", keyField: "ROUSIPRO_API_KEY", keyPlaceholder: "请输入 API Key" },
        description: "做种要求：做种时间 ≥24 小时或单种分享率 ≥1.0。",
      },
      {
        key: "NICEPT_COOKIE",
        label: "NicePT",
        siteURL: "https://www.nicept.net/",
        kind: "text",
        secret: true,
        siteAuth: { modeKey: "NICEPT_AUTH_TYPE", keyField: "NICEPT_API_KEY", keyPlaceholder: "请输入访问令牌" },
        description: "H&R：12天内做种3天 或 单种分享率 ≥2.0",
      },
      {
        key: "PTT_COOKIE", label: "PTTime", kind: "text", secret: true,
        siteURL: "https://www.pttime.org/",
        placeholder: "请输入 PTTime Cookie",
        description: "无 H&R 要求。",
      },
      {
        key: "MAIN_SITE",
        label: "主站选择（配合排序器使用）",
        kind: "enum",
        options: mainSiteOptions,
      },
    ],
  },
  {
    code: "emby",
    title: "Emby",
    fields: [
      { key: "EMBY_URL", label: "EMBY地址", kind: "text" },
      { key: "EMBY_API_KEY", label: "EMBY密钥", kind: "text", secret: true },
    ],
  },
  {
    code: "plex",
    title: "Plex",
    fields: [
      { key: "PLEX_URL", label: "PLEX地址", kind: "text" },
      { key: "PLEX_TOKEN", label: "X-PLEX-TOKEN", kind: "text", secret: true },
    ],
  },
  {
    code: "jellyfin",
    title: "Jellyfin",
    fields: [
      { key: "JELLYFIN_URL", label: "JELLYFIN地址", kind: "text" },
      {
        key: "JELLYFIN_API_KEY",
        label: "JELLYFIN密钥",
        kind: "text",
        secret: true,
      },
      { key: "JELLYFIN_USER", label: "JELLYFIN用户", kind: "text" },
    ],
  },
  {
    code: "wechat",
    title: "微信",
    fields: [
      { key: "WECHAT_CORP_ID", label: "微信企业ID", kind: "text" },
      {
        key: "WECHAT_CORP_SECRET",
        label: "微信企业密钥",
        kind: "text",
        secret: true,
      },
      { key: "WECHAT_AGENT_ID", label: "微信应用ID", kind: "text" },
      { key: "WECHAT_PROXY", label: "微信代理", kind: "text" },
      {
        key: "WECHAT_PHOTO",
        label: "微信默认推送图片",
        kind: "text",
        placeholder: "需外网可以访问",
      },
      {
        key: "WECHAT_TOKEN",
        label: "微信token",
        description: "需在企业微信配置微信回调地址：/api/v1/message",
        kind: "text",
        secret: true,
      },
      {
        key: "WECHAT_ENCODING_AES_KEY",
        label: "微信aes_key",
        kind: "text",
        secret: true,
      },
      {
        key: "WECHAT_TO_USER",
        label: "微信touser",
        kind: "text",
        placeholder: "|分割，默认@all",
      },
      { key: "WECHAT_BANNER", label: "微信封面推送", kind: "bool" },
      { key: "WECHAT_NOTIFY_SUBSCRIBE", label: "订阅成功", kind: "bool", section: "消息", inline: true },
      { key: "WECHAT_NOTIFY_SUBSCRIBE_FAILED", label: "订阅失败", kind: "bool", inline: true },
      { key: "WECHAT_NOTIFY_DOWNLOAD_START", label: "开始下载", kind: "bool", inline: true },
      { key: "WECHAT_NOTIFY_DOWNLOAD_COMPLETE", label: "下载完成", kind: "bool", inline: true },
      { key: "WECHAT_NOTIFY_DOWNLOAD_FAILED", label: "下载失败", kind: "bool", inline: true },
      { key: "WECHAT_NOTIFY_AGENT_CHAT", label: "Agent对话", kind: "bool", inline: true },
    ],
    note: "该设置只针对于微信、TG",
  },
  {
    code: "telegram",
    title: "Telegram",
    fields: [
      {
        key: "TELEGRAM_BOT_TOKEN",
        label: "Telegram Bot Token",
        kind: "text",
        secret: true,
      },
      { key: "TELEGRAM_CHAT_ID", label: "Telegram Chat ID", kind: "text" },
      {
        key: "TELEGRAM_WHITELIST",
        label: "Telegram白名单，英文逗号分割",
        kind: "text",
      },
      { key: "TELEGRAM_SPOILER", label: "图片防剧透", kind: "bool" },
      { key: "TELEGRAM_NOTIFY_SUBSCRIBE", label: "订阅成功", kind: "bool", section: "消息", inline: true },
      { key: "TELEGRAM_NOTIFY_SUBSCRIBE_FAILED", label: "订阅失败", kind: "bool", inline: true },
      { key: "TELEGRAM_NOTIFY_DOWNLOAD_START", label: "开始下载", kind: "bool", inline: true },
      { key: "TELEGRAM_NOTIFY_DOWNLOAD_COMPLETE", label: "下载完成", kind: "bool", inline: true },
      { key: "TELEGRAM_NOTIFY_DOWNLOAD_FAILED", label: "下载失败", kind: "bool", inline: true },
      { key: "TELEGRAM_NOTIFY_AGENT_CHAT", label: "Agent对话", kind: "bool", inline: true },
    ],
    note: "该设置只针对于微信、TG",
  },
  {
    code: "qbittorrent",
    title: "Qbittorrent",
    fields: [
      { key: "QBITTORRENT_URL", label: "qbittorrent地址", kind: "text" },
      { key: "QBITTORRENT_USERNAME", label: "qbittorrent用户名", kind: "text" },
      {
        key: "QBITTORRENT_PASSWORD",
        label: "qbittorrent密码",
        kind: "text",
        secret: true,
      },
      {
        key: "QBITTORRENT_DOWNLOAD_PATH",
        label: "qbittorrent下载地址",
        kind: "text",
      },
      {
        key: "QBITTORRENT_CATEGORY",
        label: "qbittorrent下载分类",
        kind: "text",
      },
    ],
  },
  {
    code: "transmission",
    title: "Transmission",
    fields: [
      { key: "TRANSMISSION_URL", label: "Transmission地址", kind: "text" },
      {
        key: "TRANSMISSION_USERNAME",
        label: "Transmission用户名",
        kind: "text",
      },
      {
        key: "TRANSMISSION_PASSWORD",
        label: "Transmission密码",
        kind: "text",
        secret: true,
      },
      {
        key: "TRANSMISSION_DOWNLOAD_PATH",
        label: "Transmission下载地址",
        kind: "text",
      },
      { key: "TRANSMISSION_LABEL", label: "Transmission标签", kind: "text" },
    ],
  },
  {
    code: "aria2",
    title: "aria2",
    fields: [
      { key: "ARIA2_URL", label: "aria2地址", kind: "text", placeholder: "http://127.0.0.1:6800/jsonrpc" },
      { key: "ARIA2_SECRET", label: "aria2密钥", kind: "text", secret: true },
      { key: "ARIA2_DOWNLOAD_PATH", label: "aria2下载地址", kind: "text" },
    ],
  },
  {
    code: "thunder",
    title: "迅雷",
    fields: [
      {
        key: "THUNDER_URL",
        label: "Docker迅雷地址",
        placeholder: "需版本号>=v3.21.0",
        kind: "text",
      },
      {
        key: "THUNDER_FILE_ID",
        label: "Docker迅雷file_id",
        description: "F12 控制台 network 面板中 /drive/v1/files 即对应目录ID",
        kind: "text",
      },
      {
        key: "THUNDER_AUTHORIZATION",
        label: "Docker迅雷Authorization",
        description: "位于 Requests Headers",
        kind: "text",
        secret: true,
      },
    ],
  },
  {
    code: "pan115",
    title: "115网盘",
    pan115Login: true,
    pan115Scan: true,
    fields: [
      {
        key: "PAN115_COOKIE",
        label: "115 Cookie",
        kind: "textarea",
        secret: true,
        placeholder: "UID=...; CID=...; SEID=...",
        description:
          "115 生活事件与部分接口所需的 Cookie，可点上方「扫码获取 Cookie」自动填入，也可手动粘贴；凭据加密保存，清空后自动关闭 115 事件监听。",
      },
      {
        key: "PAN115_SAVE_PATH",
        label: "离线下载保存目录",
        kind: "text",
        placeholder: "115 目录 ID，留空保存到根目录",
        description: "填写 115 网盘目录 ID；留空表示离线下载保存到根目录",
      },
      {
        key: scanPathsKey,
        label: "扫描目录",
        kind: "paths",
        description: "选择需要扫描 115 网盘已有的视频并入库的目录，可添加多个；留空表示不扫描任何目录。",
      },
    ],
  },
  {
    code: "clouddrive2",
    title: "CloudDrive2",
    fields: [
      { key: "CLOUDNAS_URL", label: "CD2地址", kind: "text" },
      { key: "CLOUDNAS_USERNAME", label: "CD2用户名", kind: "text" },
      {
        key: "CLOUDNAS_PASSWORD",
        label: "CD2密码",
        kind: "text",
        secret: true,
      },
      { key: "CLOUDNAS_SAVEPATH", label: "CD2保存路径", kind: "text" },
    ],
  },
  {
    code: "cloud-upload",
    title: "网盘上传",
    fields: [
      { key: "CLOUD_UPLOAD_PATHS", label: "目录选择", kind: "upload-paths" },
      { key: "CLOUD_UPLOAD_CONFLICT", label: "同名文件处理", kind: "enum", options: [{ value: "skip", label: "跳过" }, { value: "overwrite", label: "覆盖" }, { value: "keep_both", label: "保留两者" }] },
      { key: "CLOUD_UPLOAD_ENABLE", label: "开启目录监控", kind: "bool", description: "保存后生效。关闭页面不影响后台监控；关闭开关将停止新上传。" },
    ],
  },
  {
    code: "strm",
    title: "STRM 生成",
    strmGenerate: true,
    fields: [
      {
        key: strmPathsKey,
        label: "网盘strm映射",
        kind: "strm-paths",
      },
      {
        key: strmDownloadEnableKey,
        label: "下载媒体",
        kind: "bool",
      },
      {
        key: strmDownloadExtensionsKey,
        label: "下载媒体",
        kind: "text",
        description: "生成 STRM 时下载这些后缀的文件，保留原文件名和目录结构，同时下载 5 个文件。增量生成跳过已有文件，全量生成重新下载。",
      },
      {
        key: strmPlayBaseKey,
        label: "STRM文件播放地址",
        kind: "text",
        placeholder: "http://192.168.1.10:3750",
        description: "strm 内容使用的播放地址前缀，留空时按本次生成的请求来源兜底；容器部署建议显式填写对外可访问地址。",
      },
      {
        key: "PAN115_EVENT_ENABLE",
        label: "115事件监听",
        kind: "bool",
        description: "保存后每 30 秒检查 115 文件变更，按上方映射目录、媒体格式、大小及排除规则生成或更新 STRM。未填写 Cookie 时不可开启，清空 Cookie 会自动关闭。",
      },
      {
        key: strmEmbyRefreshKey,
        label: "生成后刷新 Emby 媒体库",
        kind: "bool",
        description: "开启后每次生成 strm 都会请求一次 Emby 媒体库刷新；需要先配置 Emby 地址与密钥。",
      },
      { key: strmEmbyMediaEnableKey, label: "定时获取媒体信息", kind: "bool", description: "按设定间隔扫描 Emby 中缺少媒体信息的 STRM 视频并异步刷新。" },
      { key: strmEmbyMediaIntervalKey, label: "媒体信息刷新间隔", kind: "text", unit: "分钟", placeholder: "60" },
      { key: strmEmbyMediaAfterRefreshKey, label: "刷新媒体库后立即刷新视频信息", kind: "bool", description: "只有开启生成后刷新 Emby 媒体库时可用；媒体库刷新请求受理后加入异步预热队列。" },
    ],
  },
  {
    code: "filter",
    title: "过滤",
    fields: [{ key: "DEFAULT_FILTER", label: "默认过滤规则", kind: "json" }],
  },
  {
    code: "sort",
    title: "排序",
    fields: [
      { key: "DEFAULT_SORT", label: "排序器", kind: "sort" },
    ],
  },
  {
    code: "task",
    title: "定时任务",
    fields: [
      {
        key: "RANK_PAGE",
        label: "图书馆榜单自动订阅",
        placeholder: "1-5，代表订阅多少页",
        kind: "int",
      },
      {
        key: "RANK_TYPE",
        label: "JAVDB榜单自动订阅",
        kind: "enum",
        options: rankTypeOptions,
      },
      {
        key: "BRAND_TYPE",
        label: "厂牌榜单自动订阅",
        placeholder: "如 s1-0 订阅 S1 最新发售，空则不订阅",
        kind: "text",
      },
      {
        key: "RANK_SCHEDULE_TIME",
        label: "榜单订阅定时任务",
        placeholder: "cron表达式",
        description: "5 段 cron（分 时 日 月 周），如 0 20 * * * 表示每天 20:00",
        kind: "text",
      },
      {
        key: "ACTOR_SCHEDULE_TIME",
        label: "演员订阅定时任务",
        placeholder: "cron表达式",
        description: "5 段 cron（分 时 日 月 周），如 0 21 * * * 表示每天 21:00",
        kind: "text",
      },
      {
        key: "TAG_SCHEDULE_TIME",
        label: "标签订阅定时任务",
        placeholder: "cron表达式",
        description: "5 段 cron（分 时 日 月 周），如 30 21 * * * 表示每天 21:30",
        kind: "text",
      },
      {
        key: "DOWNLOAD_SCHEDULE_TIME",
        label: "番号订阅定时任务",
        description: "5 段 cron（分 时 日 月 周），建议设置在榜单与演员订阅之后",
        kind: "text",
      },
      {
        key: "MAX_ACTOR",
        label: "最大共演人数",
        description: "用于演员订阅番号，不适用于榜单订阅",
        kind: "int",
        unit: "人",
      },
      {
        key: "TAG_MAX_SUB_PER_RUN",
        label: "标签订阅单次上限",
        description: "每个标签每次最多订阅的番号数，默认10",
        kind: "int",
        unit: "个",
      },
      {
        key: "PT_SEARCH_INTERVAL",
        label: "PT站搜索最小间隔",
        description: "默认10，填0不限制",
        kind: "int",
        unit: "秒",
      },
    ],
  },
  {
    code: "translate",
    title: "翻译",
    fields: [
      {
        key: "TRANSLATION_ENGINE",
        label: "翻译引擎",
        description: "选择 OpenAI 需先在「AI 模型 → 翻译模型」配置接口、模型与密钥，未配置齐全时不可选；Google 无需申请密钥，留空即用免密钥接口",
        kind: "enum",
        options: [
          { value: defaultTranslationEngine, label: "关闭" },
          { value: "openai", label: "OpenAI", requires: translationOpenAIKeys },
          { value: "google", label: "Google" },
          { value: "baidu", label: "百度" },
          { value: "deeplx", label: "DeepLX" },
        ],
      },
      {
        key: "BAIDU_APP_ID",
        label: "百度翻译APPID",
        kind: "text",
        description: "百度翻译开放平台的 APPID",
      },
      {
        key: "BAIDU_API_KEY",
        label: "百度大模型文本翻译API_KEY",
        kind: "text",
        secret: true,
        description: "百度翻译开放平台的开发者密钥（密钥），用于 MD5 签名鉴权，需已开通文本翻译服务",
      },
      {
        key: "GOOGLE_API_KEY",
        label: "Google Cloud Translation API_KEY",
        kind: "text",
        secret: true,
        description: "留空使用免密钥接口，无需申请即可翻译；填写后改走官方 Cloud Translation API，配额与计费按该 key 所属项目结算",
      },
      {
        key: "DEEPLX_URL",
        label: "DeepLX 地址",
        kind: "text",
        placeholder: "http://127.0.0.1:1188",
        description: "只填服务地址即可，会自动补 /translate；填完整 /translate 地址也可以",
      },
    ],
  },
  {
    code: "agent",
    title: "AI 模型",
    fields: [],
    // 对话与翻译是两套独立配置：可以分别指向不同模型，互不覆盖。
    tabs: [
      {
        code: "conversation",
        title: "对话 Agent",
        fields: [
          { key: "AGENT_ENABLE", label: "启用对话 Agent", kind: "bool" },
          {
            key: "OPENAI_MODEL",
            label: "模型名称",
            kind: "text",
            placeholder: "gpt-4o-mini",
          },
          {
            key: "OPENAI_URL",
            label: "接口地址（OpenAI 兼容）",
            kind: "text",
            placeholder: "https://api.openai.com/v1",
          },
          {
            key: "OPENAI_API_KEY",
            label: "API Key",
            kind: "text",
            secret: true,
            placeholder: "sk-...",
          },
          {
            key: "AGENT_SYSTEM_PROMPT",
            label: "自定义 System Prompt（留空使用内置提示词）",
            kind: "textarea",
            wide: true,
            placeholder: "你是一个媒体库助手……",
          },
        ],
        test: { label: "测试 OpenAI", urlKey: "OPENAI_URL", modelKey: "OPENAI_MODEL", apiKeyKey: "OPENAI_API_KEY" },
      },
      {
        code: "translation",
        title: "翻译模型",
        fields: [
          {
            key: "TRANSLATION_OPENAI_MODEL",
            label: "模型名称",
            description: "修改后需重启后端服务生效",
            kind: "text",
            placeholder: "gpt-4o-mini",
          },
          {
            key: "TRANSLATION_OPENAI_URL",
            label: "接口地址（OpenAI 兼容）",
            kind: "text",
            placeholder: "https://api.openai.com/v1",
          },
          {
            key: "TRANSLATION_OPENAI_API_KEY",
            label: "API Key",
            kind: "text",
            secret: true,
            placeholder: "sk-...",
          },
          {
            key: "TRANSLATION_PROMPT",
            label: "翻译 Prompt",
            kind: "textarea",
            wide: true,
            placeholder: "留空使用内置翻译提示词",
          },
        ],
        test: { label: "测试翻译模型", urlKey: "TRANSLATION_OPENAI_URL", modelKey: "TRANSLATION_OPENAI_MODEL", apiKeyKey: "TRANSLATION_OPENAI_API_KEY" },
      },
    ],
  },
  {
    code: "other",
    title: "其他",
    fields: [
      {
        key: "IMAGE_MODE",
        label: "图片模式",
        kind: "enum",
        options: imageModeOptions,
      },
      {
        key: "PROXY",
        label: "代理地址",
        description: "http://host:port 或 socks5://host:port 或 socks5://user:pass@host:port",
        kind: "text",
      },
      {
        key: "EXTERNAL_DOMAIN",
        label: "外网访问地址",
        description: "页面封面与微信封面推送使用的地址前缀；留空时页面用相对地址、微信直接用图床原图",
        kind: "text",
      },
      {
        key: "BYPASS_ENGINE",
        label: "爬虫增强类型",
        description: "选择“不使用”关闭爬虫增强，保留已填写的服务地址",
        kind: "enum",
        options: [
          { value: "", label: "不使用" },
          { value: "cloudflare_bypass_for_scraping", label: "ByPass" },
          { value: "flaresolverr", label: "FlareSolverr" },
          { value: "scrapling", label: "Scrapling" },
        ],
      },
      {
        key: "BYPASS_URL",
        label: "爬虫增强",
        description: "选择增强类型后填写服务地址；仅在采集遇到 Cloudflare 人机页面时调用",
        kind: "text",
      },
      {
        key: "BYPASS_USE_PROXY",
        label: "爬虫增强是否使用代理",
        description: "开启后，爬虫增强使用上方配置的代理地址访问源站；未配置代理地址时不传代理。选择“不使用”会自动关闭此开关。",
        kind: "bool",
      },
      { key: "ENABLE_BT_ANTI_LEECH", label: "BT种子下完即撤种", kind: "bool" },
      {
        key: "ENABLE_AUTO_COMPLETE",
        label: "已入库资源跳过下载",
        kind: "bool",
      },
      {
        key: "LOG_RETENTION_DAYS",
        label: "定时删除日志",
        kind: "int",
        unit: "天",
        placeholder: "默认 30；0 或留空表示不自动删除",
      },
    ],
  },
];

/** 设置入口按用户可识别的业务对象归类，组内顺序即二级按钮展示顺序。 */
const categories: SettingCategory[] = [
  {
    code: "site",
    title: "站点设置",
    groupCodes: [
      "site",
      "filter",
      "sort",
      "task",
      "translate",
      "agent",
      "other",
    ],
  },
  {
    code: "player",
    title: "播放器设置",
    groupCodes: ["emby", "plex", "jellyfin"],
  },
  { code: "message", title: "消息渠道", groupCodes: ["wechat", "telegram"] },
  {
    code: "downloader",
    title: "下载器",
    groupCodes: ["downloader-defaults", "qbittorrent", "transmission", "aria2", "thunder"],
  },
  {
    code: "netdisk",
    title: "网盘",
    groupCodes: ["pan115", "clouddrive2", "strm", "cloud-upload"],
  },
];

function filterId(name: string) {
  return `setting-${name}`;
}

/** 由独立状态而非通用草稿承载的字段类型：这些字段不写入 draft，也不参与草稿比较。 */
function isStructuredField(kind: FieldKind): boolean {
  return kind === "json" || kind === "sort" || kind === "paths" || kind === "strm-paths" || kind === "upload-paths";
}

/** 分组的全部字段：包含页签内字段，保证草稿初始化、变更判断与保存始终覆盖整组。 */
function groupFields(group: SettingGroup): SettingField[] {
  return [...group.fields, ...(group.tabs ?? []).flatMap((tab) => tab.fields)];
}

/** 选项依赖的配置键是否都已填写；缺一即视为未配置。 */
function hasRequiredValues(keys: string[] | undefined, draft: Draft): boolean {
  return (keys ?? []).every((key) => String(draft[key] ?? "").trim() !== "");
}

/**
 * OpenAI 翻译引擎要求「AI 模型 → 翻译模型」的接口、模型与密钥三者齐全，Prompt 可以为空。
 * 未配置齐全时不允许停留在 openai，统一回落默认引擎，保证设置页展示与后端装配判定一致。
 */
function applyTranslationEngineGuard(draft: Draft): Draft {
  if (draft.TRANSLATION_ENGINE !== "openai" || hasRequiredValues(translationOpenAIKeys, draft)) {
    return draft;
  }
  return { ...draft, TRANSLATION_ENGINE: defaultTranslationEngine };
}

/** 站点名称统一附带外链图标，图标不重复参与无障碍名称。 */
function SettingTitle({ field }: { field: SettingField }) {
  return field.siteURL ? (
    <a className="settings-site-link" href={field.siteURL} target="_blank" rel="noopener noreferrer">
      {field.label}<IconLaunch aria-hidden="true" />
    </a>
  ) : <>{field.label}</>;
}

function fieldPlaceholder(field: SettingField): string | undefined {
  if (field.placeholder !== undefined) return field.placeholder;
  if (field.kind === "enum") return "请选择";
  if (field.kind === "text" || field.kind === "textarea" || field.kind === "int") {
    return `请输入${field.label}`;
  }
  return undefined;
}

function bypassPlaceholder(engine: string): string {
  switch (engine) {
    case "cloudflare_bypass_for_scraping":
      return "http://127.0.0.1:8200";
    case "flaresolverr":
      return "http://127.0.0.1:8191/v1";
    case "scrapling":
      return "http://127.0.0.1:3000";
    default:
      return "请选择爬虫增强类型后输入服务地址";
  }
}

function parseFilterDraft(raw?: string): {
  draft: FilterDraft;
  unknown: Record<string, unknown>;
} {
  const draft: FilterDraft = { ...emptyFilterDraft };
  const unknown: Record<string, unknown> = {};
  if (!raw) return { draft, unknown };
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return { draft, unknown };
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
    return { draft, unknown };
  for (const [key, value] of Object.entries(
    parsed as Record<string, unknown>,
  )) {
    if (key === "min_size" || key === "max_size") {
      draft[key] =
        typeof value === "string" || typeof value === "number"
          ? String(value)
          : "";
      continue;
    }
    if (filterSwitches.some((item) => item.key === key)) {
      draft[key as FilterSwitchKey] = value === true || value === "true";
      continue;
    }
    unknown[key] = value;
  }
  return { draft, unknown };
}

/** 解析数据库中的排序标签；空值保持为空，未识别的标签舍弃。 */
function parseSortOrder(raw?: string): string[] {
  const known = sortTags.map((tag) => tag.value);
  return (raw ?? "")
    .split(",")
    .map((tag) => tag.trim())
    .filter((tag) => known.includes(tag));
}

/** 序列化筛选设置；全部为空时保存空字符串，不在前端重新生成默认值。 */
function serializeFilterDraft(filter: FilterDraft, unknown: Record<string, unknown>): string {
  const isEmpty = Object.keys(unknown).length === 0
    && filterSwitches.every((item) => filter[item.key] === false)
    && filter.min_size === ""
    && filter.max_size === "";
  return isEmpty ? "" : JSON.stringify({ ...unknown, ...filter });
}

/** 解析扫描目录配置；非法内容按空数组处理，坏数据不阻塞整个设置页渲染。 */
function parseScanPaths(raw?: string): Pan115ScanPath[] {
  if (!raw) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (!Array.isArray(parsed)) return [];
  return parsed.flatMap((item) => {
    if (!item || typeof item !== "object") return [];
    const { id, path } = item as { id?: unknown; path?: unknown };
    if (typeof id !== "string" || typeof path !== "string") return [];
    if (id.trim() === "" || path.trim() === "") return [];
    return [{ id, path }];
  });
}

/** 解析 strm 网盘映射配置；非法内容按空数组处理，坏数据不阻塞整个设置页渲染。 */
function parseStrmPaths(raw?: string): StrmMapping[] {
  if (!raw) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (!Array.isArray(parsed)) return [];
  return parsed.flatMap((item) => {
    if (!item || typeof item !== "object") return [];
    const {
      kind,
      id,
      path,
      local_path: localPath,
      formats,
      min_size_mb: minSizeMB,
      exclude,
    } = item as Record<string, unknown>;
    if (kind !== "115" && kind !== "cd2") return [];
    if (typeof id !== "string" || typeof path !== "string" || typeof localPath !== "string") return [];
    return [{
      kind,
      id,
      path,
      local_path: localPath,
      formats: formats === undefined ? [...DEFAULT_STRM_FORMATS] : normalizeStrmFormats(formats),
      min_size_mb: typeof minSizeMB === "number" && Number.isFinite(minSizeMB) && minSizeMB > 0 ? Math.trunc(minSizeMB) : 0,
      exclude: normalizeStrmExcludes(exclude),
    }];
  });
}

/**
 * 序列化 strm 网盘映射：只提交已选齐网盘目录与本地目录的映射。
 * 本地路径必须是固定 /strm 根目录下的绝对形式路径，与后端 STRM_PATHS 校验保持一致。
 */
function serializeStrmPaths(mappings: StrmMapping[]): string {
  const complete = mappings.filter(
    (item) => item.id.trim() !== "" && item.path.trim() !== "" && item.local_path.startsWith("/") && item.formats.length > 0,
  );
  return complete.length === 0 ? "" : JSON.stringify(complete);
}

/**
 * 系统设置页。
 *
 * 字段命名与分组内容对齐对标站的 /config；入口按站点、播放器、消息渠道和
 * 下载器四类显示为一级标签，下方连续展示当前分类的全部分组。
 * 每个分类统一保存；敏感值以普通文本框回显并随当前分类一起提交；布尔、数字、枚举、JSON 与排序标签
 * 的取值由后端 writableSettings 权威校验。
 */
/**
 * 站点分组文本框的固定行数。
 *
 * 站点分组的 COOKIE 是长凭据，统一固定三行，并允许用户手动上下拉伸；馒头令牌使用单行输入框。
 * 避免各字段被内容长度撑成互不相同的高度。
 * 这里刻意不用 autoSize：Arco 在每次输入时都会重写 height / min-height / max-height，
 * 手动拖出来的高度会被顶回去，初始行数只能用 rows 固定。
 */
const SITE_TEXTAREA_ROWS = 3;

/** OpenAI 兼容连接测试使用的三个草稿键。 */
type OpenAITestKeys = { urlKey: string; modelKey: string; apiKeyKey: string };

/**
 * 用当前草稿测试一组 OpenAI 兼容配置。
 * 待测字段由调用方在触发时给出，只提交草稿，不保存设置，也不依赖启用开关。
 */
function useOpenAITest(
  draft: Draft,
  message: { success: (text: string) => void; error: (text: string) => void },
) {
  return useMutation({
    mutationFn: async (keys: OpenAITestKeys) => {
      const started = performance.now();
      try {
        return await apiRequest<{ message: string }>("/system/settings/openai/test", {
          method: "POST",
          body: JSON.stringify({
            url: String(draft[keys.urlKey] ?? "").trim(),
            model: String(draft[keys.modelKey] ?? "").trim(),
            api_key: String(draft[keys.apiKeyKey] ?? "").trim(),
          }),
        });
      } catch (error) {
        // 后端不可达时没有返回耗时，使用浏览器实际等待时长保持提示格式一致。
        if (error instanceof Error && /^OpenAI 连接失败 [(][0-9]+ms[)](?:：.+)?$/.test(error.message)) throw error;
        const reason = error instanceof Error ? error.message : "请求失败，请稍后重试";
        throw new Error(`OpenAI 连接失败 (${Math.round(performance.now() - started)}ms)：${reason}`);
      }
    },
    retry: false,
    onSuccess: (result) => message.success(result.message),
    onError: (error: Error) => message.error(error.message),
  });
}

export function SettingsPage() {
  const [activeCategoryCode, setActiveCategoryCode] = useState(
    categories[0].code,
  );
  const [activeGroupCode, setActiveGroupCode] = useState(categories[0].groupCodes[0]);
  const [activeTabCodes, setActiveTabCodes] = useState<Record<string, string>>({});
  const [draft, setDraft] = useState<Draft>({});
  const [filter, setFilter] = useState<FilterDraft>(emptyFilterDraft);
  const [filterUnknown, setFilterUnknown] = useState<Record<string, unknown>>(
    {},
  );
  const [sortOrder, setSortOrder] = useState<string[]>([]);
  const [scanPaths, setScanPaths] = useState<Pan115ScanPath[]>([]);
  const [strmPaths, setStrmPaths] = useState<StrmMapping[]>([]);
  const [uploadPaths, setUploadPaths] = useState<UploadMapping[]>([]);
  const [savedSnapshot, setSavedSnapshot] = useState<SettingsUpdate>({
    values: {},
  });
  const [message, messageHolder] = useFeedbackMessage();
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ["system-settings"],
    queryFn: () => apiRequest<SystemSettings>("/system/settings"),
  });
  const activeCategory =
    categories.find((category) => category.code === activeCategoryCode) ??
    categories[0];
  const activeGroups = activeCategory.groupCodes
    .map((groupCode) => groups.find((group) => group.code === groupCode))
    .filter((group): group is SettingGroup => Boolean(group));
  const activeGroup = groups.find((group) => group.code === activeGroupCode) ?? activeGroups[0];
  // 页签只决定展示哪一组字段：草稿、变更判断与保存始终覆盖整个分组。
  const activeTabs = activeGroup.tabs ?? [];
  const activeTab = activeTabs.find((tab) => tab.code === activeTabCodes[activeGroup.code]) ?? activeTabs[0];
  const activeFields = activeTab ? activeTab.fields : activeGroup.fields;
  const activeTest = activeTab?.test;

  const syncDraft = (settings: SystemSettings) => {
    const values = settings.values ?? {};
    setSavedSnapshot({ values: { ...values } });
    const next: Draft = {};
    for (const group of groups) {
      for (const field of groupFields(group)) {
        if (isStructuredField(field.kind)) continue;
        const raw = Object.prototype.hasOwnProperty.call(values, field.key)
          ? values[field.key]
          : field.key === "LOG_RETENTION_DAYS"
            ? "30"
            : field.key === strmDownloadExtensionsKey ? defaultDownloadExtensions : field.key === "CLOUD_UPLOAD_CONFLICT" ? "skip" : "";
        next[field.key] = field.kind === "bool" ? raw === "true" : raw;
        if (field.siteAuth) {
          const auth = field.siteAuth;
          next[auth.modeKey] = values[auth.modeKey] === "cookie" ? "cookie" : "key";
          next[auth.keyField] = values[auth.keyField] ?? "";
        }
      }
    }
    const parsed = parseFilterDraft(values.DEFAULT_FILTER);
    if (!next.BYPASS_ENGINE) next.BYPASS_USE_PROXY = false;
    if (!String(next.PAN115_COOKIE ?? "").trim()) next.PAN115_EVENT_ENABLE = false;
    setDraft(applyTranslationEngineGuard(next));
    setFilter(parsed.draft);
    setFilterUnknown(parsed.unknown);
    setSortOrder(parseSortOrder(values.DEFAULT_SORT));
    setScanPaths(parseScanPaths(values[scanPathsKey]));
    setStrmPaths(parseStrmPaths(values[strmPathsKey]));
    try { const parsed: unknown = JSON.parse(values.CLOUD_UPLOAD_PATHS || "[]"); setUploadPaths(Array.isArray(parsed) ? parsed as UploadMapping[] : []); } catch { setUploadPaths([]); }
  };

  useEffect(() => {
    if (query.data) syncDraft(query.data);
  }, [query.data]);

  const update = useMutation({
    mutationFn: (payload: SettingsUpdate) =>
      apiRequest<SystemSettings>("/system/settings", {
        method: "PUT",
        body: JSON.stringify(payload),
      }),
    onSuccess: (settings) => {
      queryClient.setQueryData(["system-settings"], settings);
      syncDraft(settings);
      message.success("设置已保存");
    },
    onError: (error: Error) => {
      message.error(error.message);
    },
  });

  // 测试直接提交当前草稿，不触发保存，也不依赖 Agent 开关；对话与翻译各自测试自己的配置。
  const openAITest = useOpenAITest(draft, message);

  // 编辑任意字段后都重新校验 OpenAI 翻译不变式，删除翻译模型配置时引擎立即回落。
  const setValue = (key: string, value: string | boolean) =>
    setDraft((previous) => applyTranslationEngineGuard({
      ...previous,
      [key]: value,
      ...(key === "BYPASS_ENGINE" && value === "" ? { BYPASS_USE_PROXY: false } : {}),
      ...(key === "PAN115_COOKIE" && !String(value).trim() ? { PAN115_EVENT_ENABLE: false } : {}),
    }));

  const selectCategory = (categoryCode: string) => {
    setActiveCategoryCode(categoryCode);
    const category = categories.find((item) => item.code === categoryCode);
    if (category?.groupCodes[0]) setActiveGroupCode(category.groupCodes[0]);
  };

  const moveSortTag = (index: number, offset: number) => {
    setSortOrder((previous) => {
      const moved = previous[index];
      if (moved === undefined) return previous;
      const next = [...previous];
      next.splice(index, 1);
      next.splice(index + offset, 0, moved);
      return next;
    });
  };

  const baselineValue = (key: string) => {
    if (groups.some((group) => groupFields(group).some((field) => field.siteAuth?.modeKey === key))) {
      return savedSnapshot.values[key] === "cookie" ? "cookie" : "key";
    }
    const saved = savedSnapshot.values[key];
    if (saved !== undefined) return saved;
    const field = groups
      .flatMap((group) => groupFields(group))
      .find((item) => item.key === key);
    if (field?.kind === "bool") return "false";
    if (key === "LOG_RETENTION_DAYS") return "30";
    if (key === strmDownloadExtensionsKey) return defaultDownloadExtensions;
    if (field && isStructuredField(field.kind)) return "";
    return "";
  };

  const buildPayload = () => {
    const payload: Record<string, string> = {};
    for (const group of [activeGroup]) {
      for (const field of groupFields(group)) {
        if (field.kind === "upload-paths") { payload[field.key] = JSON.stringify(uploadPaths); continue; }
        if (field.kind === "json") {
          payload[field.key] = serializeFilterDraft(filter, filterUnknown);
          continue;
        }
        if (field.kind === "sort") {
          payload[field.key] = sortOrder.join(",");
          continue;
        }
        if (field.kind === "paths") {
          payload[field.key] = scanPaths.length === 0 ? "" : JSON.stringify(scanPaths);
          continue;
        }
        if (field.kind === "strm-paths") {
          payload[field.key] = serializeStrmPaths(strmPaths);
          continue;
        }
        const value = draft[field.key];
        if (field.kind === "bool") {
          payload[field.key] = value === true ? "true" : "false";
          continue;
        }
        payload[field.key] = typeof value === "string"
          ? (isPromptSetting(field.key) ? limitPrompt(value) : value).trim()
          : "";
        if (field.siteAuth) {
          for (const key of [field.siteAuth.modeKey, field.siteAuth.keyField]) {
            if (key) payload[key] = String(draft[key] ?? "").trim();
          }
        }
      }
    }
    // 跨分组不变式：OpenAI 翻译未配置齐全时引擎必须回落默认值；
    // 在「AI 模型」分组删除翻译模型配置后保存，也要一并提交回落结果，避免设置值与实际装配不一致。
    const engine = typeof draft.TRANSLATION_ENGINE === "string" ? draft.TRANSLATION_ENGINE : "";
    if (engine !== baselineValue("TRANSLATION_ENGINE")) payload.TRANSLATION_ENGINE = engine;
    return payload;
  };

  const currentPayload = buildPayload();
  const hasChanges = Object.keys(currentPayload).some(
    (key) => currentPayload[key] !== baselineValue(key),
  );

  const submit = () => {
    update.mutate({ values: buildPayload() });
  };

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isSettingsSaveShortcut(event)) return;
      event.preventDefault();
      if (!update.isPending) submit();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [update.isPending, submit]);

  const reset = () => {
    if (query.data) syncDraft(query.data);
  };

  const renderField = (field: SettingField, group: SettingGroup) => {
    if (field.key === strmDownloadEnableKey) return null;
    if (field.key === strmDownloadExtensionsKey) {
      return (
        <div className="settings-field" key={field.key}>
          <label className="settings-field-label" htmlFor="strm-download-enable">下载媒体</label>
          <div className="settings-download-media">
            <Switch id="strm-download-enable" aria-label="下载媒体"
              checked={draft[strmDownloadEnableKey] === true}
              onChange={(checked) => setValue(strmDownloadEnableKey, checked)} />
            <InputTag aria-label="下载媒体后缀" disabled={draft[strmDownloadEnableKey] !== true}
              value={downloadExtensionTags(String(draft[field.key] ?? ""))}
              placeholder="输入后缀并按回车" allowClear saveOnBlur tokenSeparators={[",", "，", " "]}
              validate={(value) => /^\.?[a-zA-Z0-9]{1,16}$/.test(String(value).trim()) && String(value).replace(/^\./, "").toLowerCase() !== "strm"}
              onChange={(values) => setValue(field.key, JSON.stringify([...new Set(values.map((value) => String(value).trim().replace(/^\./, "").toLowerCase()))]))} />
          </div>
          <span className="settings-field-description">{field.description}</span>
        </div>
      );
    }
    if (field.key === strmEmbyMediaIntervalKey) return null;
    // BYPASS_ENGINE 与 BYPASS_URL 合并为“左侧选择、右侧地址”的单一控件。
    if (field.key === "BYPASS_ENGINE") return null;
    if (field.siteAuth) {
      const auth = field.siteAuth;
      const keyMode = draft[auth.modeKey] === "key";
      const credentialKey = keyMode ? auth.keyField : field.key;
      const cookieLabel = field.label === "PTTime" ? "PTTime Cookie" : `${field.label} COOKIE`;
      return (
        <div className="settings-field" key={field.key}>
          <span className="settings-field-label"><SettingTitle field={field} /></span>
          <div className="settings-option-radio" role="group" aria-label={`${field.label} 鉴权方式`}>
            <Radio.Group value={keyMode ? "key" : "cookie"} onChange={(mode: string) => setValue(auth.modeKey, mode)}>
              <Radio value="key">密钥</Radio>
              <Radio value="cookie">Cookie</Radio>
            </Radio.Group>
          </div>
          {keyMode ? (
            <div>
              <Input
                aria-label={`${field.label} 密钥`}
                value={String(draft[credentialKey] ?? "")}
                placeholder={auth.keyPlaceholder}
                onChange={(value) => setValue(credentialKey, value)}
              />
            </div>
          ) : (
            <Input.TextArea
              aria-label={cookieLabel}
              className="settings-site-textarea"
              value={String(draft[credentialKey] ?? "")}
              placeholder={`请输入 ${field.label} Cookie`}
              rows={SITE_TEXTAREA_ROWS}
              onChange={(value) => setValue(credentialKey, value)}
            />
          )}
          <span className="settings-field-description">
            <span>{field.description}</span>
          </span>
        </div>
      );
    }
    if (field.kind === "paths") {
      return (
        <div className="settings-field settings-field-wide" key={field.key}>
          <span className="settings-field-label">{field.label}</span>
          <Pan115ScanPathsField value={scanPaths} onChange={setScanPaths} />
          {field.description ? <span className="settings-field-description">{field.description}</span> : null}
        </div>
      );
    }
    if (field.kind === "strm-paths") {
      return (
        <div className="settings-field settings-field-wide" key={field.key}>
          <span className="settings-field-label">{field.label}</span>
          <StrmPathsField
            value={strmPaths}
            onChange={setStrmPaths}
          />
          {field.description ? <span className="settings-field-description">{field.description}</span> : null}
        </div>
      );
    }
    if (field.kind === "upload-paths") return <div className="settings-field settings-field-wide" key={field.key}><span className="settings-field-label">{field.label}</span><UploadPathsField value={uploadPaths} onChange={setUploadPaths}/></div>;
    if (field.key === "CLOUD_UPLOAD_CONFLICT") return <div className="settings-field settings-field-wide" key={field.key}><span className="settings-field-label">{field.label}</span><Radio.Group aria-label={field.label} value={draft[field.key] || "skip"} onChange={(value)=>setValue(field.key,value)} options={field.options}/></div>;
    if (field.kind === "json") {
      return (
        <div className="settings-field settings-field-wide" key={field.key}>
          <span className="settings-field-label">{field.label}</span>
          <div className="settings-filter">
            {filterSwitches.map((item) => (
              <div className="settings-filter-row settings-toggle-row" key={item.key}>
                <Switch
                  id={filterId(item.key)}
                  size="small"
                  type="round"
                  checkedIcon={<IconCheck />}
                  uncheckedIcon={<IconClose />}
                  aria-label={item.label}
                  checked={filter[item.key]}
                  onChange={(checked: boolean) =>
                    setFilter((previous) => ({
                      ...previous,
                      [item.key]: checked,
                    }))
                  }
                />
                <label className="settings-field-label" htmlFor={filterId(item.key)}>{item.label}</label>
              </div>
            ))}
            <div className="settings-filter-range">
              <label className="settings-field" htmlFor={filterId("min_size")}>
                <span className="settings-field-label">最小体积(MB)</span>
                <div className="settings-input-with-unit">
                  <Input type="number"
                    aria-label="最小体积(MB)"
                    id={filterId("min_size")}
                    value={filter.min_size}
                    min={0}
                    addAfter="MB"
                    placeholder="请输入最小体积"
                    onChange={(value) =>
                      setFilter((previous) => ({
                        ...previous,
                        min_size: value === undefined ? "" : String(value),
                      }))
                    }
                  />
                </div>
              </label>
              <label className="settings-field" htmlFor={filterId("max_size")}>
                <span className="settings-field-label">最大体积(MB)</span>
                <div className="settings-input-with-unit">
                  <Input type="number"
                    aria-label="最大体积(MB)"
                    id={filterId("max_size")}
                    value={filter.max_size}
                    min={0}
                    addAfter="MB"
                    placeholder="请输入最大体积"
                    onChange={(value) =>
                      setFilter((previous) => ({
                        ...previous,
                        max_size: value === undefined ? "" : String(value),
                      }))
                    }
                  />
                </div>
              </label>
            </div>
          </div>
        </div>
      );
    }

    if (field.kind === "sort") {
      return (
        <div className="settings-field settings-field-wide" key={field.key}>
          <span className="settings-field-label">{field.label}</span>
          <ul className="settings-sort-list" role="group">
            {sortOrder.map((tag, index) => (
              <li className="settings-sort-row" key={tag}>
                <span>
                  {sortTags.find((item) => item.value === tag)?.label ?? tag}
                </span>
                <span className="settings-sort-actions">
                  <Button
                    type="secondary"
                    disabled={index === 0}
                    onClick={() => moveSortTag(index, -1)}
                  >
                    上移
                  </Button>
                  <Button
                    type="secondary"
                    disabled={index === sortOrder.length - 1}
                    onClick={() => moveSortTag(index, 1)}
                  >
                    下移
                  </Button>
                </span>
              </li>
            ))}
          </ul>
        </div>
      );
    }

    if (field.kind === "bool") {
      return (
        <div className={"settings-field" + (field.inline ? " settings-field-inline" : "")} key={field.key}>
          <div className="settings-toggle-row">
            <Switch
              id={filterId(field.key)}
              size="small"
              type="round"
              checkedIcon={<IconCheck />}
              uncheckedIcon={<IconClose />}
              aria-label={field.label}
              checked={draft[field.key] === true}
              disabled={(field.key === "BYPASS_USE_PROXY" && !draft.BYPASS_ENGINE) ||
                (field.key === "PAN115_EVENT_ENABLE" && !String(draft.PAN115_COOKIE ?? "").trim()) ||
                (field.key === strmEmbyMediaAfterRefreshKey && draft[strmEmbyRefreshKey] !== true)}
              onChange={(checked: boolean) => { setValue(field.key, checked); if (field.key === strmEmbyRefreshKey && !checked) setValue(strmEmbyMediaAfterRefreshKey, false); }}
            />
            {/* 文字位于开关右侧，原生 label 关联保留点击切换和禁用行为。 */}
            <label className="settings-field-label" htmlFor={filterId(field.key)}>{field.label}</label>
            {field.key === strmEmbyMediaEnableKey ? (
              <div className="settings-input-with-unit settings-emby-media-interval">
                <Input id={strmEmbyMediaIntervalKey} type="number" min={1} max={10080} value={String(draft[strmEmbyMediaIntervalKey] ?? "60")} disabled={draft[strmEmbyMediaEnableKey] !== true} onChange={(value) => setValue(strmEmbyMediaIntervalKey, value)} addAfter="分钟" aria-label="媒体信息刷新间隔" />
              </div>
            ) : null}
          </div>
          {field.description ? <span className="settings-field-description">{field.description}</span> : null}
        </div>
      );
    }
    if (field.kind === "enum") {
      // 选项自带空值时（如「不订阅」「不使用」），空值本身就是已选项，不能按未选择提示。
      const enumOptions = field.options ?? [];
      const enumValue =
        typeof draft[field.key] === "string" ? (draft[field.key] as string) : "";
      const enumSelected = enumOptions.some((option) => option.value === enumValue);
      return (
        <div className="settings-field settings-field-wide" key={field.key} aria-label={field.label}>
          <span className="settings-field-label">{field.label}</span>
          <div className={`settings-option-radio${field.key === "MAIN_SITE" ? " settings-main-site-options" : ""}`}>
            {enumSelected ? null : <span className="settings-field-placeholder">请选择</span>}
            <Radio.Group value={enumValue} onChange={(value: string | number) => setValue(field.key, String(value))}>
              {enumOptions.map((option) => (
                <Radio
                  key={option.value}
                  value={option.value}
                  disabled={!hasRequiredValues(option.requires, draft)}
                >
                  {option.label}
                </Radio>
              ))}
            </Radio.Group>
          </div>
          {field.description ? <span className="settings-field-description">{field.description}</span> : null}
        </div>
      );
    }

    const value =
      typeof draft[field.key] === "string" ? (draft[field.key] as string) : "";
    const siteField = group.code === "site";
    const promptField = isPromptSetting(field.key);
    const placeholder = fieldPlaceholder(field);
    if (promptField) {
      const count = Array.from(value).length;
      return (
        <div className="settings-field settings-field-wide" key={field.key}>
          <label className="settings-field-label" htmlFor={filterId(field.key)}>{field.label}</label>
          <div className="settings-prompt-input">
            <Input.TextArea
              id={filterId(field.key)}
              aria-describedby={`${filterId(field.key)}-limit`}
              className="settings-textarea settings-prompt-textarea"
              value={value}
              placeholder={placeholder}
              rows={5}
              onChange={(nextValue) => setValue(field.key, limitPrompt(nextValue))}
            />
            <span id={`${filterId(field.key)}-limit`} className={`settings-prompt-count${count >= PROMPT_MAX_CHARS ? " settings-prompt-count-limit" : ""}`} aria-live="polite">
              {count}/{PROMPT_MAX_CHARS}
            </span>
          </div>
        </div>
      );
    }
    if (field.key === "BYPASS_URL") {
      const engine = typeof draft.BYPASS_ENGINE === "string" ? draft.BYPASS_ENGINE : "";
      return (
        <div className="settings-field" key={field.key}>
          <label className="settings-field-label" htmlFor={filterId(field.key)}>{field.label}</label>
          <Input.Group compact>
            <Select
              aria-label="爬虫增强类型"
              value={engine}
              placeholder="选择类型"
              options={[
                { value: "", label: "不使用" },
                { value: "cloudflare_bypass_for_scraping", label: "ByPass" },
                { value: "flaresolverr", label: "FlareSolverr" },
                { value: "scrapling", label: "Scrapling" },
              ]}
              onChange={(value) => setValue("BYPASS_ENGINE", value)}
              style={{ width: 220 }}
            />
            <Input
              id={filterId(field.key)}
              value={value}
              disabled={engine === ""}
              placeholder={bypassPlaceholder(engine)}
              onChange={(nextValue: string) => setValue(field.key, nextValue)}
              style={{ width: "calc(100% - 220px)" }}
            />
          </Input.Group>
          <div className="settings-field-description">
            <span>选择“不使用”关闭爬虫增强，已填写的服务地址会保留。{field.description}</span>
            <span className="settings-reference-links" aria-label="爬虫增强项目地址">
              {bypassProjects.map((project) => (
                <a key={project.url} href={project.url} target="_blank" rel="noreferrer">{project.name}</a>
              ))}
            </span>
          </div>
        </div>
      );
    }
    return (
      <div
        className={"settings-field" + (field.wide ? " settings-field-wide" : "")}
        key={field.key}
      >
        <label className="settings-field-label" htmlFor={filterId(field.key)}>
          <SettingTitle field={field} />
        </label>
        {(siteField && field.key !== "MTEAM_API_KEY") || field.kind === "textarea" ? (
          <Input.TextArea
            id={filterId(field.key)}
            aria-label={field.key === "PTT_COOKIE" ? "PTTime Cookie" : undefined}
            className={siteField ? "settings-site-textarea" : "settings-textarea"}
            value={value}
            placeholder={placeholder}
            rows={siteField ? SITE_TEXTAREA_ROWS : undefined}
            autoSize={
              siteField
                ? undefined
                : {
                    minRows: field.kind === "textarea" ? 5 : 3,
                    maxRows: field.kind === "textarea" ? 10 : 6,
                  }
            }
            onChange={(nextValue: string) => setValue(field.key, nextValue)}
          />
        ) : field.kind === "int" ? (
          <div className="settings-input-with-unit">
            <Input type="number"
              id={filterId(field.key)}
              value={value}
              min={0}
              addAfter={field.unit}
              placeholder={placeholder}
              onChange={(nextValue) => setValue(field.key, nextValue === undefined ? "" : String(nextValue))}
            />
          </div>
        ) : (
          <Input
            id={filterId(field.key)}
            type="text"
            value={value}
            placeholder={placeholder}
            onChange={(nextValue: string) => setValue(field.key, nextValue)}
          />
        )}
        {field.description ? <span className="settings-field-description">{field.description}</span> : null}
      </div>
    );
  };

  // 连续 inline 字段与标题组成一个字段容器，复用普通字段的内部间距；选项可自动换行。
  const renderFieldSequence = (fields: SettingField[]) => {
    const nodes: ReactNode[] = [];
    let inlineFields: SettingField[] = [];
    const flushInlineFields = () => {
      if (inlineFields.length === 0) return;
      const rowFields = inlineFields;
      inlineFields = [];
      nodes.push(
        <div className="settings-field settings-field-wide" key={rowFields[0].key}>
          {rowFields[0].section ? <h3 className="settings-field-section settings-field-label">{rowFields[0].section}</h3> : null}
          <div className="settings-inline-row">
            {rowFields.map((rowField) => renderField(rowField, activeGroup))}
          </div>
        </div>,
      );
    };
    fields.forEach((field, index) => {
      if (field.section && field.section !== fields[index - 1]?.section) {
        flushInlineFields();
        if (!field.inline) nodes.push(
          <h3 className="settings-field-section" key={`section-${field.section}`}>
            {field.section}
          </h3>,
        );
      }
      if (field.inline) {
        inlineFields.push(field);
        return;
      }
      flushInlineFields();
      nodes.push(<Fragment key={field.key}>{renderField(field, activeGroup)}</Fragment>);
    });
    flushInlineFields();
    return nodes;
  };

  return (
    <section className="settings-page">
      {messageHolder}
      <PageState
        isLoading={query.isLoading}
        error={query.error}
        isEmpty={false}
        emptyText=""
        onRetry={() => void query.refetch()}
      >
        <ContentCard className="settings-workspace">
          <div className="settings-navigation">
            <Tabs
              className="settings-category-tabs"
              activeTab={activeCategory.code}
              onChange={selectCategory}
              animation={false}
              aria-label="设置类别"
            >
              {categories.map((category) => (
                <TabPane key={category.code} title={category.title} />
              ))}
            </Tabs>
          </div>
          <div className="settings-secondary-navigation">
            <Tabs
              className="settings-group-tabs"
              type="capsule"
              size="small"
              activeTab={activeGroup.code}
              onChange={setActiveGroupCode}
              animation={false}
              aria-label={`${activeCategory.title}分组`}
            >
              {activeGroups.map((group) => (
                <TabPane key={group.code} title={group.title} />
              ))}
            </Tabs>
          </div>
          <div className="settings-form-shell">
            {activeTabs.length > 0 && activeTab ? (
              <div className="settings-model-tabs">
                <Tabs
                  className="settings-model-tabs-bar"
                  type="capsule"
                  size="small"
                  activeTab={activeTab.code}
                  onChange={(code: string) => setActiveTabCodes((previous) => ({ ...previous, [activeGroup.code]: code }))}
                  animation={false}
                  aria-label={`${activeGroup.title}配置`}
                >
                  {activeTabs.map((tab) => (
                    <TabPane key={tab.code} title={tab.title} />
                  ))}
                </Tabs>
              </div>
            ) : null}
            <section className="settings-group-section">
              <div className="settings-fields">
                {activeGroup.pan115Login ? (
                  <Pan115LoginPanel onCookie={(cookie) => setValue("PAN115_COOKIE", cookie)} />
                ) : null}
                {renderFieldSequence(activeFields)}
                {activeGroup.code === "cloud-upload" ? <div className="settings-field settings-field-wide"><UploadMonitorStatus /></div> : null}
                {activeGroup.strmGenerate ? <div className="settings-field settings-field-wide"><EmbyMediaInfoAction /></div> : null}
                {activeGroup.strmGenerate ? (
                  <div className="settings-field settings-field-wide">
                    <StrmGenerateAction value={strmPaths} />
                  </div>
                ) : null}
                {activeGroup.pan115Scan ? (
                  <div className="settings-field settings-field-wide">
                    <Pan115LibraryScanAction value={scanPaths} />
                  </div>
                ) : null}
                {activeTest ? (
                  <div className="settings-field">
                    <div>
                      <Button
                        loading={openAITest.isPending}
                        disabled={openAITest.isPending}
                        onClick={() => openAITest.mutate(activeTest)}
                      >
                        {activeTest.label}
                      </Button>
                    </div>
                  </div>
                ) : null}
              </div>
              {activeGroup.note ? <p className="settings-group-note">{activeGroup.note}</p> : null}
              {activeGroup.code === "cloud-upload" ? (
                <>
                  <Divider />
                  <UploadTables />
                </>
              ) : null}
            </section>
          </div>
        </ContentCard>
        {/* 操作栏放在卡片之外：它要横向拉通到内容区两侧，宽度不再受卡片边界约束。 */}
        <div className="settings-form-footer">
          <Button
            type="primary"
            icon={<IconSave />}
            loading={update.isPending}
            disabled={update.isPending}
            onClick={submit}
          >
            保存设置
          </Button>
          <Button
            type="secondary"
            icon={<IconUndo />}
            disabled={!hasChanges || update.isPending}
            onClick={reset}
          >
            重置
          </Button>
        </div>
      </PageState>
    </section>
  );
}
