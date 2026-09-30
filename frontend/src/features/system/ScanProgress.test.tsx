// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { Pan115LibraryScanAction } from "./Pan115ScanPathsField";
import { StrmGenerateAction } from "./StrmPathsField";
import { apiRequest } from "../../shared/api/client";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it.each(["scan", "strm"])("%s 在按钮下更新动态总数与百分比，重试重置旧进度", async (kind) => {
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  vi.stubGlobal("fetch", vi.fn(async () => new Response(new ReadableStream<Uint8Array>({ start(c) { controller = c; } }), { headers: { "Content-Type": "application/x-ndjson" } })));
  const client = new QueryClient();
  render(<QueryClientProvider client={client}>{kind === "scan" ? <Pan115LibraryScanAction value={[{id: "1", path: "/movies"}]} /> : <StrmGenerateAction value={[{kind: "115", id: "1", path: "/movies", local_path: "/movies", formats: ["mp4"], min_size_mb: 0, exclude: []}]} />}</QueryClientProvider>);
  const button = screen.getByRole("button", { name: kind === "scan" ? "扫描入库" : "生成 strm" });
  fireEvent.click(button);
  await screen.findByText("已处理 / 总数：0 / 0");
  const progress = (processed: number, total: number, percent: number) => act(async () => {
    controller.enqueue(new TextEncoder().encode(JSON.stringify({ type: "progress", progress: { phase: "discovering", processed, total, percent, current: "/影片" } }) + "\n"));
  });
  await progress(2, 4, 50);
  expect(await screen.findByText("已处理 / 总数：2 / 4")).toBeInTheDocument();
  expect(screen.getByText("百分比：50%")).toBeInTheDocument();
  expect(button).toBeDisabled();
  await progress(2, 8, 25);
  expect(await screen.findByText("已处理 / 总数：2 / 8")).toBeInTheDocument();
  expect(screen.getByText("百分比：25%")).toBeInTheDocument();
  await act(async () => controller.enqueue(new TextEncoder().encode('{"type":"error","error":{"message":"测试读取失败"}}\n')));
  await screen.findByText(/失败：测试读取失败/);
  expect(screen.getByText("处理失败")).toBeInTheDocument();
  fireEvent.click(button);
  await screen.findByText("已处理 / 总数：0 / 0");
  await progress(0, 0, 100);
  const result = kind === "scan" ? { directories: [], files: 0, matched: 0, created: 0, skipped: 0 } : { mappings: [], files: 0, created: 0, failed: 0, emby: { attempted: false, refreshed: false, message: "" } };
  await act(async () => controller.enqueue(new TextEncoder().encode(JSON.stringify({ type: "result", result }) + "\n")));
  expect(await screen.findByText("处理完成")).toBeInTheDocument();
  expect(button).toBeEnabled();
});

it("跨 UTF-8 分块解析进度，缺少最终结果时拒绝假成功", async () => {
  const bytes = new TextEncoder().encode('{"type":"progress","progress":{"phase":"discovering","processed":1,"total":2,"percent":50,"current":"/影片"}}\n');
  vi.stubGlobal("fetch", vi.fn(async () => new Response(new ReadableStream({ start(c) { for (const byte of bytes) c.enqueue(new Uint8Array([byte])); c.close(); } }), {headers: {"Content-Type":"application/x-ndjson"}})));
  const updates: string[] = [];
  await expect(apiRequest("/strm/scan", {method: "POST"}, p => updates.push(p.current))).rejects.toThrow("未收到处理结果");
  expect(updates).toEqual(["/影片"]);
});
