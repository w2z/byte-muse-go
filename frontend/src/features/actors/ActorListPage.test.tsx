// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { apiRequest } from "../../shared/api/client";
import { ActorListPage } from "./ActorListPage";
Object.defineProperty(window,"matchMedia",{writable:true,value:(query:string)=>({matches:false,media:query,addListener(){},removeListener(){}})});
vi.mock("../../shared/api/client",()=>({apiRequest:vi.fn()}));
afterEach(()=>{cleanup();vi.resetAllMocks();});
it("全部和热门分别请求服务端范围，切换时清空搜索和页码",async()=>{
 vi.mocked(apiRequest).mockResolvedValue({items:[],total:0,page:1,page_size:15});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 render(<MemoryRouter><QueryClientProvider client={client}><ActorListPage /></QueryClientProvider></MemoryRouter>);
 const user=userEvent.setup();
 await user.click(screen.getByRole("tab",{name:"全部演员"}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toContain("subscription=all"));
 await user.type(screen.getByLabelText("演员名称"),"Alias{Enter}");
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toContain("keywords=Alias"));
 await user.click(screen.getByRole("tab",{name:"热门"}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe("/actors?page=1&page_size=15&subscription=hot"));
 expect(screen.getByLabelText("演员名称")).toHaveValue("");
});
