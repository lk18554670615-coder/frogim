[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$ServerOrigin,
    [ValidateSet("apk", "aab", "all")]
    [string]$Format = "all",
    [string]$TermsUrl,
    [string]$PrivacyUrl,
    [string]$PlatformAuthUrl,
    [switch]$BuildOnly,
    [switch]$PreflightOnly
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "../..")).Path
$mobileRoot = Join-Path $repoRoot "apps/mobile"
$flutter = Join-Path $mobileRoot ".fvm/flutter_sdk/bin/flutter.bat"

$origin = $ServerOrigin.TrimEnd("/")
$originUri = $null
if (-not [Uri]::TryCreate($origin, [UriKind]::Absolute, [ref]$originUri) -or
    $originUri.Scheme -ne "https" -or
    -not [string]::IsNullOrEmpty($originUri.UserInfo) -or
    $originUri.AbsolutePath -ne "/" -or
    -not [string]::IsNullOrEmpty($originUri.Query) -or
    -not [string]::IsNullOrEmpty($originUri.Fragment)) {
    throw "ServerOrigin must be an HTTPS origin without credentials, path, query or fragment."
}
if ([string]::IsNullOrWhiteSpace($PlatformAuthUrl)) { $PlatformAuthUrl = "$origin/platform" }
$authUri = $null
if (-not [Uri]::TryCreate($PlatformAuthUrl, [UriKind]::Absolute, [ref]$authUri) -or
    $authUri.Scheme -ne 'https' -or $authUri.UserInfo -or $authUri.Query -or $authUri.Fragment -or
    $authUri.AbsolutePath -notin @('/', '/platform')) {
    throw 'PlatformAuthUrl must be an HTTPS root or /platform address.'
}
$PlatformAuthUrl = $PlatformAuthUrl.TrimEnd('/')
if ($BuildOnly -and $PreflightOnly) { throw 'BuildOnly and PreflightOnly are mutually exclusive.' }
if ([string]::IsNullOrWhiteSpace($TermsUrl)) {
    $TermsUrl = "$origin/legal/terms.html"
}
if ([string]::IsNullOrWhiteSpace($PrivacyUrl)) {
    $PrivacyUrl = "$origin/legal/privacy.html"
}

foreach ($entry in @{
    TermsUrl = $TermsUrl
    PrivacyUrl = $PrivacyUrl
}.GetEnumerator()) {
    $uri = $null
    if (-not [Uri]::TryCreate($entry.Value, [UriKind]::Absolute, [ref]$uri) -or $uri.Scheme -ne "https") {
        throw "$($entry.Key) must be an absolute HTTPS URL."
    }
}

if (-not $BuildOnly) {
try {
    $healthResponse = Invoke-WebRequest -UseBasicParsing -Method Get -Uri "$origin/health" -MaximumRedirection 3 -TimeoutSec 15
}
catch {
    throw "Required release endpoint is unavailable: $origin/health ($($_.Exception.Message))"
}
if ([int]$healthResponse.StatusCode -lt 200 -or [int]$healthResponse.StatusCode -ge 400) {
    throw "Required release endpoint returned HTTP $($healthResponse.StatusCode): $origin/health"
}

try {
    $readyResponse = Invoke-WebRequest -UseBasicParsing -Method Get -Uri "$origin/ready" -MaximumRedirection 3 -TimeoutSec 15
}
catch {
    throw "Required release endpoint is unavailable: $origin/ready ($($_.Exception.Message))"
}
try {
    $readyPayload = $readyResponse.Content | ConvertFrom-Json
}
catch {
    throw "Required release endpoint returned invalid JSON: $origin/ready"
}
if ([int]$readyResponse.StatusCode -ne 200 -or $readyPayload.status -ne "ready") {
    throw "Required release endpoint is not ready: $origin/ready"
}

try {
    $authPolicyResponse = Invoke-WebRequest -UseBasicParsing -Method Get -Uri "$PlatformAuthUrl/v2/config/auth" -MaximumRedirection 3 -TimeoutSec 15
}
catch {
    throw "Required authentication contract is unavailable: $PlatformAuthUrl/v2/config/auth ($($_.Exception.Message))"
}
try {
    $authPolicy = $authPolicyResponse.Content | ConvertFrom-Json
}
catch {
    throw "Required authentication contract returned invalid JSON: $PlatformAuthUrl/v2/config/auth"
}
# Platform authentication advertises capabilities, not the legacy enterprise
# password-policy fields. Require the contract used by this platform-only build.
if ([int]$authPolicyResponse.StatusCode -ne 200 -or
    $authPolicy.tenantAuthentication -ne $true -or
    $authPolicy.passwordLoginEnabled -ne $true -or
    $authPolicy.passwordChangeEnabled -ne $true -or
    $authPolicy.registrationEnabled -isnot [bool] -or
    $authPolicy.otpLoginEnabled -isnot [bool] -or
    $authPolicy.passwordResetEnabled -isnot [bool]) {
    throw "Authentication contract is incompatible with this client: $PlatformAuthUrl/v2/config/auth"
}

function Get-WebResponseText($response) {
    if ($response.Content -is [byte[]]) {
        return [Text.Encoding]::UTF8.GetString($response.Content)
    }
    return [string]$response.Content
}

function Assert-ProductionLegalDocument([string]$url, [string]$label) {
    try {
        $response = Invoke-WebRequest -UseBasicParsing -Method Get -Uri $url -MaximumRedirection 3 -TimeoutSec 15
    }
    catch {
        throw "Required release endpoint is unavailable: $url ($($_.Exception.Message))"
    }
    if ([int]$response.StatusCode -lt 200 -or [int]$response.StatusCode -ge 400) {
        throw "Required release endpoint returned HTTP $($response.StatusCode): $url"
    }

    $content = Get-WebResponseText $response
    if ([string]::IsNullOrWhiteSpace($content) -or $content.Length -lt 500) {
        throw "$label is empty or too short for production: $url"
    }
    if ($content -notmatch '\u9752\u86D9\u5471\u5471') {
        throw "$label does not contain the current product name: $url"
    }
    $forbiddenPatterns = @(
        '\u90BB\u91CC\u901A\u8BAF',
        '\u5F00\u53D1\u6D4B\u8BD5',
        '\u6D4B\u8BD5\u9636\u6BB5',
        '\u4EC5\u4F9B\u6D4B\u8BD5',
        '\u5360\u4F4D\u6587\u672C',
        '\u4E0D\u80FD\u66FF\u4EE3\u6B63\u5F0F'
    )
    foreach ($pattern in $forbiddenPatterns) {
        if ($content -match $pattern) {
            throw "$label still contains a non-production marker: $url"
        }
    }
}

Assert-ProductionLegalDocument -url $TermsUrl -label 'Terms document'
Assert-ProductionLegalDocument -url $PrivacyUrl -label 'Privacy document'

}
if ($BuildOnly) { Write-Host "Build only: online acceptance has NOT passed; this artifact cannot authorize cutover." }

if ($PreflightOnly) {
    Write-Host "Android release preflight completed."
    return
}

if (-not (Test-Path -LiteralPath $flutter -PathType Leaf)) {
    throw "FVM Flutter was not found. Run 'fvm install' in apps/mobile first."
}

$defines = @(
    "--dart-define=APP_ENV=production"
    "--dart-define=PLATFORM_AUTH_URL=$PlatformAuthUrl"
    "--dart-define=ENABLE_DEMO=false"
    "--dart-define=TERMS_URL=$TermsUrl"
    "--dart-define=PRIVACY_URL=$PrivacyUrl"
    "--dart-define=MEDIA_MAX_BYTES=104857600"
)

# Client SDK credentials only; never use the server-side master secret here.
$getuiNames = @('GETUI_APP_ID', 'GETUI_APP_KEY', 'GETUI_APP_SECRET')
$getuiReady = @($getuiNames | Where-Object { [string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($_)) }).Count -eq 0
$defines += "--dart-define=GETUI_ENABLED=$($getuiReady.ToString().ToLowerInvariant())"
if ($getuiReady) {
    foreach ($name in $getuiNames) { $defines += "--dart-define=$name=$([Environment]::GetEnvironmentVariable($name))" }
} elseif (-not $BuildOnly) { throw 'Getui client SDK credentials are required for a production release. BuildOnly artifacts still require device acceptance.' }

function Invoke-FlutterReleaseBuild([string]$target) {
    $previousGetuiAppId = $env:GETUI_APPID
    Push-Location $mobileRoot
    try {
        # Gradle's native manifest and Dart's registration must use the same app.
        if ($getuiReady) { $env:GETUI_APPID = $env:GETUI_APP_ID }
        & $flutter build $target --release @defines
        if ($LASTEXITCODE -ne 0) {
            throw "Flutter $target release build failed with exit code $LASTEXITCODE."
        }
    }
    finally {
        $env:GETUI_APPID = $previousGetuiAppId
        Pop-Location
    }
}

function Assert-FlutterApkAssets([string]$apkPath) {
    if (-not (Test-Path -LiteralPath $apkPath -PathType Leaf)) {
        throw "Flutter APK was not created: $apkPath"
    }

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::OpenRead($apkPath)
    try {
        $requiredEntries = @(
            "assets/flutter_assets/AssetManifest.bin"
            "assets/flutter_assets/assets/brand/qingwaguagua-mark-transparent.png"
            "assets/flutter_assets/packages/cupertino_icons/assets/CupertinoIcons.ttf"
        )
        foreach ($entry in $requiredEntries) {
            $asset = $archive.GetEntry($entry)
            if ($null -eq $asset -or $asset.Length -le 0) {
                throw "Flutter APK is incomplete; required asset is missing: $entry"
            }
        }
    }
    finally {
        $archive.Dispose()
    }
}

if ($Format -in @("apk", "all")) {
    Invoke-FlutterReleaseBuild "apk"
    Assert-FlutterApkAssets (Join-Path $mobileRoot "build/app/outputs/flutter-apk/app-release.apk")
}
if ($Format -in @("aab", "all")) {
    Invoke-FlutterReleaseBuild "appbundle"
}

$versionLine = Select-String -LiteralPath (Join-Path $mobileRoot "pubspec.yaml") -Pattern '^version:\s*(\S+)' | Select-Object -First 1
if ($null -eq $versionLine) {
    throw "pubspec.yaml does not contain a version."
}
$version = $versionLine.Matches[0].Groups[1].Value.Replace("+", "-")
$targetName = $originUri.Host.Replace(":", "-")
$releaseDir = Join-Path $repoRoot "build/releases/android/$version-$targetName"
[void](New-Item -ItemType Directory -Path $releaseDir -Force)

$published = [System.Collections.Generic.List[object]]::new()
$sdkRoot = if ($env:ANDROID_HOME) { $env:ANDROID_HOME } else { Join-Path $env:LOCALAPPDATA "Android/Sdk" }
$apksigner = Get-ChildItem -LiteralPath (Join-Path $sdkRoot "build-tools") -Recurse -Filter "apksigner.bat" -ErrorAction Stop |
    Sort-Object FullName -Descending |
    Select-Object -First 1 -ExpandProperty FullName
$javaHome = if ($env:JAVA_HOME) { $env:JAVA_HOME } else { "C:/Program Files/Android/Android Studio/jbr" }
if (-not (Test-Path -LiteralPath (Join-Path $javaHome "bin/java.exe") -PathType Leaf)) {
    throw "Android Studio JBR was not found; set JAVA_HOME before building."
}
$previousJavaHome = $env:JAVA_HOME
$env:JAVA_HOME = $javaHome

try {
if ($Format -in @("apk", "all")) {
    $sourceApk = Join-Path $mobileRoot "build/app/outputs/flutter-apk/app-release.apk"
    $targetApk = Join-Path $releaseDir "qingwaguagua-im-$version-$targetName.apk"
    Copy-Item -LiteralPath $sourceApk -Destination $targetApk -Force
    try {
        # Current Android Studio JBR emits a native-access warning on stderr.
        # PowerShell 5 wraps it as NativeCommandError under Stop even when the
        # signer succeeds, so judge this native tool by its exit code and its
        # explicit signed-scheme report.
        $savedErrorActionPreference = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        try {
            $verifyOutput = & $apksigner verify --verbose --print-certs $targetApk 2>&1
            $verifyExitCode = $LASTEXITCODE
        }
        finally {
            $ErrorActionPreference = $savedErrorActionPreference
        }
        if ($verifyExitCode -ne 0 -or -not ($verifyOutput -match 'Verified using v2 scheme \(APK Signature Scheme v2\): true')) {
            throw "APK signature verification failed."
        }
        $certificateDigest = [regex]::Match(
            ($verifyOutput -join "`n"),
            'certificate SHA-256 digest:\s*([0-9a-fA-F]{64})'
        ).Groups[1].Value.ToLowerInvariant()
        if ([string]::IsNullOrWhiteSpace($certificateDigest)) {
            throw "APK signer certificate digest was not reported."
        }
    }
    finally {
        $env:JAVA_HOME = $javaHome
    }
    $published.Add([ordered]@{
        type = "apk"
        file = [IO.Path]::GetFileName($targetApk)
        bytes = (Get-Item -LiteralPath $targetApk).Length
        sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $targetApk).Hash.ToLowerInvariant()
        signerCertificateSha256 = $certificateDigest
    })
}

if ($Format -in @("aab", "all")) {
    $sourceAab = Join-Path $mobileRoot "build/app/outputs/bundle/release/app-release.aab"
    $targetAab = Join-Path $releaseDir "qingwaguagua-im-$version-$targetName.aab"
    Copy-Item -LiteralPath $sourceAab -Destination $targetAab -Force
    $jarsigner = Join-Path $javaHome "bin/jarsigner.exe"
    $savedErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        & $jarsigner -verify $targetAab 2>&1 | Out-Null
        $jarVerifyExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $savedErrorActionPreference
    }
    if ($jarVerifyExitCode -ne 0) {
        throw "AAB signature verification failed."
    }
    $published.Add([ordered]@{
        type = "aab"
        file = [IO.Path]::GetFileName($targetAab)
        bytes = (Get-Item -LiteralPath $targetAab).Length
        sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $targetAab).Hash.ToLowerInvariant()
    })
}
}
finally {
    $env:JAVA_HOME = $previousJavaHome
}

$manifest = [ordered]@{
    schemaVersion = 1
    getuiEnabled = $getuiReady
    platformAuthUrl = $PlatformAuthUrl
    onlinePreflightPassed = (-not $BuildOnly)
    appVersion = $versionLine.Matches[0].Groups[1].Value
    serverOrigin = $origin
    termsUrl = $TermsUrl
    privacyUrl = $PrivacyUrl
    gitCommit = (git -C $repoRoot rev-parse HEAD).Trim()
    builtAtUtc = [DateTime]::UtcNow.ToString("o")
    artifacts = $published
}
$manifestPath = Join-Path $releaseDir "release-manifest.json"
[IO.File]::WriteAllText(
    $manifestPath,
    ($manifest | ConvertTo-Json -Depth 5) + [Environment]::NewLine,
    [Text.UTF8Encoding]::new($false)
)

Write-Host "Android release completed: $releaseDir"
$published | ForEach-Object {
    Write-Host "$($_.type) $($_.file) $($_.bytes) bytes sha256=$($_.sha256)"
}
