import { CatalogFilters, catalogFilterParams } from "../../shared/ui/CatalogFilters";
import { Button, Grid } from "@arco-design/web-react";
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

/**
 * JavDB 热门榜的三个周期。
 *
 * 依据对标站榜单页编译产物 assets/Rank-BMeKi9W2.js：来源为 javdb 时渲染三个分段按钮
 * 日榜 / 周榜 / 月榜，点击后把 type 置为 daily / weekly / monthly 并发起 GET /ranks?type=。
 */
const JAVDB_PERIODS: RankPeriod[] = [
  { value: "daily", label: "日榜" },
  { value: "weekly", label: "周榜" },
  { value: "monthly", label: "月榜" },
];

/**
 * JavLibrary 想要榜的五个周期。
 *
 * 同样依据对标站编译产物：来源为 javlib 时分段按钮的取值就是 "1" 到 "5"，页面直接把它们
 * 作为 type 发出，所以这里保持原始取值，不翻译成别的语义。
 */
const JAVLIB_PERIODS: RankPeriod[] = [
  { value: "1", label: "1" },
  { value: "2", label: "2" },
  { value: "3", label: "3" },
  { value: "4", label: "4" },
  { value: "5", label: "5" },
];

/** 榜单来源与各来源下的周期，顺序与对标站一致。 */
const RANK_SOURCES: RankSource[] = [
  { value: "javdb", label: "JavDB 热门榜", periods: JAVDB_PERIODS, defaultPeriod: "daily" },
  { value: "javlib", label: "JavLibrary 想要榜", periods: JAVLIB_PERIODS, defaultPeriod: "1" },
];

/**
 * 榜单页。
 *
 * 布局依据对标站的榜单页编译产物 assets/Rank-BMeKi9W2.js：
 * 页面标题一行，标题下方是一条筛选条（榜单来源 + 榜单周期），再往下是响应式番号卡片网格，
 * 列数 grid-cols-1 / sm:2 / md:3 / lg:5，即 MediaCardGrid 的 columns="wide"（1024px 提前到 5 列），
 * 名次由页面通过 renderMeta 注入，页面不自行实现卡片样式与分页条。
 *
 * rank_type 取值依据（不靠猜测）：
 * - 服务端 backend/internal/application/catalog_queries.go 的 Rank 只做 TrimSpace，空串回落成 "monthly"，
 *   没有任何白名单或取值范围校验。
 * - 仓储 backend/internal/platform/database/catalog_queries.go 的 Rank 用 `WHERE r.rank_type = ?`
 *   直接等值匹配 rank_entries.rank_type，取到什么完全由旧库 cache 表 namespace='rank' 的 key 决定。
 * - 旧库 未加密代码/最终源码/lady.db 中该表的 key 实际为 daily / weekly / monthly / 1 / 2 / 3 / 4 / 5
 *   （另有 actors 和若干厂牌 key），因此 daily、weekly、monthly、1~5 都是服务端真实支持的取值。
 * - HTTP 层 backend/internal/transport/httpapi/handler.go 的 listRank 也只透传 type，没有枚举排行榜类型的接口，
 *   所以前端无法动态拉取选项，只能使用上述旧库中已确认存在的取值。
 *
 * 订阅状态和类型在服务端过滤后分页；筛选后的序号不表示原榜单名次。
 */
export function RankPage() {
  const [source, setSource] = useState(RANK_SOURCES[0].value);
  const [period, setPeriod] = useState(RANK_SOURCES[0].defaultPeriod);
  const [subscription, setSubscription] = useState("");
  const [videoType, setVideoType] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);

  const currentSource = RANK_SOURCES.find((item) => item.value === source) ?? RANK_SOURCES[0];

  const query = useQuery({
    queryKey: ["ranks", period, page, pageSize, subscription, videoType],
    queryFn: () => apiRequest<Page<Media>>(`/ranks?type=${period}&page=${page}&page_size=${pageSize}${catalogFilterParams(subscription, videoType)}`),
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
      <div className="page-toolbar">
        <div className="rank-filter-bar filter-toolbar" role="search">
          <Grid.Row gutter={[12, 12]} justify="start" align="center">
            <Grid.Col xs={24} sm={12} md={8} xl={4}>
              <div className="filter-field"><span className="filter-label">榜单来源</span><div className="rank-filter" role="group" aria-label="榜单来源">
                {RANK_SOURCES.map((item) => (
                  <button
                    key={item.value}
                    type="button"
                    aria-pressed={item.value === source}
                    onClick={() => changeSource(item.value)}
                  >
                    {item.label}
                  </button>
                ))}
              </div>
              </div>
            </Grid.Col>
            <Grid.Col xs={24} sm={12} md={8} xl={4}>
              <div className="filter-field"><span className="filter-label">榜单周期</span><div className="rank-filter" role="group" aria-label="榜单周期">
                {currentSource.periods.map((item) => (
                  <button
                    key={item.value}
                    type="button"
                    aria-pressed={item.value === period}
                    onClick={() => changePeriod(item.value)}
                  >
                    {item.label}
                  </button>
                ))}
              </div>
              </div>
            </Grid.Col>
          <CatalogFilters subscription={subscription} videoType={videoType} onSubscriptionChange={(value) => { setSubscription(value); setPage(1); }} onVideoTypeChange={(value) => { setVideoType(value); setPage(1); }} />
          <Grid.Col xs={24} sm={12} md={8} xl={4}><div className="filter-actions"><Button type="primary" onClick={() => { if (page === 1) void query.refetch(); else setPage(1); }}>搜索</Button><Button onClick={() => { if (!subscription && !videoType && page === 1) void query.refetch(); setSubscription(""); setVideoType(""); setPage(1); }}>重置</Button></div></Grid.Col>
          </Grid.Row>
        </div>
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
        renderMeta={(_media, index) => <div className="code-card-meta">{subscription || videoType ? "筛选结果第 " : "第 "}{rankOf(index)}{subscription || videoType ? " 项" : " 名"}</div>}
      />
    </section>
  );
}
