// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { Pan115LibraryScanAction } from "./Pan115ScanPathsField";
import { ScanProgressDisplay } from "./ScanProgress";
import { StrmGenerateAction } from "./StrmPathsField";
import { apiRequest, type ScanProgress } from "../../shared/api/client";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it.each(["scan", "strm"])("%s 从持久化任务恢复动态进度并执行暂停继续取消", async (kind) => {
  const base = kind === "scan" ? "/pan115/library/scan" : "/strm/scan";
  let state = "running";
  let total = 4;
  const task = () => ({ id: "task-1", kind: kind === "scan" ? "library" : "strm", mode: "incremental", state,
    progress: { phase: "discovering", processed: 2, total, percent: Math.floor(200 / total), current: "/影片" }, result: null, error: "" });
  const fetchMock = vi.fn(async (_input: string, options?: RequestInit) => {
    if (options?.method === "POST") {
      const action = JSON.parse(String(options.body)).action;
      state = action === "pause" ? "paused" : action === "resume" ? "running" : "canceled";
    }
    return Response.json(task());
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = () => <QueryClientProvider client={client}>{kind === "scan" ? <Pan115LibraryScanAction value={[{id: "1", path: "/movies"}]} /> : <StrmGenerateAction value={[{kind: "115", id: "1", path: "/movies", local_path: "/movies", formats: ["mp4"], min_size_mb: 0, exclude: []}]} />}</QueryClientProvider>;
  const rendered = render(view());
  const button = screen.getByRole("button", { name: kind === "scan" ? "扫描入库" : "全量生成 strm" });
  expect(await screen.findByText("50% - 2/4")).toBeInTheDocument();
  expect(button).toBeDisabled();
  total = 8;
  await act(async () => { await client.refetchQueries({queryKey: ["scan-task", base]}); });
  expect(await screen.findByText("25% - 2/8")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", {name: "暂停"}));
  expect(await screen.findByText("已暂停")).toBeInTheDocument();
  expect(button).toBeDisabled();
  rendered.unmount();
  render(view());
  expect(await screen.findByText("已暂停")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", {name: "继续"}));
  expect(await screen.findByText("扫描中，总数持续更新")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", {name: "取消"}));
  await waitFor(() => expect(screen.queryByText("25% - 2/8")).not.toBeInTheDocument());
  expect(screen.queryByText("已取消")).not.toBeInTheDocument();
  expect(screen.getByRole("button", {name: kind === "scan" ? "扫描入库" : "全量生成 strm"})).toBeEnabled();
  expect(fetchMock.mock.calls.filter(([, options]) => options?.method === "POST").map(([, options]) => JSON.parse(String(options?.body)).action)).toEqual(["pause", "resume", "cancel"]);
});

it("跨 UTF-8 分块解析进度，缺少最终结果时拒绝假成功", async () => {
  const bytes = new TextEncoder().encode('{"type":"progress","progress":{"phase":"discovering","processed":1,"total":2,"percent":50,"current":"/影片"}}\n');
  vi.stubGlobal("fetch", vi.fn(async () => new Response(new ReadableStream({ start(c) { for (const byte of bytes) c.enqueue(new Uint8Array([byte])); c.close(); } }), {headers: {"Content-Type":"application/x-ndjson"}})));
  const updates: string[] = [];
  await expect(apiRequest("/strm/scan", {method: "POST"}, p => updates.push(p.current))).rejects.toThrow("未收到处理结果");
  expect(updates).toEqual(["/影片"]);
});

it("只在任务进行中显示进度条", () => {
  const progress: ScanProgress = { phase: "discovering", processed: 2, total: 4, percent: 50, current: "/影片" };
  for (const state of ["completed", "canceled", "failed"] as const) {
    const { container, unmount } = render(<ScanProgressDisplay label="生成" progress={progress} state={state} />);
    expect(container).toBeEmptyDOMElement();
    unmount();
  }
  const running = render(<ScanProgressDisplay label="生成" progress={progress} state="running" />);
  expect(screen.getByText("50% - 2/4")).toBeInTheDocument();
  running.unmount();
  render(<ScanProgressDisplay label="生成" progress={progress} state="interrupted" />);
  expect(screen.getByText("服务重启，任务已中断，请重新启动")).toBeInTheDocument();
  expect(screen.queryByText("50% - 2/4")).not.toBeInTheDocument();
});
