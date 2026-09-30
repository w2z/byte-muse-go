import { Button, Modal, Progress, Select } from "@arco-design/web-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { apiRequest } from "../../shared/api/client";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";

/** 115 扫码状态；取值与后端 domain.Pan115LoginState 一一对应。 */
type Pan115LoginStatus = "waiting" | "scanned" | "authorized" | "expired" | "canceled";

/** 扫码会话启动结果；qr_code 是后端直接返回的 PNG data URL，前端不再自行生成二维码。 */
type Pan115LoginSession = { session_id: string; qr_code: string; expires_at: string };

/** 单个容量数值；115 未返回格式化文本时回退字节数展示。 */
type Pan115SpaceAmount = { size: number; formatted: string };

/** 云下载配额；115 以任务个数计量，缺失时为 null 表示 115 未提供该数据。 */
type Pan115Quota = { total: number; used: number; remaining: number };

/** 账号快照；只含展示字段，令牌始终留在后端。 */
type Pan115Account = {
  id: string;
  name: string;
  avatar: string;
  level: string;
  space: { total: Pan115SpaceAmount; used: Pan115SpaceAmount; remaining: Pan115SpaceAmount };
  quota: Pan115Quota | null;
};

type Pan115LoginResult = { status: Pan115LoginStatus; account: Pan115Account | null };
type Pan115AccountResponse = { linked: boolean; account: Pan115Account | null };

/** Cookie 扫码渠道；取值与后端 pan115 渠道常量一一对应。 */
type Pan115CookieClientType =
  | "alipaymini"
  | "wechatmini"
  | "115android"
  | "115ios"
  | "web"
  | "115ipad"
  | "tv";

/** Cookie 扫码会话；client_type 是后端归一化后的渠道回显。 */
type Pan115CookieSession = {
  session_id: string;
  qr_code: string;
  client_type: Pan115CookieClientType;
  expires_at: string;
};

type Pan115CookieResult = { status: Pan115LoginStatus; cookie: string };

/**
 * 扫码渠道选项。
 * 115 按渠道下发不同客户端的 Cookie，因此这里必须与后端允许清单保持一致，
 * 默认渠道与 115 官方默认一致（支付宝小程序）。
 */
const cookieChannelOptions: { value: Pan115CookieClientType; label: string }[] = [
  { value: "alipaymini", label: "支付宝小程序" },
  { value: "wechatmini", label: "微信小程序" },
  { value: "115android", label: "安卓 App" },
  { value: "115ios", label: "iOS App" },
  { value: "web", label: "网页版" },
  { value: "115ipad", label: "iPad" },
  { value: "tv", label: "TV" },
];

/** 渠道中文名；用于扫码弹窗里的扫码提示，未知渠道回落到原值。 */
function channelLabel(value: string): string {
  return cookieChannelOptions.find((item) => item.value === value)?.label ?? value;
}

/**
 * 面板属性。
 * onCookie 由设置页注入：扫码拿到的 Cookie 只写入设置草稿，是否保存仍由用户决定，
 * 面板不直接调用设置接口，避免绕过设置页的统一校验与联动（如清空 Cookie 关闭事件监听）。
 */
type Pan115LoginPanelProps = {
  onCookie: (cookie: string) => void;
};

const accountQueryKey = ["pan115-account"] as const;
/** 二维码有效期约 5 分钟，2 秒轮询兼顾反馈及时与上游压力。 */
const pollInterval = 2000;
/** 状态查询的重试预算：115 在等待扫码时会把请求挂起约 30 秒，偶发失败不应中断整次扫码。 */
const pollRetries = 4;

/** 扫码过程中的提示文案；authorized 由成功分支接管，不在轮询态展示。 */
const statusText: Record<Pan115LoginStatus, string> = {
  waiting: "请使用 115 手机客户端扫描二维码",
  scanned: "已扫码，请在手机上确认登录",
  authorized: "登录成功",
  expired: "二维码已过期",
  canceled: "本次登录已取消",
};

/** 容量展示；缺少格式化文本时回退字节数，避免出现空值。 */
function formatSpace(amount: Pan115SpaceAmount): string {
  return amount.formatted || `${amount.size} B`;
}

/** 进度百分比；总量缺失或为零时按 0 处理，避免出现 NaN 与负值。 */
function percentOf(used: number, total: number): number {
  if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0) return 0;
  return Math.min(100, Math.max(0, (used / total) * 100));
}

/** 百分比展示：整数（含 0）不带小数点，其余保留一位小数，例如 0%、98.7%、100%。 */
function formatPercent(percent: number): string {
  const fixed = percent.toFixed(1);
  return `${fixed.endsWith(".0") ? fixed.slice(0, -2) : fixed}%`;
}

/** 空间容量与云下载配额共用同一行结构：标签 + 数值一行，进度条后紧跟百分比。 */
function MeterRow({ label, value, percent }: { label: string; value: string; percent: number }) {
  return (
    <div className="settings-pan115-meter">
      <div className="settings-pan115-meter-head">
        <span className="settings-pan115-meter-label">{label}</span>
        <span className="settings-pan115-meter-value">{value}</span>
      </div>
      <div className="settings-pan115-meter-track">
        <Progress
          className="settings-pan115-meter-bar"
          percent={percent}
          showText={false}
          strokeWidth={6}
          aria-label={`${label} ${value}`}
        />
        <span className="settings-pan115-meter-percent">{formatPercent(percent)}</span>
      </div>
    </div>
  );
}

/**
 * 115 网盘账号面板：扫码绑定、展示已绑定账号与容量、解除绑定。
 * 只负责 115 账号生命周期；离线下载目录等配置仍由设置表单统一保存。
 */
export function Pan115LoginPanel({ onCookie }: Pan115LoginPanelProps) {
  const [session, setSession] = useState<Pan115LoginSession | null>(null);
  const [channel, setChannel] = useState<Pan115CookieClientType>("alipaymini");
  const [cookieSession, setCookieSession] = useState<Pan115CookieSession | null>(null);
  const [message, messageHolder] = useFeedbackMessage();
  const queryClient = useQueryClient();
  // useFeedbackMessage 每次渲染都会新建回调对象，用 ref 固定引用，避免 effect 依赖它反复触发。
  const messageRef = useRef(message);
  messageRef.current = message;

  const account = useQuery({
    queryKey: accountQueryKey,
    queryFn: () => apiRequest<Pan115AccountResponse>("/pan115/account"),
  });

  const start = useMutation({
    mutationFn: () => apiRequest<Pan115LoginSession>("/pan115/login/sessions", { method: "POST" }),
    onSuccess: (result) => setSession(result),
    onError: (error: Error) => messageRef.current.error(error.message),
  });

  const sessionID = session?.session_id ?? null;
  const status = useQuery({
    queryKey: ["pan115-login-session", sessionID],
    queryFn: () => apiRequest<Pan115LoginResult>(`/pan115/login/sessions/${sessionID}`),
    enabled: sessionID !== null,
    // 先用重试预算吸收上游抖动，只有连续失败才把结论交给用户；二维码在此期间保持可见。
    retry: pollRetries,
    retryDelay: pollInterval,
    refetchInterval: (query) => {
      const value = query.state.data?.status;
      if (query.state.error || value === "authorized" || value === "expired" || value === "canceled") return false;
      return pollInterval;
    },
  });

  const unlink = useMutation({
    mutationFn: () => apiRequest<void>("/pan115/account", { method: "DELETE" }),
    onSuccess: () => {
      messageRef.current.success("已解除 115 账号绑定");
      void queryClient.invalidateQueries({ queryKey: accountQueryKey });
    },
    onError: (error: Error) => messageRef.current.error(error.message),
  });

  // 会话结束时作废二维码：离开页面或重新获取都不会让旧二维码继续可扫。
  useEffect(() => {
    if (sessionID === null) return;
    return () => {
      void apiRequest<void>(`/pan115/login/sessions/${sessionID}`, { method: "DELETE" }).catch(() => undefined);
    };
  }, [sessionID]);

  const loginStatus = status.data?.status ?? null;
  const authorizedName = status.data?.account?.name ?? "";
  useEffect(() => {
    if (loginStatus !== "authorized") return;
    messageRef.current.success(authorizedName ? `115 账号「${authorizedName}」登录成功` : "115 账号登录成功");
    setSession(null);
    void queryClient.invalidateQueries({ queryKey: accountQueryKey });
  }, [loginStatus, authorizedName, queryClient]);

  // Cookie 扫码：按渠道申请二维码，授权后把 Cookie 交给设置页草稿，服务端不落库。
  const startCookie = useMutation({
    mutationFn: (clientType: Pan115CookieClientType) =>
      apiRequest<Pan115CookieSession>("/pan115/cookie/login/sessions", {
        method: "POST",
        body: JSON.stringify({ client_type: clientType }),
      }),
    onSuccess: (result) => setCookieSession(result),
    onError: (error: Error) => messageRef.current.error(error.message),
  });

  const cookieSessionID = cookieSession?.session_id ?? null;
  const cookieStatus = useQuery({
    queryKey: ["pan115-cookie-login-session", cookieSessionID],
    queryFn: () => apiRequest<Pan115CookieResult>(`/pan115/cookie/login/sessions/${cookieSessionID}`),
    enabled: cookieSessionID !== null,
    // 与令牌扫码同源：重试预算吸收上游抖动，连续失败才把结论交给用户。
    retry: pollRetries,
    retryDelay: pollInterval,
    refetchInterval: (query) => {
      const value = query.state.data?.status;
      if (query.state.error || value === "authorized" || value === "expired" || value === "canceled") return false;
      return pollInterval;
    },
  });

  // 会话结束时作废二维码，避免关闭弹窗后旧二维码仍可被扫描。
  useEffect(() => {
    if (cookieSessionID === null) return;
    return () => {
      void apiRequest<void>(`/pan115/cookie/login/sessions/${cookieSessionID}`, { method: "DELETE" }).catch(
        () => undefined,
      );
    };
  }, [cookieSessionID]);

  const cookieLoginStatus = cookieStatus.data?.status ?? null;
  const cookieValue = cookieStatus.data?.cookie ?? "";
  useEffect(() => {
    if (cookieLoginStatus !== "authorized" || cookieValue === "") return;
    onCookie(cookieValue);
    setCookieSession(null);
    messageRef.current.success("已获取 115 Cookie 并填入设置，请点击保存生效");
  }, [cookieLoginStatus, cookieValue, onCookie]);

  const cookieFailed = cookieLoginStatus === "expired" || cookieLoginStatus === "canceled";
  const cookieRetrying = cookieStatus.failureCount > 0 && !cookieStatus.isError;

  const bound = account.data?.linked === true ? account.data.account : null;
  const failed = loginStatus === "expired" || loginStatus === "canceled";
  const retrying = status.failureCount > 0 && !status.isError;

  return (
    <div className="settings-field">
      {messageHolder}
      <span className="settings-field-label">115 账号</span>
      {account.isLoading ? (
        <span className="settings-field-description">正在读取 115 绑定状态…</span>
      ) : account.isError ? (
        <div className="settings-pan115-block">
          <span className="settings-field-description">读取 115 绑定状态失败：{account.error.message}</span>
          <div className="settings-pan115-actions">
            <Button type="secondary" onClick={() => void account.refetch()}>重新读取</Button>
          </div>
        </div>
      ) : bound ? (
        <div className="settings-pan115-block">
          <div className="settings-pan115-account">
            {bound.avatar ? <img className="settings-pan115-avatar" src={bound.avatar} alt="" /> : null}
            <div className="settings-pan115-account-text">
              <strong>{bound.name}</strong>
              <span className="settings-field-description">等级 {bound.level || "未知"}</span>
            </div>
          </div>
          {/* 两个计量同宽：宽度取「空间容量 已用 … / …」文字行，进度条不再铺满整个字段。 */}
          <div className="settings-pan115-meters">
            <MeterRow
              label="空间容量"
              value={`已用 ${formatSpace(bound.space.used)} / ${formatSpace(bound.space.total)}`}
              percent={percentOf(bound.space.used.size, bound.space.total.size)}
            />
            {bound.quota ? (
              <MeterRow
                label="云下载配额"
                value={`已用 ${bound.quota.used} / ${bound.quota.total}`}
                percent={percentOf(bound.quota.used, bound.quota.total)}
              />
            ) : (
              <span className="settings-field-description">云下载配额暂不可用</span>
            )}
          </div>
          <div className="settings-pan115-actions">
            <Button
              type="secondary"
              status="danger"
              loading={unlink.isPending}
              disabled={unlink.isPending}
              onClick={() => unlink.mutate()}
            >
              解除绑定
            </Button>
          </div>
        </div>
      ) : (
        <div className="settings-pan115-block">
          <div className="settings-pan115-actions">
            <Button type="primary" loading={start.isPending} disabled={start.isPending} onClick={() => start.mutate()}>
              扫码登录
            </Button>
          </div>
          <span className="settings-field-description">使用 115 手机客户端扫码授权，令牌加密保存在本地，可随时解除绑定。</span>
        </div>
      )}
      {/* Cookie 扫码独立于账号绑定：它换取生活事件等接口所需的 Cookie，与 OpenAPI 令牌互不影响。 */}
      <div className="settings-pan115-block settings-pan115-cookie">
        <span className="settings-field-description">
          选择扫码渠道后获取 Cookie，授权成功会自动填入「115 Cookie」设置项，点击保存后生效。
        </span>
        <div className="settings-pan115-actions">
          <Select
            className="settings-pan115-channel"
            size="small"
            value={channel}
            aria-label="扫码渠道"
            options={cookieChannelOptions}
            onChange={(value) => {
              const next = value as Pan115CookieClientType;
              setChannel(next);
              // 弹窗已打开时切换渠道直接换一张二维码，避免用户误扫上一个渠道的码。
              if (cookieSession !== null) startCookie.mutate(next);
            }}
          />
          <Button
            type="secondary"
            loading={startCookie.isPending}
            disabled={startCookie.isPending}
            onClick={() => startCookie.mutate(channel)}
          >
            扫码获取 Cookie
          </Button>
        </div>
      </div>
      {/* 二维码用弹窗展示：内联展开会把设置表单撑高，且关闭弹窗即作废当前扫码会话。 */}
      <Modal
        title="115 扫码登录"
        visible={session !== null}
        footer={null}
        unmountOnExit
        maskClosable={false}
        onCancel={() => setSession(null)}
      >
        {session ? (
          <div className="settings-pan115-block settings-pan115-login">
            <img className="settings-pan115-qrcode" src={session.qr_code} alt="115 登录二维码" />
            <span className="settings-field-description">
              {retrying ? "网络异常，正在重试…" : statusText[loginStatus ?? "waiting"]}
              {failed ? "，请重新获取二维码" : ""}
            </span>
            {status.isError ? (
              <span className="settings-field-description">查询扫码状态失败：{status.error.message}</span>
            ) : null}
            <div className="settings-pan115-actions">
              {failed ? (
                <Button type="primary" loading={start.isPending} disabled={start.isPending} onClick={() => start.mutate()}>
                  重新获取二维码
                </Button>
              ) : (
                <Button type="secondary" onClick={() => setSession(null)}>取消</Button>
              )}
            </div>
          </div>
        ) : null}
      </Modal>
      <Modal
        title={`115 扫码获取 Cookie（${channelLabel(cookieSession?.client_type ?? channel)}）`}
        visible={cookieSession !== null}
        footer={null}
        unmountOnExit
        maskClosable={false}
        onCancel={() => setCookieSession(null)}
      >
        {cookieSession ? (
          <div className="settings-pan115-block settings-pan115-login">
            <img className="settings-pan115-qrcode" src={cookieSession.qr_code} alt="115 获取 Cookie 二维码" />
            <span className="settings-field-description">
              {cookieRetrying
                ? "网络异常，正在重试…"
                : `请使用${channelLabel(cookieSession.client_type)}扫码并确认授权`}
              {cookieFailed ? "，请重新获取二维码" : ""}
            </span>
            {cookieStatus.isError ? (
              <span className="settings-field-description">查询扫码状态失败：{cookieStatus.error.message}</span>
            ) : null}
            <div className="settings-pan115-actions">
              {cookieFailed ? (
                <Button
                  type="primary"
                  loading={startCookie.isPending}
                  disabled={startCookie.isPending}
                  onClick={() => startCookie.mutate(channel)}
                >
                  重新获取二维码
                </Button>
              ) : (
                <Button type="secondary" onClick={() => setCookieSession(null)}>取消</Button>
              )}
            </div>
          </div>
        ) : null}
      </Modal>
    </div>
  );
}
