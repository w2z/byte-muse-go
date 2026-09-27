/** 与 api/openapi.yaml 0.1.0 对齐的第一阶段响应类型。 */

export type SubscriptionStatus = "none" | "active" | "canceled";
export type LibraryStatus = "unknown" | "absent" | "present";
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
  status: DownloadStatus;
  external_id?: string | null;
  error_message?: string | null;
  created_at: string;
  updated_at: string;
};

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

export type Actor = {
  name: string;
  photo: string | null;
  limit_date: string | null;
};
