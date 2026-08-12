[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$ServerOrigin,
    [ValidateSet("apk", "aab", "all")]
    [string]$Format = "all",
    [string]$TermsUrl,
    [string]$PrivacyUrl
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "../..")).Path
$mobileRoot = Join-Path $repoRoot "apps/mobile"
$flutter = Join-Path $mobileRoot ".fvm/flutter_sdk/bin/flutter.bat"

if (-not (Test-Path -LiteralPath $flutter -PathType Leaf)) {
    throw "FVM Flutter was not found. Run 'fvm install' in apps/mobile first."
}

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
if ([string]::IsNullOrWhiteSpace($TermsUrl)) {
    $TermsUrl = "$origin/legal/terms"
}
if ([string]::IsNullOrWhiteSpace($PrivacyUrl)) {
    $PrivacyUrl = "$origin/legal/privacy"
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

foreach ($url in @("$origin/health", $TermsUrl, $PrivacyUrl)) {
    try {
        $response = Invoke-WebRequest -UseBasicParsing -Method Get -Uri $url -MaximumRedirection 3 -TimeoutSec 15
    }
    catch {
        throw "Required release endpoint is unavailable: $url ($($_.Exception.Message))"
    }
    if ([int]$response.StatusCode -lt 200 -or [int]$response.StatusCode -ge 400) {
        throw "Required release endpoint returned HTTP $($response.StatusCode): $url"
    }
}

$defines = @(
    "--dart-define=APP_ENV=production"
    "--dart-define=API_BASE_URL=$origin"
    "--dart-define=WS_URL=$($origin -replace '^https://', 'wss://')/im"
    "--dart-define=ENABLE_DEMO=false"
    "--dart-define=TERMS_URL=$TermsUrl"
    "--dart-define=PRIVACY_URL=$PrivacyUrl"
    "--dart-define=MEDIA_MAX_BYTES=104857600"
)

function Invoke-FlutterReleaseBuild([string]$target) {
    Push-Location $mobileRoot
    try {
        & $flutter build $target --release @defines
        if ($LASTEXITCODE -ne 0) {
            throw "Flutter $target release build failed with exit code $LASTEXITCODE."
        }
    }
    finally {
        Pop-Location
    }
}

if ($Format -in @("apk", "all")) {
    Invoke-FlutterReleaseBuild "apk"
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
    $targetApk = Join-Path $releaseDir "linli-im-$version-$targetName.apk"
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
    $targetAab = Join-Path $releaseDir "linli-im-$version-$targetName.aab"
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
