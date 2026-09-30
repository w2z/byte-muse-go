import dayjs from "dayjs";
import { afterEach, describe, expect, it, vi } from "vitest";
import { clampDateTimeRange, getDisabledDateTime, getDateTimeShortcuts, isFutureDate, serializeDateTimeRange } from "./dateTimeRange";

const NOW = dayjs("2026-09-25T15:30:45");
afterEach(() => vi.useRealTimers());
describe('disabled time guard', () => {
  it('handles a missing date', () => {
    expect(() => getDisabledDateTime(NOW)(undefined)).not.toThrow();
    expect(getDisabledDateTime(NOW)(undefined)).toEqual({});
    expect(getDisabledDateTime(NOW)({} as never)).toEqual({});
  });
});

describe("下载和日志共用的时间范围", () => {
  it("默认选择今天并截止当前时刻", () => {
    const [start, end] = getDateTimeShortcuts(NOW)[0].value();
    expect(start.format("YYYY-MM-DD HH:mm:ss")).toBe("2026-09-25 00:00:00");
    expect(end.toISOString()).toBe(NOW.toISOString());
  });

  it("提供底部快捷时间范围", () => {
    const shortcuts = getDateTimeShortcuts(NOW);
    expect(shortcuts.map((shortcut) => shortcut.text)).toEqual(["今天", "昨天", "本周", "本月"]);
    expect(shortcuts[0].value()[1].toISOString()).toBe(NOW.toISOString());
    expect(shortcuts[1].value()[0].format("YYYY-MM-DD")).toBe("2026-09-24");
    expect(shortcuts[1].value()[1].format("YYYY-MM-DD HH:mm:ss")).toBe("2026-09-24 23:59:59");
    expect(shortcuts[2].value()[0].format("YYYY-MM-DD HH:mm:ss")).toBe("2026-09-21 00:00:00");
    expect(shortcuts[3].value()[0].format("YYYY-MM-DD HH:mm:ss")).toBe("2026-09-01 00:00:00");
    for (const shortcut of shortcuts) expect(shortcut.value()[1].isAfter(NOW)).toBe(false);
  });

  it("禁止选择未来日期和当前时刻之后的时间", () => {
    expect(isFutureDate(dayjs("2026-09-26"), NOW)).toBe(true);
    expect(isFutureDate(dayjs("2026-09-25"), NOW)).toBe(false);
    const disabled = getDisabledDateTime(NOW)(NOW);
    expect(disabled.disabledHours?.()).toContain(23);
    expect(disabled.disabledMinutes?.()).toContain(59);
    expect(disabled.disabledSeconds?.()).toContain(59);
    expect(disabled.disabledHours?.()).not.toContain(15);
    expect(disabled.disabledMinutes?.()).not.toContain(30);
    expect(disabled.disabledSeconds?.()).not.toContain(45);
    expect(getDisabledDateTime(NOW)(NOW.subtract(1, "day"))).toEqual({});
    expect(getDisabledDateTime(NOW)(NOW.subtract(1, "hour")).disabledMinutes?.()).toEqual([]);
  });

  it("将手动输入的未来时间钳制到当前时刻", () => {
    const [start, end] = clampDateTimeRange([NOW.subtract(1, "hour"), NOW.add(1, "day")], NOW);
    expect(start.toISOString()).toBe(NOW.subtract(1, "hour").toISOString());
    expect(end.toISOString()).toBe(NOW.toISOString());
  });
  it("完整保留时分秒，清空或不完整范围不生成筛选", () => {
    const start = NOW.subtract(2, "hour");
    const end = NOW.subtract(1, "hour");
    expect(serializeDateTimeRange([start, end], NOW)).toEqual([start.toISOString(), end.toISOString()]);
    expect(serializeDateTimeRange([], NOW)).toBeUndefined();
    expect(serializeDateTimeRange([start], NOW)).toBeUndefined();
    expect(serializeDateTimeRange([dayjs("invalid"), end], NOW)).toBeUndefined();
  });
  it("本周从周一开始，周日不跨入下一周", () => {
    for (const date of ["2026-09-21", "2026-09-27"]) {
      expect(getDateTimeShortcuts(dayjs(date))[2].value()[0].format("YYYY-MM-DD")).toBe("2026-09-21");
    }
  });
  it("快捷项按点击时刻计算，跨天不保留创建时刻", () => {
    vi.useFakeTimers();
    vi.setSystemTime(dayjs("2026-09-30T23:59:59").toDate());
    const shortcuts = getDateTimeShortcuts();
    vi.setSystemTime(dayjs("2026-10-01T00:00:02").toDate());
    expect(shortcuts[0].value()[0].format("YYYY-MM-DD")).toBe("2026-10-01");
    expect(shortcuts[0].value()[1].format("HH:mm:ss")).toBe("00:00:02");
  });
});
