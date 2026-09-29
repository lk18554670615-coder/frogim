#requires -Version 7.0
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Manifest)
$ErrorActionPreference = 'Stop'
# Read-only release gate. Passing this check never stops services or activates
# a tenant. Evidence is operator-owned and must describe actual acceptance.
$release = Get-Content -LiteralPath $Manifest -Raw | ConvertFrom-Json -AsHashtable
$required = @('go', 'admin', 'flutter', 'web', 'android', 'iosAppleSdk',
    'androidInstall', 'iosInstall', 'threeClientSmoke', 'getuiRealDevice',
    'voipLockedScreen', 'migrationRehearsal', 'rollbackRehearsal',
    'backupRestore', 'offsiteBackup', 'certificateRenewal', 'serverPreflight')
$failures = [Collections.Generic.List[string]]::new()
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
foreach ($name in @('edge', 'platform', 'enterprise', 'android', 'ios', 'web')) {
    $artifact = $release.artifacts[$name]
    if (!$artifact -or !$artifact.path -or !(Test-Path -LiteralPath $artifact.path -PathType Leaf) -or
        $artifact.sha256 -notmatch '^[0-9a-f]{64}$' -or
        (Get-FileHash -LiteralPath $artifact.path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $artifact.sha256) {
        $failures.Add("${name}: pinned artifact missing or changed")
    }
}
if ($release.rehearsalMinutes -le 0 -or $release.rehearsalMinutes -gt 90 -or
    $release.rollbackMinutes -le 0 -or $release.rollbackMinutes -gt 30) { $failures.Add('Rehearsal does not fit the two-hour window') }
if ($release.freeBytes -lt ($release.requiredBytes + 10GB) -or $release.requiredBytes -le 0) { $failures.Add('Insufficient verified disk headroom') }
if ($failures.Count) {
    $failures | ForEach-Object { Write-Output "NOT READY: $_" }
    throw 'Release blocked before maintenance. No services or data were changed.'
}
Write-Output 'Release evidence verified. Proceed only through the tenant-migrate receipt workflow; this check performed no deployment.'
