import { afterEach, describe, expect, it, vi } from "vitest";
import { newIdempotencyKey } from "./client";

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

afterEach(() => vi.unstubAllGlobals());

describe("写操作幂等键", () => {
  it("安全上下文优先使用 crypto.randomUUID", () => {
    const randomUUID = vi.fn(() => "11111111-2222-4333-8444-555555555555");
    vi.stubGlobal("crypto", { randomUUID });
    expect(newIdempotencyKey()).toBe("11111111-2222-4333-8444-555555555555");
    expect(randomUUID).toHaveBeenCalledTimes(1);
  });

  // 局域网 HTTP 不是安全上下文，浏览器不会暴露 randomUUID；旧实现直接调用会让订阅点击抛错。
  it("缺少 randomUUID 时回退到 getRandomValues，仍生成唯一合法 UUID", () => {
    let call = 0;
    vi.stubGlobal("crypto", {
      getRandomValues: (bytes: Uint8Array) => {
        for (let index = 0; index < bytes.length; index += 1) bytes[index] = (call * 31 + index * 7) % 256;
        call += 1;
        return bytes;
      },
    });
    expect(typeof crypto.randomUUID).toBe("undefined");
    const keys = new Set(Array.from({ length: 20 }, () => newIdempotencyKey()));
    expect(keys.size).toBe(20);
    for (const key of keys) expect(key).toMatch(UUID_V4);
  });

  it("完全没有 Web Crypto 时用随机数兜底，键长仍满足后端要求", () => {
    vi.stubGlobal("crypto", {});
    expect(typeof crypto.randomUUID).toBe("undefined");
    const keys = new Set(Array.from({ length: 20 }, () => newIdempotencyKey()));
    expect(keys.size).toBe(20);
    for (const key of keys) {
      expect(key).toMatch(UUID_V4);
      expect(key.length).toBeGreaterThanOrEqual(8);
    }
  });
});
