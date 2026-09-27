import { Button, Input } from "@arco-design/web-react";
import { IconSearch } from "@arco-design/web-react/icon";
import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { Media } from "../../shared/api/types";
import { DEFAULT_PAGE_SIZE } from "../../shared/ui/ListPagination";
import { MediaCardGrid } from "../../shared/ui/MediaCardGrid";
import { PageHeader } from "../../shared/ui/PageHeader";
import "./SearchPage.css";

/**
 * 搜索页：按番号、标题或译名检索目录。
 *
 * 布局对齐对标站 对标站 的搜索页：页面标题一行，下面是一条「输入框 + 搜索按钮」的工具行，
 * 再下面是公共 MediaCardGrid 提供的响应式卡片网格与分页条；分页保留服务端权威。
 *
 * 请求策略以后端代码为准：仓储 Search 在关键字为空时不下 WHERE 条件、直接返回全量目录，
 * 所以空关键字是合法查询（等价于浏览全部），不会伪造空结果；但为了避免每次按键都打服务端，
 * 只有回车或点击「搜索」才提交关键字，未提交前用 useQuery 的 enabled 关闭请求。
 * 未提交时不展示分页条，空态文案也换成引导语，这两点通过 empty / paginated 显式告知网格组件。
 */
export function SearchPage() {
  const [draft, setDraft] = useState("");
  // null 表示尚未提交过搜索，此时不发请求；空字符串是合法关键字，表示浏览全部。
  const [keyword, setKeyword] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);

  const query = useQuery({
    queryKey: ["catalog-search", keyword, page, pageSize],
    queryFn: () =>
      apiRequest<Page<Media>>(
        `/complex/search?q=${encodeURIComponent(keyword ?? "")}&page=${page}&page_size=${pageSize}`,
      ),
    enabled: keyword !== null,
  });

  const notSearched = keyword === null;
  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;

  /** 提交当前输入框内容：新关键字从第一页开始查。 */
  function commit() {
    setPage(1);
    setKeyword(draft.trim());
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    commit();
  }

  /** 切换每页条数：页长变了，页码必须回到第一页，否则会请求到越界页。 */
  function changePageSize(next: number) {
    setPageSize(next);
    setPage(1);
  }

  return (
    <section>
      <PageHeader title="搜索" />
      <form className="page-toolbar" role="search" onSubmit={submit}>
        <div className="toolbar-group search-field">
          <Input
            value={draft}
            onChange={setDraft}
            onPressEnter={commit}
            allowClear
            aria-label="搜索关键字"
            placeholder="番号、标题或译名"
            suffix={<IconSearch />}
          />
          <Button type="primary" htmlType="submit">
            搜索
          </Button>
        </div>
        <div className="toolbar-note">{notSearched ? "回车或点击搜索" : `共 ${total} 条结果`}</div>
      </form>
      <MediaCardGrid
        items={items}
        query={{
          isLoading: !notSearched && query.isLoading,
          error: notSearched ? null : query.error,
          refetch: query.refetch,
        }}
        empty={notSearched || undefined}
        paginated={!notSearched}
        emptyText={notSearched ? "输入番号、标题或译名后搜索" : `未找到与「${keyword}」匹配的内容`}
        total={total}
        page={page}
        pageSize={pageSize}
        onPageChange={setPage}
        onPageSizeChange={changePageSize}
      />
    </section>
  );
}
