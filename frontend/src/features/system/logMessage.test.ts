import { describe, expect, it } from "vitest";
import { formatLogMessage, shouldDisplayLog } from "./logMessage";

describe("日志单行文案", () => {
  it("将列表查询数量拼接到同一行", () => {
    expect(formatLogMessage("订阅列表查询完成", { count: 15 })).toBe("订阅列表查询完成，获取到 15 条数据");
  });

  it("将榜单类型和数量转换为可读文案", () => {
    expect(formatLogMessage("榜单查询完成", { rank_type: "monthly", count: 15 })).toBe("榜单查询完成，类型：monthly，获取到 15 条数据");
  });

  it("将错误原因和未知属性保持在同一行", () => {
    expect(formatLogMessage("定时任务执行失败", { task: "榜单同步", error: "连接超时", retry: 1 })).toBe("定时任务执行失败，任务：榜单同步，原因：连接超时，重试次数：1");
  });

  it("隐藏没有删除数据的清理日志，并显示删除数量", () => {
    expect(shouldDisplayLog("定时清理日志完成", { deleted: 0 })).toBe(false);
    expect(formatLogMessage("定时清理日志完成", { deleted: 12, retention_days: 30 })).toBe("清理系统日志（12条）");
  });

  it("将管理员登录成功和失败显示为指定单行模板", () => {
    expect(formatLogMessage("管理员登录成功", { username: "admin" })).toBe("管理员登录成功：admin");
    expect(formatLogMessage("管理员登录失败", { error: "用户名或密码错误", ip: "127.0.0.1" })).toBe("管理员登录失败：用户名或密码错误，IP：127.0.0.1");
  });
});
