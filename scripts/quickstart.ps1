param(
    [ValidateSet("openai","minimax","glm","custom")]
    [string]$Provider = "openai",
    [string]$TelegramToken = "",
    [string]$AgentUrl = "",
    [string]$AgentKey = "",
    [string]$AgentModel = "gpt-4o-mini",
    [string]$SystemPrompt = "",
    [string]$SkillsSourceDir = "openclaw-skills",
    [string]$SkillsInstallDir = "data/skills",
    [string]$ConfigPath = "config.json",
    [switch]$Run
)

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
        workers = 4
        queue_size = 64
        poll_timeout_second = 25
        request_timeout_sec = 60
        data_dir = "data"
        history_turns = 8
        agent_retry_count = 2
        skills_source_dir = $SkillsSourceDir
        skills_install_dir = $SkillsInstallDir
        health_port = 18080
        restart_backoff_ms = 1000
        restart_max_ms = 30000
    }
}

$json = $cfg | ConvertTo-Json -Depth 5
Set-Content -Path $ConfigPath -Value $json -Encoding utf8
Write-Host "Config written to $ConfigPath"

if ($Run) {
    go run ./cmd/clawlite run --config $ConfigPath
}
