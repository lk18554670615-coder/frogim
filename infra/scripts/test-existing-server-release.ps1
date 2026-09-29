#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Manifest,
    [ValidateSet('server-deploy', 'open-business')][string]$Stage = 'open-business'
)
$ErrorActionPreference = 'Stop'
# Read-only release gate. Passing this check never stops services or activates
# a tenant. Evidence is operator-owned and must describe actual acceptance.
$release = Get-Content -LiteralPath $Manifest -Raw | ConvertFrom-Json -AsHashtable
$required = @('go', 'admin', 'web', 'migrationRehearsal', 'rollbackRehearsal',
    'backupRestore', 'offsiteBackup', 'certificateRenewal', 'serverPreflight')
$backupDelivery = if ($release.backupDelivery) { $release.backupDelivery } else { 'offsite' }
if ($backupDelivery -eq 'server-local-approved') {
    $required = @($required | Where-Object { $_ -ne 'offsiteBackup' })
    $required += @('serverLocalBackupApproval', 'serverBackupVerified')
}
$clientChecks = @('flutter', 'android', 'iosAppleSdk', 'androidInstall',
    'iosInstall', 'threeClientSmoke', 'getuiRealDevice', 'voipLockedScreen', 'passwordlessAccess')
$artifacts = @('edge', 'platform', 'enterprise', 'web')
$openingScope = if ($release.openingScope) { $release.openingScope } else { 'three-clients' }
if ($Stage -eq 'open-business') {
    if ($openingScope -eq 'web-first') {
        $required += @('flutter', 'webFirstApproval', 'webBusinessSmoke', 'mobileUpgradeEntry', 'passwordlessAccess')
    } else {
        $required += $clientChecks
        $artifacts += @('android', 'ios')
    }
}
$failures = [Collections.Generic.List[string]]::new()
if ($backupDelivery -notin @('offsite', 'server-local-approved')) { $failures.Add('Unknown backup delivery scope') }
if ($backupDelivery -eq 'server-local-approved' -and
    ($release.offsiteBackupStatus -ne 'deferred' -or $release.originalDataPreserved -isnot [bool] -or !$release.originalDataPreserved)) {
    $failures.Add('Server-local cutover must retain original data and explicitly defer offsite delivery')
}
if ($openingScope -notin @('three-clients', 'web-first')) { $failures.Add('Unknown opening scope') }
if ($Stage -eq 'open-business' -and $openingScope -eq 'web-first' -and
    ($release.mobileBusinessAccess -ne 'disabled' -or $release.mobileUpgradeDestination -ne 'https://18.163.165.233/app/' -or
    $release.deferredChecks.Count -ne 7 -or
    @($clientChecks | Where-Object { $_ -notin @('flutter', 'passwordlessAccess') -and $_ -notin $release.deferredChecks }).Count -ne 0)) {
    $failures.Add('Web-first opening must disable mobile business access and explicitly retain all seven deferred client checks')
}
if ($Stage -eq 'server-deploy' -and
    ($release.deploymentMode -ne 'isolated' -or $release.businessWritesEnabled -isnot [bool] -or $release.businessWritesEnabled)) {
    $failures.Add('Server deployment requires explicit isolation and disabled business writes')
}
if ($release.target -ne '18.163.165.233' -or $release.origin -ne 'https://18.163.165.233' -or
    $release.platformAuthUrl -ne 'https://18.163.165.233/platform') { $failures.Add('Target or public addressing mismatch') }
if ($release.commit -notmatch '^[0-9a-f]{40}$' -or !$release.tag) { $failures.Add('Missing frozen commit/tag') }
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$head = & git -C $repo rev-parse HEAD
if ($LASTEXITCODE -ne 0 -or $head -ne $release.commit) { $failures.Add('HEAD differs from release commit') }
$dirty = & git -C $repo status --porcelain
if ($LASTEXITCODE -ne 0 -or $dirty) { $failures.Add('Working tree is not clean') }
if ($release.tag -and $release.tag -match '^[a-zA-Z0-9][a-zA-Z0-9._/-]+$') {
    $tagCommit = & git -C $repo rev-parse --verify "refs/tags/$($release.tag)^{commit}" 2>$null
    if ($LASTEXITCODE -ne 0 -or $tagCommit -ne $release.commit) { $failures.Add('Tag does not identify release commit') }
} else { $failures.Add('Invalid release tag') }
foreach ($name in $required) {
    $evidence = $release.evidence[$name]
    if (!$evidence -or $evidence.status -ne 'passed' -or !$evidence.operator -or $evidence.commit -ne $release.commit) {
        $failures.Add("${name}: acceptance missing or belongs to another commit"); continue
    }
    if (!$evidence.path -or ![IO.Path]::IsPathFullyQualified($evidence.path) -or
        !(Test-Path -LiteralPath $evidence.path -PathType Leaf) -or $evidence.sha256 -notmatch '^[0-9a-f]{64}$') {
        $failures.Add("${name}: evidence file/hash missing"); continue
    }
    if ((Get-FileHash -LiteralPath $evidence.path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $evidence.sha256) {
        $failures.Add("${name}: evidence has changed")
    }
    $checked = [DateTimeOffset]::MinValue
    if (![DateTimeOffset]::TryParse([string]$evidence.checkedAt, [ref]$checked) -or $checked -gt [DateTimeOffset]::UtcNow) {
        $failures.Add("${name}: invalid verification time")
    } elseif ($name -eq 'serverPreflight' -and $checked -lt [DateTimeOffset]::UtcNow.AddHours(-2)) {
        $failures.Add('serverPreflight: older than two hours')
    }
}
foreach ($name in $artifacts) {
    $artifact = $release.artifacts[$name]
    if (!$artifact -or !$artifact.path -or !(Test-Path -LiteralPath $artifact.path -PathType Leaf) -or
        $artifact.sha256 -notmatch '^[0-9a-f]{64}$' -or
        (Get-FileHash -LiteralPath $artifact.path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $artifact.sha256) {
        $failures.Add("${name}: pinned artifact missing or changed")
    }
}
if ($Stage -eq 'open-business' -and
    ($release.rehearsalMinutes -le 0 -or $release.rehearsalMinutes -gt 90 -or
    $release.rollbackMinutes -le 0 -or $release.rollbackMinutes -gt 30)) { $failures.Add('Rehearsal does not fit the two-hour window') }
if ($release.freeBytes -lt ($release.requiredBytes + 10GB) -or $release.requiredBytes -le 0) { $failures.Add('Insufficient verified disk headroom') }
if ($failures.Count) {
    $failures | ForEach-Object { Write-Output "NOT READY: $_" }
    throw "Release stage '$Stage' blocked. No services or data were changed."
}
if ($Stage -eq 'server-deploy') {
    Write-Output 'Isolated server deployment evidence verified. Client, VoIP and complete cutover timing acceptance remains required before opening; no business write authorization was granted.'
} else {
    Write-Output "Opening evidence verified (scope: $openingScope). Proceed only through the tenant-migrate receipt workflow; this check performed no deployment or activation."
}
