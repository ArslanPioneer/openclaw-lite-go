param(
    [string]$SourceDir = "..\openclaw\skills",
    [string]$TargetDir = ".\openclaw-skills"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -Path $SourceDir -PathType Container)) {
    throw "Source skills dir not found: $SourceDir"
}

New-Item -ItemType Directory -Force -Path $TargetDir | Out-Null
Copy-Item -Recurse -Force -Path (Join-Path $SourceDir "*") -Destination $TargetDir

Write-Host "Synced skills:"
Write-Host "  from: $SourceDir"
Write-Host "  to:   $TargetDir"
