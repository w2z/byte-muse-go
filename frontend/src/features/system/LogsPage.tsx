import { Button, DatePicker, Divider, Input, Select, Grid } from "@arco-design/web-react";
import { IconDelete, IconSearch } from "@arco-design/web-react/icon";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import dayjs from "dayjs";
import { clampLogTimeRange, getDisabledLogTime, getLogTimeShortcuts, isFutureLogDate } from "./logTimeRange";
import { formatLogMessage, shouldDisplayLog } from "./logMessage";
import { apiRequest, type Page } from "../../shared/api/client";
import type { LogRecord } from "../../shared/api/types";
import { AppDialog } from "../../shared/ui/AppDialog";
import { useFeedbackMessage } from "../../shared/ui/FeedbackMessage";
import { ListPagination } from "../../shared/ui/ListPagination";
import { PageHeader } from "../../shared/ui/PageHeader";
import { PageState } from "../../shared/ui/PageState";
import "./LogsPage.css";

const CATEGORIES = ["采集同步", "订阅查询", "下载", "媒体库", "通知", "系统", "Agent", "其他"] as const;
const LOG_PAGE_SIZE_OPTIONS = [100, 200, 300, 400, 500];
const DEFAULT_LOG_PAGE_SIZE = LOG_PAGE_SIZE_OPTIONS[0];
type Category = (typeof CATEGORIES)[number];
type Level = LogRecord["level"];

function toISOString(value: string): string | undefined {
  const parsed = dayjs(value);
  return parsed.isValid() ? parsed.toISOString() : undefined;
}

function rangeToStrings(range: [dayjs.Dayjs, dayjs.Dayjs] | undefined): [string, string] | undefined {
  return range ? [range[0].toISOString(), range[1].toISOString()] : undefined;
}

function levelClass(level: Level): string {
  return level === "error" ? "error" : level === "warning" ? "warn" : level === "debug" ? "other" : "info";
}

function formatTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false });
}

/** 系统日志页面：筛选区和日志流共用白色内容区，筛选规则由后端统一执行。 */
export function LogsPage() {
  const queryClient = useQueryClient();
  const [clearConfirmVisible, setClearConfirmVisible] = useState(false);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_LOG_PAGE_SIZE);
  const [draftKeyword, setDraftKeyword] = useState("");
  const [draftCategory, setDraftCategory] = useState<Category | "">("");
  const [draftLevel, setDraftLevel] = useState<Level | "">("");
  const [draftTimeRange, setDraftTimeRange] = useState<[string, string] | undefined>();
  const [keyword, setKeyword] = useState("");
  const [category, setCategory] = useState<Category | "">("");
  const [level, setLevel] = useState<Level | "">("");
  const [timeRange, setTimeRange] = useState<[string, string] | undefined>();
  const [message, messageHolder] = useFeedbackMessage();

  const query = useQuery({
    queryKey: ["logs", page, pageSize, keyword, category, level, timeRange],
    queryFn: () => {
      const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
      if (keyword) params.set("keyword", keyword);
      if (category) params.set("category", category);
      if (level) params.set("level", level);
      if (timeRange?.[0]) params.set("start_time", toISOString(timeRange[0]) ?? timeRange[0]);
      if (timeRange?.[1]) params.set("end_time", toISOString(timeRange[1]) ?? timeRange[1]);
      return apiRequest<Page<LogRecord>>(`/logs?${params.toString()}`);
    },
    refetchInterval: 1000,
    refetchIntervalInBackground: true,
  });
  const clearMutation = useMutation({
    mutationFn: () => {
      const params = new URLSearchParams();
      if (keyword) params.set("keyword", keyword);
      if (category) params.set("category", category);
      if (level) params.set("level", level);
      if (timeRange?.[0]) params.set("start_time", toISOString(timeRange[0]) ?? timeRange[0]);
      if (timeRange?.[1]) params.set("end_time", toISOString(timeRange[1]) ?? timeRange[1]);
      const suffix = params.toString() ? `?${params.toString()}` : "";
      return apiRequest<void>(`/logs${suffix}`, { method: "DELETE" });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["logs"] });
      setClearConfirmVisible(false);
      message.success("日志已清空");
    },
    onError: (error: Error) => message.error(error.message),
  });
  const records = query.data?.items ?? [];
  const visibleRecords = records.filter((record) => shouldDisplayLog(record.message, record.attrs));
  const applyFilters = () => {
    const nextKeyword = draftKeyword.trim();
    const unchanged = nextKeyword === keyword && draftCategory === category && draftLevel === level && JSON.stringify(draftTimeRange) === JSON.stringify(timeRange) && page === 1;
    setKeyword(nextKeyword);
    setCategory(draftCategory);
    setLevel(draftLevel);
    setTimeRange(draftTimeRange);
    setPage(1);
    if (unchanged) void query.refetch();
  };
  const resetFilters = () => {
    setDraftKeyword("");
    setDraftCategory("");
    setDraftLevel("");
    setDraftTimeRange(undefined);
    setKeyword("");
    setCategory("");
    setLevel("");
    setTimeRange(undefined);
    setPage(1);
  };
  /** 切换每页条数：页长变了，页码必须回到第一页，否则会请求到越界页。 */
  const changePageSize = (next: number) => {
    setPageSize(next);
    setPage(1);
  };
  /** 打开清空确认弹窗；真正的删除只在弹窗里点「确定清空」时执行。
      清空范围随当前已生效的筛选条件变化，所以文案要区分「当前筛选结果」和「全部日志」。 */
  const confirmClearLogs = () => {
    if (clearMutation.isPending) return;
    setClearConfirmVisible(true);
  };
  /** 弹窗关闭：请求进行中不允许关（点遮罩、ESC、右上角 X 都走这里），避免请求结果无处反馈。 */
  const closeClearConfirm = () => {
    if (clearMutation.isPending) return;
    setClearConfirmVisible(false);
  };

  return (
    <section className="logs-page">
      <PageHeader title="日志" />
      <PageState isLoading={query.isPending && !query.data} error={query.error} isEmpty={false} onRetry={() => void query.refetch()}>
        <div className="logs-content-card">
          {/* 复用公共筛选布局，日期范围优先保留输入宽度，空间不足时在标签下方展开。 */}
          <div className="logs-filters">
            <Grid.Row gutter={[12, 12]} justify="start" align="center">
              <Grid.Col xs={24} sm={12} md={8} xl={4}>
                <div className="logs-field filter-field">
                  <span className="logs-filter-label filter-label">关键词</span>
                  <Input allowClear prefix={<IconSearch />} placeholder="搜索日志..." value={draftKeyword} onChange={setDraftKeyword} className="logs-keyword filter-control" />
                </div>
              </Grid.Col>
              <Grid.Col xs={24} sm={12} md={8} xl={4}>
                <div className="logs-field filter-field">
                  <span className="logs-filter-label filter-label">分类</span>
                  <Select aria-label="日志分类" placeholder="全部分类" value={draftCategory || undefined} allowClear onChange={(value) => setDraftCategory((value as Category | undefined) ?? "")} className="logs-select filter-control">
                    {CATEGORIES.map((item) => <Select.Option key={item} value={item}>{item}</Select.Option>)}
                  </Select>
                </div>
              </Grid.Col>
              <Grid.Col xs={24} sm={12} md={8} xl={4}>
                <div className="logs-field filter-field">
                  <span className="logs-filter-label filter-label">级别</span>
                  <Select aria-label="日志级别" placeholder="全部级别" value={draftLevel || undefined} allowClear onChange={(value) => setDraftLevel((value as Level | undefined) ?? "")} className="logs-select filter-control">
                    <Select.Option value="info">INFO</Select.Option>
                    <Select.Option value="warning">WARNING</Select.Option>
                    <Select.Option value="error">ERROR</Select.Option>
                    <Select.Option value="debug">DEBUG</Select.Option>
                  </Select>
                </div>
              </Grid.Col>
              <Grid.Col xs={24} sm={12} md={8} xl={4}>
                <div className="logs-field filter-field filter-field--range">
                  <span className="logs-filter-label filter-label">时间段</span>
                  <DatePicker.RangePicker
                    aria-label="日志时间段"
                    showTime={{ format: "HH:mm:ss" }}
                    format="YYYY-MM-DD HH:mm:ss"
                    value={draftTimeRange?.map((value) => dayjs(value))}
                    shortcuts={getLogTimeShortcuts()}
                    disabledDate={(current) => isFutureLogDate(current)}
                    disabledTime={(current) => getDisabledLogTime()(current)}
                    onChange={(_dateStrings, values) => setDraftTimeRange(values?.length === 2 && values[0] && values[1] ? rangeToStrings(clampLogTimeRange([values[0], values[1]])) : undefined)}
                    allowClear
                    className="logs-time-range filter-control"
                  />
                </div>
              </Grid.Col>
              <Grid.Col xs={24} sm={12} md={8} xl={4}>
                <div className="filter-actions">
                  <Button type="primary" icon={<IconSearch />} onClick={applyFilters}>查询</Button>
                  <Button onClick={resetFilters}>重置</Button>
                <Button className="logs-clear-button" status="danger" icon={<IconDelete />} onClick={confirmClearLogs}>清空日志</Button>
                </div>
              </Grid.Col>
            </Grid.Row>
          </div>
          <Divider className="logs-divider" />
          <div className="logs-terminal" role="log" aria-label="系统日志" aria-live="polite">
            {query.isPending && !query.data ? (
              <div className="logs-loading" role="status" aria-label="日志加载中"><span className="logs-loading-spinner" aria-hidden="true" />加载中...</div>
            ) : visibleRecords.length === 0 ? (
              <div className="logs-empty">暂无日志记录</div>
            ) : visibleRecords.map((record) => (
              <div className={`logs-line logs-line--${levelClass(record.level)}`} key={`${record.time}-${record.level}-${record.category}-${record.message}-${JSON.stringify(record.attrs ?? {})}`}>
                <span className="logs-dot" aria-hidden="true" />
                <time className="logs-time" dateTime={record.time}>{formatTime(record.time)}</time>
                <span className="logs-category">{record.category}</span>
                <span className="logs-level">{record.level.toUpperCase()}</span>
                <span className="logs-message">{formatLogMessage(record.message, record.attrs)}</span>
              </div>
            ))}
          </div>
          <ListPagination page={page} total={query.data?.total ?? 0} pageSize={pageSize} pageSizeOptions={LOG_PAGE_SIZE_OPTIONS} onChange={setPage} onPageSizeChange={changePageSize} plain />
        </div>
      </PageState>
      {messageHolder}
      {/* 清空确认弹窗走公共 AppDialog：点遮罩空白、ESC、右上角 X 三种方式都能关。
          Arco 的 Modal.confirm 遮罩关闭只认 click，在弹窗内拖选后到遮罩上松手会误关，本项目不用它。 */}
      <AppDialog
        title="清空日志"
        visible={clearConfirmVisible}
        onClose={closeClearConfirm}
        footer={<>
          <Button onClick={closeClearConfirm}>取消</Button>
          <Button status="danger" loading={clearMutation.isPending} disabled={clearMutation.isPending} onClick={() => clearMutation.mutate()}>确定清空</Button>
        </>}
      >
        {keyword || category || level || timeRange ? "确定清空当前筛选结果中的日志吗？清空后无法恢复。" : "确定清空全部日志吗？清空后无法恢复。"}
      </AppDialog>
    </section>
  );
}
