import { Button, InputNumber, Popconfirm, Radio, Switch } from "@arco-design/web-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, isApiError } from "../api/client";
import type { Media, Subscription, SubscriptionMode } from "../api/types";
import { useFeedbackMessage } from "./FeedbackMessage";
import { AppDialog } from "./AppDialog";

type SubscriptionFilterForm = {
  only_chinese: boolean;
  only_uc: boolean;
  exclude_uc: boolean;
  only_free: boolean;
  only_uhd: boolean;
  exclude_uhd: boolean;
  min_size: number | null;
  max_size: number | null;
};

const emptyFilter: SubscriptionFilterForm = {
  only_chinese: false,
  only_uc: false,
  exclude_uc: false,
  only_free: false,
  only_uhd: false,
  exclude_uhd: false,
  min_size: null,
  max_size: null,
};

type MediaSubscriptionActionsProps = { media: Media };

/** 所有番号页共用的订阅、取消与编辑操作；成功后统一刷新查询缓存。 */
export function MediaSubscriptionActions({ media }: MediaSubscriptionActionsProps) {
  const [message, messageHolder] = useFeedbackMessage();
  const queryClient = useQueryClient();
  const [visible, setVisible] = useState(false);
  const [editing, setEditing] = useState(false);
  const [mode, setMode] = useState<SubscriptionMode>("strict");
  const [filter, setFilter] = useState<SubscriptionFilterForm>(emptyFilter);
  const active = media.active_subscription ?? null;

  async function refresh() {
    await queryClient.invalidateQueries();
  }

  const save = useMutation({
    mutationFn: async () => {
      if (editing && active) {
        return apiRequest<Subscription>("/subscriptions/" + encodeURIComponent(active.id), {
          method: "PUT",
          body: JSON.stringify({ mode, filter, version: active.version }),
        });
      }
      return apiRequest<Subscription>("/subscriptions", {
        method: "POST",
        headers: { "Idempotency-Key": crypto.randomUUID() },
        body: JSON.stringify({ media_id: media.id, mode, filter }),
      });
    },
    onSuccess: async () => {
      setVisible(false);
      message.success?.(editing ? "订阅已更新" : "订阅已创建");
      await refresh();
    },
    onError: (error: Error) => {
      if (!editing && isApiError(error) && error.status === 409) {
        const localStatus = media.library_status === "present" ? "本地文件已存在" : media.library_status === "absent" ? "本地文件不存在" : "本地文件状态未知";
        message.error?.(`该番号已订阅，未重复添加；${localStatus}`);
        void queryClient.invalidateQueries();
        return;
      }
      message.error?.(error.message);
    },
  });

  const cancel = useMutation({
    mutationFn: () => apiRequest<Subscription>("/subscriptions/" + encodeURIComponent(active!.id) + "/cancel", { method: "POST" }),
    onSuccess: async () => {
      message.success?.("订阅已取消");
      await refresh();
    },
    onError: (error: Error) => message.error?.(error.message),
  });

  function openCreate() {
    setEditing(false);
    setMode("strict");
    setFilter({ ...emptyFilter });
    setVisible(true);
  }

  function openEdit() {
    const source = active?.filter ?? {};
    setEditing(true);
    setMode(active?.mode ?? "strict");
    setFilter({
      only_chinese: source.only_chinese === true,
      only_uc: source.only_uc === true,
      exclude_uc: source.exclude_uc === true,
      only_free: source.only_free === true,
      only_uhd: source.only_uhd === true,
      exclude_uhd: source.exclude_uhd === true,
      min_size: typeof source.min_size === "number" ? source.min_size : null,
      max_size: typeof source.max_size === "number" ? source.max_size : null,
    });
    setVisible(true);
  }

  function setBoolean(key: keyof SubscriptionFilterForm, value: boolean) {
    setFilter((current) => ({ ...current, [key]: value }));
  }

  const showActiveActions = media.display_status === "subscribed" || media.display_status === "downloading";

  return (
    <>
      {messageHolder}
      {showActiveActions && active ? (
        <>
          <Popconfirm
            title="确认取消订阅？"
            content={`取消后 ${media.code} 会从订阅列表移除。`}
            okText="确定取消"
            cancelText="取消"
            okType="primary"
            onOk={() => cancel.mutate()}
            disabled={cancel.isPending}
            okButtonProps={{ status: "danger", loading: cancel.isPending }}
            cancelButtonProps={{ disabled: cancel.isPending }}
          >
            <Button status="danger" disabled={cancel.isPending} loading={cancel.isPending}>取消订阅</Button>
          </Popconfirm>
          <Button disabled={cancel.isPending} onClick={openEdit}>编辑</Button>
        </>
      ) : (
        // subscribe-action 由 .code-card 内的样式渲染成描边主色，与卡片底部其它按钮同一观感。
        <Button type="primary" className="subscribe-action" onClick={openCreate}>订阅</Button>
      )}
      <AppDialog
        title={(editing ? "编辑订阅 " : "订阅 ") + media.code}
        visible={visible}
        onClose={() => setVisible(false)}
        footer={<><Button onClick={() => setVisible(false)}>取消</Button><Button type="primary" loading={save.isPending} disabled={save.isPending} onClick={() => save.mutate()}>保存</Button></>}
      >
        <div className="subscription-filter-grid">
          <label><span>仅中文</span><Switch aria-label="仅中文" checked={filter.only_chinese} onChange={(value) => setBoolean("only_chinese", value)} /></label>
          <label><span>仅无码</span><Switch aria-label="仅无码" checked={filter.only_uc} onChange={(value) => setBoolean("only_uc", value)} /></label>
          <label><span>排除无码</span><Switch aria-label="排除无码" checked={filter.exclude_uc} onChange={(value) => setBoolean("exclude_uc", value)} /></label>
          <label><span>仅免费</span><Switch aria-label="仅免费" checked={filter.only_free} onChange={(value) => setBoolean("only_free", value)} /></label>
          <label><span>仅 UHD</span><Switch aria-label="仅 UHD" checked={filter.only_uhd} onChange={(value) => setBoolean("only_uhd", value)} /></label>
          <label><span>排除 UHD</span><Switch aria-label="排除 UHD" checked={filter.exclude_uhd} onChange={(value) => setBoolean("exclude_uhd", value)} /></label>
          <label className="subscription-filter-number"><span>最小体积 MB</span><InputNumber aria-label="最小体积 MB" min={0} value={filter.min_size ?? undefined} onChange={(value) => setFilter((current) => ({ ...current, min_size: value ?? null }))} /></label>
          <label className="subscription-filter-number"><span>最大体积 MB</span><InputNumber aria-label="最大体积 MB" min={0} value={filter.max_size ?? undefined} onChange={(value) => setFilter((current) => ({ ...current, max_size: value ?? null }))} /></label>
        </div>
        <Radio.Group className="subscription-mode" value={mode} onChange={setMode}>
          <Radio value="strict">严格模式</Radio>
          <Radio value="preload">预下载模式</Radio>
        </Radio.Group>
      </AppDialog>
    </>
  );
}
