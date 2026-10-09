import { Button, Dropdown, Menu } from "@arco-design/web-react";
import { IconHeart } from "@arco-design/web-react/icon";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { subscribeCatalog, type CatalogSubscriptionProgress } from "../api/catalogSubscriptions";
import type { Media } from "../api/types";
import { useFeedbackMessage } from "./FeedbackMessage";

/** 筛选区批量订阅入口；固定点击时的数据范围，卸载时停止后续操作，完成后刷新服务端状态。 */
export function CatalogSubscriptionButton({ items, total, listPath, disabled, onComplete }: { items: Media[]; total: number; listPath: string; disabled: boolean; onComplete?: () => void }) {
  const client = useQueryClient();
  const [message, holder] = useFeedbackMessage();
  const [progress, setProgress] = useState<CatalogSubscriptionProgress | null>(null);
  const [visible, setVisible] = useState(false);
  const running = useRef<AbortController | null>(null);
  useEffect(() => () => running.current?.abort(), []);

  /** 同步锁拦截连续点击；收集失败不写入，单项失败仍处理其余影片并汇总。 */
  async function start(scope: "page" | "all") {
    if (running.current || disabled) return;
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
    <Dropdown trigger="click" position="br" disabled={disabled || !!progress} popupVisible={visible} onVisibleChange={setVisible} droplist={
      <Menu onClickMenuItem={(key) => { if (key === "page" || key === "all") void start(key); }}>
        <Menu.Item key="page" disabled={!items.length}>订阅当前页</Menu.Item>
        <Menu.Item key="all" disabled={!total}>订阅所有</Menu.Item>
      </Menu>
    }>
      <Button type="primary" icon={<IconHeart />} disabled={disabled || !!progress} loading={!!progress}>一键订阅</Button>
    </Dropdown>
    {progress && <div className="catalog-subscription-progress" role="status">{progress.phase === "collecting" ? "读取影片" : "订阅中"} {progress.processed}/{progress.total}（{progress.total ? Math.round(progress.processed / progress.total * 100) : 0}%）</div>}
  </>;
}
