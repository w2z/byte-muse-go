import { CatalogSubscriptionButton } from "../../shared/ui/CatalogSubscriptionButton";
import { CatalogFilters, catalogFilterParams } from "../../shared/ui/CatalogFilters";
import { Button, Grid, Radio } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { Media } from "../../shared/api/types";
import { DEFAULT_PAGE_SIZE } from "../../shared/ui/ListPagination";
import { MediaCardGrid } from "../../shared/ui/MediaCardGrid";
import { PageHeader } from "../../shared/ui/PageHeader";
import "./RankPage.css";

/** 一个榜单周期选项：value 不经过任何映射，直接作为 GET /ranks 的 type 参数发给服务端。 */
type RankPeriod = {
  value: string;
  label: string;
};

/** 一个榜单来源：只用于前端分组展示，来源本身不参与请求。 */
type RankSource = {
  value: string;
  label: string;
  /** 该来源下的周期选项，选项的 value 就是服务端的 rank_type。 */
  periods: RankPeriod[];
  /** 切到该来源时使用的默认周期，避免上一个来源的周期值残留成非法组合。 */
  defaultPeriod: string;
};

/** JavDB 热门榜的三个周期，value 即服务端 rank_entries.rank_type。 */
const JAVDB_PERIODS: RankPeriod[] = [
  { value: "daily", label: "日榜" },
  { value: "weekly", label: "周榜" },
  { value: "monthly", label: "月榜" },
];

/**
 * JavLibrary 想要榜只有一个榜单键，没有可切换的周期。
 *
 * 服务端 ports.CollectionRankKey 对非 javdb 来源返回“来源:周期”，JavLibrary 的 rank_type 就是
 * javlibrary:wanted；这里直接使用该键，页面不再提供 1~5 之类的分段选项。
 */
const JAVLIB_PERIODS: RankPeriod[] = [{ value: "javlibrary:wanted", label: "想要榜" }];

/** 榜单来源与各来源下的周期，顺序与对标站一致。 */
const RANK_SOURCES: RankSource[] = [
  { value: "javdb", label: "JavDB 热门榜", periods: JAVDB_PERIODS, defaultPeriod: "daily" },
  { value: "javlib", label: "JavLibrary 想要榜", periods: JAVLIB_PERIODS, defaultPeriod: "javlibrary:wanted" },
];

/**
 * 榜单页。
 *
 * 页面标题一行，标题下方是一条筛选条（榜单来源 + 订阅状态/影片类型/VR + 搜索/重置），
 * 再往下是响应式番号卡片网格，即 MediaCardGrid 的 columns="wide"（1024px 提前到 5 列），
 * 名次由页面通过 renderMeta 注入，页面不自行实现卡片样式与分页条。
 *
 * 只有来源存在多个周期时才渲染“榜单周期”分段按钮；JavLibrary 想要榜只有单一榜单键，
 * 直接在一个页面里分页展示，不再按 1~5 分段。
 *
 * rank_type 取值：来源 value 只是前端分组，周期 value 才是服务端 rank_type。
 * JavDB 为 daily / weekly / monthly，JavLibrary 为 javlibrary:wanted（后端 ports.CollectionRankKey）。
 *
 * 订阅状态、类型和 VR 在服务端过滤后分页；筛选后的序号不表示原榜单名次。
 */
export function RankPage() {
  const [source, setSource] = useState(RANK_SOURCES[0].value);
  const [period, setPeriod] = useState(RANK_SOURCES[0].defaultPeriod);
  const [subscription, setSubscription] = useState("");
  const [videoType, setVideoType] = useState("");
  const [vr, setVR] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);

  const currentSource = RANK_SOURCES.find((item) => item.value === source) ?? RANK_SOURCES[0];

  const listPath = `/ranks?type=${period}&${catalogFilterParams(subscription, videoType, vr).replace(/^&/, "")}`;
  const query = useQuery({
    queryKey: ["ranks", period, page, pageSize, subscription, videoType, vr],
    queryFn: () => apiRequest<Page<Media>>(`/ranks?type=${period}&page=${page}&page_size=${pageSize}${catalogFilterParams(subscription, videoType, vr)}`),
  });
  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;

  /** 切换榜单来源：同时把周期换成新来源的默认值，并回到第一页。 */
  function changeSource(next: string) {
    const target = RANK_SOURCES.find((item) => item.value === next) ?? RANK_SOURCES[0];
    setSource(target.value);
    setPeriod(target.defaultPeriod);
    setPage(1);
  }

  /** 切换榜单周期：换了一批数据，页码必须回到第一页。 */
  function changePeriod(next: string) {
    setPeriod(next);
    setPage(1);
  }

  /** 切换每页条数：页长变了，页码必须回到第一页，否则名次换算会和实际返回的数据错位。 */
  function changePageSize(next: number) {
    setPageSize(next);
    setPage(1);
  }

  /** 名次按服务端分页换算：第 page 页第 index 项的名次 = (page-1)*pageSize + index + 1。 */
  function rankOf(index: number) {
    return (page - 1) * pageSize + index + 1;
  }

  return (
    <section>
      <PageHeader title="榜单" />
      <div className="rank-filter-bar filter-toolbar" role="search">
          <Grid.Row gutter={[12, 12]} justify="start" align="center">
            <Grid.Col xs={24} sm={12} md={8} xl={4}>
              <div className="filter-field">
                <span className="filter-label">榜单来源</span>
                <Radio.Group
                  className="rank-filter"
                  type="button"
                  name="rank-source"
                  aria-label="榜单来源"
                  value={source}
                  onChange={changeSource}
                  options={RANK_SOURCES.map(({ value, label }) => ({ value, label }))}
                />
              </div>
            </Grid.Col>
            {currentSource.periods.length > 1 && (
              <Grid.Col xs={24} sm={12} md={8} xl={4}>
                <div className="filter-field">
                  <span className="filter-label">榜单周期</span>
                  <Radio.Group
                    className="rank-filter"
                    type="button"
                    name="rank-period"
                    aria-label="榜单周期"
                    value={period}
                    onChange={changePeriod}
                    options={currentSource.periods}
                  />
                </div>
              </Grid.Col>
            )}
          <CatalogFilters vr={vr} onVRChange={(value) => { setVR(value); setPage(1); }} subscription={subscription} videoType={videoType} onSubscriptionChange={(value) => { setSubscription(value); setPage(1); }} onVideoTypeChange={(value) => { setVideoType(value); setPage(1); }} />
          <Grid.Col xs={24} sm={12} md={8} xl={4}><div className="filter-actions"><Button type="primary" onClick={() => { if (page === 1) void query.refetch(); else setPage(1); }}>搜索</Button><Button onClick={() => { if (!subscription && !videoType && !vr && page === 1) void query.refetch(); setSubscription(""); setVideoType(""); setVR(""); setPage(1); }}>重置</Button></div></Grid.Col>
          <Grid.Col xs={24} sm={12} md={8} xl={4} className="catalog-subscription-column"><div className="catalog-subscription-actions"><CatalogSubscriptionButton items={items} total={total} listPath={listPath} disabled={query.isFetching || !!query.error} onComplete={() => setPage(1)} /></div></Grid.Col>
          </Grid.Row>
      </div>
      <MediaCardGrid
        items={items}
        query={query}
        emptyText="暂无榜单数据"
        total={total}
        page={page}
        pageSize={pageSize}
        onPageChange={setPage}
        onPageSizeChange={changePageSize}
        columns="wide"
        renderMeta={(_media, index) => <div className="code-card-meta">{subscription || videoType || vr ? "筛选结果第 " : "第 "}{rankOf(index)}{subscription || videoType || vr ? " 项" : " 名"}</div>}
      />
    </section>
  );
}
