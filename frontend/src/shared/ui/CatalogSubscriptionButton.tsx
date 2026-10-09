import { Button, Dropdown, Menu } from "@arco-design/web-react";
import { IconHeart } from "@arco-design/web-react/icon";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { subscribeCatalog, type CatalogSubscriptionProgress } from "../api/catalogSubscriptions";
import type { Media } from "../api/types";
import { apiRequest, type Page } from "../api/client";
import { useFeedbackMessage } from "./FeedbackMessage";

/** 筛选区批量订阅入口；跨页可订阅性由服务端筛选确认，完成后刷新状态，卸载后停止后续操作。 */
export function CatalogSubscriptionButton({ items, total, listPath, disabled, onComplete }: { items: Media[]; total: number; listPath: string; disabled: boolean; onComplete?: () => void }) {
  const client = useQueryClient();
  const [message, holder] = useFeedbackMessage();
  const [progress, setProgress] = useState<CatalogSubscriptionProgress | null>(null);
  const [visible, setVisible] = useState(false);
  const running = useRef<AbortController | null>(null);
  useEffect(() => () => running.current?.abort(), []);
  const hasPageUnsubscribed = items.some((media) => media.subscription_status !== "active");
  const [path, search] = listPath.split("?");
  const params = new URLSearchParams(search);
  const subscription = params.get("subscription");
  // 当前筛选已限定订阅状态或当前页足以裁决时，不再额外请求。
  const needsLookup = total > items.length && !hasPageUnsubscribed && !subscription;
  params.set("subscription", "none");
  params.set("page", "1");
  params.set("page_size", "1");
  const availability = useQuery({
    queryKey: ["catalog-subscription-availability", listPath],
    queryFn: ({ signal }) => apiRequest<Page<Media>>(`${path}?${params}`, { signal }),
    enabled: needsLookup && !disabled && !progress,
    retry: false,
  });
  const hasUnsubscribed = total > 0 && subscription !== "active" && (
    hasPageUnsubscribed || subscription === "none" ||
    (needsLookup && availability.isSuccess && !availability.isFetching && availability.data.total > 0)
  );
  const buttonDisabled = disabled || !!progress || !hasUnsubscribed;

  /** 同步锁拦截连续点击；收集失败不写入，单项失败仍处理其余影片并汇总。 */
  async function start(scope: "page" | "all") {
    if (running.current || buttonDisabled || (scope === "page" && !hasPageUnsubscribed)) return;
    const controller = new AbortController();
    running.current = controller;
    setVisible(false);
    setProgress({ phase: scope === "all" ? "collecting" : "subscribing", processed: 0, total: scope === "all" ? total : items.length, succeeded: 0, skipped: 0, failed: 0 });
    try {
      const result = await subscribeCatalog({ scope, items, listPath, signal: controller.signal, onProgress: (next) => { if (!controller.signal.aborted) setProgress(next); } });
      if (!controller.signal.aborted) {
        const text = result.stoppedReason
          ? `订阅已停止：成功 ${result.succeeded}，跳过 ${result.skipped}，失败 ${result.failed}，未处理 ${result.total - result.processed}；${result.stoppedReason}`
          : `订阅完成：成功 ${result.succeeded}，跳过 ${result.skipped}，失败 ${result.failed}`;
        if (result.failed) message.error(text); else message.success(text);
        onComplete?.();
      }
    } catch (error) {
      if (!controller.signal.aborted) message.error(error instanceof Error ? error.message : "批量订阅失败");
    } finally {
      await client.invalidateQueries();
      running.current = null;
      if (!controller.signal.aborted) setProgress(null);
    }
  }

  return <>
    {holder}
    <Dropdown trigger="click" position="br" disabled={buttonDisabled} popupVisible={visible && !buttonDisabled} onVisibleChange={setVisible} droplist={
      <Menu onClickMenuItem={(key) => { if (key === "page" || key === "all") void start(key); }}>
        <Menu.Item key="page" disabled={!hasPageUnsubscribed} aria-disabled={!hasPageUnsubscribed}>订阅当前页</Menu.Item>
        <Menu.Item key="all" disabled={!hasUnsubscribed} aria-disabled={!hasUnsubscribed}>订阅所有</Menu.Item>
      </Menu>
    }>
      <Button type="primary" icon={<IconHeart />} disabled={buttonDisabled} loading={!!progress}>一键订阅</Button>
    </Dropdown>
    {progress && <div className="catalog-subscription-progress" role="status">{progress.phase === "collecting" ? "读取影片" : "订阅中"} {progress.processed}/{progress.total}（{progress.total ? Math.round(progress.processed / progress.total * 100) : 0}%）</div>}
  </>;
}
