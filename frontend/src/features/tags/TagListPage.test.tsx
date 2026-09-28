// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import '@testing-library/jest-dom/vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { apiRequest } from '../../shared/api/client';
import { TagListPage } from './TagListPage';
vi.mock('../../shared/api/client', () => ({ apiRequest: vi.fn() }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });
function showPage() {
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
 return render(<QueryClientProvider client={client}><TagListPage /></QueryClientProvider>);
}
it('显示标签及影片数，翻页后搜索重置到首页', async () => {
 vi.mocked(apiRequest).mockResolvedValue({items:[{name:'标签甲',media_count:3}],total:31,page:1,page_size:15});
 const user = userEvent.setup(); showPage();
 expect(await screen.findByText('标签甲')).toBeInTheDocument();
 expect(screen.getByText('关联影片 3 部')).toBeInTheDocument();
 expect(screen.queryByText('标签功能尚未接入')).not.toBeInTheDocument();
 await user.click(screen.getByText('2', {selector:'.arco-pagination-item'}));
 await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe('/tags?page=2&page_size=15'));
 await user.type(screen.getByLabelText('搜索标签'), '标签甲');
 await user.click(screen.getByRole('button', {name:/^搜索$/}));
 await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe('/tags?page=1&page_size=15&search=%E6%A0%87%E7%AD%BE%E7%94%B2'));
});
it('没有标签时展示空状态', async () => {
 vi.mocked(apiRequest).mockResolvedValue({items:[],total:0,page:1,page_size:15}); showPage();
 expect(await screen.findByText('暂无标签，采集影片详情后可在此查看')).toBeInTheDocument();
});
it('接口失败可以重试', async () => {
 vi.mocked(apiRequest).mockRejectedValueOnce(new Error('读取失败')).mockResolvedValue({items:[{name:'恢复标签',media_count:1}],total:1,page:1,page_size:15});
 const user = userEvent.setup(); showPage();
 await user.click(await screen.findByRole('button',{name:'重试'}));
 expect(await screen.findByText('恢复标签')).toBeInTheDocument();
});
