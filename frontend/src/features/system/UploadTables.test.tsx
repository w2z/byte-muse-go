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
