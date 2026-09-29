#requires -Version 7.0
param([switch]$Test)
$ErrorActionPreference = 'Stop'
$backupRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$backupBuild = Join-Path $backupRoot 'build/platform-backup'
New-Item -ItemType Directory -Path $backupBuild -Force | Out-Null
$endpoint = if ($IsWindows) { 'npipe:////./pipe/dockerDesktopLinuxEngine' } else { 'unix:///var/run/docker.sock' }
$previousGoos = $env:GOOS
$previousGoarch = $env:GOARCH
$previousCgo = $env:CGO_ENABLED
Push-Location (Join-Path $backupRoot 'server')
try {
    $env:GOOS = 'linux'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    & go build -trimpath -o (Join-Path $backupBuild 'platform-backup') ./cmd/platform-backup
    if ($LASTEXITCODE -ne 0) { throw 'Platform backup helper did not compile.' }
    & go build -trimpath -o (Join-Path $backupBuild 'backup-offsite') ./cmd/backup-offsite
    if ($LASTEXITCODE -ne 0) { throw 'Scoped offsite helper did not compile.' }
    if ($Test) {
        & go test -c -o (Join-Path $backupBuild 'directory-backup.test') ./internal/directorybackup
        if ($LASTEXITCODE -ne 0) { throw 'Platform backup integration tests did not compile.' }
    }
} finally {
    Pop-Location
    $env:GOOS = $previousGoos
    $env:GOARCH = $previousGoarch
    $env:CGO_ENABLED = $previousCgo
}
& docker --host $endpoint build -f (Join-Path $backupRoot 'infra/tenancy/Dockerfile.platform-backup') -t frogim/platform-backup:local $backupRoot
if ($LASTEXITCODE -ne 0) { throw 'Platform backup helper image failed.' }
if ($Test) {
    # Only a disposable dependency is used. This never mounts the persistent
    # default enterprise, its DB, real credentials or a Docker socket.
    & docker --host $endpoint compose -f (Join-Path $backupRoot 'infra/tenancy/compose.test.yml') up -d --wait platform-db
    if ($LASTEXITCODE -ne 0) { throw 'Disposable platform PostgreSQL is unavailable.' }
    $image = (& docker --host $endpoint image inspect frogim/platform-backup:local --format '{{.Id}}').Trim()
    if ($LASTEXITCODE -ne 0 -or $image -notmatch '^sha256:[a-f0-9]{64}$') { throw 'Immutable helper image ID unavailable.' }
    $testBinary = Join-Path $backupBuild 'directory-backup.test'
    & docker --host $endpoint run --rm --read-only --cap-drop ALL --security-opt no-new-privileges --pids-limit 128 --memory 512m --network frogim-tenancy-test_platform --tmpfs /tmp:rw,nosuid,nodev,size=268435456 --mount "type=bind,source=$testBinary,target=/opt/frogim/test,readonly" --env TENANCY_DIRECTORY_BACKUP_TEST=local --env 'TENANCY_DIRECTORY_BACKUP_DATABASE_URL=postgres://tenancy_test:local-disposable-tests-only@platform-db:5432/tenancy_test?sslmode=disable' --env TENANCY_PG_DUMP=/usr/local/bin/pg_dump --env TENANCY_PG_RESTORE=/usr/local/bin/pg_restore --env TENANCY_PLATFORM_BACKUP_CLI=/opt/frogim/platform-backup --entrypoint /opt/frogim/test $image '-test.v' '-test.timeout=120s'
    if ($LASTEXITCODE -ne 0) { throw 'Platform backup/restore integration tests failed; no original databases were targeted.' }
}
