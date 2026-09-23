/** 统一 API 错误。页面只展示 message，并用 status 区分鉴权与服务端失败。 */
export class ApiError extends Error {
  readonly status?: number;

  constructor(message: string, status?: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

type ErrorBody = {
  message?: unknown;
};

/**
 * 将 HTTP 失败响应或网络异常转换为 ApiError。
 * 传入 Response 时异步读取契约错误体；传入 Error 时同步返回。
 */
export function toApiError(input: Response): Promise<ApiError>;
export function toApiError(input: Error): ApiError;
export function toApiError(input: Response | Error): Promise<ApiError> | ApiError {
  if (input instanceof Error) {
    if (input instanceof ApiError) {
      return input;
    }
    if (input instanceof TypeError) {
      return new ApiError("网络连接失败，请稍后重试");
    }
    return new ApiError(input.message || "请求失败");
  }

  return readHttpError(input);
}

async function readHttpError(response: Response): Promise<ApiError> {
  let message = httpStatusMessage(response.status);
  try {
    const body = (await response.json()) as ErrorBody;
    if (typeof body.message === "string" && body.message.trim() !== "") {
      message = body.message;
    }
  } catch {
    // 非 JSON 响应沿用 HTTP 状态说明。
  }
  return new ApiError(message, response.status);
}

function httpStatusMessage(status: number): string {
  if (status === 401) {
    return "登录已失效，请重新登录";
  }
  if (status === 403) {
    return "没有权限执行该操作";
  }
  if (status === 404) {
    return "请求的资源不存在";
  }
  if (status >= 500) {
    return `服务暂时不可用（${status}）`;
  }
  return `请求失败（${status}）`;
}
