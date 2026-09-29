$ErrorActionPreference = "Stop"

$dockerfile = Get-Content -Raw (Join-Path $PSScriptRoot "Dockerfile")
if (($dockerfile | Select-String -Pattern '(?m)^FROM ' -AllMatches).Matches.Count -lt 3) {
    throw "Dockerfile 必须使用前端、后端和运行时多阶段构建"
}
if ($dockerfile -notmatch 'FROM gcr.io/distroless/static-debian12:nonroot') {
    throw "最终镜像必须使用非 root 的 distroless 运行时"
}
$runtime = $dockerfile.Substring($dockerfile.LastIndexOf('FROM gcr.io/distroless'))
if ($runtime -match 'COPY (frontend|backend)/') {
    throw "最终阶段不得复制前后端源码"
}
if ($runtime -notmatch 'COPY --from=backend-build.+/app/bytemuse' -or $runtime -notmatch 'COPY --from=frontend-build.+/app/web') {
    throw "最终阶段必须只装配服务二进制和前端静态产物"
}

# 定时任务按进程本地时间解释 Cron：缺少 zoneinfo 或 TZ 会让默认排期（20:00/21:00/21:30/22:00）静默偏移。
if ($runtime -notmatch 'COPY --from=backend-build[^\r\n]*zoneinfo') {
    throw "最终阶段必须复制运行所需的时区数据"
}
if ($runtime -notmatch '(?m)^ENV TZ=Asia/Shanghai') {
    throw "最终镜像必须固定 TZ=Asia/Shanghai"
}
if ($runtime -notmatch 'HEALTHCHECK[^\r\n]*doctor') {
    throw "最终镜像必须提供调用 doctor 的健康检查"
}
if ($runtime -notmatch 'ARG BYTEMUSE_VERSION' -or $runtime -notmatch 'BYTEMUSE_VERSION=\$BYTEMUSE_VERSION') {
    throw "构建流程必须能注入 BYTEMUSE_VERSION 运行版本"
}
if ($runtime -notmatch '_pragma=foreign_keys\(1\)' -or $runtime -notmatch '_pragma=journal_mode\(WAL\)') {
    throw "镜像默认 DATABASE_DSN 必须保留 SQLite 外键与 WAL 参数"
}
if ($runtime -notmatch '(?m)^USER nonroot:nonroot') {
    throw "最终镜像必须以非 root 用户运行"
}
if ($runtime -notmatch 'CMD \["serve"\]') {
    throw "容器默认命令必须是 serve，HTTP 与定时任务才在同一进程内运行"
}
$composeFiles = Get-ChildItem -LiteralPath $PSScriptRoot -Filter 'compose.*.yaml'
foreach ($composeFile in $composeFiles) {
    $text = Get-Content -Raw -LiteralPath $composeFile.FullName
    if ($text -notmatch '(?m)^\s+TZ:\s*\$\{TZ:-Asia/Shanghai\}') {
        throw "$($composeFile.Name) 必须把 TZ 固定为 Asia/Shanghai 并允许覆盖"
    }
    if ($text -notmatch '(?m)^\s+image:\s*\$\{BYTEMUSE_IMAGE:-') {
        throw "$($composeFile.Name) 必须使用可覆盖的 BYTEMUSE_IMAGE 镜像变量"
    }
}

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw "未找到 docker 命令：Dockerfile 与 Compose 静态检查已通过，但无法解析 Compose 配置"
}

# 这里只校验 Compose 结构，不需要真实凭据：缺失的必填变量用占位值补齐，已设置的值不覆盖。
$placeholders = @{ ADMIN_USERNAME = 'verify'; ADMIN_PASSWORD = 'verify'; SESSION_SECRET = 'verify-session-secret-at-least-32-bytes'; DATABASE_DSN = 'verify' }
foreach ($name in $placeholders.Keys) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) {
        Set-Item -Path "env:$name" -Value $placeholders[$name]
    }
}

foreach ($composeFile in $composeFiles) {
    $configuration = docker compose -f $composeFile.FullName config --services 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "$($composeFile.Name) 配置解析失败: $configuration"
    }
    $services = @($configuration | Where-Object { $_ -and $_.Trim() })
    if ($services.Count -ne 1 -or $services[0].Trim() -ne 'bytemuse') {
        throw "$($composeFile.Name) 必须且只能包含 bytemuse 应用服务"
    }
}

Write-Output "deployment verification passed"
