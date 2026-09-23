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

$composeFiles = Get-ChildItem -LiteralPath $PSScriptRoot -Filter 'compose.*.yaml'
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
