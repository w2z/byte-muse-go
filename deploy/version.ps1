# 生成发布版本记录 version.json。
#
# 版本号规则：0.1.<提交计数>，计数排除只修改 version.json 的提交。
# 因此同一次代码提交对应唯一版本，发布提交本身不改变版本号，也不会形成自触发循环。
#
# 运行时机：代码提交后自动执行（.githooks/post-commit），也可在推送前手工执行。
# 只有本次提交包含构建输入（backend/、frontend/、deploy/Dockerfile、.dockerignore）时才更新；
# 文档、规范、Compose 等改动不产生镜像，不写版本记录。
#
# 版本记录是构建流程的输入：workflow 只读取并校验，不回写仓库。
[CmdletBinding()]
param(
    # 静默模式：由提交钩子调用时只输出结果，不输出过程信息。
    [switch]$Quiet
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
# 原生命令的 stderr 不参与解析，只作为失败时的诊断信息。
if (Test-Path variable:PSNativeCommandUseErrorActionPreference) {
    $PSNativeCommandUseErrorActionPreference = $false
}

$root = Split-Path -Parent $PSScriptRoot
$recordPath = Join-Path $root 'version.json'
# 与 .github/workflows/docker.yml 的 paths 白名单一致：新增构建输入时两处必须同步修改。
$buildInputs = @('backend', 'frontend', 'deploy/Dockerfile', '.dockerignore')
$image = 'ghcr.io/w2z/byte-muse-go'
$source = 'https://github.com/w2z/byte-muse-go'

function Invoke-Git {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
    $stderrPath = [System.IO.Path]::GetTempFileName()
    try {
        $output = & git -C $root @Arguments 2>$stderrPath
        if ($LASTEXITCODE -ne 0) {
            $detail = (Get-Content -LiteralPath $stderrPath -Raw -Encoding UTF8 -ErrorAction SilentlyContinue)
            throw ('git {0} 执行失败（退出码 {1}）：{2}' -f ($Arguments -join ' '), $LASTEXITCODE, ($detail -replace '\s+$', ''))
        }
        $output
    }
    finally {
        Remove-Item -LiteralPath $stderrPath -Force -ErrorAction SilentlyContinue
    }
}

# 取单值输出：合并为一行并去掉首尾空白。
function Invoke-GitText {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
    return (@(Invoke-Git @Arguments) -join "`n").Trim()
}

function Write-Status {
    param([string]$Message)
    if (-not $Quiet) {
        Write-Host $Message
    }
}

$gitDir = Invoke-GitText rev-parse --absolute-git-dir

# 变基、合并、拣选过程中不自动创建提交，避免打断正在进行的操作。
$inProgress = @('MERGE_HEAD', 'CHERRY_PICK_HEAD', 'REVERT_HEAD') |
    Where-Object { Test-Path -LiteralPath (Join-Path $gitDir $_) }
if ($inProgress -or (Test-Path -LiteralPath (Join-Path $gitDir 'rebase-merge')) -or (Test-Path -LiteralPath (Join-Path $gitDir 'rebase-apply'))) {
    Write-Status '当前处于变基、合并或拣选过程，跳过版本记录更新'
    exit 0
}

$changed = @(Invoke-Git show --name-only --format= HEAD -- @buildInputs)
if ($changed.Count -eq 0) {
    Write-Status '本次提交不含构建输入改动，无需更新版本记录'
    exit 0
}

$count = [int](Invoke-GitText rev-list --count HEAD -- ':(exclude)version.json')
$version = "0.1.$count"

if (Test-Path -LiteralPath $recordPath) {
    $recorded = (Get-Content -LiteralPath $recordPath -Raw -Encoding UTF8 | ConvertFrom-Json).version
    if ($recorded -eq $version) {
        Write-Status "版本记录已是最新（$version），无需修改"
        exit 0
    }
}

$commit = Invoke-GitText rev-parse HEAD
$shortCommit = Invoke-GitText rev-parse --short HEAD
$builtAt = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss') + 'Z'

# 从实际构建提交生成说明，排除版本回写和纯文档提交；每条沿用正式版本计数，不能按列表位置推算。
# 从完整历史重建，首次启用也能展示跨旧版本的更新，不依赖用户安装过中间版本。
if ((Invoke-GitText rev-parse --is-shallow-repository) -eq 'true') {
    throw '生成更新说明需要完整 Git 历史，请先获取完整历史再生成版本记录'
}
$changes = @(foreach ($revision in @(Invoke-Git log --format=%H HEAD -- @buildInputs)) {
    $revisionCount = Invoke-GitText rev-list --count $revision -- ':(exclude)version.json'
    $message = Invoke-GitText log -1 --format=%s $revision
    # 只保留提交标题的说明部分；清理旧提交的控制字符，正文不进入公开版本记录。
    $message = ($message -replace '\p{Cc}', '') -replace '^.*?\b(?:feat|fix|docs|style|refactor|perf|test|chore)(?:\([^)]*\))?!?:\s*', ''
    if (-not [string]::IsNullOrWhiteSpace($message)) {
        [ordered]@{ version = "0.1.$revisionCount"; message = $message.Trim() }
    }
})

$record = [ordered]@{
    version      = $version
    commit       = $commit
    short_commit = $shortCommit
    built_at     = $builtAt
    image        = $image
    tags         = @($version, "sha-$shortCommit", 'latest')
    source       = $source
    changes      = $changes
    note         = '由 deploy/version.ps1 在代码提交时写入，请勿手工修改'
}
$json = (($record | ConvertTo-Json -Depth 4) -replace "`r`n", "`n") + "`n"
[System.IO.File]::WriteAllText($recordPath, $json, (New-Object System.Text.UTF8Encoding($false)))

Invoke-Git add -- version.json | Out-Null
Invoke-Git commit -m "🔧 chore(发布): 更新 version.json 到 $version" -- version.json | Out-Null

$head = Invoke-GitText rev-parse --short HEAD
Write-Status "已写入版本记录 $version（发布提交 $head），推送时请一并推送本次发布提交"
