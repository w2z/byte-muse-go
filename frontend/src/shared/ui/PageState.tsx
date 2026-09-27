import { Button, Empty, Result, Spin } from "@arco-design/web-react";
import type { ReactNode } from "react";

type PageStateProps = {
  isLoading: boolean;
  error: Error | null;
  /** 空数据整页替换为 Empty；表格型页面改用表格内 noDataElement 时不传。 */
  isEmpty?: boolean;
  emptyText?: string;
  onRetry?: () => void;
  /** 空数据时仍需保留在正文下方的内容，例如服务端分页条。 */
  emptyExtra?: ReactNode;
  children: ReactNode;
};

/** 页面级加载、失败和空数据状态。失败时展示已转换的 API 错误消息。 */
export function PageState({
  isLoading,
  error,
  isEmpty,
  emptyText,
  onRetry,
  emptyExtra,
  children,
}: PageStateProps) {
  if (isLoading) {
    return (
      <div className="page-state" role="status">
        <Spin tip="加载中" />
      </div>
    );
  }
  if (error) {
    return (
      <Result
        status="error"
        title="加载失败"
        subTitle={error.message}
        extra={onRetry ? <Button onClick={onRetry}>重试</Button> : null}
      />
    );
  }
  if (isEmpty) {
    return (
      <div className="page-state-empty">
        <div className="page-state-empty-body">
          <Empty description={emptyText} />
        </div>
        {emptyExtra}
      </div>
    );
  }
  return children;
}
