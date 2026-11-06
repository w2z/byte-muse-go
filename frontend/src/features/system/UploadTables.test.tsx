// @vitest-environment jsdom
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import "@testing-library/jest-dom/vitest";
import {cleanup,render,screen,waitFor} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach,expect,it,vi} from "vitest";
import {UploadTables} from "./UploadTables";
import {apiRequest} from "../../shared/api/client";
vi.mock("../../shared/api/client",()=>({apiRequest:vi.fn()}));
afterEach(cleanup);
it("所有控制按钮默认禁用，状态返回后只启用服务端允许的动作",async()=>{
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 let resolveStatus:(value:unknown)=>void=()=>{};
 vi.mocked(apiRequest).mockImplementation(async(path)=>path==="/cloud-upload/status"?await new Promise(resolve=>{resolveStatus=resolve}):{items:[],total:0});
 render(<QueryClientProvider client={client}><UploadTables/></QueryClientProvider>);
 for(const name of ["暂停","停止","清除已完成","继续","删除所有任务"])expect(screen.getByRole("button",{name})).toBeDisabled();
 for(const name of ["清空已完成","清空错误"])expect(screen.getByRole("button",{name})).toBeDisabled();
 resolveStatus({actions:{pause:true,stop:true,clear_completed:false,resume:false,delete_all:true}});
 await waitFor(()=>expect(screen.getByRole("button",{name:"暂停"})).toBeEnabled());
 expect(screen.getByRole("button",{name:"继续"})).toBeDisabled();
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
  if (String(path).startsWith("/cloud-upload/directories/progress")) return { items: [{ key: "dir-1", local_path: "/videos", remote_path: "/cloud", kind: "115", size: 1, progress: 0, speed: 0, uploaded: 0, total: 2, skipped: 0, failed: 0, actions: { pause: true, stop: true, start: false, delete: true } }] };
  if (String(path).startsWith("/cloud-upload/files")) return { items: [{ key: "file-1", path: "/videos/a.mkv", remote_path: "/cloud/a.mkv", size: 2048, uploaded_bytes: 1024, progress: 50, speed: 0, state: "verifying", error: "", actions: { pause: true, stop: true, start: false, delete: true } }], total: 1 };
  return { actions: {} };
 });
 render(<QueryClientProvider client={client}><UploadTables /></QueryClientProvider>);
 await userEvent.click(await screen.findByRole("button", { name: "暂停目录 /videos" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "pause", scope: "directory", key: "dir-1" }));
 expect(screen.queryByRole("button", { name: "开始目录 /videos" })).not.toBeInTheDocument();
 await userEvent.click(screen.getByRole("tab", { name: "文件列表" }));
 expect(screen.getByRole("columnheader", { name: "本地路径" })).toBeInTheDocument();
 expect(await screen.findByText("/cloud/a.mkv")).toBeInTheDocument();
 expect(screen.getByText("已校验 1.0 KiB / 2.0 KiB")).toBeInTheDocument();
 expect(screen.queryByRole("button", { name: "开始文件 /videos/a.mkv" })).not.toBeInTheDocument();
 expect(screen.queryByRole("button", { name: "重新上传文件 /videos/a.mkv" })).not.toBeInTheDocument();
 await userEvent.click(await screen.findByRole("button", { name: "删除文件 /videos/a.mkv" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "delete", scope: "file", key: "file-1" }));
 client.clear();
});
it("按当前列表清空，并在三次失败后显示重新上传", async () => {
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const posts: unknown[] = [];
 vi.mocked(apiRequest).mockImplementation(async (path, options) => {
  if (path === "/cloud-upload/control" && options?.method === "POST") { posts.push(JSON.parse(String(options.body))); return { actions: {} }; }
  if (String(path).startsWith("/cloud-upload/directories/progress")) return { items: [{ key: "dir-1", local_path: "/videos", remote_path: "/cloud", kind: "115", size: 1, progress: 100, speed: 0, uploaded: 1, total: 1, skipped: 0, failed: 0, queued_at: "2026-11-07T01:00:00Z", completed_at: "2026-11-07T02:00:00Z", actions: { retry: true, delete: true } }] };
  if (String(path).startsWith("/cloud-upload/files")) return { items: [{ key: "file-1", path: "/videos/a.mkv", remote_path: "/cloud/a.mkv", size: 2048, uploaded_bytes: 0, progress: 0, speed: 0, state: "failed", error: "远端文件身份无法确认", retries: 3, max_retries: 3, queued_at: "2026-11-07T01:00:00Z", completed_at: "", actions: { retry: true, delete: true } }, { key: "file-2", path: "/videos/b.mkv", remote_path: "/cloud/b.mkv", size: 2048, uploaded_bytes: 1024, progress: 50, speed: 0, state: "retrying", error: "远端文件身份无法确认", retries: 2, max_retries: 3, actions: { pause: true, delete: true } }], total: 2 };
  return { actions: { clear_completed_directories: true, clear_failed_directories: false, clear_completed_files: true, clear_failed_files: true } };
 });
 render(<QueryClientProvider client={client}><UploadTables /></QueryClientProvider>);
 expect(await screen.findByRole("columnheader", { name: "添加时间" })).toBeInTheDocument();
 expect(screen.getByRole("columnheader", { name: "完成时间" })).toBeInTheDocument();
 expect(await screen.findByText("/videos")).toBeInTheDocument();
 expect(document.querySelector(".settings-upload-progress-success .arco-progress-is-success")).toBeTruthy();
 await userEvent.click(screen.getByRole("button", { name: "清空已完成" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "clear_completed", scope: "directories" }));
 await userEvent.click(screen.getByRole("button", { name: "重新上传目录 /videos" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "retry", scope: "directory", key: "dir-1" }));
 await userEvent.click(screen.getByRole("tab", { name: "文件列表" }));
 expect(await screen.findByText("处理中 (2/3)")).toBeInTheDocument();
 expect(screen.queryByRole("button", { name: "重新上传文件 /videos/b.mkv" })).not.toBeInTheDocument();
 expect(screen.getByText("远端文件身份无法确认")).toHaveClass("settings-upload-error");
 expect(document.querySelector(".settings-upload-progress-error .arco-progress-is-error")).toBeTruthy();
 await userEvent.hover(screen.getByText("处理中 (2/3)"));
 expect(await screen.findByText("当前重试次数:2次，总共3次")).toBeInTheDocument();
 await userEvent.click(screen.getByRole("button", { name: "重新上传文件 /videos/a.mkv" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "retry", scope: "file", key: "file-1" }));
 await userEvent.click(screen.getByRole("button", { name: "清空错误" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "clear_failed", scope: "files" }));
 client.clear();
});
