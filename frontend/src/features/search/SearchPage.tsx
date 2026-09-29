import { Button, Input, Grid } from "@arco-design/web-react";
import { IconSearch } from "@arco-design/web-react/icon";
import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { apiRequest, type Page } from "../../shared/api/client";
import type { Media } from "../../shared/api/types";
import { DEFAULT_PAGE_SIZE } from "../../shared/ui/ListPagination";
import { MediaCardGrid } from "../../shared/ui/MediaCardGrid";
import { PageHeader } from "../../shared/ui/PageHeader";
import "./SearchPage.css";

/**
 * 搜索页：按完整标签，或番号、标题、译名检索目录。
 *
 * 布局对齐对标站 对标站 的搜索页：页面标题一行，下面是一条「输入框 + 搜索按钮」的工具行，
 * 再下面是公共 MediaCardGrid 提供的响应式卡片网格与分页条；分页保留服务端权威。
 *
 * 请求策略以后端代码为准：仓储 Search 在关键字为空时不下 WHERE 条件、直接返回全量目录，
 * 所以空关键字是合法查询（等价于浏览全部），不会伪造空结果；但为了避免每次按键都打服务端，
 * 标签链接或 URL 查询会直接搜索；手工关键字在回车或点击「搜索」时提交。
 * 未提交时不展示分页条，空态文案也换成引导语，这两点通过 empty / paginated 显式告知网格组件。
 */
export function SearchPage() {
 const [params]=useSearchParams();
 return <SearchResults key={params.toString()} />;
}

/** 查询参数变化时重新创建本页状态，返回/前进不会沿用旧页码和输入值。 */
function SearchResults() {
  const [params,setParams]=useSearchParams();
  const tag=params.get("tag")??"";
  // 输入框显示实际生效的筛选值，与请求中标签优先的规则保持一致。
  const [draft, setDraft] = useState(tag || params.get("q") || "");
  // null 表示尚未提交过搜索，此时不发请求；空字符串是合法关键字，表示浏览全部。
  const keyword=params.has("q")?params.get("q")!:null;
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);

  const query = useQuery({
    queryKey: ["catalog-search", keyword, tag, page, pageSize],
    queryFn: ({signal}) =>
      apiRequest<Page<Media>>(
        `/complex/search?${tag?"tag="+encodeURIComponent(tag):"q="+encodeURIComponent(keyword??"")}&page=${page}&page_size=${pageSize}`,{signal},
      ),
    enabled: keyword !== null || !!tag,
  });

  const notSearched = keyword === null && !tag;
  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;

  /** 未修改标签时保留精确筛选；修改输入后从第一页搜索新关键字。 */
  function commit() {
    setPage(1);
    const value = draft.trim();
    setParams(tag && value === tag ? { tag } : { q: value });
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
      <form className="filter-toolbar" role="search" onSubmit={submit}>
        <Grid.Row gutter={[12, 12]} justify="start" align="center">
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="filter-labeled search-field">
              <span className="filter-label">关键词</span>
              <Input
                className="search-input"
                value={draft}
                onChange={setDraft}
                onPressEnter={commit}
                allowClear
                aria-label="搜索关键字"
                placeholder="番号、标题或译名"
                suffix={<IconSearch />}
              />
            </div>
          </Grid.Col>
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="filter-actions">
              <Button type="primary" htmlType="submit">
                搜索
              </Button>
            </div>
          </Grid.Col>
        </Grid.Row>
      </form>
        <div className="toolbar-note search-results-note">{tag?`标签：${tag} · 共 ${total} 条结果`:notSearched ? "回车或点击搜索" : `共 ${total} 条结果`}</div>
      <MediaCardGrid
        items={items}
        query={{
          isLoading: !notSearched && query.isLoading,
          error: notSearched ? null : query.error,
          refetch: query.refetch,
        }}
        empty={notSearched || undefined}
        paginated={!notSearched}
        emptyText={notSearched ? "输入番号、标题或译名后搜索" : `未找到与「${tag||keyword}」匹配的内容`}
        total={total}
        page={page}
        pageSize={pageSize}
        onPageChange={setPage}
        onPageSizeChange={changePageSize}
      />
    </section>
  );
}
