import { Button, Checkbox, DatePicker, Input, InputNumber, Select, Space } from "@arco-design/web-react";
import { useState, type ReactNode } from "react";
import dayjs from "dayjs";
import { getDateTimeShortcuts, getDisabledDateTime, isFutureDate, serializeDateTimeRange } from "../../shared/dateTimeRange";

export type DownloadFilterKind = "text" | "enum" | "number" | "bytes" | "seconds" | "time";

/** 表头筛选保留草稿，确认后提交原始字节/秒值；清除只影响当前列。 */
export function DownloadColumnFilter({ label, kind, values, options, loading, error, onRetry, onApply, onDraftChange, unitValue, onUnitChange }: {
  label: string; kind: DownloadFilterKind; values: string[]; options?: { value: string; label: ReactNode; count?: number }[]; onApply: (values: string[]) => void;
  loading?: boolean; error?: Error | null; onRetry?: () => void;
  onDraftChange?: (values: string[]) => void; unitValue?: number; onUnitChange?: (unit: number) => void;
}) {
  const [localDraft, setLocalDraft] = useState(values);
  const [localUnit, setLocalUnit] = useState(1);
  const draft = onDraftChange ? values : localDraft;
  const setDraft = onDraftChange ?? setLocalDraft;
  const unit = unitValue ?? localUnit;
  const setUnit = onUnitChange ?? setLocalUnit;
  const numeric = ["number", "bytes", "seconds"].includes(kind);
  const invalid = numeric && draft[0] && draft[1] && Number(draft[0]) > Number(draft[1]);
  return <div className="download-column-filter" role="group" aria-label={label + "筛选条件"}>
    <Space direction="vertical" style={{ width: "100%" }}>
      <strong>{label}</strong>
      {kind === "text" && <Input aria-label={label + "关键词"} placeholder="包含关键词" allowClear value={draft[0] ?? ""} onChange={(v) => setDraft(v ? [v] : [])} onPressEnter={() => onApply(draft)} />}
      {kind === "enum" && <div className="download-filter-options">
        {loading ? <span role="status">加载中…</span> : error ? <div role="alert">{error.message}<Button onClick={onRetry}>重试</Button></div>
          : options?.length ? options.map((option) => <div className="download-filter-option" key={option.value}>
            <Checkbox checked={draft.includes(option.value)} onChange={(checked) => setDraft(checked ? [...draft, option.value] : draft.filter((value) => value !== option.value))}>{option.label}</Checkbox>
            {option.count != null && <span className="download-filter-count" aria-label={String(option.label) + "任务数"}>{option.count}</span>}
          </div>) : <span role="status">暂无可选项</span>}
      </div>}
      {kind === "time" && <DatePicker.RangePicker aria-label={label + "范围"} showTime={{ format: "HH:mm:ss" }} format="YYYY-MM-DD HH:mm:ss" shortcuts={getDateTimeShortcuts()}
        value={draft.length === 2 ? [dayjs(draft[0]), dayjs(draft[1])] : undefined} disabledDate={isFutureDate} disabledTime={(current) => getDisabledDateTime()(current)}
        onChange={(_, dates) => setDraft(serializeDateTimeRange(dates) ?? [])} placeholder={["开始时间", "结束时间"]} />}
      {numeric && <>
        <InputNumber aria-label={label + "最小值"} min={0} placeholder="最小值（含）" style={{ width: "100%" }} value={draft[0] ? Number(draft[0]) / unit : undefined} onChange={(v) => setDraft([v == null ? "" : String(v * unit), draft[1] ?? ""])} />
        <InputNumber aria-label={label + "最大值"} min={0} placeholder="最大值（含）" style={{ width: "100%" }} value={draft[1] ? Number(draft[1]) / unit : undefined} onChange={(v) => setDraft([draft[0] ?? "", v == null ? "" : String(v * unit)])} />
        {kind !== "number" && <Select aria-label={label + "单位"} value={unit} onChange={setUnit} options={kind === "bytes" ? ["B", "KB", "MB", "GB", "TB"].map((unitLabel, i) => ({ label: unitLabel + (label.includes("速度") ? "/s" : ""), value: 1024 ** i })) : [{ label: "秒", value: 1 }, { label: "小时", value: 3600 }, { label: "天", value: 86400 }]} />}
      </>}
      {invalid && <span role="alert">最小值不能大于最大值</span>}
      <Space><Button type="primary" disabled={!!invalid || !!loading || !!error} onClick={() => onApply(kind === "enum" || draft.some((v) => v !== "") ? draft : [])}>确定</Button><Button onClick={() => { setDraft([]); onApply([]); }}>清除</Button></Space>
    </Space>
  </div>;
}
