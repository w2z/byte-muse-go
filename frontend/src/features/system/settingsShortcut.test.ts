import { describe, expect, it } from "vitest";
import { isSettingsSaveShortcut } from "./SettingsPage";

describe("设置页保存快捷键", () => {
  it("识别 Ctrl+S 且忽略其它组合键", () => {
    expect(isSettingsSaveShortcut({ key: "s", ctrlKey: true } as KeyboardEvent)).toBe(true);
    expect(isSettingsSaveShortcut({ key: "S", ctrlKey: true } as KeyboardEvent)).toBe(true);
    expect(isSettingsSaveShortcut({ key: "s", ctrlKey: false } as KeyboardEvent)).toBe(false);
    expect(isSettingsSaveShortcut({ key: "p", ctrlKey: true } as KeyboardEvent)).toBe(false);
  });
});
