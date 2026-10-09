// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { Pan115LibraryScanAction } from "./Pan115ScanPathsField";
import { ScanProgressDisplay, ScanTaskControls, type ScanTask } from "./ScanProgress";
import { StrmGenerateAction } from "./StrmPathsField";
import { apiRequest, type ScanProgress } from "../../shared/api/client";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it("生成进度目录旁的信息按钮打开可展开的文件进度表格", async () => {
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
  const fetchMock = vi.fn(async (input: string) => {
    const nested = String(input).includes("parent=root");
    return Response.json({ available:true,total:1,items:nested ? [{id:"file",name:"poster.jpg",kind:"file",operation:"download",state:"processing",total:1,processed:0,failed:0,percent:50,bytes:512,size:1024}] : [{id:"root",name:"电影",kind:"directory",operation:"",state:"scanning",total:2,processed:1,failed:0,percent:50,bytes:0,size:0}] });
  });
  vi.stubGlobal("fetch",fetchMock);
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  render(<QueryClientProvider client={client}><ScanProgressDisplay label="生成" state="running" taskId="task-1" progress={{phase:"processing",processed:1,total:2,percent:50,current:"/电影"}} /></QueryClientProvider>);
  expect(fetchMock).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button",{name:"查看 STRM 文件进度"}));
  expect(await screen.findByText("电影")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button",{name:"展开目录 电影"}));
  expect(await screen.findByText("poster.jpg")).toBeInTheDocument();
  expect(screen.getByText("512 B / 1.0 KiB")).toBeInTheDocument();
  expect(screen.getByText("下载附件")).toBeInTheDocument();
  const toggle = screen.getByRole("switch", { name: "隐藏已完成" });
  expect(toggle).toHaveAttribute("aria-checked", "false");
  expect(fetchMock.mock.calls.every(([url]) => String(url).includes("hide_completed=false"))).toBe(true);
  fireEvent.click(toggle);
  await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes("parent=root") && String(url).includes("hide_completed=true"))).toBe(true));
  expect(toggle).toHaveAttribute("aria-checked", "true");
  expect(screen.getByRole("dialog").style.width).toBe("60vw");
  expect(screen.getByRole("dialog")).toHaveStyle({ height: "60dvh", overflow: "auto" });
  expect(screen.getByRole("region", { name: "STRM 文件列表" })).toBeInTheDocument();
  expect(document.querySelector(".strm-file-dialog-scroll")).not.toBeInTheDocument();
  expect(document.querySelectorAll("table")).toHaveLength(2);
  document.querySelectorAll("table").forEach(table => expect(table.style.width).toBe(""));
});

it.each(["failed", "interrupted", "canceled", "completed"] as const)("%s 的可恢复任务显示红色继续按钮且发送 retry", (state) => {
  const action = vi.fn();
  const task = { id: "retry-1", state, can_retry: true } as ScanTask<unknown>;
  const view = render(<ScanTaskControls task={task} pending={false} onAction={action} />);
  const button = screen.getByRole("button", { name: "继续失败的任务" });
  expect(button).toHaveClass("arco-btn-status-danger");
  fireEvent.click(button); expect(action).toHaveBeenCalledWith("retry");
  view.rerender(<ScanTaskControls task={task} pending={true} onAction={action} />);
  expect(button).toBeDisabled();
  view.rerender(<ScanTaskControls task={{ ...task, can_retry: false }} pending={false} onAction={action} />);
  expect(screen.queryByRole("button", { name: "继续失败的任务" })).not.toBeInTheDocument();
});

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
  const interrupted = render(<ScanProgressDisplay label="生成" progress={progress} state="interrupted" />);
  expect(screen.getByText("服务重启，任务已中断，请重新启动")).toBeInTheDocument();
  expect(screen.queryByText("50% - 2/4")).not.toBeInTheDocument();
  interrupted.rerender(<ScanProgressDisplay label="生成" progress={progress} state="interrupted" canRetry />);
  expect(screen.getByText("服务重启，任务已中断，可继续失败的任务")).toBeInTheDocument();
});
