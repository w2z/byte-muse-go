import { Button, Empty, Result, Spin } from "@arco-design/web-react";
import type { ReactNode } from "react";

type PageStateProps = {
  isLoading: boolean;
  error: Error | null;
  isEmpty: boolean;
  emptyText: string;
  onRetry?: () => void;
  children: ReactNode;
};

/** 页面级加载、失败和空数据状态。失败时展示已转换的 API 错误消息。 */
export function PageState({
  isLoading,
  error,
  isEmpty,
  emptyText,
  onRetry,
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
    return <Empty description={emptyText} />;
  }
  return children;
}
