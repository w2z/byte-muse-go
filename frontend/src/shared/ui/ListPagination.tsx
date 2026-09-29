import { Pagination } from "@arco-design/web-react";

/**
 * 每页条数可选项。
 *
 * 这里是唯一权威：分页条渲染的选项、列表页的默认值、请求参数里的 page_size 都取自这里。
 * 默认业务列表上限 200 与后端 backend/internal/ports 的 MaxPageSize 保持一致；日志页和全部标签通过 pageSizeOptions 单独使用 100-500。
 */
export const PAGE_SIZE_OPTIONS = [15, 30, 50, 100, 200];

/** 默认每页条数，必须是 PAGE_SIZE_OPTIONS 的成员，否则下拉框读不到当前值。 */
export const DEFAULT_PAGE_SIZE = PAGE_SIZE_OPTIONS[0];

type ListPaginationProps = {
  /** 当前页码，从 1 开始。 */
  page: number;
  /** 服务端返回的总条数。 */
  total: number;
  /** 每页条数，必须与请求参数一致。 */
  pageSize?: number;
  /** 只在页码真实变化时回调，不会用非法页码调用。 */
  onChange: (page: number) => void;
  /** 每页条数变化时回调；实现方必须同时把页码重置为 1，否则会请求到越界页。 */
  onPageSizeChange: (pageSize: number) => void;
  /** 放在卡片网格下方（无外层卡片边框）时传 true。两种形态目前视觉一致，仅为调用点保留语义。 */
  plain?: boolean;
  /** 当前列表可用的每页条数选项；未传时使用全局默认选项。 */
  pageSizeOptions?: number[];
};

/**
 * 列表分页条。
 *
 * 服务端分页是权威：总数和当前页都来自接口返回，总页数由 total / pageSize 换算，不缓存跨页数据。
 * 总数、页码、每页条数统一交给 Arco Pagination 渲染（showTotal + 默认分页按钮 + sizeCanChange），
 * 列表页只负责把 pageSize 写进请求参数，不各自拼装分页 UI。
 */
export function ListPagination({ page, total, pageSize = DEFAULT_PAGE_SIZE, onChange, onPageSizeChange, plain = false, pageSizeOptions = PAGE_SIZE_OPTIONS }: ListPaginationProps) {
  return (
    <div className={plain ? "list-pagination plain" : "list-pagination"}>
      <Pagination
        current={page}
        total={total}
        pageSize={pageSize}
        showTotal={(value) => `共 ${value} 条`}
        sizeCanChange
        sizeOptions={pageSizeOptions}
        onChange={(nextPage, nextPageSize) => {
          // 改每页条数时 Arco 会额外派发一次 onChange(1, 新条数)，这里把两类变化拆开处理，
          // 让每个回调只表达一件事，避免重复触发同一批数据的两次请求。
          if (nextPageSize !== pageSize) {
            onPageSizeChange(nextPageSize);
            return;
          }
          if (nextPage !== page) onChange(nextPage);
        }}
      />
    </div>
  );
}
