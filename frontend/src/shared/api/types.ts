/** 与 api/openapi.yaml 0.1.0 对齐的第一阶段响应类型。 */

/** 已存影片标签及去重关联数，不包含追新订阅状态。 */
/** 标签名称保留来源原文；limit_date=null 表示未订阅。 */
export type CatalogTag = { name: string; media_count: number; category: string; limit_date: string | null };

export type SubscriptionStatus = "none" | "active" | "canceled";
export type LibraryStatus = "unknown" | "absent" | "present";
/** 影片单值类型；尚未分类时为 null。 */
export type VideoType = "censored" | "uncensored" | "uncensored_cracked" | "leaked";
export type DownloadStatus =
  | "queued"
  | "searching"
  | "submitted"
  | "downloading"
  | "completed"
  | "failed"
  | "unknown";
export type SubscriptionMode = "strict" | "preload";
export type MediaDisplayStatus =
  | "unsubscribed"
  | "subscribed"
  | "downloading"
  | "completed"
  | "failed"
  | "unknown";
export type DatabaseDriver = "sqlite" | "postgres" | "mysql";

export type User = {
  id: string;
  username: string;
};

export type LoginResponse = {
  user: User;
};

export type Media = {
  /** 详情接口返回的扩展资料；列表不加载。 */
  details?: MediaDetails;
  video_type: VideoType | null;
  id: string;
  code: string;
  title: string;
  translated_title: string | null;
  poster_url?: string | null;
  banner_url?: string | null;
  preview_url?: string | null;
  still_photos?: string[];
  release_date?: string | null;
  duration_minutes?: number | null;
  subscription_status: SubscriptionStatus;
  library_status: LibraryStatus;
  display_status?: MediaDisplayStatus;
  active_subscription?: Subscription | null;
  download_status?: DownloadStatus | null;
  created_at: string;
  updated_at: string;
};

/** 已保存的影片资料；null 表示未采集或无法确认，数值零和 false 均为有效资料。 */
export type MediaDetails = {
  actors: string[];
  tags: string[];
  producer: string | null;
  publisher: string | null;
  series: string | null;
  release_code: string | null;
  plot: string | null;
  director: string | null;
  rating: number | null;
  want_count: number | null;
  translation_engine: string | null;
  mosaic: boolean | null;
  censored: boolean | null;
  resolution: string | null;
};

export type Subscription = {
  id: string;
  media_id: string;
  status: SubscriptionStatus;
  mode: SubscriptionMode;
  filter: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  version: number;
  media?: Media;
};

export type DownloadTask = {
  id: string;
  media_id: string;
  code?: string | null;
  available_actions?: DownloadAction[];
  source_site?: string | null;
  source_kind?: "pt" | "bt" | null;
  downloader?: string | null;
  info_hash?: string | null;
  transfer_status?: "downloading" | "paused" | "stopped" | "failed" | "completed" | null;
  added_at?: string | null;
  completed_at?: string | null;
  status: DownloadStatus;
  external_id?: string | null;
  error_message?: string | null;
  created_at: string;
  updated_at: string;
};

/** 后端按任务状态及下载器实际能力返回的允许操作。 */
export type DownloadAction = "pause" | "stop" | "resume" | "retry" | "delete" | "delete_files";

export type ScheduledTask = {
  name: string;
  cron: string;
  last_run: string | null;
  running: boolean;
};

export type LogRecord = {
  time: string;
  level: "debug" | "info" | "warning" | "error";
  category: string;
  message: string;
  attrs?: Record<string, unknown>;
};

export type Dashboard = {
  active_subscriptions: number;
  completed_downloads: number;
  media_count: number;
};

export type SystemSettings = {
  database_driver: DatabaseDriver;
  values: Record<string, string>;
  configured: Record<string, boolean>;
};

/** 顶栏版本标签数据：当前运行版本与发布仓库版本的比较结果。 */
export type SystemVersion = {
  /** 当前运行版本；未注入构建版本时为 dev。 */
  current: string;
  /** 发布仓库当前记录的版本；检查失败时为空串。 */
  latest: string;
  /** latest 高于 current 时为 true，前端据此提示升级。 */
  has_update: boolean;
  /** 发布来源元数据；检查失败时为空串，不作为界面跳转入口。 */
  release_url: string;
  /** 本次结论的产生时间，UTC RFC 3339。 */
  checked_at: string;
  /** 远端检查的失败原因；已确认可用时为空串。 */
  check_error: string;
};

/** 容器内升级状态；success 仅表示新服务已通过就绪检查。 */
export type SystemUpgrade = {
  enabled: boolean;
  phase: "idle" | "downloading" | "restarting" | "success" | "failed";
  target: string;
  error: string;
};

export type Actor = {
  name: string;
  photo: string | null;
  limit_date: string | null;
};
