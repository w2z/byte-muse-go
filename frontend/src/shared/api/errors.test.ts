import { describe, expect, it } from "vitest";
import { toApiError } from "./errors";

describe("API 错误转换", () => {
  it("把契约错误体转换为带消息的错误", async () => {
    const response = new Response(
      JSON.stringify({ code: "media_not_found", message: "番号不存在" }),
      { status: 404, headers: { "Content-Type": "application/json" } },
    );

    await expect(toApiError(response)).resolves.toMatchObject({
      name: "ApiError",
      message: "番号不存在",
      status: 404,
    });
  });

  it("在响应体不可解析时使用 HTTP 状态说明", async () => {
    const response = new Response("upstream unavailable", { status: 503 });

    await expect(toApiError(response)).resolves.toMatchObject({
      message: "服务暂时不可用（503）",
      status: 503,
    });
  });

  it("网络失败转换为可展示错误", () => {
    const error = toApiError(new TypeError("Failed to fetch"));
    expect(error).toMatchObject({
      name: "ApiError",
      message: "网络连接失败，请稍后重试",
    });
  });
});
