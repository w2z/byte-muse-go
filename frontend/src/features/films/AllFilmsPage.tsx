import { Button, Input, Select } from "@arco-design/web-react";
import { IconSearch } from "@arco-design/web-react/icon";
import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { apiRequest, type Page } from "../../shared/api/client";
import type { DownloadStatus, LibraryStatus, Media, SubscriptionStatus, VideoType } from "../../shared/api/types";
import { DEFAULT_PAGE_SIZE } from "../../shared/ui/ListPagination";
import { MediaCardGrid } from "../../shared/ui/MediaCardGrid";
import { PageHeader } from "../../shared/ui/PageHeader";

const subscriptionOptions: { label: string; value: string }[] = [
  { label: "全部订阅", value: "" }, { label: "已订阅", value: "active" }, { label: "未订阅", value: "none" },
];
const downloadOptions: { label: string; value: string }[] = [
  { label: "全部下载状态", value: "" }, { label: "排队", value: "queued" }, { label: "搜索中", value: "searching" }, { label: "已提交", value: "submitted" }, { label: "下载中", value: "downloading" }, { label: "已完成", value: "completed" }, { label: "失败", value: "failed" }, { label: "未知", value: "unknown" },
];
const libraryOptions: { label: string; value: string }[] = [
  { label: "全部媒体库状态", value: "" }, { label: "已入库", value: "present" }, { label: "未入库", value: "absent" }, { label: "未知", value: "unknown" },
];

/** 类型由服务端字段筛选；未分类对应数据库 NULL。 */
const videoTypeOptions = [
  { label: "全部类型", value: "" }, { label: "有码", value: "censored" },
  { label: "无码", value: "uncensored" }, { label: "无码破解", value: "uncensored_cracked" },
  { label: "流出", value: "leaked" }, { label: "未分类", value: "unknown" },
];

/** 数据库中的完整影片目录，筛选条件由服务端分页查询。 */
export function AllFilmsPage() {
  const [draft, setDraft] = useState("");
  const [search, setSearch] = useState("");
  const [subscription, setSubscription] = useState<SubscriptionStatus | "">("");
  const [download, setDownload] = useState<DownloadStatus | "">("");
  const [library, setLibrary] = useState<LibraryStatus | "">("");
  const [videoType, setVideoType] = useState<VideoType | "unknown" | "">("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const query = useQuery({
    queryKey: ["media", "all", search, subscription, download, library, videoType, page, pageSize],
    queryFn: () => {
      const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
      if (search) params.set("search", search);
      if (subscription) params.set("subscription", subscription);
      if (download) params.set("download", download);
      if (library) params.set("library", library);
      if (videoType) params.set("video_type", videoType);
      return apiRequest<Page<Media>>(`/media?${params.toString()}`);
    },
  });
  const commit = () => { setPage(1); setSearch(draft.trim()); };
  const submit = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); commit(); };
  const changeFilter = (setter: (value: never) => void, value: string) => { setPage(1); setter(value as never); };
  const changePageSize = (next: number) => { setPageSize(next); setPage(1); };
  return (
    <section>
      <PageHeader title="所有影片" />
      <form className="page-toolbar" role="search" onSubmit={submit}>
        <div className="toolbar-group search-field">
          <Input value={draft} onChange={setDraft} onPressEnter={commit} allowClear aria-label="搜索影片" placeholder="名称或番号" suffix={<IconSearch />} />
          <Button type="primary" htmlType="submit">搜索</Button>
        </div>
        <Select aria-label="订阅状态筛选" value={subscription} onChange={(value) => changeFilter(setSubscription, value)} options={subscriptionOptions} />
        <Select aria-label="影片类型筛选" value={videoType} onChange={(value) => changeFilter(setVideoType, value)} options={videoTypeOptions} />
        <Select aria-label="下载状态筛选" value={download} onChange={(value) => changeFilter(setDownload, value)} options={downloadOptions} />
        <Select aria-label="媒体库状态筛选" value={library} onChange={(value) => changeFilter(setLibrary, value)} options={libraryOptions} />
      </form>
      <MediaCardGrid items={query.data?.items ?? []} query={query} emptyText="暂无影片" total={query.data?.total ?? 0} page={page} pageSize={pageSize} onPageChange={setPage} onPageSizeChange={changePageSize} />
    </section>
  );
}
