param(
    [switch]$PullCode,
    [switch]$PruneImages
)

$ErrorActionPreference = "Stop"

function Require-Command {
    param([string]$Name)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Missing required command: $Name"
    }
}

Require-Command "docker"
docker compose version | Out-Null

$root = Split-Path -Parent $PSScriptRoot

if ($PullCode -and (Test-Path (Join-Path $root ".git"))) {
    Require-Command "git"
    git -C $root pull --ff-only
}

Push-Location $root
try {
    if ($env:APP_VERSION) {
        $buildVersion = $env:APP_VERSION
    }
    else {
        $buildVersion = (Get-Date).ToUniversalTime().ToString("yyyyMMdd.HHmmss")
    }
    if ($env:APP_COMMIT) {
        $buildCommit = $env:APP_COMMIT
    }
    elseif (Test-Path (Join-Path $root ".git")) {
        $buildCommit = (git -C $root rev-parse --short HEAD 2>$null)
        if ([string]::IsNullOrWhiteSpace($buildCommit)) {
            $buildCommit = "unknown"
        }
    }
    else {
        $buildCommit = "unknown"
    }
    $env:APP_VERSION = $buildVersion
    $env:APP_COMMIT = $buildCommit

    docker compose up -d --build --remove-orphans --force-recreate
    if ($PruneImages) {
        docker image prune -f | Out-Null
    }
}
finally {
    Pop-Location
}

Write-Host "Update complete."
Write-Host "Build version: $($env:APP_VERSION) ($($env:APP_COMMIT))"
