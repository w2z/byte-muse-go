import { Button, InputNumber, Popconfirm, Radio, Switch } from "@arco-design/web-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { apiRequest, isApiError } from "../api/client";
import { createMediaSubscription } from "../api/catalogSubscriptions";
import type { Media, Subscription, SubscriptionMode, SystemSettings } from "../api/types";
import { useFeedbackMessage } from "./FeedbackMessage";
import { AppDialog } from "./AppDialog";

/** 弹窗展示已知字段，同时保留未展示规则，避免保存时丢失配置。 */
type SubscriptionFilterForm = Record<string, unknown> & {
  only_chinese: boolean;
  only_uc: boolean;
  exclude_uc: boolean;
  only_free: boolean;
  only_uhd: boolean;
  exclude_uhd: boolean;
  exclude_vr: boolean;
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
  exclude_vr: false,
  min_size: null,
  max_size: null,
};

/** 设置体积可能是字符串，空值表示不限；拒绝无效值，避免静默改变过滤条件。 */
function filterSize(value: unknown): number | null {
  if (value === null || value === undefined || value === "") return null;
  const number = typeof value === "number" || typeof value === "string" ? Number(value) : NaN;
  if (!Number.isFinite(number) || number < 0) throw new Error("过滤规则中的体积无效，请检查设置");
  return number;
}

/** 将全局或已保存的规则转成表单值，保留额外字段和明确的零值。 */
function filterForm(source: Record<string, unknown>): SubscriptionFilterForm {
  return {
    ...source,
    only_chinese: source.only_chinese === true || source.only_chinese === "true",
    only_uc: source.only_uc === true || source.only_uc === "true",
    exclude_uc: source.exclude_uc === true || source.exclude_uc === "true",
    only_free: source.only_free === true || source.only_free === "true",
    only_uhd: source.only_uhd === true || source.only_uhd === "true",
    exclude_uhd: source.exclude_uhd === true || source.exclude_uhd === "true",
    exclude_vr: source.exclude_vr === true || source.exclude_vr === "true",
    min_size: filterSize(source.min_size),
    max_size: filterSize(source.max_size),
  };
}

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
  const targetKey = JSON.stringify([media.id, active?.id, active?.version]);
  const currentTarget = useRef(targetKey);
  const mounted = useRef(true);
  currentTarget.current = targetKey;
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const [draftTarget, setDraftTarget] = useState(targetKey);

  async function refresh() {
    await queryClient.invalidateQueries();
  }

  const save = useMutation({
    mutationFn: async (direct?: { target: string; filter: SubscriptionFilterForm }) => {
      if (direct) {
        if (direct.target !== currentTarget.current) throw new Error("订阅状态已变化，请重试");
        return createMediaSubscription(media.id, "strict", direct.filter);
      }
      if (draftTarget !== currentTarget.current || (editing && !active)) throw new Error("订阅状态已变化，请重新打开弹窗");
      if (editing && active) {
        return apiRequest<Subscription>("/subscriptions/" + encodeURIComponent(active.id), {
          method: "PUT",
          body: JSON.stringify({ mode, filter, version: active.version }),
        });
      }
      return createMediaSubscription(media.id, mode, filter);
    },
    onSuccess: async (_result, direct) => {
      setVisible(false);
      message.success?.(!direct && editing ? "订阅已更新" : "订阅已创建");
      await refresh();
    },
    onError: (error: Error, direct) => {
      if ((direct || !editing) && isApiError(error) && error.status === 409) {
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

  // 只在打开时读取默认值，后续缓存刷新不覆盖用户在弹窗内修改的草稿。
  const open = useMutation({
    mutationFn: async (edit: boolean) => {
      let source = edit ? active?.filter ?? {} : {};
      let skipConfirm = false;
      if (!edit || Object.keys(source).length === 0) {
        const settings = await queryClient.fetchQuery({
          queryKey: ["system-settings"],
          queryFn: () => apiRequest<SystemSettings>("/system/settings"),
          staleTime: 0,
          retry: false,
        });
        skipConfirm = !edit && settings.values.SUBSCRIPTION_SKIP_CONFIRM === "true";
        const raw = settings.values.DEFAULT_FILTER;
        let parsed: unknown;
        try { parsed = raw ? JSON.parse(raw) : {}; }
        catch { throw new Error("默认过滤规则格式无效，请检查设置"); }
        if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error("默认过滤规则格式无效，请检查设置");
        source = parsed as Record<string, unknown>;
      }
      return { target: targetKey, edit, skipConfirm, mode: edit ? active?.mode ?? "strict" : "strict" as SubscriptionMode, filter: filterForm(source) };
    },
    onSuccess: (draft) => {
      if (!mounted.current || draft.target !== currentTarget.current) return;
      if (draft.skipConfirm) { save.mutate({ target: draft.target, filter: draft.filter }); return; }
      setDraftTarget(draft.target);
      setEditing(draft.edit);
      setMode(draft.mode);
      setFilter(draft.filter);
      setVisible(true);
    },
    onError: (error: Error) => message.error(error.message),
  });

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
            disabled={cancel.isPending || open.isPending}
            okButtonProps={{ status: "danger", loading: cancel.isPending }}
            cancelButtonProps={{ disabled: cancel.isPending }}
          >
            <Button status="danger" disabled={cancel.isPending || open.isPending} loading={cancel.isPending}>取消订阅</Button>
          </Popconfirm>
          <Button disabled={cancel.isPending || open.isPending} loading={open.isPending} onClick={() => open.mutate(true)}>编辑</Button>
        </>
      ) : (
        // subscribe-action 由 .code-card 内的样式渲染成描边主色，与卡片底部其它按钮同一观感。
        <Button type="primary" className="subscribe-action" disabled={open.isPending || save.isPending} loading={open.isPending || save.isPending} onClick={() => open.mutate(false)}>订阅</Button>
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
          <label><span>排除 VR</span><Switch aria-label="排除 VR" checked={filter.exclude_vr} onChange={(value) => setBoolean("exclude_vr", value)} /></label>
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
