import { useEffect, useState } from "react";
import { Badge, Button, Modal, Progress, Steps, Tag } from "@arco-design/web-react";
import { IconGithub, IconSend } from "@arco-design/web-react/icon";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiRequest } from "../../shared/api/client";
import type { SystemUpgrade, SystemVersion } from "../../shared/api/types";

const VERSION_KEY = ["system", "version"] as const;
const UPGRADE_KEY = ["system", "upgrade"] as const;
const ACTIVE_PHASES = ["downloading", "extracting", "installing", "restarting"];
const UPGRADE_STEPS = ["开始下载文件", "开始解压文件", "正在升级", "正在重启"];

/** 本地版本立即显示；更新说明按服务端版本区间展示，升级断连继续等待，目标就绪后刷新。 */
export function VersionTag() {
  const [visible, setVisible] = useState(false);
  const [waitingTarget, setWaitingTarget] = useState("");
  const [waitExpired, setWaitExpired] = useState(false);
  const client = useQueryClient();
  const { data, error, isFetching } = useQuery({
    queryKey: VERSION_KEY,
    queryFn: () => apiRequest<SystemVersion>("/system/version"),
    staleTime: 60 * 60 * 1000,
  });
  const check = useMutation({
    mutationFn: async () => {
      await client.cancelQueries({ queryKey: VERSION_KEY });
      return apiRequest<SystemVersion>("/system/version?refresh=true");
    },
    onSuccess: (result) => client.setQueryData(VERSION_KEY, result),
  });
  const upgrade = useQuery({
    queryKey: UPGRADE_KEY,
    queryFn: () => apiRequest<SystemUpgrade>("/system/upgrade"),
    enabled: visible || Boolean(waitingTarget),
    retry: false,
    refetchInterval: (query) => waitingTarget || ACTIVE_PHASES.includes(query.state.data?.phase ?? "") ? 1500 : false,
    refetchIntervalInBackground: true,
  });
  const install = useMutation({
    mutationFn: () => apiRequest<SystemUpgrade>("/system/upgrade", { method: "POST", body: JSON.stringify({ target: data?.latest }) }),
    onSuccess: (result) => { client.setQueryData(UPGRADE_KEY, result); setWaitingTarget(result.target); },
  });
  useEffect(() => {
    if (upgrade.data && ACTIVE_PHASES.includes(upgrade.data.phase)) setWaitingTarget(upgrade.data.target);
    if (upgrade.data?.phase === "failed") setWaitingTarget("");
    if (waitingTarget && upgrade.data?.phase === "success" && upgrade.data.target === waitingTarget) window.location.reload();
  }, [upgrade.data, waitingTarget]);
  useEffect(() => {
    setWaitExpired(false);
    if (!waitingTarget) return;
    const timer = window.setTimeout(() => setWaitExpired(true), 12 * 60 * 1000);
    return () => window.clearTimeout(timer);
  }, [waitingTarget]);
  const version = data?.current || import.meta.env.VITE_APP_VERSION;
  const hasUpdate = Boolean(data?.has_update);
  const checking = check.isPending || isFetching;
  const busy = install.isPending || Boolean(waitingTarget);
  const phase = install.isPending ? "downloading" : upgrade.data?.phase ?? "idle";
  const completedSteps = install.isPending ? 0 : Math.min(4, Math.max(0, upgrade.data?.completed_steps ?? (phase === "success" ? 4 : ACTIVE_PHASES.indexOf(phase))));
  // 服务保留上次任务的终态；历史成功或失败不能让步骤常驻，断连等待期间仍保留进度。
  const showProgress = (busy || ACTIVE_PHASES.includes(phase)) && phase !== "success" && phase !== "failed";
  const failure = install.error?.message || upgrade.data?.error || check.error?.message || data?.check_error || error?.message;
  const showChanges = hasUpdate && !busy && !checking && !failure && !waitExpired && Boolean(data?.changes?.length);
  const summary = waitExpired ? "等待服务恢复超时，请检查容器日志后刷新页面。" : busy ? (phase === "success" ? "升级完成，正在刷新页面…" : UPGRADE_STEPS[Math.min(completedSteps, 3)] + "…")
    : checking ? "正在检查更新…" : failure ? failure : hasUpdate ? `新版本 ${data?.latest} 已发布`
      : data?.latest ? "当前已是最新版本" : "点击下方按钮检查更新";
  return (
    <>
      <span className="header-version">
        <button className="header-version-trigger" type="button" aria-haspopup="dialog" onClick={() => setVisible(true)}>
          <Badge className="header-version-badge" dot count={hasUpdate ? 1 : 0}>
            <Tag className="header-version-tag" color={hasUpdate ? "orangered" : undefined} icon={<IconGithub />}>{`v${version}`}</Tag>
          </Badge>
        </button>
      </span>
      <Modal className="version-modal" visible={visible} simple closable title={null} footer={null} unmountOnExit
        style={{ width: 600, maxWidth: "calc(100vw - 32px)", padding: 0, background: "transparent" }} onCancel={() => setVisible(false)}>
        <div className="version-dialog">
          <IconSend className="version-dialog-decoration" aria-hidden />
          <h2>{hasUpdate ? `升级至 ${data?.latest}` : "版本更新"}</h2>
          <div className="version-dialog-content">
            <p className="version-dialog-label">当前版本</p>
            <strong className="version-dialog-number">{`v${version}`}</strong>
            {showProgress && <div className="version-upgrade-progress">
              <div role="group" aria-label="升级步骤" className="version-upgrade-steps">
                <Steps direction="horizontal" size="small" lineless current={completedSteps + 1}>
                  {UPGRADE_STEPS.map((label, index) => <Steps.Step key={label}
                    style={{ flex: 1, minWidth: 0, marginRight: 0, display: "flex", flexDirection: "column", alignItems: "center" }}
                    title={<span style={{ display: "inline-block", maxWidth: "4em", whiteSpace: "normal", textAlign: "center" }} className={index < completedSteps ? "is-complete" : index === completedSteps ? "is-current" : undefined}
                      aria-current={index === completedSteps ? "step" : undefined}>{label}</span>} />)}
                </Steps>
              </div>
              <div role="progressbar" aria-label="升级进度" aria-valuemin={0} aria-valuemax={100} aria-valuenow={completedSteps * 25}>
                <div aria-hidden="true"><Progress percent={completedSteps * 25} status="normal" /></div>
              </div>
            </div>}
            <div role="status" aria-live="polite">
              {showChanges ? <ol className="version-dialog-changes" aria-label="更新内容" tabIndex={0}>
                {data?.changes?.map((change, index) => <li key={`${change.version}-${index}`}>{change.message}</li>)}
              </ol> : <p className={failure && !busy ? "version-dialog-error" : undefined}>{summary}</p>}
            </div>
            {hasUpdate && !busy && <p className="version-dialog-note">升级期间服务会短暂重启，完成后页面自动刷新。</p>}
            {hasUpdate && upgrade.data?.enabled === false && <p className="version-dialog-error">当前镜像不支持容器内升级，请先更新一次镜像。</p>}
          </div>
          <div className="version-dialog-actions">
            {hasUpdate ? <>
              <Button type="primary" loading={busy} disabled={upgrade.data?.enabled === false} onClick={() => install.mutate()}>立即升级</Button>
              <Button type="text" onClick={() => setVisible(false)}>{busy ? "关闭" : "暂不升级"}</Button>
            </> : <Button type="primary" loading={checking} onClick={() => check.mutate()}>立即检查更新</Button>}
          </div>
        </div>
      </Modal>
    </>
  );
}
