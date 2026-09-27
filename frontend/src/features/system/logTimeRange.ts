import dayjs, { type Dayjs } from "dayjs";

export type LogTimeRange = [Dayjs, Dayjs];

/** 将手动输入或快捷项产生的范围限制在当前时刻之前，并保持起止顺序。 */
export function clampLogTimeRange(range: LogTimeRange, now: Dayjs = dayjs()): LogTimeRange {
  const start = range[0].isAfter(now) ? now.startOf("day") : range[0];
  const end = range[1].isAfter(now) ? now : range[1];
  return start.isAfter(end) ? [end, end] : [start, end];
}

/** 返回以当前本地时间为基准的“今天”范围，结束时间精确到当前时刻。 */
export function getTodayLogTimeRange(now: Dayjs = dayjs()): LogTimeRange {
  return [now.startOf("day"), now];
}

/** 日志时间快捷范围显示在日期面板底部，所有范围都不会超过当前时刻。 */
export function getLogTimeShortcuts(now: Dayjs = dayjs()) {
  return [
    { text: "1小时内", value: () => [now.subtract(1, "hour"), now] },
    { text: "今天", value: () => getTodayLogTimeRange(now) },
    { text: "昨天", value: () => [now.subtract(1, "day").startOf("day"), now.subtract(1, "day").endOf("day")] },
    { text: "一周内", value: () => [now.subtract(7, "day").startOf("day"), now] },
    { text: "一个月内", value: () => [now.subtract(1, "month").startOf("day"), now] },
  ];
}

/** 只允许选择当前时刻及之前的日期。 */
export function isFutureLogDate(current: Dayjs, now: Dayjs = dayjs()): boolean {
  return current.isAfter(now, "day");
}

/** 对今天禁用当前时刻之后的小时、分钟和秒，防止手动输入未来时间。 */
export function getDisabledLogTime(now: Dayjs = dayjs()) {
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
