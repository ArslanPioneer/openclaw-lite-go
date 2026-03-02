param(
    [ValidateSet("openai","minimax","glm","custom")]
    [string]$Provider = "openai",
    [string]$TelegramToken = "",
    [string]$AgentUrl = "",
    [string]$AgentKey = "",
    [string]$AgentModel = "gpt-4o-mini",
    [string]$SystemPrompt = "",
    [int]$Workers = 4,
    [int]$QueueSize = 64,
    [int]$PollTimeoutSecond = 25,
    [int]$RequestTimeoutSec = 60,
    [string]$DataDir = "/app/data",
    [int]$HistoryTurns = 8,
    [int]$AgentRetryCount = 2,
    [string]$SkillsSourceDir = "/app/openclaw-skills",
    [string]$SkillsInstallDir = "/app/data/skills",
    [int]$HealthPort = 18080,
    [int]$RestartBackoffMs = 1000,
    [int]$RestartMaxMs = 30000
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

if ([string]::IsNullOrWhiteSpace($AgentUrl)) {
    switch ($Provider) {
        "openai" { $AgentUrl = "https://api.openai.com/v1" }
        "minimax" { $AgentUrl = "https://api.minimaxi.com/v1" }
        "glm" { $AgentUrl = "https://open.bigmodel.cn/api/paas/v4" }
        default { $AgentUrl = "" }
    }
}

if ([string]::IsNullOrWhiteSpace($TelegramToken)) {
    $TelegramToken = Read-Host "Telegram Bot Token"
}
if ([string]::IsNullOrWhiteSpace($AgentKey)) {
    $AgentKey = Read-Host "Agent API Key"
}
if ([string]::IsNullOrWhiteSpace($AgentModel)) {
    $AgentModel = Read-Host "Agent Model"
}
if ([string]::IsNullOrWhiteSpace($TelegramToken) -or [string]::IsNullOrWhiteSpace($AgentKey)) {
    throw "TelegramToken and AgentKey are required."
}

$root = Split-Path -Parent $PSScriptRoot
$dataDir = Join-Path $root "data"
$configPath = Join-Path $dataDir "config.json"
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null

$cfg = @{
    telegram = @{
        bot_token = $TelegramToken
    }
    agent = @{
        provider = $Provider
        base_url = $AgentUrl
        api_key = $AgentKey
        model = $AgentModel
        system_prompt = $SystemPrompt
    }
    runtime = @{
        workers = $Workers
        queue_size = $QueueSize
        poll_timeout_second = $PollTimeoutSecond
        request_timeout_sec = $RequestTimeoutSec
        data_dir = $DataDir
        history_turns = $HistoryTurns
        agent_retry_count = $AgentRetryCount
        skills_source_dir = $SkillsSourceDir
        skills_install_dir = $SkillsInstallDir
        health_port = $HealthPort
        restart_backoff_ms = $RestartBackoffMs
        restart_max_ms = $RestartMaxMs
    }
}

$cfg | ConvertTo-Json -Depth 6 | Set-Content -Path $configPath -Encoding utf8

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

    docker compose up -d --build --force-recreate
}
finally {
    Pop-Location
}

Write-Host "Deploy complete."
Write-Host "Build version: $($env:APP_VERSION) ($($env:APP_COMMIT))"
Write-Host "Config: $configPath"
Write-Host "Health: http://127.0.0.1:$HealthPort/healthz"
