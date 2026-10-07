import { describe, expect, it } from "vitest";
import { formatLogMessage, shouldDisplayLog } from "./logMessage";

describe("日志单行文案", () => {
  it("将列表查询数量拼接到同一行", () => {
    expect(formatLogMessage("订阅列表查询完成", { count: 15 })).toBe("订阅列表查询完成，获取到 15 条数据");
  });

  it("将榜单类型和数量转换为可读文案", () => {
    expect(formatLogMessage("榜单查询完成", { rank_type: "monthly", count: 15 })).toBe("榜单查询完成，类型：monthly，获取到 15 条数据");
  });

  it("在查询数量后追加本次命中的番号列表", () => {
    expect(formatLogMessage("订阅列表查询完成", { count: 2, codes: ["ABC-001", "DEF-002"] })).toBe("订阅列表查询完成，获取到 2 条数据，（ABC-001、DEF-002）");
    expect(formatLogMessage("下载任务查询完成", { count: 1, codes: ["JUR-868"] })).toBe("下载任务查询完成，获取到 1 条数据，（JUR-868）");
  });

  it("没有命中番号时不输出空的番号括号", () => {
    expect(formatLogMessage("下载任务查询完成", { count: 0, codes: [] })).toBe("下载任务查询完成，获取到 0 条数据");
  });

  it("影片详情显示番号而不是内部媒体编号", () => {
    expect(formatLogMessage("影片详情查询完成", { code: "JUR-868" })).toBe("影片详情查询完成，番号：JUR-868");
  });

  it("STRM 单片刷新日志显示番号，并保留无法识别番号时的文件名", () => {
    expect(formatLogMessage("STRM 视频信息刷新请求完成", { code: "SSIS-001", filename: "SSIS-001-C.strm", task_id: "emby-test" })).toBe(
      "STRM 视频信息刷新请求完成，番号：SSIS-001，文件名：SSIS-001-C.strm，任务 ID：emby-test",
    );
    expect(formatLogMessage("STRM 视频信息刷新失败", { filename: "自制影片.strm", error: "连接超时" })).toBe(
      "STRM 视频信息刷新失败，文件名：自制影片.strm，原因：连接超时",
    );
  });

  it("订阅日志用番号标识对象，不出现订阅编号", () => {
    expect(formatLogMessage("订阅已取消", { code: "JUR-868" })).toBe("订阅已取消，番号：JUR-868");
    expect(formatLogMessage("订阅已编辑", { code: "JUR-868" })).toBe("订阅已编辑，番号：JUR-868");
    expect(formatLogMessage("订阅已保存", { code: "JUR-868", created: true })).toBe("订阅已保存，番号：JUR-868，新建：是");
    expect(formatLogMessage("取消订阅失败", { error: "订阅不存在" })).toBe("取消订阅失败，原因：订阅不存在");
    expect(formatLogMessage("订阅已取消", { subscription_id: "TZJE46TNYLY2JDY76NYNC3BQVE" })).toBe("订阅已取消");
    expect(formatLogMessage("订阅已保存，新建：是", { media_id: "cbab7927e57d8b599a4df340a1" })).toBe("订阅已保存，新建：是");
  });

  it("消息通知日志显示渠道、事件与是否带封面", () => {
    expect(formatLogMessage("消息通知已发送", { channel: "tg", event: "subscribe", with_cover: true })).toBe(
      "消息通知已发送，渠道：tg，事件：subscribe，带封面：是",
    );
  });

  it("消息通知跳过日志说明跳过原因", () => {
    expect(formatLogMessage("消息通知已跳过", { channel: "wx", event: "subscribe", reason: "事件开关未启用" })).toBe(
      "消息通知已跳过，渠道：wx，事件：subscribe，原因：事件开关未启用",
    );
    expect(formatLogMessage("消息通知已跳过", { channel: "wx", event: "subscribe", reason: "渠道未配置" })).toBe(
      "消息通知已跳过，渠道：wx，事件：subscribe，原因：渠道未配置",
    );
  });

  it("Telegram 图文消息日志显示消息类型与防剧透状态", () => {
    expect(formatLogMessage("Telegram 图文消息已发送", { kind: "番号卡片", spoiler: true })).toBe(
      "Telegram 图文消息已发送，消息类型：番号卡片，防剧透：是",
    );
    expect(formatLogMessage("Telegram 图文消息已发送", { kind: "推送通知", spoiler: false })).toBe(
      "Telegram 图文消息已发送，消息类型：推送通知，防剧透：否",
    );
  });

  it("将错误原因和未知属性保持在同一行", () => {
    expect(formatLogMessage("定时任务执行失败", { task: "榜单同步", error: "连接超时", retry: 1 })).toBe("定时任务执行失败，任务：榜单同步，原因：连接超时，重试次数：1");
  });

  it("隐藏没有删除数据的清理日志，并显示删除数量", () => {
    expect(shouldDisplayLog("定时清理日志完成", { deleted: 0 })).toBe(false);
    expect(formatLogMessage("定时清理日志完成", { deleted: 12, retention_days: 30 })).toBe("清理系统日志（12条）");
  });

  it("将清理任务完成日志显示删除数量，并隐藏零删除记录", () => {
    expect(shouldDisplayLog("定时任务执行完成", { task: "清理系统日志", deleted: 0 })).toBe(false);
    expect(formatLogMessage("定时任务执行完成", { task: "清理系统日志", deleted: 12 })).toBe("清理系统日志（12条）");
  });

  it("将管理员登录成功和失败显示为指定单行模板", () => {
    expect(formatLogMessage("管理员登录成功", { username: "admin" })).toBe("管理员登录成功：admin");
    expect(formatLogMessage("管理员登录失败", { error: "用户名或密码错误", ip: "127.0.0.1" })).toBe("管理员登录失败：用户名或密码错误，IP：127.0.0.1");
  });
});
