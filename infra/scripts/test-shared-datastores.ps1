#requires -Version 7.0
param()
$ErrorActionPreference = 'Stop'
$sharedTestRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$sharedTestEndpoint = if ($IsWindows) { 'npipe:////./pipe/dockerDesktopLinuxEngine' } else { 'unix:///var/run/docker.sock' }
$existing = @(& docker --host $sharedTestEndpoint volume ls -q --filter 'label=com.docker.compose.project=frogim-shared-default')
if ($LASTEXITCODE -ne 0 -or $existing.Count -gt 0) { throw 'Run on a clean disposable Docker engine before initializing persistent shared data. Existing volumes are never adopted or deleted by this test.' }
$image = (& docker --host $sharedTestEndpoint image inspect frogim/enterprise-bundle:local --format '{{index .RepoDigests 0}}').Trim()
if ($LASTEXITCODE -ne 0 -or $image -notmatch '^frogim/enterprise-bundle@sha256:[a-f0-9]{64}$') { throw 'Build the enterprise bundle image first.' }
$testCLI = Join-Path $sharedTestRoot $(if ($IsWindows) { 'build/tenancy-local/tenant-backup-shared-test.exe' } else { 'build/tenancy-local/tenant-backup-shared-test' })
& go -C (Join-Path $sharedTestRoot 'server') build -o $testCLI ./cmd/tenant-backup
if ($LASTEXITCODE -ne 0) { throw 'Operator CLI build failed.' }
$values = @{TENANCY_SHARED_BUNDLE_TEST='local'; TENANCY_COLD_BACKUP_TEST='local'; TENANCY_ENTERPRISE_BUNDLE_IMAGE=$image; TENANCY_MEDIA_REPAIR_CLI=$testCLI}
$previous = @{}
try {
    foreach ($key in $values.Keys) { $previous[$key]=[Environment]::GetEnvironmentVariable($key,'Process'); [Environment]::SetEnvironmentVariable($key,$values[$key],'Process') }
    & go -C (Join-Path $sharedTestRoot 'server') test ./internal/deployment -run '^TestSharedEnterpriseBundleLocalDocker$' -count=1 -v -timeout 20m
    if ($LASTEXITCODE -ne 0) { throw 'Shared datastore cold backup/restore drill failed.' }
} finally {
    foreach ($key in $previous.Keys) { [Environment]::SetEnvironmentVariable($key,$previous[$key],'Process') }
}
