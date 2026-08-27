$ErrorActionPreference = 'Stop'

$rootDir = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$containerName = if ($env:WUKONG_LOG_TEST_CONTAINER) { $env:WUKONG_LOG_TEST_CONTAINER } else { 'nexachat-wukongim-1' }
$managerToken = if ($env:IM_WUKONG_MANAGER_TOKEN) { $env:IM_WUKONG_MANAGER_TOKEN } else { 'local-wukong-manager-token-change-me' }
$composeFiles = @(
  'compose',
  '-f', (Join-Path $rootDir 'infra\compose.yaml'),
  '-f', (Join-Path $rootDir 'infra\compose.wukong.yaml')
)

$startedAt = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.fffZ')
& docker @composeFiles --profile probe build wukong-probe | Out-Host
if ($LASTEXITCODE -ne 0) { throw 'failed to build wukong-probe' }

$strictErrorPreference = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
$probeOutput = (& docker @composeFiles --profile probe run --rm --no-deps wukong-probe `
  -api http://wukongim:5001 `
  -manager-api http://wukongim:5300 `
  -tcp tcp://wukongim:5100 `
  -manager-token $managerToken `
  -redaction-only `
  -timeout 30s | Out-String)
$probeExitCode = $LASTEXITCODE
$ErrorActionPreference = $strictErrorPreference
if ($probeExitCode -ne 0) {
  Write-Output $probeOutput
  throw 'wukong-probe failed'
}
Write-Output $probeOutput.TrimEnd()
if ($probeOutput -notmatch '"duplicateMasterDeviceKick": true') {
  throw 'probe did not prove duplicate master-device eviction'
}

$ErrorActionPreference = 'Continue'
$rawLogs = (& docker logs --since $startedAt $containerName 2>&1 | Out-String)
$logExitCode = $LASTEXITCODE
$ErrorActionPreference = $strictErrorPreference
if ($logExitCode -ne 0) { throw 'failed to read raw WuKongIM container logs' }
if ($rawLogs -notmatch 'close old conn for master') {
  throw 'raw WuKongIM logs did not retain the duplicate master-device diagnostic'
}
if ($rawLogs -match 'WK_LOG_REDACTION_(TOKEN|MESSAGE)_MARKER') {
  throw 'raw WuKongIM logs leaked a runtime token or message-body canary'
}
if ($rawLogs -match '(?i)(aesKey|aesIV|expectToken|actToken|msgKey|signStr|verifyString)') {
  throw 'raw WuKongIM logs contain a forbidden cryptographic credential field'
}

Write-Output 'WuKongIM raw-log redaction gate passed'
