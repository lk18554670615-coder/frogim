#requires -Version 7.0
# Synthetic evidence in a disposable repository tests the two gate boundaries.
# These files must never be used as deployment evidence.
$ErrorActionPreference = 'Stop'
$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ('frogim-release-gate-' + [guid]::NewGuid().ToString('N'))
$fixtureRepo = Join-Path $fixtureRoot 'repo'
$scriptDirectory = Join-Path $fixtureRepo 'infra/scripts'
New-Item -ItemType Directory -Path $scriptDirectory -Force | Out-Null
$gate = Join-Path $scriptDirectory 'test-existing-server-release.ps1'
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'test-existing-server-release.ps1') -Destination $gate
& git -C $fixtureRepo init -q
& git -C $fixtureRepo add .
& git -C $fixtureRepo -c user.name=ReleaseGateTest -c user.email=release-gate@example.invalid commit -qm fixture
if ($LASTEXITCODE -ne 0) { throw 'Disposable test commit failed' }
& git -C $fixtureRepo tag fixture
$commit = (& git -C $fixtureRepo rev-parse HEAD).Trim()
$proof = Join-Path $fixtureRoot 'synthetic-proof.txt'
Set-Content -LiteralPath $proof -Value 'SYNTHETIC TEST ONLY. Never deployment evidence.'
$hash = (Get-FileHash -LiteralPath $proof -Algorithm SHA256).Hash.ToLowerInvariant()
$release = @{
    target = '18.163.165.233'; origin = 'https://18.163.165.233'; platformAuthUrl = 'https://18.163.165.233/platform'
    deploymentMode = 'isolated'; businessWritesEnabled = $false; commit = $commit; tag = 'fixture'
    rehearsalMinutes = 60; rollbackMinutes = 20; freeBytes = 30GB; requiredBytes = 10GB
    evidence = @{}; artifacts = @{}
}
foreach ($name in @('go','admin','web','migrationRehearsal','rollbackRehearsal','backupRestore','offsiteBackup','certificateRenewal','serverPreflight')) {
    $release.evidence[$name] = @{status='passed'; operator='synthetic-test'; commit=$commit; checkedAt=[DateTimeOffset]::UtcNow.ToString('o'); path=$proof; sha256=$hash}
}
foreach ($name in @('edge','platform','enterprise','web')) { $release.artifacts[$name] = @{path=$proof;sha256=$hash} }
$manifest = Join-Path $fixtureRoot 'manifest.json'
function Assert-Gate([string]$Stage, [bool]$Pass) {
    $release | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath $manifest -Encoding utf8
    $output = & (Join-Path $PSHOME 'pwsh.exe') -NoProfile -File $gate -Manifest $manifest -Stage $Stage 2>&1
    if (($LASTEXITCODE -eq 0) -ne $Pass) { throw "Unexpected gate result for ${Stage}: $output" }
}
Assert-Gate server-deploy $true
Assert-Gate open-business $false
$release.businessWritesEnabled = $true
Assert-Gate server-deploy $false
$release.businessWritesEnabled = 'false'
Assert-Gate server-deploy $false
$release.businessWritesEnabled = $false
$release.openingScope = 'web-first'
Assert-Gate open-business $false
$release.mobileBusinessAccess = 'disabled'
$release.mobileUpgradeDestination = 'https://18.163.165.233/app/'
$release.deferredChecks = @('android','iosAppleSdk','androidInstall','iosInstall','threeClientSmoke','getuiRealDevice','voipLockedScreen')
foreach ($name in @('flutter','webFirstApproval','webBusinessSmoke','mobileUpgradeEntry','passwordlessAccess')) {
    $release.evidence[$name] = @{status='passed';operator='synthetic-test';commit=$commit;checkedAt=[DateTimeOffset]::UtcNow.ToString('o');path=$proof;sha256=$hash}
}
Assert-Gate open-business $true
$release.mobileBusinessAccess = 'enabled'
Assert-Gate open-business $false
$release.mobileBusinessAccess = 'disabled'
$release.deferredChecks = @('android')
Assert-Gate open-business $false
$release.openingScope = 'unknown'
Assert-Gate open-business $false
$release.openingScope = 'three-clients'
Assert-Gate open-business $false
foreach ($name in @('flutter','android','iosAppleSdk','androidInstall','iosInstall','threeClientSmoke','getuiRealDevice','voipLockedScreen','passwordlessAccess')) {
    $release.evidence[$name] = @{status='passed';operator='synthetic-test';commit=$commit;checkedAt=[DateTimeOffset]::UtcNow.ToString('o');path=$proof;sha256=$hash}
}
foreach ($name in @('android','ios')) { $release.artifacts[$name] = @{path=$proof;sha256=$hash} }
Assert-Gate open-business $true
$release.rehearsalMinutes = 0
$release.rollbackMinutes = 0
Assert-Gate server-deploy $true
Assert-Gate open-business $false
$release.rehearsalMinutes = 91
$release.rollbackMinutes = 31
Assert-Gate open-business $false
$release.rehearsalMinutes = 60
$release.rollbackMinutes = 20
Set-Content -LiteralPath $proof -Value 'CHANGED SYNTHETIC PROOF'
Assert-Gate open-business $false
Write-Output 'PASS: isolated deploy, strict three-client opening, explicit Web-first approval and retained mobile deferrals are enforced; changed evidence is rejected.'
exit 0
