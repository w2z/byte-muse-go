import { CatalogFilters, videoTypeOptions } from "../../shared/ui/CatalogFilters";
import { Button, Input, Select, Tag, Grid } from "@arco-design/web-react";
import { IconSearch } from "@arco-design/web-react/icon";
import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { DownloadStatus, LibraryStatus, Media, SubscriptionStatus, VideoType } from "../../shared/api/types";
import { DEFAULT_PAGE_SIZE } from "../../shared/ui/ListPagination";
import { MediaCardGrid } from "../../shared/ui/MediaCardGrid";
import { PageHeader } from "../../shared/ui/PageHeader";

const downloadOptions: { label: string; value: string }[] = [
  { label: "全部下载状态", value: "" }, { label: "排队", value: "queued" }, { label: "搜索中", value: "searching" }, { label: "已提交", value: "submitted" }, { label: "下载中", value: "downloading" }, { label: "已完成", value: "completed" }, { label: "失败", value: "failed" }, { label: "未知", value: "unknown" },
];
const libraryOptions: { label: string; value: string }[] = [
  { label: "全部媒体库状态", value: "" }, { label: "已入库", value: "present" }, { label: "未入库", value: "absent" }, { label: "未知", value: "unknown" },
];


/** 数据库中的完整影片目录，筛选条件由服务端分页查询。 */
export function AllFilmsPage() {
  const [draft, setDraft] = useState("");
  const [search, setSearch] = useState("");
  const [subscription, setSubscription] = useState<SubscriptionStatus | "">("");
  const [download, setDownload] = useState<DownloadStatus | "">("");
  const [library, setLibrary] = useState<LibraryStatus | "">("");
  const [videoType, setVideoType] = useState<VideoType | "unknown" | "">("");
  const [vr, setVR] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const query = useQuery({
    queryKey: ["media", "all", search, subscription, download, library, videoType, vr, page, pageSize],
    queryFn: () => {
      const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
      if (search) params.set("search", search);
      if (subscription) params.set("subscription", subscription);
      if (download) params.set("download", download);
      if (library) params.set("library", library);
      if (videoType) params.set("video_type", videoType);
      if (vr) params.set("vr", vr);
      return apiRequest<Page<Media>>(`/media?${params.toString()}`);
    },
  });
  /** 相同条件（包括空条件）也重新查询；条件变化时由查询键触发第一页请求。 */
  const commit = () => {
    const nextSearch = draft.trim();
    if (page === 1 && search === nextSearch) {
      void query.refetch();
      return;
    }
    setPage(1);
    setSearch(nextSearch);
  };
  const submit = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); commit(); };
  const changeFilter = (setter: (value: never) => void, value: string) => { setPage(1); setter(value as never); };
  const changePageSize = (next: number) => { setPageSize(next); setPage(1); };
  /** 清空搜索草稿及已生效的筛选，保留用户选择的每页条数。 */
  const reset = () => {
    setDraft("");
    setSearch("");
    setSubscription("");
    setDownload("");
    setLibrary("");
    setVideoType("");
    setVR("");
    setPage(1);
  };
  return (
    <section>
      <PageHeader title="所有影片" />
      <form className="filter-toolbar all-films-toolbar" role="search" onSubmit={submit}>
        <Grid.Row gutter={[12, 12]} justify="start" align="center">
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="filter-labeled"><span className="filter-label">关键词</span><Input value={draft} onChange={setDraft} allowClear aria-label="搜索影片" placeholder="名称或番号" suffix={<IconSearch />} /></div>
          </Grid.Col>
          <CatalogFilters subscription={subscription} videoType={videoType} vr={vr} onVRChange={(value) => changeFilter(setVR, value)} onSubscriptionChange={(value) => changeFilter(setSubscription, value)} onVideoTypeChange={(value) => changeFilter(setVideoType, value)} />
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="filter-labeled"><span className="filter-label">下载状态</span><Select aria-label="下载状态筛选" value={download} onChange={(value) => changeFilter(setDownload, value)} options={downloadOptions} /></div>
          </Grid.Col>
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="filter-labeled"><span className="filter-label">媒体库状态</span><Select aria-label="媒体库状态筛选" value={library} onChange={(value) => changeFilter(setLibrary, value)} options={libraryOptions} /></div>
          </Grid.Col>
          <Grid.Col xs={24} sm={12} md={8} xl={4}>
            <div className="filter-actions all-films-actions">
              <Button type="primary" htmlType="submit">搜索</Button>
              <Button htmlType="button" onClick={reset}>重置</Button>
            </div>
          </Grid.Col>
        </Grid.Row>
      </form>
      <MediaCardGrid items={query.data?.items ?? []} query={query} emptyText="暂无影片" total={query.data?.total ?? 0} page={page} pageSize={pageSize} onPageChange={setPage} onPageSizeChange={changePageSize}
        renderMeta={(media) => <div><Tag color={media.video_type ? "arcoblue" : undefined}>{videoTypeOptions.find((option) => option.value === (media.video_type ?? "unknown"))?.label}</Tag></div>}
      />
    </section>
  );
}
