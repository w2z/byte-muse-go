// @vitest-environment jsdom
// jsdom 不提供媒体查询；Arco Grid 使用此接口订阅响应式断点。
Object.defineProperty(window, "matchMedia", { writable: true, value: (query: string) => ({ matches: false, media: query, addListener() {}, removeListener() {} }) });
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { apiRequest } from '../../shared/api/client';
import { TagListPage } from './TagListPage';
import { MemoryRouter } from 'react-router-dom';
vi.mock('../../shared/api/client', () => ({ apiRequest: vi.fn() }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });
function showPage() {
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
 return render(<MemoryRouter><QueryClientProvider client={client}><TagListPage /></QueryClientProvider></MemoryRouter>);
}
it('显示标签及影片数，翻页后搜索重置到首页', async () => {
 vi.mocked(apiRequest).mockResolvedValue({items:[{name:'标签甲',media_count:3,category:'主题',limit_date:'2026-09-28'}],total:31,page:1,page_size:15});
 const user = userEvent.setup(); showPage();
 expect(await screen.findByText('标签甲')).toBeInTheDocument();
 expect(screen.getByText('关联影片 3 部')).toBeInTheDocument();
 expect(screen.queryByText('标签功能尚未接入')).not.toBeInTheDocument();
 await user.click(screen.getByText('2', {selector:'.arco-pagination-item'}));
 await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe('/tags?page=2&page_size=15&subscription=active'));
 await user.type(screen.getByLabelText('搜索标签'), '标签甲');
 await user.click(screen.getByRole('button', {name:/^搜索$/}));
 await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe('/tags?page=1&page_size=15&subscription=active&search=%E6%A0%87%E7%AD%BE%E7%94%B2'));
});
it('空名称和相同名称重复搜索仍发起查询，并保留类型筛选', async () => {
 vi.mocked(apiRequest).mockResolvedValue({items:[{name:'标签甲',media_count:3,category:'主题',limit_date:null}],total:1,page:1,page_size:15});
 const user=userEvent.setup();showPage();
 await screen.findByText('标签甲');
 let count=vi.mocked(apiRequest).mock.calls.length;
 await user.click(screen.getByRole('button',{name:/^搜索$/}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.length).toBe(count+1));
 await user.click(screen.getByRole('button',{name:'筛选类型：主题'}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toContain('category='));
 count=vi.mocked(apiRequest).mock.calls.length;
 await user.type(screen.getByLabelText('搜索标签'),'   {Enter}');
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.length).toBe(count+1));
 expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe('/tags?page=1&page_size=15&subscription=active&category=%E4%B8%BB%E9%A2%98');
 await user.clear(screen.getByLabelText('搜索标签'));
 await user.type(screen.getByLabelText('搜索标签'),'标签{Enter}');
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toContain('search='));
 count=vi.mocked(apiRequest).mock.calls.length;
 await user.click(screen.getByRole('button',{name:/^搜索$/}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.length).toBe(count+1));
});
it('重复搜索期间显示加载状态，完成后恢复列表', async () => {
 const result={items:[{name:'标签甲',media_count:3,category:'主题',limit_date:null}],total:1,page:1,page_size:15};
 vi.mocked(apiRequest).mockResolvedValueOnce(result);
 const user=userEvent.setup();showPage();
 await screen.findByText('标签甲');
 let finish!: (value: typeof result) => void;
 vi.mocked(apiRequest).mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve;}));
 await user.click(screen.getByRole('button',{name:/^搜索$/}));
 expect(await screen.findByRole('status')).toHaveTextContent('加载中');
 expect(screen.getByRole('region',{name:'标签列表'})).toHaveAttribute('aria-busy','true');
 finish(result);
 expect(await screen.findByText('标签甲')).toBeInTheDocument();
 expect(screen.queryByRole('status')).not.toBeInTheDocument();
 expect(screen.getByRole('region',{name:'标签列表'})).toHaveAttribute('aria-busy','false');
});
it('没有标签时展示空状态', async () => {
 vi.mocked(apiRequest).mockResolvedValue({items:[],total:0,page:1,page_size:15}); showPage();
 expect(await screen.findByText('暂无已订阅标签')).toBeInTheDocument();
});
it('点击卡片类型同步筛选框、保留名称和订阅范围并回到第一页', async () => {
 vi.mocked(apiRequest).mockResolvedValue({items:[{name:'标签甲',media_count:3,category:'主题',limit_date:null}],total:231,page:1,page_size:100});
 const user=userEvent.setup();showPage();
 await user.click(screen.getByRole('tab',{name:'全部标签'}));
 await user.type(screen.getByLabelText('搜索标签'),'标签');
 await user.click(screen.getByRole('button',{name:/^搜索$/}));
 await user.click(await screen.findByText('2',{selector:'.arco-pagination-item'}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toContain('page=2'));
 await user.click(await screen.findByRole('button',{name:'筛选类型：主题'}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe('/tags?page=1&page_size=100&subscription=all&search=%E6%A0%87%E7%AD%BE&category=%E4%B8%BB%E9%A2%98'));
 expect(document.querySelector('.tag-type-filter')).toHaveTextContent('主题');
 expect(screen.getByLabelText('搜索标签')).toHaveValue('标签');
});
it('重置清空两项筛选、保留全部标签范围，重复重置仍刷新', async () => {
 vi.mocked(apiRequest).mockResolvedValue({items:[{name:'标签甲',media_count:3,category:'主题',limit_date:null}],total:231,page:1,page_size:100});
 const user=userEvent.setup();showPage();
 await user.click(screen.getByRole('tab',{name:'全部标签'}));
 await user.click(await screen.findByRole('button',{name:'筛选类型：主题'}));
 await user.type(screen.getByLabelText('搜索标签'),'标签');
 await user.click(screen.getByRole('button',{name:/^搜索$/}));
 await user.click(await screen.findByText('2',{selector:'.arco-pagination-item'}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toContain('page=2'));
 await user.click(screen.getByRole('button',{name:'重置'}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe('/tags?page=1&page_size=100&subscription=all'));
 expect(screen.getByLabelText('搜索标签')).toHaveValue('');
 expect(document.querySelector('.tag-type-filter')).toHaveTextContent('全部类型');
 const count=vi.mocked(apiRequest).mock.calls.length;
 await user.click(screen.getByRole('button',{name:'重置'}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.length).toBeGreaterThan(count));
});
it('每行显示个数支持1至10且调整布局不重新查询', async () => {
 vi.mocked(apiRequest).mockResolvedValue({items:[{name:'布局标签',category:'主题',media_count:1,limit_date:null}],total:1,page:1,page_size:15});
 const user=userEvent.setup();showPage();
 await screen.findByRole('link',{name:'布局标签'});
 const count=vi.mocked(apiRequest).mock.calls.length;
 const slider=screen.getByRole('slider');
 expect(slider).toHaveAttribute('aria-valuemin','1');
 expect(slider).toHaveAttribute('aria-valuemax','10');
 expect(slider).toHaveAttribute('aria-valuenow','5');
 const input=screen.getByRole('spinbutton',{name:'每行显示个数'});
 await user.clear(input);await user.type(input,'10');await user.tab();
 expect(slider).toHaveAttribute('aria-valuenow','10');
 expect(document.querySelector('.tag-card-grid')).toHaveStyle({'--tag-columns':'10'});
 expect(vi.mocked(apiRequest).mock.calls.length).toBe(count);
});
it('切换全部标签再返回订阅中，只显示接口返回的活动订阅', async () => {
 const subscribed={name:'已订阅标签',category:'主题',media_count:2,limit_date:'2026-09-28'};
 const unsubscribed={name:'未订阅标签',category:'主题',media_count:1,limit_date:null};
 vi.mocked(apiRequest).mockImplementation(async(path)=>({items:path.includes('subscription=active')?[subscribed]:[subscribed,unsubscribed],total:path.includes('subscription=active')?1:2,page:1,page_size:15}));
 const user=userEvent.setup();showPage();
 expect(await screen.findByRole('link',{name:'已订阅标签'})).toBeInTheDocument();
 expect(screen.queryByRole('link',{name:'未订阅标签'})).not.toBeInTheDocument();
 await user.click(screen.getByRole('tab',{name:'全部标签'}));
 expect(await screen.findByRole('link',{name:'未订阅标签'})).toBeInTheDocument();
 await user.click(screen.getByRole('tab',{name:'订阅中'}));
 await waitFor(()=>expect(screen.queryByRole('link',{name:'未订阅标签'})).not.toBeInTheDocument());
 expect(screen.getByRole('link',{name:'已订阅标签'})).toBeInTheDocument();
});
it('按订阅状态展示操作并把标签带到搜索页，切换全部标签由服务端过滤', async()=>{
 vi.mocked(apiRequest).mockResolvedValue({items:[{name:'制服',category:'服装',media_count:3,limit_date:'2026-09-28'}],total:1,page:1,page_size:15});
 const user=userEvent.setup();showPage();
 expect(await screen.findByRole('link',{name:'制服'})).toHaveAttribute('href','/search?tag=%E5%88%B6%E6%9C%8D');
 expect(screen.getByText('服装')).toBeInTheDocument();
 await user.click(screen.getByRole('button',{name:'编辑'}));
 expect(screen.getByLabelText('限制日期')).toHaveValue('2026-09-28');
 fireEvent.input(screen.getByLabelText('限制日期'),{target:{value:'2026-09-19'}});
 await user.click(screen.getByRole('button',{name:'确认'}));
 await waitFor(()=>expect(vi.mocked(apiRequest)).toHaveBeenCalledWith('/tags/%E5%88%B6%E6%9C%8D/subscription',{method:'PUT',body:'{"limit_date":"2026-09-19"}'}));
 await waitFor(()=>expect(screen.queryByLabelText('限制日期')).not.toBeInTheDocument());
 await user.click(screen.getByRole('tab',{name:'全部标签'}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe('/tags?page=1&page_size=100&subscription=all'));
});
it('接口失败可以重试', async () => {
 vi.mocked(apiRequest).mockRejectedValueOnce(new Error('读取失败')).mockResolvedValue({items:[{name:'恢复标签',media_count:1}],total:1,page:1,page_size:15});
 const user = userEvent.setup(); showPage();
 await user.click(await screen.findByRole('button',{name:'重试'}));
 expect(await screen.findByText('恢复标签')).toBeInTheDocument();
});
