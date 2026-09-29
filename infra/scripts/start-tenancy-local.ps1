#requires -Version 7.0
param([switch]$SkipBuild)
$ErrorActionPreference = 'Stop'
$localRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$compose = Join-Path $localRoot 'infra/tenancy/compose.local.yml'
$configDir = Join-Path $localRoot '.data/tenancy-local'
$binDir = Join-Path $localRoot 'build/tenancy-local/bin'
function Assert-Exit([string]$stage) { if ($LASTEXITCODE -ne 0) { throw "$stage failed; no production operation was attempted." } }
& docker info --format '{{.OSType}}'
Assert-Exit 'Docker Linux engine check'
Push-Location (Join-Path $localRoot 'server')
try {
    & go run ./cmd/tenancy-local -root $configDir
    Assert-Exit 'Local configuration generation'
    & go run ./cmd/prepare-ipregion -out .data/ip2region
    Assert-Exit 'Offline IP region assets'
    # Keep credentials private on Windows. Never print environment contents.
    if ($IsWindows) {
        $principal = [Security.Principal.WindowsIdentity]::GetCurrent().Name
        & icacls $configDir /inheritance:r /grant:r "${principal}:(OI)(CI)F" 'SYSTEM:(OI)(CI)F' /Q | Out-Null
        Assert-Exit 'Private configuration permissions'
        # Reset children to the restricted parent ACL. Applying /inheritance:r
        # recursively would remove inherited grants from the files themselves.
        & icacls (Join-Path $configDir '*') /reset /T /Q | Out-Null
        Assert-Exit 'Private configuration inheritance'
    }
    if (!$SkipBuild) {
        New-Item -ItemType Directory -Path $binDir -Force | Out-Null
        $savedGoOS = $env:GOOS; $savedGoArch = $env:GOARCH; $savedCGO = $env:CGO_ENABLED
        try {
            $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
            # Migration tools are built but never automatically run/import accounts.
            foreach ($entry in @{ platform = './cmd/platform'; enterprise = './cmd/server'; 'tenancy-local' = './cmd/tenancy-local'; 'tenant-agent' = './cmd/tenant-agent'; 'tenant-import' = './cmd/tenant-import'; 'tenant-preflight' = './cmd/tenant-preflight' }.GetEnumerator()) {
                & go build -trimpath -o (Join-Path $binDir $entry.Key) $entry.Value
                Assert-Exit $entry.Key
            }
        } finally { $env:GOOS = $savedGoOS; $env:GOARCH = $savedGoArch; $env:CGO_ENABLED = $savedCGO }
    }
} finally { Pop-Location }
if (!$SkipBuild) {
    Push-Location (Join-Path $localRoot 'apps/admin')
    try {
        & npm run build
        Assert-Exit 'Enterprise admin build'
        & npm run build:platform
        Assert-Exit 'Platform admin build'
    } finally { Pop-Location }
    Push-Location (Join-Path $localRoot 'apps/mobile')
    try {
        $savedPubHost = $env:PUB_HOSTED_URL
        $env:PUB_HOSTED_URL = 'https://pub.dev'
        & node --test tool/verify_tenant_push_worker.cjs
        Assert-Exit 'Scoped browser push worker regression'
        $webOutput = Join-Path $localRoot 'build/tenancy-local/web'
        & fvm flutter build web --release --no-pub --base-href=/app/ "--output=$webOutput" --dart-define=APP_ENV=development --dart-define=PLATFORM_AUTH_URL=https://127.0.0.1:18443
        Assert-Exit 'Unified authentication Flutter Web build'
    } finally { $env:PUB_HOSTED_URL = $savedPubHost; Pop-Location }
    & docker compose -f $compose build platform-api enterprise-im
    Assert-Exit 'Local runtime image'
}
# Only this dedicated project is affected; named volumes survive restarts.
& docker compose -f $compose up -d --wait --wait-timeout 90
Assert-Exit 'Local runtime readiness'
# Bind-mounted Caddyfiles are not part of Compose's configuration hash. Recreate
# just the two local gateways so an edited route cannot keep serving stale rules.
& docker compose -f $compose up -d --no-deps --force-recreate --wait --wait-timeout 30 platform-gateway enterprise-gateway
Assert-Exit 'Local gateway configuration refresh'
& docker compose -f $compose run --rm --no-deps local-bootstrap
Assert-Exit 'Default enterprise activation'
Push-Location (Join-Path $localRoot 'server')
try {
    & go run ./cmd/tenancy-local -root $configDir -verify
    Assert-Exit 'Local public gateway authentication verification'
} finally { Pop-Location }
Write-Host 'Platform admin: http://127.0.0.1:18900/'
Write-Host 'Unified client: https://127.0.0.1:18443/app/'
Write-Host 'Enterprise admin/API: https://127.0.0.1:18444/'
Write-Host "Private local credentials: $configDir\credentials.json"
Write-Host 'No system CA trust was installed. Browser TLS trust is a separate, explicit local setup step.'
