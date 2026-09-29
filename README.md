# ByteMuse Go

ByteMuse 的 Go 重建版本。单个容器同时提供 Web UI、REST API 与后台调度器，支持 SQLite、PostgreSQL 和 MySQL。

- 镜像地址：`ghcr.io/w2z/byte-muse-go`
- 容器端口：`3750`；数据卷：`/data`；默认时区：`Asia/Shanghai`
- 版本、提交与镜像摘要记录在 `version.json`

## 镜像标签

| 标签 | 说明 |
| --- | --- |
| `latest` | 最新一次成功构建 |
| `0.1.<提交计数>` | 版本号，同一提交恒定 |
| `sha-<短提交>` | 按提交定位 |

## Docker

### 构建镜像

```bash
docker build -f deploy/Dockerfile -t bytemuse-go:local .
```

### SQLite 部署

```bash
docker run -d --name bytemuse \
  -p 18043:3750 \
  -e APP_ENV=production \
  -e TZ=Asia/Shanghai \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换为32字节以上随机串' \
  -v bytemuse-data:/data \
  ghcr.io/w2z/byte-muse-go:latest
```

### PostgreSQL 部署

```bash
docker run -d --name bytemuse \
  -p 18043:3750 \
  -e APP_ENV=production \
  -e TZ=Asia/Shanghai \
  -e DATABASE_DRIVER=postgres \
  -e DATABASE_DSN='postgres://bytemuse:请替换为数据库密码@请替换为数据库主机:5432/bytemuse?sslmode=disable' \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换为32字节以上随机串' \
  ghcr.io/w2z/byte-muse-go:latest
```

### MySQL 部署

```bash
docker run -d --name bytemuse \
  -p 18043:3750 \
  -e APP_ENV=production \
  -e TZ=Asia/Shanghai \
  -e DATABASE_DRIVER=mysql \
  -e DATABASE_DSN='bytemuse:请替换为数据库密码@tcp(请替换为数据库主机:3306)/bytemuse?charset=utf8mb4&parseTime=true&loc=Local' \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换为32字节以上随机串' \
  ghcr.io/w2z/byte-muse-go:latest
```

## Docker Compose

`ADMIN_USERNAME`、`ADMIN_PASSWORD`、`SESSION_SECRET` 为必填项，缺失时 Compose 直接报错。PostgreSQL 和 MySQL 还需提供 `DATABASE_DSN`。以下命令为 POSIX shell 写法；PowerShell 先逐条执行 `$env:ADMIN_USERNAME='admin'` 等赋值，再运行 `docker compose ...`。

### 构建镜像

```bash
docker compose -f deploy/compose.sqlite.yaml build
docker compose -f deploy/compose.postgres.yaml build
docker compose -f deploy/compose.mysql.yaml build
```

### SQLite 部署

```bash
ADMIN_USERNAME=admin \
ADMIN_PASSWORD='请替换为管理员密码' \
SESSION_SECRET='请替换为32字节以上随机串' \
BYTEMUSE_IMAGE=ghcr.io/w2z/byte-muse-go:latest \
docker compose -f deploy/compose.sqlite.yaml up -d
```

### PostgreSQL 部署

```bash
ADMIN_USERNAME=admin \
ADMIN_PASSWORD='请替换为管理员密码' \
SESSION_SECRET='请替换为32字节以上随机串' \
DATABASE_DSN='postgres://bytemuse:请替换为数据库密码@请替换为数据库主机:5432/bytemuse?sslmode=disable' \
BYTEMUSE_IMAGE=ghcr.io/w2z/byte-muse-go:latest \
docker compose -f deploy/compose.postgres.yaml up -d
```

### MySQL 部署

```bash
ADMIN_USERNAME=admin \
ADMIN_PASSWORD='请替换为管理员密码' \
SESSION_SECRET='请替换为32字节以上随机串' \
DATABASE_DSN='bytemuse:请替换为数据库密码@tcp(请替换为数据库主机:3306)/bytemuse?charset=utf8mb4&parseTime=true&loc=Local' \
BYTEMUSE_IMAGE=ghcr.io/w2z/byte-muse-go:latest \
docker compose -f deploy/compose.mysql.yaml up -d
```