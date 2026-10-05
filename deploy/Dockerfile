# syntax=docker/dockerfile:1.7

# 前端与后端编译产物与构建机架构无关，两个构建阶段固定在构建平台执行，
# 多架构构建只在编译 Go 二进制时按 TARGETARCH 交叉编译，避免重复跑前端构建。
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend-build
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY frontend/ ./
# 前端构建前检查规范入口及链接，文档仅进入构建阶段。
COPY AGENTS.md /src/AGENTS.md
COPY docs/ /src/docs/
# 与运行阶段使用同一构建参数，让页面首次渲染即可显示当前发布版本。
ARG BYTEMUSE_VERSION=dev
RUN BYTEMUSE_VERSION=$BYTEMUSE_VERSION npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS backend-build
# 构建阶段安装时区数据，最终镜像只复制运行必需的 zoneinfo。
RUN apk add --no-cache tzdata
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ ./
ARG TARGETOS=linux
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/bytemuse ./cmd/bytemuse && \
    mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
# 版本由构建流程注入，供运行信息与 Agent 版本问答使用。
ARG BYTEMUSE_VERSION=dev
LABEL org.opencontainers.image.title="ByteMuse" \
      org.opencontainers.image.description="ByteMuse 单容器服务：Web、REST API 与后台调度" \
      org.opencontainers.image.version="$BYTEMUSE_VERSION"
WORKDIR /app
COPY --from=backend-build --chown=nonroot:nonroot /out/bytemuse /app/bytemuse
COPY --from=backend-build --chown=nonroot:nonroot /out/data /data
# 时区数据供定时任务使用：cron 按进程本地时间解释表达式，缺少 zoneinfo 会静默退回 UTC。
COPY --from=backend-build --chown=nonroot:nonroot /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=frontend-build --chown=nonroot:nonroot /src/frontend/dist /app/web
# TZ 必须与使用方所在时区一致：设置页默认排期为 20:00/21:00/21:30/22:00 本地时间。
ENV TZ=Asia/Shanghai \
    HTTP_ADDR=:3750 \
    WEB_STATIC_DIR=/app/web \
    DATABASE_DRIVER=sqlite \
    DATABASE_DSN=file:/data/bytemuse.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000) \
    BYTEMUSE_VERSION=$BYTEMUSE_VERSION
EXPOSE 3750
VOLUME ["/data"]
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/app/bytemuse", "doctor"]
ENTRYPOINT ["/app/bytemuse"]
CMD ["serve"]
