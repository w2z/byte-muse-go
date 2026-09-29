import { Button } from "@arco-design/web-react";
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

/** 账号快照；只含展示字段，令牌始终留在后端。 */
type Pan115Account = {
  id: string;
  name: string;
  avatar: string;
  level: string;
  space: { total: Pan115SpaceAmount; used: Pan115SpaceAmount; remaining: Pan115SpaceAmount };
};

type Pan115LoginResult = { status: Pan115LoginStatus; account: Pan115Account | null };
type Pan115AccountResponse = { linked: boolean; account: Pan115Account | null };

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

/**
 * 115 网盘账号面板：扫码绑定、展示已绑定账号与容量、解除绑定。
 * 只负责 115 账号生命周期；离线下载目录等配置仍由设置表单统一保存。
 */
export function Pan115LoginPanel() {
  const [session, setSession] = useState<Pan115LoginSession | null>(null);
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
              <span className="settings-field-description">
                等级 {bound.level || "未知"} · 已用 {formatSpace(bound.space.used)} / {formatSpace(bound.space.total)}
              </span>
            </div>
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
      ) : session ? (
        <div className="settings-pan115-block">
          <img className="settings-pan115-qrcode" src={session.qr_code} alt="115 登录二维码" />
          <span className="settings-field-description">
            {retrying ? "网络异常，正在重试…" : statusText[loginStatus ?? "waiting"]}
            {failed ? "，请重新获取二维码" : ""}
          </span>
          <div className="settings-pan115-actions">
            {failed ? (
              <Button
                type="primary"
                loading={start.isPending}
                disabled={start.isPending}
                onClick={() => { setSession(null); start.mutate(); }}
              >
                重新获取二维码
              </Button>
            ) : (
              <Button type="secondary" onClick={() => setSession(null)}>取消</Button>
            )}
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
      {status.isError ? <span className="settings-field-description">查询扫码状态失败：{status.error.message}</span> : null}
    </div>
  );
}
