import { Button } from "@arco-design/web-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest } from "../api/client";
import type { Actor } from "../api/types";
import { useFeedbackMessage } from "./FeedbackMessage";
import { AppDialog } from "./AppDialog";

type ActorSubscriptionActionsProps = { actor: Actor };

/** 演员订阅操作；订阅和编辑共用限制日期，取消只清空日期。 */
export function ActorSubscriptionActions({ actor }: ActorSubscriptionActionsProps) {
  const queryClient = useQueryClient();
  const [message, messageHolder] = useFeedbackMessage();
  const [visible, setVisible] = useState(false);
  const [limitDate, setLimitDate] = useState(actor.limit_date ?? "");
  const save = useMutation({
    mutationFn: () => apiRequest<Actor>("/actors/" + encodeURIComponent(actor.name) + "/subscription", { method: "PUT", body: JSON.stringify({ limit_date: limitDate }) }),
    onSuccess: async () => { setVisible(false); message.success?.("演员订阅已保存"); await queryClient.invalidateQueries({ queryKey: ["actors"] }); },
    onError: (error: Error) => message.error?.(error.message),
  });
  const cancel = useMutation({
    mutationFn: () => apiRequest<Actor>("/actors/" + encodeURIComponent(actor.name) + "/subscription", { method: "DELETE" }),
    onSuccess: async () => { message.success?.("演员订阅已取消"); await queryClient.invalidateQueries({ queryKey: ["actors"] }); },
    onError: (error: Error) => message.error?.(error.message),
  });
  function openEditor() { setLimitDate(actor.limit_date ?? ""); setVisible(true); }
  return (
    <>
      {messageHolder}
      {actor.limit_date ? (
        <div className="actor-card-actions">
          <Button status="danger" loading={cancel.isPending} disabled={cancel.isPending} onClick={() => cancel.mutate()}>取消订阅</Button>
          <Button disabled={cancel.isPending} onClick={openEditor}>编辑</Button>
        </div>
      ) : <Button type="primary" onClick={openEditor}>订阅</Button>}
      <AppDialog title={(actor.limit_date ? "编辑演员 " : "订阅演员 ") + actor.name} visible={visible} onClose={() => setVisible(false)} footer={<><Button onClick={() => setVisible(false)}>取消</Button><Button type="primary" loading={save.isPending} disabled={save.isPending} onClick={() => save.mutate()}>保存</Button></>}>
        <label className="actor-subscription-date"><span>限制日期</span><input className="actor-subscription-date-input" aria-label="限制日期" type="date" value={limitDate} onInput={(event) => setLimitDate(event.currentTarget.value)} onChange={(event) => setLimitDate(event.currentTarget.value)} /></label>
      </AppDialog>
    </>
  );
}
