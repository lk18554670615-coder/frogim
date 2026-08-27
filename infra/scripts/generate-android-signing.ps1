[CmdletBinding()]
param(
    [string]$Alias = "linli-upload"
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "../..")).Path
$androidDir = Join-Path $repoRoot "apps/mobile/android"
$keyStore = Join-Path $androidDir "release-upload.jks"
$propertiesFile = Join-Path $androidDir "key.properties"
$keytool = "C:\Program Files\Android\Android Studio\jbr\bin\keytool.exe"

if (-not (Test-Path -LiteralPath $keytool -PathType Leaf)) {
    throw "Android Studio keytool was not found: $keytool"
}
if (Test-Path -LiteralPath $keyStore -PathType Leaf) {
    throw "Refusing to replace the existing Android release key: $keyStore"
}
if (Test-Path -LiteralPath $propertiesFile -PathType Leaf) {
    throw "Refusing to replace the existing Android signing properties: $propertiesFile"
}

function New-RandomPassword {
    $bytes = [byte[]]::new(36)
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
    }
    finally {
        $generator.Dispose()
    }
    return [Convert]::ToBase64String($bytes).Replace("+", "-").Replace("/", "_").TrimEnd("=")
}

$password = New-RandomPassword
$env:LINLI_ANDROID_STORE_PASSWORD = $password
$env:LINLI_ANDROID_KEY_PASSWORD = $password
try {
    & $keytool `
        -genkeypair `
        -keystore $keyStore `
        -storetype PKCS12 `
        -storepass:env LINLI_ANDROID_STORE_PASSWORD `
        -keypass:env LINLI_ANDROID_KEY_PASSWORD `
        -alias $Alias `
        -keyalg RSA `
        -keysize 4096 `
        -sigalg SHA256withRSA `
        -validity 9125 `
        -dname "CN=Linli Communications, OU=Mobile, O=Linli, L=Shenzhen, ST=Guangdong, C=CN"
    if ($LASTEXITCODE -ne 0) {
        throw "keytool failed with exit code $LASTEXITCODE"
    }

    $properties = @(
        "RELEASE_STORE_FILE=release-upload.jks"
        "RELEASE_STORE_PASSWORD=$password"
        "RELEASE_KEY_ALIAS=$Alias"
        "RELEASE_KEY_PASSWORD=$password"
    ) -join [Environment]::NewLine
    [System.IO.File]::WriteAllText($propertiesFile, $properties + [Environment]::NewLine, [System.Text.UTF8Encoding]::new($false))
}
finally {
    Remove-Item Env:LINLI_ANDROID_STORE_PASSWORD -ErrorAction SilentlyContinue
    Remove-Item Env:LINLI_ANDROID_KEY_PASSWORD -ErrorAction SilentlyContinue
}

Write-Host "Android release upload key generated."
Write-Host "Keystore: $keyStore"
Write-Host "Properties: $propertiesFile"
Write-Host "Both files are excluded from Git. Back them up offline before publishing."
