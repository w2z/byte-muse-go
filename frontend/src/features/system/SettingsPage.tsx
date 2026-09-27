import { Button, Input, InputNumber, Radio, Switch, Tabs } from "@arco-design/web-react";
import { IconSave, IconUndo } from "@arco-design/web-react/icon";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { apiRequest } from "../../shared/api/client";
import type { SystemSettings } from "../../shared/api/types";
import { ContentCard } from "../../shared/ui/ContentCard";
import { PageState } from "../../shared/ui/PageState";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";

type FieldKind = "text" | "textarea" | "bool" | "int" | "enum" | "json" | "sort";
type SettingOption = { value: string; label: string };
type SettingField = {
  key: string;
  label: string;
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
};
type SettingGroup = { code: string; title: string; fields: SettingField[] };
type SettingCategory = { code: string; title: string; groupCodes: string[] };
type SettingsUpdate = { values: Record<string, string> };
const TabPane = Tabs.TabPane;

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
  { value: "Rousi", label: "肉丝" },
];

const imageModeOptions: SettingOption[] = [
  { value: "INVISIBLE", label: "无图" },
  { value: "VISIBLE", label: "有图" },
  { value: "BLUR", label: "模糊" },
];

const rankTypeOptions: SettingOption[] = [
  { value: "", label: "不订阅" },
  { value: "daily", label: "每日" },
  { value: "weekly", label: "weekly" },
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

/** 分组、顺序与字段命名对齐对标站 对标站/config。 */
const groups: SettingGroup[] = [
  {
    code: "site",
    title: "站点",
    fields: [
      { key: "MTEAM_API_KEY", label: "馒头令牌", kind: "text", secret: true },
      { key: "PTT_COOKIE", label: "PTT COOKIE", kind: "text", secret: true },
      { key: "ROUSI_COOKIE", label: "肉丝 COOKIE", kind: "text", secret: true },
      {
        key: "NICEPT_COOKIE",
        label: "NicePT COOKIE",
        kind: "text",
        secret: true,
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
    ],
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
      { key: "TELEGRAM_SPOILER", label: "推送防剧透", kind: "bool" },
    ],
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
    code: "filter",
    title: "过滤",
    fields: [{ key: "DEFAULT_FILTER", label: "默认过滤规则", kind: "json" }],
  },
  {
    code: "sort",
    title: "排序",
    fields: [
      { key: "DEFAULT_SORT", label: "排序器", kind: "sort" },
      {
        key: "MAIN_SITE",
        label: "主站选择（配合排序器使用）",
        kind: "enum",
        options: mainSiteOptions,
      },
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
        kind: "text",
      },
      {
        key: "ACTOR_SCHEDULE_TIME",
        label: "演员订阅定时任务",
        placeholder: "cron表达式",
        kind: "text",
      },
      {
        key: "TAG_SCHEDULE_TIME",
        label: "标签订阅定时任务",
        placeholder: "cron表达式",
        kind: "text",
      },
      {
        key: "DOWNLOAD_SCHEDULE_TIME",
        label: "番号订阅定时任务",
        description: "cron表达式，建议设置在榜单与演员订阅之后",
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
        kind: "enum",
        options: [
          { value: "none", label: "关闭" },
          { value: "openai", label: "OpenAI" },
          { value: "google", label: "Google" },
          { value: "baidu", label: "百度" },
          { value: "deeplx", label: "DeepLX" },
        ],
      },
      { key: "BAIDU_APP_ID", label: "百度翻译APPID", kind: "text" },
      {
        key: "BAIDU_API_KEY",
        label: "百度大模型文本翻译API_KEY",
        kind: "text",
        secret: true,
      },
      {
        key: "GOOGLE_API_KEY",
        label: "Google Cloud Translation API_KEY",
        kind: "text",
        secret: true,
      },
      {
        key: "DEEPLX_URL",
        label: "DeepLX 地址",
        kind: "text",
        placeholder: "http://127.0.0.1:1188",
      },
      {
        key: "TRANSLATION_PROMPT",
        label: "自定义翻译 Prompt",
        kind: "textarea",
        wide: true,
        placeholder: "留空使用内置翻译提示词；仅 OpenAI 翻译引擎使用",
      },
    ],
  },
  {
    code: "agent",
    title: "Agent",
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
        description: "配置微信封面推送并开启图片缓存时用于微信图片推送",
        kind: "text",
      },
      {
        key: "BYPASS_URL",
        label: "爬虫增强",
        description: "真实浏览器模拟网页访问，https://github.com/sarperavci/CloudflareBypassForScraping 或者 https://github.com/FlareSolverr/FlareSolverr",
        kind: "text",
      },
      { key: "JAVDB_HOST", label: "JAVDB API地址", kind: "text" },
      { key: "ENABLE_BT_ANTI_LEECH", label: "BT种子下完即撤种", kind: "bool" },
      { key: "ENABLE_PHOTO_CACHE", label: "图片持久化", kind: "bool" },
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
    groupCodes: ["qbittorrent", "transmission", "thunder", "clouddrive2"],
  },
];

function filterId(name: string) {
  return `setting-${name}`;
}

function fieldPlaceholder(field: SettingField): string | undefined {
  if (field.placeholder !== undefined) return field.placeholder;
  if (field.kind === "enum") return "请选择";
  if (field.kind === "text" || field.kind === "textarea" || field.kind === "int") {
    return `请输入${field.label}`;
  }
  return undefined;
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

/**
 * 系统设置页。
 *
 * 字段命名与分组内容对齐对标站 对标站 的 /config；入口按站点、播放器、消息渠道和
 * 下载器四类显示为一级标签，下方连续展示当前分类的全部分组。
 * 每个分类统一保存；敏感值以普通文本框回显并随当前分类一起提交；布尔、数字、枚举、JSON 与排序标签
 * 的取值由后端 writableSettings 权威校验。
 */
/**
 * 站点分组文本框的固定行数。
 *
 * 站点分组的字段都是长凭据（馒头令牌与各家 COOKIE），统一固定三行，并允许用户手动上下拉伸，
 * 避免各字段被内容长度撑成互不相同的高度。
 * 这里刻意不用 autoSize：Arco 在每次输入时都会重写 height / min-height / max-height，
 * 手动拖出来的高度会被顶回去，初始行数只能用 rows 固定。
 */
const SITE_TEXTAREA_ROWS = 3;

export function SettingsPage() {
  const [activeCategoryCode, setActiveCategoryCode] = useState(
    categories[0].code,
  );
  const [activeGroupCode, setActiveGroupCode] = useState(categories[0].groupCodes[0]);
  const [draft, setDraft] = useState<Draft>({});
  const [filter, setFilter] = useState<FilterDraft>(emptyFilterDraft);
  const [filterUnknown, setFilterUnknown] = useState<Record<string, unknown>>(
    {},
  );
  const [sortOrder, setSortOrder] = useState<string[]>([]);
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

  const syncDraft = (settings: SystemSettings) => {
    const values = settings.values ?? {};
    setSavedSnapshot({ values: { ...values } });
    const next: Draft = {};
    for (const group of groups) {
      for (const field of group.fields) {
        if (field.kind === "json" || field.kind === "sort") continue;
        const raw = Object.prototype.hasOwnProperty.call(values, field.key)
          ? values[field.key]
          : field.key === "LOG_RETENTION_DAYS"
            ? "30"
            : "";
        next[field.key] = field.kind === "bool" ? raw === "true" : raw;
      }
    }
    const parsed = parseFilterDraft(values.DEFAULT_FILTER);
    setDraft(next);
    setFilter(parsed.draft);
    setFilterUnknown(parsed.unknown);
    setSortOrder(parseSortOrder(values.DEFAULT_SORT));
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

  const setValue = (key: string, value: string | boolean) =>
    setDraft((previous) => ({ ...previous, [key]: value }));

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

  const buildPayload = () => {
    const payload: Record<string, string> = {};
    for (const group of [activeGroup]) {
      for (const field of group.fields) {
        if (field.kind === "json") {
          payload[field.key] = serializeFilterDraft(filter, filterUnknown);
          continue;
        }
        if (field.kind === "sort") {
          payload[field.key] = sortOrder.join(",");
          continue;
        }
        const value = draft[field.key];
        if (field.kind === "bool") {
          payload[field.key] = value === true ? "true" : "false";
          continue;
        }
        payload[field.key] = typeof value === "string" ? value.trim() : "";
      }
    }
    return payload;
  };

  const currentPayload = buildPayload();
  const baselineValue = (key: string) => {
    const saved = savedSnapshot.values[key];
    if (saved !== undefined) return saved;
    const field = groups
      .flatMap((group) => group.fields)
      .find((item) => item.key === key);
    if (field?.kind === "bool") return "false";
    if (key === "LOG_RETENTION_DAYS") return "30";
    if (field?.kind === "sort" || field?.kind === "json") return "";
    return "";
  };
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
    if (field.kind === "json") {
      return (
        <div className="settings-field settings-field-wide" key={field.key}>
          <span className="settings-field-label">{field.label}</span>
          <div className="settings-filter">
            {filterSwitches.map((item) => (
              <label className="settings-filter-row settings-toggle-row" key={item.key}>
                <Switch
                  aria-label={item.label}
                  checked={filter[item.key]}
                  onChange={(checked: boolean) =>
                    setFilter((previous) => ({
                      ...previous,
                      [item.key]: checked,
                    }))
                  }
                />
                <span>{item.label}</span>
              </label>
            ))}
            <div className="settings-filter-range">
              <label className="settings-field" htmlFor={filterId("min_size")}>
                <span className="settings-field-label">最小体积(MB)</span>
                <div className="settings-input-with-unit">
                  <InputNumber
                    aria-label="最小体积(MB)"
                    id={filterId("min_size")}
                    value={filter.min_size === "" ? undefined : Number(filter.min_size)}
                    min={0}
                    suffix="MB"
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
                  <InputNumber
                    aria-label="最大体积(MB)"
                    id={filterId("max_size")}
                    value={filter.max_size === "" ? undefined : Number(filter.max_size)}
                    min={0}
                    suffix="MB"
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
        <div className="settings-field settings-toggle-row" key={field.key}>
          <Switch
            aria-label={field.label}
            checked={draft[field.key] === true}
            onChange={(checked: boolean) => setValue(field.key, checked)}
          />
          <div className="settings-toggle-copy">
            <span className="settings-field-label">{field.label}</span>
            {field.description ? <span className="settings-field-description">{field.description}</span> : null}
          </div>
        </div>
      );
    }

    if (field.kind === "enum") {
      return (
        <div className="settings-field settings-field-wide" key={field.key} aria-label={field.label}>
          <span className="settings-field-label">{field.label}</span>
          {field.description ? <span className="settings-field-description">{field.description}</span> : null}
          <div className="settings-option-radio">
            {typeof draft[field.key] !== "string" || draft[field.key] === "" ? (
              <span className="settings-field-placeholder">请选择</span>
            ) : null}
            <Radio.Group value={typeof draft[field.key] === "string" ? draft[field.key] : ""} onChange={(value: string | number) => setValue(field.key, String(value))}>
              {(field.options ?? []).map((option) => <Radio key={option.value} value={option.value}>{option.label}</Radio>)}
            </Radio.Group>
          </div>
        </div>
      );
    }

    const value =
      typeof draft[field.key] === "string" ? (draft[field.key] as string) : "";
    const siteField = group.code === "site";
    const promptField =
      field.key === "TRANSLATION_PROMPT" || field.key === "AGENT_SYSTEM_PROMPT";
    const placeholder = fieldPlaceholder(field);
    return (
      <div
        className={"settings-field" + (field.wide ? " settings-field-wide" : "")}
        key={field.key}
      >
        <label className="settings-field-label" htmlFor={filterId(field.key)}>
          {field.label}
        </label>
        {field.description ? <span className="settings-field-description">{field.description}</span> : null}
        {siteField || field.kind === "textarea" ? (
          <Input.TextArea
            id={filterId(field.key)}
            className={
              promptField
                ? "settings-textarea " +
                  (field.key === "TRANSLATION_PROMPT"
                    ? "settings-translation-prompt"
                    : "settings-agent-prompt")
                : siteField
                  ? "settings-site-textarea"
                  : "settings-textarea"
            }
            value={value}
            placeholder={placeholder}
            rows={promptField ? 5 : siteField ? SITE_TEXTAREA_ROWS : undefined}
            autoSize={
              promptField || siteField
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
            <InputNumber
              id={filterId(field.key)}
              value={value === "" ? undefined : Number(value)}
              min={0}
              suffix={field.unit}
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
      </div>
    );
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
            <section className="settings-group-section">
              <div className="settings-fields">
                {activeGroup.fields.map((field) => renderField(field, activeGroup))}
              </div>
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
