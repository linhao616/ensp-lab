<#
.SYNOPSIS
    ensp-lab 的 Windows 构建入口（等价于 `make build`）。

.DESCRIPTION
    Windows 开发机通常没有 GNU make，若因此退回手敲 `go build`，
    版本信息就不会被注入，二进制会在启动日志与 /api/version 中自报 stale=true。
    本脚本用纯 PowerShell 复刻 Makefile 的 build target：
      1. 前端增量构建（src/ package.json vite.config.ts 比 dist/index.html 新时才重跑）
      2. 注入与 Makefile 完全一致的 ldflags 到 internal/buildinfo
      3. 产出唯一交付物 ensp-lab.exe，并清掉历史遗留的重复二进制 server.exe

    ⚠️ 禁止直接 `go build`。请用本脚本（Windows）或 `make build`（Linux/macOS/CI）。

.PARAMETER ForceUI
    忽略增量判断，强制重新执行 npm install + npm run build。

.EXAMPLE
    ./build.ps1
.EXAMPLE
    ./build.ps1 -ForceUI
#>
[CmdletBinding()]
param(
    [switch]$ForceUI
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# 始终以脚本所在目录（仓库根）为工作目录，允许从任意路径调用。
$RepoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Push-Location $RepoRoot

# Invoke-Checked 执行外部命令并在非零退出码时中断，避免「报错了但继续往下构建」。
function Invoke-Checked {
    param(
        [Parameter(Mandatory = $true)][string]$File,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [string]$WorkDir = $null
    )
    if ($WorkDir) { Push-Location $WorkDir }
    try {
        & $File @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "命令失败（exit $LASTEXITCODE）: $File $($Arguments -join ' ')"
        }
    } finally {
        if ($WorkDir) { Pop-Location }
    }
}

# Get-GitValue 执行 git 并返回单行输出；git 缺失/失败时返回给定兜底值，
# 保证干净克隆或无 git 环境下构建照样能进行（与 Makefile 的 `|| echo` 对齐）。
function Get-GitValue {
    param(
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Fallback
    )
    try {
        $out = & git @Arguments 2>$null
        if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace(($out | Out-String))) {
            return $Fallback
        }
        return (($out | Select-Object -First 1) | Out-String).Trim()
    } catch {
        return $Fallback
    }
}

try {
    $FrontendDir = Join-Path $RepoRoot 'frontend'
    $UiBundle    = Join-Path $FrontendDir 'dist/index.html'
    $NodeModules = Join-Path $FrontendDir 'node_modules'

    # ---- 1. 前端增量构建 ----
    if (-not (Test-Path $NodeModules)) {
        Write-Host '[ui] node_modules 缺失，执行 npm install ...' -ForegroundColor Cyan
        Invoke-Checked -File 'npm' -Arguments @('install') -WorkDir $FrontendDir
    }

    $needUI = $ForceUI.IsPresent -or (-not (Test-Path $UiBundle))
    if (-not $needUI) {
        $bundleTime = (Get-Item $UiBundle).LastWriteTimeUtc
        # 与 Makefile 的 prereq 集合保持一致：src/** + package.json + vite.config.ts
        $watched = @()
        $srcDir = Join-Path $FrontendDir 'src'
        if (Test-Path $srcDir) {
            $watched += Get-ChildItem -Path $srcDir -Recurse -File
        }
        foreach ($name in @('package.json', 'vite.config.ts')) {
            $p = Join-Path $FrontendDir $name
            if (Test-Path $p) { $watched += Get-Item $p }
        }
        foreach ($f in $watched) {
            if ($f.LastWriteTimeUtc -gt $bundleTime) {
                $needUI = $true
                Write-Host "[ui] 检测到源码更新: $($f.Name)" -ForegroundColor Cyan
                break
            }
        }
    }

    if ($needUI) {
        Write-Host '[ui] 构建前端 ...' -ForegroundColor Cyan
        Invoke-Checked -File 'npm' -Arguments @('run', 'build') -WorkDir $FrontendDir
    } else {
        Write-Host '[ui] 前端产物已是最新，跳过' -ForegroundColor DarkGray
    }

    # ---- 2. 采集版本信息（与 Makefile 同源同兜底）----
    # 仅当「当前目录就是 git 仓库根」时才注入真实 git 信息；
    # 开发副本/部署副本（无 .git，git 命令会解析到父仓库）一律注入
    # version=dev / commit=unknown / dirty=false —— 这样运行时 buildinfo
    # 的陈旧自检（规则 2b cat-file 血缘不匹配）会跳过 git 判定，不再误报 stale。
    $gitVersion = 'dev'
    $gitCommit  = 'unknown'
    $gitDirty  = 'false'
    try {
        $toplevel = (& git rev-parse --show-toplevel 2>$null | Out-String).Trim()
        if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($toplevel)) {
            $tl   = [System.IO.Path]::GetFullPath($toplevel).TrimEnd('\')
            $repo = [System.IO.Path]::GetFullPath($RepoRoot).TrimEnd('\')
            if ($tl -eq $repo) {
                $gitVersion = Get-GitValue -Arguments @('describe', '--tags', '--always') -Fallback 'dev'
                $gitCommit  = Get-GitValue -Arguments @('rev-parse', '--short', 'HEAD')   -Fallback 'unknown'
                $status = & git status --porcelain 2>$null
                if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace(($status | Out-String))) {
                    $gitDirty = 'true'
                }
            }
        }
    } catch {
        # git 不可用：保持 dev/unknown/false
    }

    $buildTime = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')

    # ---- 3. 构建 Go 二进制 ----
    $exeSuffix = (& go env GOEXE)
    if ($null -eq $exeSuffix) { $exeSuffix = '' }
    $exeSuffix = $exeSuffix.Trim()
    $bin = "ensp-lab$exeSuffix"

    # 清掉历史遗留的重复二进制：唯一交付物只有 ensp-lab.exe。
    $legacy = Join-Path $RepoRoot 'server.exe'
    if (Test-Path $legacy) {
        Remove-Item $legacy -Force
        Write-Host '[build] 已删除重复二进制 server.exe' -ForegroundColor DarkGray
    }

    $pkg = 'ensp-lab/internal/buildinfo'
    # 关于引号：cmd/go 用 quoted.Split 解析 -ldflags 的值，会自行剥离成对的单/双引号，
    # 因此 "-X 'pkg.V=1'" 与 "-X pkg.V=1" 注入效果等价（经 A/B 实测确认）——
    # 曾有一次"去掉单引号"的改动其实是 no-op，勿再当成 bug 反复"修复"。
    # 这里保留加引号写法：四个值当前均不含空格（git describe / 短 SHA /
    # RFC3339 时间戳 / true|false），但加引号在将来某个值含空格时也不会被拆成
    # 多个 token，属更防御的写法。
    $ldflags = "-X '$pkg.Version=$gitVersion' -X '$pkg.BuildTime=$buildTime' -X '$pkg.Commit=$gitCommit' -X '$pkg.Dirty=$gitDirty'"

    Write-Host '[build] 编译 Go 二进制 ...' -ForegroundColor Cyan
    Invoke-Checked -File 'go' -Arguments @('build', '-ldflags', $ldflags, '-o', $bin, './cmd/server')

    Write-Host "built $bin  version=$gitVersion  commit=$gitCommit  dirty=$gitDirty  time=$buildTime" -ForegroundColor Green
} finally {
    Pop-Location
}
