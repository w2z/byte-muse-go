import dayjs from "dayjs";
import { describe, expect, it } from "vitest";
import { clampLogTimeRange, getDisabledLogTime, getLogTimeShortcuts, getTodayLogTimeRange, isFutureLogDate } from "./logTimeRange";

const NOW = dayjs("2026-09-25T15:30:45+08:00");
describe('disabled time guard', () => {
  it('handles a missing date', () => {
    expect(() => getDisabledLogTime(NOW)(undefined as never)).not.toThrow();
    expect(getDisabledLogTime(NOW)(undefined as never)).toEqual({});
    expect(getDisabledLogTime(NOW)({} as never)).toEqual({});
  });
});

describe("日志时间范围", () => {
  it("默认选择今天并截止当前时刻", () => {
    const [start, end] = getTodayLogTimeRange(NOW);
    expect(start.format("YYYY-MM-DD HH:mm:ss")).toBe("2026-09-25 00:00:00");
    expect(end.toISOString()).toBe(NOW.toISOString());
  });

  it("提供底部快捷时间范围", () => {
    const shortcuts = getLogTimeShortcuts(NOW);
    expect(shortcuts.map((shortcut) => shortcut.text)).toEqual(["1小时内", "今天", "昨天", "一周内", "一个月内"]);
    expect(shortcuts[0].value()[1].toISOString()).toBe(NOW.toISOString());
    expect(shortcuts[2].value()[0].format("YYYY-MM-DD")).toBe("2026-09-24");
  });

  it("禁止选择未来日期和当前时刻之后的时间", () => {
    expect(isFutureLogDate(dayjs("2026-09-26"), NOW)).toBe(true);
    expect(isFutureLogDate(dayjs("2026-09-25"), NOW)).toBe(false);
    const disabled = getDisabledLogTime(NOW)(NOW);
    expect(disabled.disabledHours?.()).toContain(23);
    expect(disabled.disabledMinutes?.()).toContain(59);
    expect(disabled.disabledSeconds?.()).toContain(59);
  });

  it("将手动输入的未来时间钳制到当前时刻", () => {
    const [start, end] = clampLogTimeRange([NOW.subtract(1, "hour"), NOW.add(1, "day")], NOW);
    expect(start.toISOString()).toBe(NOW.subtract(1, "hour").toISOString());
    expect(end.toISOString()).toBe(NOW.toISOString());
  });
});
