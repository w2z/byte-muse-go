import { Badge, Tag, Tooltip } from "@arco-design/web-react";
import { IconGithub } from "@arco-design/web-react/icon";
import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../../shared/api/client";
import type { SystemVersion } from "../../shared/api/types";

/** 前端缓存时长与后端一致：后端已按 1 小时缓存上游结果，前端不重复请求。 */
const VERSION_STALE_TIME = 60 * 60 * 1000;

/**
 * 顶栏版本标签：显示当前运行版本，发现新版本时换成提示色并可跳转发布仓库。
 *
 * 标签只做展示：不传 checkable（可选中）等交互属性，文字选中由 .header-version-tag 关闭。
 * 图标使用 Arco 的 IconGithub，与跳转的发布仓库语义一致。
 * 有更新时在标签右上角显示 Arco Badge 小红点，并换成 orangered 底色、可点击跳转发布仓库。
 * 默认态不传 Tag 的 color：Arco 对非预设色值会走自定义色分支（白字 + 内联背景），
 * 传 "default" 反而会让标签文字不可见。
 * 首次渲染直接显示构建时注入的本地版本；异步检查只补充运行版本和更新状态，失败不隐藏标签。
 */
export function VersionTag() {
  const { data, isError } = useQuery({
    queryKey: ["system", "version"],
    queryFn: () => apiRequest<SystemVersion>("/system/version"),
    staleTime: VERSION_STALE_TIME,
  });
  const version = data?.current || import.meta.env.VITE_APP_VERSION;
  const latest = data?.latest ?? "";
  const releaseURL = data?.release_url ?? "";
  const hasUpdate = Boolean(data?.has_update);
  const checkError = data?.check_error || (isError ? "暂时无法检查更新" : "");
  const tip = hasUpdate
    ? `发现新版本 v${latest}，点击查看发布仓库`
    : `当前版本 v${version}` + (checkError
      ? `（检查更新失败：${checkError}）`
      : data ? "，已是最新版本" : "，正在检查更新");
  const tag = (
    <Tag className="header-version-tag" color={hasUpdate ? "orangered" : undefined} icon={<IconGithub />}>{`v${version}`}</Tag>
  );
  const label = hasUpdate && releaseURL ? (
    <a className="header-version-link" href={releaseURL} target="_blank" rel="noreferrer" aria-label={tip}>{tag}</a>
  ) : (
    <span>{tag}</span>
  );
  return (
    <span className="header-version">
      <Tooltip content={tip}>
        {/* Arco 的 dot 只在 count 为正数时渲染，这里用 1 占位；无更新时传 0，角标与数字都不出现。 */}
        <Badge className="header-version-badge" dot count={hasUpdate ? 1 : 0}>{label}</Badge>
      </Tooltip>
    </span>
  );
}
