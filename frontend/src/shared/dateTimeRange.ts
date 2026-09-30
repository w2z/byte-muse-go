import dayjs, { type Dayjs } from "dayjs";

export type DateTimeRange = [Dayjs, Dayjs];

/** 将日期时间范围限制在当前时刻，避免手动输入、快捷项或浏览器时间变化产生未来值。 */
export function clampDateTimeRange(range: DateTimeRange, now: Dayjs = dayjs()): DateTimeRange {
  const start = range[0].isAfter(now) ? now.startOf("day") : range[0];
  const end = range[1].isAfter(now) ? now : range[1];
  return start.isAfter(end) ? [end, end] : [start, end];
}

/** 提供统一的日历快捷范围：今天、昨天、本周、本月，所有结束时间不超过当前时刻。 */
export function getDateTimeShortcuts(now?: Dayjs) {
  // 在使用快捷项时读取时钟，避免面板打开后跨分钟、跨天仍使用旧时间；本周固定从周一开始。
  const range = (period: "day" | "yesterday" | "week" | "month"): DateTimeRange => {
    const end = (now ?? dayjs()).startOf("second");
    if (period === "yesterday") return [end.subtract(1, "day").startOf("day"), end.subtract(1, "day").endOf("day")];
    const start = period === "week" ? end.subtract((end.day() + 6) % 7, "day").startOf("day") : end.startOf(period);
    return [start, end];
  };
  return [
    { text: "今天", value: () => range("day") },
    { text: "昨天", value: () => range("yesterday") },
    { text: "本周", value: () => range("week") },
    { text: "本月", value: () => range("month") },
  ];
}

/** 日期面板禁用当前日期之后的日期。 */
export function isFutureDate(current: Dayjs, now: Dayjs = dayjs()): boolean {
  return current.isAfter(now, "day");
}

/** 时间面板在今天禁用当前时刻之后的时、分、秒。 */
export function getDisabledDateTime(now: Dayjs = dayjs()) {
  const disabledHours = Array.from({ length: 24 }, (_, hour) => hour).filter((hour) => hour > now.hour());
  const disabledMinutes = Array.from({ length: 60 }, (_, minute) => minute).filter((minute) => minute > now.minute());
  const disabledSeconds = Array.from({ length: 60 }, (_, second) => second).filter((second) => second > now.second());
  return (current?: Dayjs) => {
    if (!current || !dayjs.isDayjs(current) || !current.isSame(now, "day")) return {};
    return {
      disabledHours: () => disabledHours,
      disabledMinutes: () => current.hour() === now.hour() ? disabledMinutes : [],
      disabledSeconds: () => current.hour() === now.hour() && current.minute() === now.minute() ? disabledSeconds : [],
    };
  };
}

/** 将完整范围限制在当前时刻并序列化为 ISO 时间；清空、不完整或无效输入不生成筛选。 */
export function serializeDateTimeRange(values: Dayjs[] | null | undefined, now: Dayjs = dayjs()): [string, string] | undefined {
  if (values?.length !== 2 || !values[0]?.isValid() || !values[1]?.isValid()) return undefined;
  const [start, end] = clampDateTimeRange([values[0], values[1]], now);
  return [start.toISOString(), end.toISOString()];
}
