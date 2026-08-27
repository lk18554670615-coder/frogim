param(
    [string]$OutputDirectory = (Join-Path $PSScriptRoot "cache"),
    [switch]$Refresh
)

$ErrorActionPreference = "Stop"
$lockPath = Join-Path $PSScriptRoot "versions.lock.json"
$lock = Get-Content -LiteralPath $lockPath -Raw -Encoding utf8 | ConvertFrom-Json
$resolvedOutput = [System.IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force -Path $resolvedOutput | Out-Null

foreach ($artifact in $lock.artifacts) {
    if (-not $artifact.url) {
        continue
    }
    $destination = Join-Path $resolvedOutput $artifact.file
    $mustDownload = $Refresh -or -not (Test-Path -LiteralPath $destination)
    if (-not $mustDownload) {
        $actual = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()
        $mustDownload = $actual -ne $artifact.sha256
    }
    if ($mustDownload) {
        $temporary = "$destination.download"
        Invoke-WebRequest -Uri $artifact.url -OutFile $temporary
        $actual = (Get-FileHash -LiteralPath $temporary -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $artifact.sha256) {
            Remove-Item -LiteralPath $temporary -Force
            throw "Checksum mismatch for $($artifact.id): expected $($artifact.sha256), got $actual"
        }
        Move-Item -LiteralPath $temporary -Destination $destination -Force
    }
    $verified = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Host "verified $($artifact.id) $verified"
}

Write-Host "Pinned artifacts are available at $resolvedOutput"
