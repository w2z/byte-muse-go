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
  if (path === "/cloud-upload/directories/progress") return { items: [{ key: "dir-1", local_path: "/videos", remote_path: "/cloud", kind: "115", size: 1, progress: 0, speed: 0, uploaded: 0, total: 2, skipped: 0, failed: 0, actions: { pause: true, stop: true, start: false, delete: true } }] };
  if (String(path).startsWith("/cloud-upload/files")) return { items: [{ key: "file-1", path: "/videos/a.mkv", size: 1, progress: 0, speed: 0, state: "waiting", error: "", actions: { pause: true, stop: true, start: false, delete: true } }], total: 1 };
  return { actions: {} };
 });
 render(<QueryClientProvider client={client}><UploadTables /></QueryClientProvider>);
 await userEvent.click(await screen.findByRole("button", { name: "暂停目录 /videos" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "pause", scope: "directory", key: "dir-1" }));
 expect(screen.getByRole("button", { name: "开始目录 /videos" })).toBeDisabled();
 await userEvent.click(screen.getByRole("tab", { name: "文件列表" }));
 await userEvent.click(await screen.findByRole("button", { name: "删除文件 /videos/a.mkv" }));
 await waitFor(() => expect(posts).toContainEqual({ action: "delete", scope: "file", key: "file-1" }));
 client.clear();
});
