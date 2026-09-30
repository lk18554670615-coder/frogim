#requires -Version 7.0
param([ValidateSet('/', '/admin/')][string]$AdminBase = '/')
$ErrorActionPreference = 'Stop'
$bundleRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
function Assert-BundleExit([string]$stage) {
    if ($LASTEXITCODE -ne 0) { throw "$stage failed. No deployment was performed." }
}
$bundleEndpoint = if ($IsWindows) { 'npipe:////./pipe/dockerDesktopLinuxEngine' } else { 'unix:///var/run/docker.sock' }
$engine = & docker --host $bundleEndpoint info --format '{{.OSType}}'
Assert-BundleExit 'Docker inspection'
if ($engine -ne 'linux') { throw 'A local Linux Docker engine is required.' }
$imReference = (& docker --host $bundleEndpoint image inspect frogim/tenancy-im:workspace --format '{{index .RepoDigests 0}}').Trim()
Assert-BundleExit 'Pinned patched IM image inspection'
if ($imReference -notmatch '^frogim/tenancy-im@sha256:[a-f0-9]{64}$') { throw 'Build the pinned local patched IM image first.' }
# Docker Desktop BuildKit needs the local tag alongside the immutable digest
# when resolving a locally built (not registry-published) source image.
$imBuildReference = $imReference.Replace('frogim/tenancy-im@','frogim/tenancy-im:workspace@')
$savedGoOS=$env:GOOS; $savedGoArch=$env:GOARCH; $savedCGO=$env:CGO_ENABLED
Push-Location $bundleRoot
try {
    $env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
    foreach ($entry in @{enterprise='./cmd/server'; 'tenant-runtime'='./cmd/tenant-runtime'; 'tenant-volume'='./cmd/tenant-volume'; 'redis-database'='./cmd/redis-database'}.GetEnumerator()) {
        & go -C server build -trimpath -o "../build/tenancy-local/bin/$($entry.Key)" $entry.Value
        Assert-BundleExit 'Enterprise runtime compilation'
    }
} finally { $env:GOOS=$savedGoOS; $env:GOARCH=$savedGoArch; $env:CGO_ENABLED=$savedCGO; Pop-Location }
Push-Location (Join-Path $bundleRoot 'apps/admin')
try {
    $savedAdminBase = $env:ENTERPRISE_ADMIN_BASE
    $savedAdminApi = $env:VITE_ADMIN_API_URL
    $env:ENTERPRISE_ADMIN_BASE = $AdminBase
    if ($AdminBase -eq '/admin/') { $env:VITE_ADMIN_API_URL = '/v2/admin' }
    try { & npm run build; Assert-BundleExit 'Enterprise admin production build' }
    finally { $env:ENTERPRISE_ADMIN_BASE = $savedAdminBase; $env:VITE_ADMIN_API_URL = $savedAdminApi }
} finally { Pop-Location }
foreach ($name in @('ip2region_v4.xdb','ip2region_v6.xdb','LICENSE.md','ipregion.lock.json')) {
    if (!(Test-Path -LiteralPath (Join-Path $bundleRoot "server/.data/ip2region/$name") -PathType Leaf)) {
        throw 'Prepared offline IP-region assets are required; no production files will be copied.'
    }
}
Push-Location $bundleRoot
try {
    & docker --host $bundleEndpoint build --pull=false --build-arg "IM_IMAGE=$imBuildReference" -f infra/tenancy/Dockerfile.enterprise-bundle -t frogim/enterprise-bundle:local .
    Assert-BundleExit 'Enterprise bundle image build'
    $result = & docker --host $bundleEndpoint image inspect frogim/enterprise-bundle:local --format '{{index .RepoDigests 0}}'
    Assert-BundleExit 'Immutable result inspection'
    Write-Host "Enterprise tools image (runtime mode is bound by its private release profile): $result"
    Write-Host 'No running services, private enterprise configuration, versions or deployment catalogs were changed.'
} finally { Pop-Location }
