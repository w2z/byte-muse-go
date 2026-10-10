// @vitest-environment jsdom
// jsdom 不提供媒体查询；Arco Grid 使用此接口订阅响应式断点。
Object.defineProperty(window, "matchMedia", { writable: true, value: (query: string) => ({ matches: false, media: query, addListener() {}, removeListener() {} }) });
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import '@testing-library/jest-dom/vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { apiRequest } from '../../shared/api/client';
import { SearchPage } from './SearchPage';

vi.mock('../../shared/api/client',()=>({apiRequest:vi.fn()}));
afterEach(()=>{cleanup();vi.resetAllMocks();});

it('演员链接直接精确搜索，修改输入后切换普通搜索', async () => {
  vi.mocked(apiRequest).mockResolvedValue({ items: [], total: 0, page: 1, page_size: 15 });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<MemoryRouter initialEntries={['/search?actor=演员甲']}><QueryClientProvider client={client}><SearchPage /></QueryClientProvider></MemoryRouter>);
  expect(await screen.findByText('演员：演员甲 · 共 0 条结果')).toBeInTheDocument();
  expect(vi.mocked(apiRequest).mock.calls[0][0]).toBe('/complex/search?actor=%E6%BC%94%E5%91%98%E7%94%B2&page=1&page_size=15');
  const user = userEvent.setup();
  await user.clear(screen.getByLabelText('搜索关键字'));
  await user.type(screen.getByLabelText('搜索关键字'), 'TEST-001');
  await user.click(screen.getByRole('button', { name: '搜索' }));
  await waitFor(() => expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe('/complex/search?q=TEST-001&page=1&page_size=15'));
});

it('标签链接直接精确搜索，手工搜索替换标签条件',async()=>{
 vi.mocked(apiRequest).mockResolvedValue({items:[],total:0,page:1,page_size:15});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 render(<MemoryRouter initialEntries={['/search?tag=制服']}><QueryClientProvider client={client}><SearchPage/></QueryClientProvider></MemoryRouter>);
 expect(await screen.findByText('未找到与「制服」匹配的内容')).toBeInTheDocument();
 expect(vi.mocked(apiRequest).mock.calls[0][0]).toBe('/complex/search?tag=%E5%88%B6%E6%9C%8D&page=1&page_size=15');
 expect(screen.getByLabelText('搜索关键字')).toHaveValue('制服');
 const user=userEvent.setup();
 await user.click(screen.getByRole('button',{name:'搜索'}));
 expect(screen.getByText('标签：制服 · 共 0 条结果')).toBeInTheDocument();
 await user.clear(screen.getByLabelText('搜索关键字'));
 await user.type(screen.getByLabelText('搜索关键字'),'TEST-001');await user.click(screen.getByRole('button',{name:'搜索'}));
 await waitFor(()=>expect(vi.mocked(apiRequest).mock.calls.at(-1)?.[0]).toBe('/complex/search?q=TEST-001&page=1&page_size=15'));
 expect(screen.getByLabelText('搜索关键字')).toHaveValue('TEST-001');
});
