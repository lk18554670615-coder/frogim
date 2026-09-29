#requires -Version 7.0
param(
    [Parameter(Mandatory=$true)][string]$PlatformUrl,
    [Parameter(Mandatory=$true)][string]$TermsUrl,
    [Parameter(Mandatory=$true)][string]$PrivacyUrl
)
$ErrorActionPreference = 'Stop'
$bundleRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$bundleEndpoint = if ($IsWindows) { 'npipe:////./pipe/dockerDesktopLinuxEngine' } else { 'unix:///var/run/docker.sock' }
function Assert-PlatformBuild([string]$stage) { if ($LASTEXITCODE -ne 0) { throw "$stage failed. No deployment performed." } }
foreach ($value in @($TermsUrl,$PrivacyUrl)) {
    $parsed = $null
    if (![Uri]::TryCreate($value, [UriKind]::Absolute, [ref]$parsed) -or $parsed.Scheme -ne 'https' -or !$parsed.Host -or $parsed.UserInfo -or $parsed.Fragment) {
        throw 'Production terms and privacy addresses must be HTTPS URLs without user-info or fragments.'
    }
}
Push-Location $bundleRoot
try {
    & go -C server run ./cmd/platform-bundle -check-public-url $PlatformUrl
    Assert-PlatformBuild 'Platform address validation'
    $engine = & docker --host $bundleEndpoint info --format '{{.OSType}}'
    Assert-PlatformBuild 'Local engine inspection'
    if ($engine -ne 'linux') { throw 'A local Linux Docker engine is required.' }
    $savedGoOS=$env:GOOS; $savedGoArch=$env:GOARCH; $savedCGO=$env:CGO_ENABLED
    try {
        $env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
        foreach ($entry in @{platform='./cmd/platform'; 'tenant-runtime'='./cmd/tenant-runtime'}.GetEnumerator()) {
            & go -C server build -trimpath -o "../build/platform-production/bin/$($entry.Key)" $entry.Value
            Assert-PlatformBuild 'Platform runtime compilation'
        }
    } finally { $env:GOOS=$savedGoOS; $env:GOARCH=$savedGoArch; $env:CGO_ENABLED=$savedCGO }
    Push-Location apps/admin
    try { & npm run build:platform; Assert-PlatformBuild 'Platform admin build' } finally { Pop-Location }
    Push-Location apps/mobile
    try {
        $webOutput = Join-Path $bundleRoot 'build/platform-production/web'
        & fvm flutter build web --release --no-pub --no-web-resources-cdn --base-href=/app/ "--output=$webOutput" --dart-define=APP_ENV=production "--dart-define=PLATFORM_AUTH_URL=$PlatformUrl" "--dart-define=TERMS_URL=$TermsUrl" "--dart-define=PRIVACY_URL=$PrivacyUrl"
        Assert-PlatformBuild 'Production unified Web build'
    } finally { Pop-Location }
    & docker --host $bundleEndpoint build --pull=false --build-arg "PLATFORM_PUBLIC_URL=$PlatformUrl" -f infra/tenancy/Dockerfile.platform-bundle -t frogim/platform-bundle:local .
    Assert-PlatformBuild 'Platform bundle image build'
    $result = & docker --host $bundleEndpoint image inspect frogim/platform-bundle:local --format '{{index .RepoDigests 0}}'
    Assert-PlatformBuild 'Immutable image inspection'
    Write-Host "Platform tools image: $result"
    Write-Host 'Image built only. No private config, existing containers, volumes, versions, catalogs or production systems changed.'
} finally { Pop-Location }
