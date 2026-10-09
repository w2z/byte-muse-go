import { apiRequest, isApiError, newIdempotencyKey, type Page } from "./client";
import type { Media, Subscription, SubscriptionMode } from "./types";

/** 创建订阅的共用入口；单片和批量均使用同一幂等请求契约。 */
export function createMediaSubscription(mediaId: string, mode: SubscriptionMode = "strict", filter: Record<string, unknown> = {}) {
  return apiRequest<Subscription>("/subscriptions", {
    method: "POST", headers: { "Idempotency-Key": newIdempotencyKey() },
    body: JSON.stringify({ media_id: mediaId, mode, filter }),
  });
}

/** 批量执行计数；读取阶段 total 使用接口总数，执行阶段使用去重后的快照大小。 */
export type CatalogSubscriptionProgress = { phase: "collecting" | "subscribing"; processed: number; total: number; succeeded: number; skipped: number; failed: number; stoppedReason?: string };

/** 固定点击时的筛选与影片快照；收集全部分页后串行写入，取消仅停止后续写入，不中断已发出的订阅。 */
export async function subscribeCatalog({ scope, items, listPath, signal, onProgress }: {
  scope: "page" | "all"; items: Media[]; listPath: string; signal: AbortSignal;
  onProgress?: (progress: CatalogSubscriptionProgress) => void;
}): Promise<CatalogSubscriptionProgress> {
  const films = new Map<string, Media>();
  const progress: CatalogSubscriptionProgress = { phase: "collecting", processed: 0, total: 0, succeeded: 0, skipped: 0, failed: 0 };
  if (scope === "all") {
    const [path, search] = listPath.split("?");
    const params = new URLSearchParams(search);
    params.set("page_size", "100");
    for (let page = 1; ; page += 1) {
      signal.throwIfAborted();
      params.set("page", String(page));
      const result = await apiRequest<Page<Media>>(`${path}?${params}`, { signal });
      result.items.forEach((item) => films.set(item.id, item));
      onProgress?.({ ...progress, processed: films.size, total: result.total });
      if (page * result.page_size >= result.total) break;
      if (!result.items.length || result.page_size <= 0) throw new Error("影片列表已变化，请刷新后重试");
    }
  } else {
    items.forEach((item) => films.set(item.id, item));
  }
  progress.phase = "subscribing";
  progress.total = films.size;
  onProgress?.({ ...progress });
  for (const media of films.values()) {
    if (signal.aborted) break;
    if (media.subscription_status === "active") {
      progress.skipped += 1;
    } else {
      try {
        await createMediaSubscription(media.id);
        progress.succeeded += 1;
      } catch (error) {
        if (isApiError(error) && error.status === 409) progress.skipped += 1;
        else {
          progress.failed += 1;
          // 登录失效或权限被收回后不能继续批量发送写请求。
          if (isApiError(error) && (error.status === 401 || error.status === 403)) progress.stoppedReason = error.message;
        }
      }
    }
    progress.processed += 1;
    onProgress?.({ ...progress });
    if (progress.stoppedReason) break;
  }
  return progress;
}
