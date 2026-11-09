// @vitest-environment jsdom
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import {act,cleanup,render,screen,waitFor,within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach,expect,it,vi} from "vitest";
import {UploadTables} from "./UploadTables";
import {apiRequest} from "../../shared/api/client";
vi.mock("../../shared/api/client",()=>({apiRequest:vi.fn()}));
afterEach(cleanup);
it("筛选只保留成功失败，跳过文件显示成功",async()=>{
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 vi.mocked(apiRequest).mockImplementation(async(path)=>String(path).startsWith("/cloud-upload/files")?{items:[{key:"skip",path:"/video",state:"skipped",size:1,progress:100,speed:0,error:""}],total:1,task_total:1,completed:1}:{items:[],total:0,actions:{}});
 render(<QueryClientProvider client={client}><UploadTables/></QueryClientProvider>);
 await userEvent.click(await screen.findByRole("tab",{name:"文件列表 (1/1)"}));
 expect((await screen.findByText("成功")).closest(".arco-tag")).toHaveClass("arco-tag-green");
 await userEvent.hover(screen.getByText("(跳过)"));
 expect(await screen.findByText("已存在相同的文件，跳过上传")).toBeInTheDocument();
 await userEvent.click(screen.getByRole("combobox",{name:"文件状态"}));
 expect(screen.queryByRole("option",{name:"已跳过"})).not.toBeInTheDocument();
 expect(screen.queryByRole("option",{name:"已变化"})).not.toBeInTheDocument();
 expect(screen.getByRole("option",{name:"成功"})).toBeInTheDocument();
 client.clear();
});
it("状态摘要使用标签栏右侧区域且不显示当前文件", async () => {
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
 vi.mocked(apiRequest).mockImplementation(async(path)=>path==="/cloud-upload/status"?{enabled:true,state:"uploading",total:1,processed:0,uploaded:0,skipped:0,failed:0,current:"/videos/current.mkv",error:"最近一次错误",actions:{}}:{items:[],total:0});
 render(<QueryClientProvider client={client}><UploadTables/></QueryClientProvider>);
 const summary = await screen.findByText(/已处理 0\/1/);
 expect(summary.closest(".arco-tabs-header-nav")).toBeTruthy();
 expect(screen.queryByText(/当前文件：/)).not.toBeInTheDocument();
 expect(screen.getByText("最近错误：最近一次错误")).toBeInTheDocument();
 client.clear();
});
it("无任务时隐藏数量，筛选为空但仍有任务时保留全局数量",async()=>{
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 vi.mocked(apiRequest).mockResolvedValue({items:[],total:0,task_total:0,completed:0});
 render(<QueryClientProvider client={client}><UploadTables/></QueryClientProvider>);
 expect(await screen.findByRole("tab",{name:"目录列表"})).toBeInTheDocument();
 expect(screen.getByRole("tab",{name:"文件列表"})).toBeInTheDocument();
 vi.mocked(apiRequest).mockImplementation(async(path)=>String(path).startsWith("/cloud-upload/directories")?{items:[],total:2,completed:1}:{items:[],total:0,task_total:3,completed:1});
 await client.invalidateQueries();
 expect(await screen.findByRole("tab",{name:"目录列表 (1/2)"})).toBeInTheDocument();
 expect(screen.getByRole("tab",{name:"文件列表 (1/3)"})).toBeInTheDocument();
 client.clear();
});
it("所有控制按钮默认禁用，状态返回后只启用服务端允许的动作",async()=>{
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 let resolveStatus:(value:unknown)=>void=()=>{};
 vi.mocked(apiRequest).mockImplementation(async(path)=>path==="/cloud-upload/status"?await new Promise(resolve=>{resolveStatus=resolve}):{items:[],total:0});
 render(<QueryClientProvider client={client}><UploadTables/></QueryClientProvider>);
 for(const name of ["暂停","停止","继续","删除所有任务"])expect(screen.getByRole("button",{name})).toBeDisabled();
 for(const name of ["清空已完成","清空错误"])expect(screen.getByRole("button",{name})).toBeDisabled();
 resolveStatus({actions:{pause:true,stop:true,clear_completed:false,resume:false,delete_all:true}});
 await waitFor(()=>expect(screen.getByRole("button",{name:"暂停"})).toBeEnabled());
 expect(screen.getByRole("button",{name:"继续"})).toBeDisabled();
 expect(screen.getByRole("button",{name:"全部重试"})).toBeDisabled();
 vi.mocked(apiRequest).mockImplementation(async(path)=>path==="/cloud-upload/status"?{actions:{pause:false,stop:true,clear_completed:false,resume:true,delete_all:true}}:{items:[],total:0});
 await userEvent.click(screen.getByRole("button",{name:"暂停"}));
 await waitFor(()=>expect(screen.getByRole("button",{name:"继续"})).toBeEnabled());
 expect(screen.getByRole("button",{name:"暂停"})).toBeDisabled();
 client.clear();
});
it("目录和文件行按钮按范围提交，目录操作由服务作用于其全部文件", async () => {
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const posts: unknown[] = [];
 vi.mocked(apiRequest).mockImplementation(async (path, options) => {
  if (path === "/cloud-upload/control" && options?.method === "POST") { posts.push(JSON.parse(String(options.body))); return {}; }
  if (String(path).startsWith("/cloud-upload/directories/progress")) return { items: [{ key: "dir-1", local_path: "/videos", remote_path: "/cloud", kind: "115", size: 1, progress: 0, speed: 0, uploaded: 0, total: 2, skipped: 0, failed: 0, actions: { pause: true, stop: true, start: false, delete: true } }], total: 2, completed: 0 };
  if (String(path).startsWith("/cloud-upload/files")) return { items: [{ key: "file-1", path: "/videos/a.mkv", remote_path: "/cloud/a.mkv", size: 2048, uploaded_bytes: 1024, progress: 50, speed: 0, state: "verifying", error: "", actions: { pause: true, stop: true, start: false, delete: true } }], total: 1, task_total: 1, completed: 0 };
  return { actions: {} };
 });
 render(<QueryClientProvider client={client}><UploadTables /></QueryClientProvider>);
 await userEvent.click(await screen.findByRole("button", { name: "暂停目录 /videos" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "pause", scope: "directory", key: "dir-1" }));
 expect(screen.queryByRole("button", { name: "开始目录 /videos" })).not.toBeInTheDocument();
 await userEvent.click(await screen.findByRole("tab", { name: "文件列表 (0/1)" }));
 expect(screen.getByRole("columnheader", { name: "路径" })).toBeInTheDocument();
 expect(await screen.findByText("网盘路径: /cloud/a.mkv")).toBeInTheDocument();
 expect(screen.queryByText("已校验 1.0 KiB / 2.0 KiB")).not.toBeInTheDocument();
 expect(screen.getByText("正在校验文件 (50%)").closest("tr")?.querySelector(".arco-progress")).toBeNull();
 expect(screen.getByText("正在校验文件 (50%)")).toHaveClass("settings-upload-verifying");
 expect(screen.queryByRole("button", { name: "开始文件 /videos/a.mkv" })).not.toBeInTheDocument();
 expect(screen.queryByRole("button", { name: "重新上传文件 /videos/a.mkv" })).not.toBeInTheDocument();
 await userEvent.click(await screen.findByRole("button", { name: "删除文件 /videos/a.mkv" }));
 await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确定" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "delete", scope: "file", key: "file-1" }));
 client.clear();
});
it("按当前列表清空，并在三次失败后显示重新上传", async () => {
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const posts: unknown[] = [];
 vi.mocked(apiRequest).mockImplementation(async (path, options) => {
  if (path === "/cloud-upload/control" && options?.method === "POST") { posts.push(JSON.parse(String(options.body))); return { actions: {} }; }
 if (String(path).startsWith("/cloud-upload/directories/progress")) return { items: [{ key: "dir-1", local_path: "/videos", remote_path: "/cloud", kind: "115", size: 1, progress: 100, speed: 0, uploaded: 1, total: 1, skipped: 0, failed: 0, queued_at: "2026-11-07T01:00:00Z", completed_at: "2026-11-07T02:00:00Z", actions: { retry: true, delete: true } }], total: 1, completed: 1 };
 if (String(path).startsWith("/cloud-upload/files")) return { items: [{ key: "file-1", path: "/videos/a.mkv", remote_path: "/cloud/a.mkv", size: 2048, uploaded_bytes: 0, progress: 0, speed: 0, state: "failed", error: "远端文件身份无法确认", retries: 3, max_retries: 3, queued_at: "2026-11-07T01:00:00Z", completed_at: "", actions: { retry: true, delete: true } }, { key: "file-2", path: "/videos/b.mkv", remote_path: "/cloud/b.mkv", size: 2048, uploaded_bytes: 1024, progress: 50, speed: 0, state: "retrying", error: "远端文件身份无法确认", retries: 2, max_retries: 3, actions: { pause: true, delete: true } }], total: 2, task_total: 2, completed: 0 };
  return { actions: { retry_all: true, delete_all: true, clear_completed_directories: true, clear_failed_directories: false, clear_completed_files: true, clear_failed_files: true } };
 });
 render(<QueryClientProvider client={client}><UploadTables /></QueryClientProvider>);
 expect(await screen.findByRole("columnheader", { name: "添加时间" })).toBeInTheDocument();
 expect(screen.getByRole("columnheader", { name: "完成时间" })).toBeInTheDocument();
 expect(screen.getByRole("combobox", { name: "目录状态" })).toBeInTheDocument();
 expect(await screen.findByText("/videos")).toBeInTheDocument();
 expect(screen.getByRole("tab", { name: "目录列表 (1/1)" })).toBeInTheDocument();
 expect(document.querySelector(".settings-upload-progress-success .arco-progress-is-success")).toBeTruthy();
 await userEvent.click(screen.getByRole("button", { name: "清空已完成" }));
 await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确定" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "clear_completed", scope: "directories" }));
 await userEvent.click(screen.getByRole("button", { name: "重新上传目录 /videos" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "retry", scope: "directory", key: "dir-1" }));
 await userEvent.click(await screen.findByRole("tab", { name: "文件列表 (0/2)" }));
 await waitFor(() => expect(screen.getByRole("tab", { name: "文件列表 (0/2)" })).toBeInTheDocument());
 expect(await screen.findByText("正在重试 (2/3)")).toBeInTheDocument();
 expect(await screen.findByText("正在重试 (2/3)；远端文件身份无法确认")).toBeInTheDocument();
 expect(screen.getByRole("tab", { name: "文件列表 (0/2)" })).toBeInTheDocument();
 expect(screen.getByRole("combobox", { name: "文件状态" })).toBeInTheDocument();
 const toolbar = screen.getByRole("combobox", { name: "文件状态" }).closest(".settings-upload-toolbar");
 expect(toolbar).toBeTruthy();
 expect(toolbar?.contains(screen.getByRole("button", { name: "全部重试" }))).toBe(true);
 expect(document.querySelector(".settings-upload-header")?.contains(toolbar)).toBe(false);
 expect(screen.queryByRole("button", { name: "暂停文件 /videos/a.mkv" })).not.toBeInTheDocument();
 expect(screen.queryByRole("button", { name: "停止文件 /videos/a.mkv" })).not.toBeInTheDocument();
 expect(screen.queryByText("状态", { selector: ".settings-upload-filter span" })).not.toBeInTheDocument();
 expect(screen.queryByRole("button", { name: "重新上传文件 /videos/b.mkv" })).not.toBeInTheDocument();
 expect(screen.getAllByText("远端文件身份无法确认").every((node) => node.classList.contains("settings-upload-error"))).toBe(true);
 expect(document.querySelector(".settings-upload-progress-error .arco-progress-is-error")).toBeTruthy();
 await userEvent.hover(screen.getByText("正在重试 (2/3)"));
 expect(await screen.findByText("当前重试次数:2次，总共3次")).toBeInTheDocument();
 await userEvent.click(screen.getByRole("button", { name: "重新上传文件 /videos/a.mkv" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "retry", scope: "file", key: "file-1" }));
 await userEvent.click(screen.getByRole("button", { name: "清空错误" }));
 await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确定" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "clear_failed", scope: "files" }));
 await userEvent.click(screen.getByRole("button", { name: "全部重试" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "retry_all" }));
 await userEvent.click(screen.getByRole("button", { name: "删除所有任务" }));
 await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确定" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "delete_all" }));
 client.clear();
});

it.each(["directory", "file"])("%s 行并发请求各自保持禁用和加载，失败只释放当前行", async (scope) => {
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const pending: { resolve: (value: unknown) => void; reject: (error: Error) => void }[] = [];
 const posts: unknown[] = [];
 vi.mocked(apiRequest).mockImplementation(async (path, options) => {
  if (path === "/cloud-upload/control") {
   posts.push(JSON.parse(String(options?.body)));
   return new Promise((resolve, reject) => pending.push({ resolve, reject }));
  }
  if (path.startsWith("/cloud-upload/directories")) return { items: ["a", "b"].map(key => ({ key, local_path: `/${key}`, remote_path: "/cloud", kind: "115", size: 1, progress: 0, speed: 0, uploaded: 0, total: 1, skipped: 0, failed: 0, actions: { pause: true, stop: true } })), total: 2 };
  if (path.startsWith("/cloud-upload/files")) return { items: ["a", "b"].map(key => ({ key, path: `/${key}`, size: 1, progress: 0, speed: 0, state: "waiting", error: "", actions: { pause: true, stop: true } })), total: 2 };
  return { actions: {} };
 });
 render(<QueryClientProvider client={client}><UploadTables /></QueryClientProvider>);
 await screen.findByRole("button", { name: "暂停目录 /a" });
 if (scope === "file") await userEvent.click(screen.getByRole("tab", { name: "文件列表 (0/2)" }));
 const kind = scope === "directory" ? "目录" : "文件";
 const first = screen.getByRole("button", { name: `暂停${kind} /a` });
 const second = screen.getByRole("button", { name: `暂停${kind} /b` });
 await userEvent.click(first);
 expect(first).toBeDisabled();
 expect(screen.getByRole("button", { name: `停止${kind} /a` })).toBeDisabled();
 expect(second).toBeEnabled();
 await userEvent.click(second);
 expect(first).toHaveClass("arco-btn-loading");
 expect(second).toHaveClass("arco-btn-loading");
 await userEvent.click(first);
 expect(posts).toEqual([{ action: "pause", scope, key: "a" }, { action: "pause", scope, key: "b" }]);
 await act(async () => { pending[0].resolve({}); pending[1].resolve({}); });
 await waitFor(() => expect(first).toBeEnabled());
 client.clear();
});

it.each(["directories", "files"])("%s 的危险操作必须确认，取消不提交且确认保留正确范围", async (scope) => {
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const posts: unknown[] = [];
 vi.mocked(apiRequest).mockImplementation(async (path, options) => {
  if (path === "/cloud-upload/control") { posts.push(JSON.parse(String(options?.body))); return {}; }
  if (path.startsWith("/cloud-upload/directories")) return { items: [{ key: "dir", local_path: "/videos", remote_path: "/cloud", kind: "115", size: 1, progress: 0, speed: 0, uploaded: 0, total: 1, skipped: 0, failed: 0, actions: { stop: true, delete: true } }], total: 1 };
  if (path.startsWith("/cloud-upload/files")) return { items: [{ key: "file", path: "/videos/a", size: 1, progress: 0, speed: 0, state: "waiting", error: "", actions: { stop: true, delete: true } }], total: 1 };
  return { actions: { stop: true, delete_all: true, clear_completed_directories: true, clear_failed_directories: true, clear_completed_files: true, clear_failed_files: true } };
 });
 render(<QueryClientProvider client={client}><UploadTables /></QueryClientProvider>);
 await screen.findByRole("button", { name: "停止目录 /videos" });
 if (scope === "files") await userEvent.click(screen.getByRole("tab", { name: "文件列表 (0/1)" }));
 const rowName = scope === "files" ? "文件 /videos/a" : "目录 /videos";
 const target = scope === "files" ? { scope: "file", key: "file" } : { scope: "directory", key: "dir" };
 const cases = [
  { name: "清空已完成", command: { action: "clear_completed", scope } },
  { name: "清空错误", command: { action: "clear_failed", scope } },
  { name: "停止", command: { action: "stop" } },
  { name: "删除所有任务", command: { action: "delete_all" } },
  { name: `停止${rowName}`, command: { action: "stop", ...target } },
  { name: `删除${rowName}`, command: { action: "delete", ...target } },
 ];
 for (const { name, command } of cases) {
  const count = posts.length;
  await waitFor(() => expect(screen.getByRole("button", { name })).toBeEnabled());
  await userEvent.click(screen.getByRole("button", { name }));
  expect(posts).toHaveLength(count);
  await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "取消" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  expect(posts).toHaveLength(count);
  await userEvent.click(screen.getByRole("button", { name }));
  await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确定" }));
  await waitFor(() => expect(posts).toHaveLength(count + 1));
  expect(posts[count]).toEqual(command);
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
 }
 client.clear();
}, 15000); // 每个列表依次验证六种操作的取消和确认，等待 Modal 退出动画。
