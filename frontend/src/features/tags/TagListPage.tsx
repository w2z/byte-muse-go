import { Button, Divider, Input, Select, Slider, Tabs, Grid } from "@arco-design/web-react";
import { Link } from "react-router-dom";
import { TagSubscriptionActions } from "./TagSubscriptionActions";
import { useQuery } from "@tanstack/react-query";
import { useState, type CSSProperties } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { CatalogTag } from "../../shared/api/types";
import { InfoCard } from "../../shared/ui/InfoCard";
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS, ListPagination } from "../../shared/ui/ListPagination";
import { PageState } from "../../shared/ui/PageState";
import { PageHeader } from "../../shared/ui/PageHeader";

/** 全部标签使用大页浏览，订阅中保持常规分页。 */
const ALL_TAG_PAGE_SIZES = [100, 200, 300, 400, 500];

/** 标签目录按类型与订阅状态在服务端分页；标题跳转精确标签搜索。 */
export function TagListPage() {
  // 默认期望每行五个，CSS 按可用宽度减少实际列数；调整布局不重新请求数据。
  const [columns, setColumns] = useState(5);
  const [draft, setDraft] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [activePageSize, setActivePageSize] = useState(DEFAULT_PAGE_SIZE);
  const [allPageSize, setAllPageSize] = useState(ALL_TAG_PAGE_SIZES[0]);
  const [tab,setTab]=useState("active");
  const pageSize = tab === "all" ? allPageSize : activePageSize;
  const [category,setCategory]=useState("");
  /** 下拉框与卡片类型共用筛选入口，避免停留在旧结果页码。 */
  function filterByCategory(value?: string) {
    setCategory(value ?? "");
    setPage(1);
  }
  const query = useQuery({
    queryKey: ["tags", search, page, pageSize, tab, category],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams({page: String(page), page_size: String(pageSize), subscription:tab});
      if (search) params.set("search", search);
      if (category) params.set("category",category);
      return apiRequest<Page<CatalogTag>>("/tags?" + params.toString(), { signal });
    },
  });
  /** 提交名称筛选；空条件或相同条件也显式刷新，翻页后搜索回到首页。 */
  function applySearch() {
    const nextSearch = draft.trim();
    setSearch(nextSearch);
    setPage(1);
    if (nextSearch === search && page === 1) void query.refetch();
  }
  /** 清空筛选并回到首页；条件已为空时仍刷新当前列表。 */
  function resetFilters() {
    setDraft("");
    setSearch("");
    setCategory("");
    setPage(1);
    if (!search && !category && page === 1) void query.refetch();
  }
  const items = query.data?.items ?? [];
  const pagination = <ListPagination page={page} pageSize={pageSize} pageSizeOptions={tab === "all" ? ALL_TAG_PAGE_SIZES : PAGE_SIZE_OPTIONS} total={query.data?.total ?? 0} onChange={setPage} onPageSizeChange={(size) => {if (tab === "all") setAllPageSize(size); else setActivePageSize(size); setPage(1);}} plain />;
  return (
    <section className="tag-page">
      <PageHeader title="标签" />
      <Tabs style={{ marginBottom: 16 }} activeTab={tab} onChange={next=>{setTab(next);setPage(1);}}><Tabs.TabPane key="active" title="订阅中"/><Tabs.TabPane key="all" title="全部标签"/></Tabs>
      <form className="filter-toolbar tag-toolbar" role="search" onSubmit={(event) => {event.preventDefault(); applySearch();}}>
        <Grid.Row gutter={[12, 12]} justify="start" align="center">
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="tag-type-filter filter-labeled">
              <span className="filter-label">标签类型</span>
              <Select aria-label="标签类型" placeholder="全部类型" value={category || undefined} allowClear onChange={filterByCategory} onClear={() => filterByCategory()} options={[{label:"全部类型",value:""},...['主题','角色','服装','体型','行为','玩法','类别','未分类'].map(value=>({label:value,value}))]}/>
            </div>
          </Grid.Col>
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="filter-labeled tag-search">
              <span className="filter-label">标签名称</span>
              <Input className="tag-name-input" aria-label="搜索标签" placeholder="标签名称" value={draft} onChange={setDraft} allowClear onClear={() => {setDraft(""); setSearch(""); setPage(1);}} />
            </div>
          </Grid.Col>
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="filter-actions">
              <Button type="primary" htmlType="submit" loading={query.isFetching}>搜索</Button>
              <Button htmlType="button" onClick={resetFilters}>重置</Button>
            </div>
          </Grid.Col>
          <Grid.Col xs={24} sm={12} md={8} xl={4} className="tag-column-cell">
            <div className="tag-column-control" role="group" aria-label="每行显示个数">
              <span>每行显示个数</span>
              <Slider className="tag-column-slider" min={1} max={10} step={1} value={columns} onChange={value => setColumns(value as number)} formatTooltip={value => `${value} 个`} showInput={{ 'aria-label': '每行显示个数', style: { width: 64 } }} />
            </div>
          </Grid.Col>
        </Grid.Row>
      </form>
      <Divider style={{ margin: "0 0 16px" }} />
      <div className="tag-list-scroll" key={[tab, search, category, page, pageSize].join("|")} role="region" aria-label="标签列表" aria-busy={query.isFetching} tabIndex={0}>
      <PageState isLoading={query.isFetching} error={query.error} isEmpty={!query.isFetching && !query.error && items.length === 0}
        emptyText={search||category ? "未找到匹配的标签" : tab==="active"?"暂无已订阅标签":"暂无标签"} onRetry={() => void query.refetch()}>
        <div className="tag-card-grid" style={{ "--tag-columns": columns } as CSSProperties}>
          {items.map((tag) => <InfoCard key={tag.name} actionsPlacement="end" title={<Link className="info-card-title-link" to={"/search?tag="+encodeURIComponent(tag.name)}>{tag.name}</Link>} meta={<><span>关联影片 {tag.media_count} 部</span>{tag.limit_date&&<span>追新起始：{tag.limit_date}</span>}</>} actions={<>
            {tag.category && <button type="button" className="info-card-meta tag-card-category" aria-label={`筛选类型：${tag.category}`} onClick={() => filterByCategory(tag.category)}>{tag.category}</button>}
            <TagSubscriptionActions tag={tag} onChanged={()=>setPage(1)}/>
          </>} />)}
        </div>
      </PageState>
      </div>
      {pagination}
    </section>
  );
}
