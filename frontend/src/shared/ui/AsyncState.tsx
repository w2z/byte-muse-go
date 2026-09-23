import { Button, Empty, Result, Spin } from "@arco-design/web-react";
import type { ReactNode } from "react";

type AsyncStateProps = {
  isLoading: boolean;
  error: Error | null;
  isEmpty: boolean;
  emptyText: string;
  onRetry?: () => void;
  children: ReactNode;
};

/** 列表和详情共用的加载、失败、空数据状态。 */
export function AsyncState({
  isLoading,
  error,
  isEmpty,
  emptyText,
  onRetry,
  children,
}: AsyncStateProps) {
  if (isLoading) {
    return <Spin tip="加载中" />;
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
