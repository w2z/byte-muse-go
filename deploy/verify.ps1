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
# 只校验仓库模板：本地未跟踪的 *.local.yaml 允许保留真实内网地址。
$composeFiles = @(Get-ChildItem -LiteralPath $PSScriptRoot -Filter 'compose.*.yaml' | Where-Object { $_.Name -notlike '*.local.yaml' })
if ($composeFiles.Count -ne 3) {
    throw "deploy 目录必须保留 compose.sqlite/postgres/mysql 三个模板"
}
foreach ($composeFile in $composeFiles) {
    $text = Get-Content -Raw -LiteralPath $composeFile.FullName
    if ($text -notmatch '(?m)^\s+TZ:\s*Asia/Shanghai\s*$') {
        throw "$($composeFile.Name) 必须把 TZ 固定为 Asia/Shanghai"
    }
    if ($text -notmatch '(?m)^\s+image:\s*ghcr\.io/w2z/byte-muse-go:') {
        throw "$($composeFile.Name) 必须直接使用已发布的 ghcr.io/w2z/byte-muse-go 镜像"
    }
    # Compose 配置直接可读可改，不依赖环境变量插值：缺省值藏在 shell 里会让部署命令与文件内容不一致。
    if ($text -match '\$\{') {
        throw "$($composeFile.Name) 不得使用环境变量插值，必须直接写出配置值"
    }
    # 仓库公开：Compose 模板不得写入内网地址，真实拓扑放本地未跟踪文件。
    if ($text -match '(?<!\d)(?:10|192\.168|172\.(?:1[6-9]|2\d|3[01]))\.\d{1,3}\.\d{1,3}\.\d{1,3}(?!\d)') {
        throw "$($composeFile.Name) 不得写入内网 IP 地址"
    }
    if ($text -notmatch '(?m)^\s+container_name:\s*byte-muse-go\s*$') {
        throw "$($composeFile.Name) 必须固定容器名为 byte-muse-go"
    }
    if ($text -notmatch '(?m)^\s+container_name:\s*cloudflarebypass_byte_muse_go\s*$') {
        throw "$($composeFile.Name) 必须固定增强服务容器名为 cloudflarebypass_byte_muse_go"
    }
    if ($text -notmatch '(?m)^\s+restart:\s*always\s*$') {
        throw "$($composeFile.Name) 必须使用 restart: always"
    }
    if ($text -notmatch '(?m)^\s+-\s+/path/to/byte-muse/data:/data\s*$') {
        throw "$($composeFile.Name) 必须把宿主机数据目录挂载到 /data"
    }
    # 缺少 /strm 时容器内目录浏览与 strm 生成只能在运行期失败，静态校验必须提前拦住。
    if ($text -notmatch '(?m)^\s+-\s+/path/to/byte-muse/strm:/strm\s*$') {
        throw "$($composeFile.Name) 必须把宿主机 strm 目录挂载到 /strm"
    }
    # 镜像的 /app 存放服务二进制与前端产物，挂载覆盖后容器无法启动。
    if ($text -match '(?m)^\s+-\s+\S+:/app\s*$') {
        throw "$($composeFile.Name) 不得挂载覆盖 /app"
    }
    if ($text -notmatch '(?m)^\s+cloudflarebypass_byte_muse_go:\s*$' -or $text -notmatch 'ghcr\.io/sarperavci/cloudflarebypassforscraping') {
        throw "$($composeFile.Name) 必须包含 cloudflarebypass_byte_muse_go 抓取增强服务"
    }
    # DSN 连接宿主机映射端口，数据库容器保持镜像默认监听端口。
    if ($composeFile.Name -match '\.postgres\.') {
        if ($text -notmatch '(?m)^\s+postgres_byte_muse_go:\s*$') {
            throw "$($composeFile.Name) 必须包含内置 postgres 服务"
        }
        if ($text -notmatch '(?m)^\s+container_name:\s*postgres_byte_muse_go\s*$') {
            throw "$($composeFile.Name) 必须固定数据库容器名为 postgres_byte_muse_go"
        }
        if ($text -match '(?m)^\s+(?:PGPORT|command):') {
            throw "$($composeFile.Name) 不得覆盖 postgres 默认监听端口"
        }
        $publishedPort = [regex]::Match($text, '(?m)^\s+-\s+"(?<port>\d+):5432"\s*$')
        if (-not $publishedPort.Success -or [int]$publishedPort.Groups['port'].Value -notin 1..65535) {
            throw "$($composeFile.Name) 必须映射宿主机端口到 postgres 容器的 5432"
        }
        if ($text -notmatch ('@请替换为宿主机地址:' + $publishedPort.Groups['port'].Value + '/')) {
            throw "$($composeFile.Name) 的 DATABASE_DSN 必须使用宿主机地址及 ports 左侧端口"
        }
    }
    if ($composeFile.Name -match '\.mysql\.') {
        if ($text -notmatch '(?m)^\s+mysql_byte_muse_go:\s*$') {
            throw "$($composeFile.Name) 必须包含内置 mysql 服务"
        }
        if ($text -notmatch '(?m)^\s+container_name:\s*mysql_byte_muse_go\s*$') {
            throw "$($composeFile.Name) 必须固定数据库容器名为 mysql_byte_muse_go"
        }
        if ($text -match '(?m)^\s+(?:MYSQL_TCP_PORT|command):') {
            throw "$($composeFile.Name) 不得覆盖 mysql 默认监听端口"
        }
        $publishedPort = [regex]::Match($text, '(?m)^\s+-\s+"(?<port>\d+):3306"\s*$')
        if (-not $publishedPort.Success -or [int]$publishedPort.Groups['port'].Value -notin 1..65535) {
            throw "$($composeFile.Name) 必须映射宿主机端口到 mysql 容器的 3306"
        }
        if ($text -notmatch ('@tcp\(请替换为宿主机地址:' + $publishedPort.Groups['port'].Value + '\)')) {
            throw "$($composeFile.Name) 的 DATABASE_DSN 必须使用宿主机地址及 ports 左侧端口"
        }
    }
}

# 本地未跟踪的 *.local.yaml 允许保留真实内网地址，但挂载契约与模板一致：
# 缺 /data 或 /strm 会让容器无法启动或只在运行期暴露，同样静态拦住。
foreach ($localComposeFile in @(Get-ChildItem -LiteralPath $PSScriptRoot -Filter 'compose.*.local.yaml')) {
    $localText = Get-Content -Raw -LiteralPath $localComposeFile.FullName
    foreach ($mount in @('/data', '/strm')) {
        if ($localText -notmatch ('(?m)^\s+-\s+\S+:' + [regex]::Escape($mount) + '\s*$')) {
            throw "$($localComposeFile.Name) 必须把宿主机目录挂载到 $mount"
        }
    }
}

# README 里的 Compose 配置是用户实际复制的来源，必须与 deploy/ 模板逐字一致，否则两处会静默分叉。
$readmePath = Join-Path (Split-Path -Parent $PSScriptRoot) 'README.md'
if (-not (Test-Path -LiteralPath $readmePath)) {
    throw '缺少 README.md'
}
$readme = (Get-Content -Raw -LiteralPath $readmePath) -replace "`r`n", "`n"
$readmeBlocks = @([regex]::Matches($readme, '(?ms)^```yaml\n(.*?)^```$') | ForEach-Object { $_.Groups[1].Value.TrimEnd("`n") })
$expectedCompose = @('compose.sqlite.yaml', 'compose.postgres.yaml', 'compose.mysql.yaml')
if ($readmeBlocks.Count -ne $expectedCompose.Count) {
    throw "README.md 必须按 SQLite/PostgreSQL/MySQL 给出 $($expectedCompose.Count) 个完整 Compose 配置块，实际 $($readmeBlocks.Count) 个"
}
for ($i = 0; $i -lt $expectedCompose.Count; $i++) {
    $expected = ((Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot $expectedCompose[$i])) -replace "`r`n", "`n").TrimEnd("`n")
    if ($readmeBlocks[$i] -ne $expected) {
        throw "README.md 第 $($i + 1) 个 Compose 配置块与 deploy/$($expectedCompose[$i]) 不一致：README 是用户的复制来源，两处必须逐字相同"
    }
}

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw "未找到 docker 命令：Dockerfile 与 Compose 静态检查已通过，但无法解析 Compose 配置"
}


foreach ($composeFile in $composeFiles) {
    $configuration = docker compose -f $composeFile.FullName config --services 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "$($composeFile.Name) 配置解析失败: $configuration"
    }
    $services = @($configuration | Where-Object { $_ -and $_.Trim() } | ForEach-Object { $_.Trim() } | Sort-Object)
    # 数据库方言模板额外带一个数据库服务；sqlite 用文件库，不需要。
    $expected = switch -Regex ($composeFile.Name) {
        '\.postgres\.' { 'byte-muse-go,cloudflarebypass_byte_muse_go,postgres_byte_muse_go'; break }
        '\.mysql\.' { 'byte-muse-go,cloudflarebypass_byte_muse_go,mysql_byte_muse_go'; break }
        default { 'byte-muse-go,cloudflarebypass_byte_muse_go' }
    }
    if (($services -join ',') -ne $expected) {
        throw "$($composeFile.Name) 的服务必须正好是 $expected，实际为 $($services -join ',')"
    }
}

Write-Output "deployment verification passed"
