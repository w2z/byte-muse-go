# syntax=docker/dockerfile:1.7

FROM node:24-alpine AS frontend-build
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.27-alpine AS backend-build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bytemuse ./cmd/bytemuse && \
    mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=backend-build --chown=nonroot:nonroot /out/bytemuse /app/bytemuse
COPY --from=backend-build --chown=nonroot:nonroot /out/data /data
COPY --from=frontend-build --chown=nonroot:nonroot /src/frontend/dist /app/web
ENV HTTP_ADDR=:3750 \
    WEB_STATIC_DIR=/app/web \
    DATABASE_DRIVER=sqlite \
    DATABASE_DSN=/data/bytemuse.db \
EXPOSE 3750
VOLUME ["/data"]
USER nonroot:nonroot
ENTRYPOINT ["/app/bytemuse"]
CMD ["serve"]
