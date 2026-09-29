import { Button, Popconfirm } from "@arco-design/web-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import dayjs from "dayjs";
import { apiRequest } from "../../shared/api/client";
import type { CatalogTag } from "../../shared/api/types";
import { AppDialog } from "../../shared/ui/AppDialog";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";

/** 标签订阅和编辑共用起始日期；取消只停止追新，不撤销影片订阅。 */
export function TagSubscriptionActions({ tag, onChanged }: { tag: CatalogTag; onChanged: () => void }) {
 const client=useQueryClient();
 const [message,holder]=useFeedbackMessage();
 const [visible,setVisible]=useState(false);
 const [date,setDate]=useState("");
 const [error,setError]=useState("");
 const mutation=useMutation({
  mutationFn: (action:"save"|"cancel")=>apiRequest<CatalogTag>("/tags/"+encodeURIComponent(tag.name)+"/subscription",{method:action==="save"?"PUT":"DELETE",...(action==="save"?{body:JSON.stringify({limit_date:date})}:{})}),
  onSuccess: async(_,action)=>{setVisible(false);message.success?.(action==="save"?"标签订阅已保存":"标签订阅已取消");onChanged();await Promise.all([client.invalidateQueries({queryKey:["tags"]}),client.invalidateQueries({queryKey:["subscriptions"]}),client.invalidateQueries({queryKey:["catalog-search"]})]);},
  onError: async(e:Error)=>{setError(e.message);message.error?.(e.message);await client.invalidateQueries({queryKey:["tags"]});},
 });
 function open(){setDate(tag.limit_date??dayjs().format("YYYY-MM-DD"));setError("");setVisible(true);}
 function save(){if(!/^\d{4}-\d{2}-\d{2}$/.test(date)){setError("请选择限制日期");return;}mutation.mutate("save");}
 return <>{holder}<div className="info-card-action-buttons">
  {tag.limit_date?<><Popconfirm title={"确认取消订阅标签："+tag.name} content="停止后续追新，保留已有影片订阅和下载记录。" onOk={()=>mutation.mutateAsync("cancel").then(()=>undefined)} disabled={mutation.isPending}>
    <Button status="danger" disabled={mutation.isPending} loading={mutation.isPending}>取消订阅</Button>
   </Popconfirm><Button disabled={mutation.isPending} onClick={open}>编辑</Button></>:<Button type="primary" disabled={mutation.isPending} onClick={open}>订阅</Button>}
 </div>
 <AppDialog title={(tag.limit_date?"编辑标签 ":"订阅标签 ")+tag.name} visible={visible} onClose={()=>{if(!mutation.isPending)setVisible(false);}}
 footer={<><Button disabled={mutation.isPending} onClick={()=>setVisible(false)}>取消</Button><Button type="primary" loading={mutation.isPending} disabled={mutation.isPending} onClick={save}>确认</Button></>}>
 <p>自动订阅该标签下起始日期当天及之后发行的影片，后续按标签追新任务继续处理。</p>
 <label className="actor-subscription-date"><span>限制日期</span><input className="actor-subscription-date-input" aria-label="限制日期" type="date" value={date} onInput={e=>setDate(e.currentTarget.value)} onChange={e=>setDate(e.currentTarget.value)} /></label>
 {error&&<p role="alert">{error}</p>}
 </AppDialog></>;
}

