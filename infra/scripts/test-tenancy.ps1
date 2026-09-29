#requires -Version 7.0
param([switch]$StartDependencies, [switch]$DeploymentDocker, [switch]$EnterpriseBundleDocker, [switch]$DeploymentStackDocker, [switch]$BackupDocker, [switch]$ProductionProfilesDocker, [switch]$PlatformBackupDocker, [switch]$OffsiteDocker)
$ErrorActionPreference = 'Stop'
$testRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$compose = Join-Path $testRoot 'infra/tenancy/compose.test.yml'
$testDockerEndpoint = if ($IsWindows) { 'npipe:////./pipe/dockerDesktopLinuxEngine' } else { 'unix:///var/run/docker.sock' }
if ($StartDependencies) {
    # Linux containers are managed by Docker Desktop; the host stays in pwsh.
    & docker --host $testDockerEndpoint compose -f $compose --profile im build enterprise-a-im
    if ($LASTEXITCODE -ne 0) { throw 'Pinned IM source and close-regression tests did not build.' }
    & docker --host $testDockerEndpoint compose -f $compose --profile im up -d --wait --wait-timeout 60
    if ($LASTEXITCODE -ne 0) { throw 'Isolated tenancy dependencies did not become healthy.' }
}
if ($PlatformBackupDocker) {
    & (Join-Path $PSScriptRoot 'build-platform-backup.ps1') -Test
    if ($LASTEXITCODE -ne 0) { throw 'Platform directory backup/recovery fixture failed.' }
}
$testEnv = @{
    TENANCY_IM_TRANSPORT_TEST = 'host'
    PLATFORM_TEST_DATABASE_URL = 'postgres://tenancy_test:local-disposable-tests-only@127.0.0.1:15473/tenancy_test?sslmode=disable'
    IM_TEST_DATABASE_URL = 'postgres://tenancy_test:local-disposable-tests-only@127.0.0.1:15474/tenancy_test?sslmode=disable'
    TENANCY_TEST_PLATFORM_DATABASE_URL = 'postgres://tenancy_test:local-disposable-tests-only@127.0.0.1:15473/tenancy_test?sslmode=disable'
    TENANCY_TEST_A_DATABASE_URL = 'postgres://tenancy_test:local-disposable-tests-only@127.0.0.1:15474/tenancy_test?sslmode=disable'
    TENANCY_TEST_B_DATABASE_URL = 'postgres://tenancy_test:local-disposable-tests-only@127.0.0.1:15475/tenancy_test?sslmode=disable'
}
$previous = @{}
if ($OffsiteDocker) {
    $offsiteTestDirectory = Join-Path $testRoot 'build/tenancy-local'
    New-Item -ItemType Directory -Path $offsiteTestDirectory -Force | Out-Null
    $offsiteTestCLI = Join-Path $offsiteTestDirectory $(if ($IsWindows) { 'backup-offsite-test.exe' } else { 'backup-offsite-test' })
    & go -C (Join-Path $testRoot 'server') build -o $offsiteTestCLI ./cmd/backup-offsite
    if ($LASTEXITCODE -ne 0) { throw 'Offsite native test CLI did not compile.' }
    $testEnv['TENANCY_OFFSITE_DOCKER_TEST'] = 'local'
    $testEnv['TENANCY_OFFSITE_CLI'] = $offsiteTestCLI
}
if ($DeploymentDocker) { $testEnv['TENANCY_DEPLOYMENT_DOCKER_TEST'] = 'local' }
if ($BackupDocker) {
    $repairTestDirectory = Join-Path $testRoot 'build/tenancy-local'
    New-Item -ItemType Directory -Path $repairTestDirectory -Force | Out-Null
    $repairTestCLI = Join-Path $repairTestDirectory $(if ($IsWindows) { 'tenant-backup-test.exe' } else { 'tenant-backup-test' })
    & go -C (Join-Path $testRoot 'server') build -o $repairTestCLI ./cmd/tenant-backup
    if ($LASTEXITCODE -ne 0) { throw 'Cold media repair operator CLI did not compile.' }
    $testEnv['TENANCY_MEDIA_REPAIR_CLI'] = $repairTestCLI
}
if ($EnterpriseBundleDocker -or $DeploymentStackDocker -or $BackupDocker -or $ProductionProfilesDocker) {
    $bundleImage = (& docker --host $testDockerEndpoint image inspect frogim/enterprise-bundle:local --format '{{index .RepoDigests 0}}').Trim()
    if ($LASTEXITCODE -ne 0 -or $bundleImage -notmatch '^frogim/enterprise-bundle@sha256:[a-f0-9]{64}$') { throw 'Build the local enterprise bundle image first.' }
    if ($EnterpriseBundleDocker -or $BackupDocker) { $testEnv['TENANCY_ENTERPRISE_BUNDLE_TEST'] = 'local' }
    if ($BackupDocker) { $testEnv['TENANCY_COLD_BACKUP_TEST'] = 'local' }
    $testEnv['TENANCY_ENTERPRISE_BUNDLE_IMAGE'] = $bundleImage
}
if ($ProductionProfilesDocker) {
    $platformImage = (& docker --host $testDockerEndpoint image inspect frogim/platform-bundle:local --format '{{index .RepoDigests 0}}').Trim()
    if ($LASTEXITCODE -ne 0 -or $platformImage -notmatch '^frogim/platform-bundle@sha256:[a-f0-9]{64}$') { throw 'Build the platform fixture image with https://app.example.test first.' }
    $testEnv['TENANCY_PLATFORM_BUNDLE_TEST'] = 'local'
    $testEnv['TENANCY_PLATFORM_BUNDLE_IMAGE'] = $platformImage
    $testEnv['TENANCY_ENTERPRISE_PRODUCTION_TEST'] = 'local'
}
if ($DeploymentStackDocker) {
    $agentImage = (& docker --host $testDockerEndpoint image inspect frogim/tenant-agent:local --format '{{index .RepoDigests 0}}').Trim()
    if ($LASTEXITCODE -ne 0 -or $agentImage -notmatch '^frogim/tenant-agent@sha256:[a-f0-9]{64}$') { throw 'Build the restricted local tenant agent image first.' }
    $testEnv['TENANCY_DEPLOYMENT_STACK_TEST'] = 'local'
    $testEnv['TENANCY_AGENT_IMAGE'] = $agentImage
}
foreach ($entry in $testEnv.GetEnumerator()) {
    $previous[$entry.Key] = [Environment]::GetEnvironmentVariable($entry.Key, 'Process')
    [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, 'Process')
}
Push-Location (Join-Path $testRoot 'server')
try {
    & go test ./internal/backup ./internal/directorybackup ./cmd/tenant-backup ./cmd/platform-backup ./cmd/backup-offsite -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw 'Authenticated backup and offline operator tests failed.' }
    & go test ./internal/deployment -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw 'Restricted deployment executor tests failed.' }
    # These packages share the disposable Docker engine. Keep infrastructure
    # lifecycle tests sequential; otherwise full-stack starts/teardowns compete
    # with the IM handshake and media timeouts of the transport suite.
    & go test -p 1 ./internal/platform ./internal/store ./internal/httpapi -run 'PlatformPostgres|TenantPostgres|TenantStack' -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw 'Tenancy integration tests failed.' }
    & go test ./internal/legacyimport ./cmd/tenant-preflight ./cmd/tenant-import ./cmd/tenant-migrate -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw 'Legacy migration preflight and durable import tests failed.' }
} finally {
    Pop-Location
    foreach ($entry in $previous.GetEnumerator()) { [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, 'Process') }
}
