#requires -Version 7.0
# Local Docker Desktop/default preview only. Never reads production config.
param([Parameter(Mandatory=$true)][string]$Root)
$ErrorActionPreference = 'Stop'
$migrationRoot = [IO.Path]::GetFullPath($Root)
$sharedDir = Join-Path $migrationRoot '.data/tenancy-local/shared'
$migrationDir = Join-Path $sharedDir 'migration'
$receiptPath = Join-Path $migrationDir 'receipt.json'
$baseCompose = Join-Path $migrationRoot 'infra/tenancy/compose.local.yml'
$sharedCompose = Join-Path $sharedDir 'compose.json'
$dockerEndpoint = if ($IsWindows) { 'npipe:////./pipe/dockerDesktopLinuxEngine' } else { 'unix:///var/run/docker.sock' }
function Invoke-LocalDocker {
    & docker --host $dockerEndpoint @args
    if ($LASTEXITCODE -ne 0) { throw 'Local shared-data operation failed; source volumes and migration receipt are preserved.' }
}
function Find-Container([string]$project, [string]$service) {
    $ids = @(Invoke-LocalDocker ps -aq --filter "label=com.docker.compose.project=$project" --filter "label=com.docker.compose.service=$service")
    if ($ids.Count -gt 1) { throw "Multiple containers found for $project/$service" }
    if ($ids.Count -eq 1) { return $ids[0].Trim() }
    return ''
}
function Save-Receipt {
    $temp = "$receiptPath.partial"
    [IO.File]::WriteAllText($temp, ($script:receipt | ConvertTo-Json -Depth 8) + "`n", [Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $temp -Destination $receiptPath -Force
}
function Complete-Step([string]$name) {
    $script:receipt.done = @($script:receipt.done) + $name
    Save-Receipt
    Write-Host "Shared local migration: $name"
}
function Read-SharedVolumes {
    $result = @{}
    foreach ($name in @('postgres','redis')) {
        $volumeName = "frogim-shared-default_$name"
        $details = Invoke-LocalDocker volume inspect $volumeName | ConvertFrom-Json
        $volume = @($details)[0]
        if ($volume.Name -ne $volumeName -or $volume.Labels.'com.docker.compose.project' -ne 'frogim-shared-default' -or $volume.Labels.'io.frogim.scope' -ne 'shared-default' -or $volume.Labels.'io.frogim.server' -ne 'default-local' -or !$volume.CreatedAt) {
            throw 'Shared volume ownership cannot be confirmed.'
        }
        $result[$name] = $volume.CreatedAt
    }
    return $result
}
function Assert-SharedVolumes {
    if (!$receipt.volumes -or $receipt.volumes.Count -ne 2) { throw 'Shared volume identity receipt is incomplete.' }
    $actual = Read-SharedVolumes
    foreach ($name in @('postgres','redis')) {
        if ($actual[$name] -cne $receipt.volumes[$name]) { throw 'Shared data volume changed; refusing to create or open empty replacement data.' }
    }
}
if (!(Test-Path -LiteralPath (Join-Path $sharedDir 'prepared.json'))) { throw 'Prepare shared local configuration first.' }
New-Item -ItemType Directory -Path $migrationDir -Force | Out-Null
if (Test-Path -LiteralPath $receiptPath) {
    $script:receipt = Get-Content -LiteralPath $receiptPath -Raw | ConvertFrom-Json -AsHashtable
    if ($receipt.version -ne 1 -or $receipt.project -ne 'frogim-tenancy-local') { throw 'Unknown shared migration receipt.' }
    if ($receipt.targets.Count -gt 0) { Assert-SharedVolumes }
    if ($receipt.done -contains 'completed') {
        Invoke-LocalDocker compose -f $sharedCompose up -d --wait --wait-timeout 90
        Write-Host 'Existing shared datastore migration preserved; source data is not imported again.'
        return
    }
} else {
    $sources = @{}
    foreach ($service in @('platform-db','platform-redis','enterprise-db','enterprise-redis')) { $sources[$service] = Find-Container 'frogim-tenancy-local' $service }
    $found = @($sources.Values | Where-Object { $_ }).Count
    if ($found -ne 0 -and $found -ne 4) { throw 'Incomplete legacy datastore set; inspect before migration.' }
    $existing = @(Invoke-LocalDocker volume ls -q --filter 'label=com.docker.compose.project=frogim-shared-default')
    if ($existing.Count -gt 0) { throw 'Existing shared volumes have no migration receipt; inspect before adopting them.' }
    $script:receipt = @{ version=1; project='frogim-tenancy-local'; sources=$sources; targets=@{}; volumes=@{}; files=@{}; done=@(); createdAt=[DateTimeOffset]::UtcNow.ToString('o') }
    Save-Receipt
}
# Stop all application writers first, including restart-managed containers.
Invoke-LocalDocker compose -f $baseCompose stop --timeout 30
foreach ($service in $receipt.sources.Keys) {
    if ($receipt.sources[$service] -and (Find-Container 'frogim-tenancy-local' $service) -ne $receipt.sources[$service]) { throw 'Legacy source container identity changed.' }
}
Invoke-LocalDocker compose -f $sharedCompose up -d --wait --wait-timeout 90
$receipt.volumes = Read-SharedVolumes
foreach ($service in @('shared-postgres','shared-redis')) {
    $id = Find-Container 'frogim-shared-default' $service
    if (!$id) { throw 'Shared datastore is absent.' }
    if ($receipt.targets.ContainsKey($service) -and $receipt.targets[$service] -ne $id) { throw 'Shared migration target changed.' }
    $receipt.targets[$service] = $id
}
Save-Receipt
if (!($receipt.sources.Values | Where-Object { $_ })) { Complete-Step 'completed'; return }
foreach ($source in $receipt.sources.Values) { Invoke-LocalDocker start $source | Out-Null }
$deadline = [DateTimeOffset]::UtcNow.AddSeconds(60)
foreach ($source in $receipt.sources.Values) {
    do {
        $health = Invoke-LocalDocker inspect --format '{{.State.Health.Status}}' $source
        if ($health -eq 'healthy') { break }
        if ([DateTimeOffset]::UtcNow -gt $deadline) { throw 'Legacy source did not become ready.' }
        Start-Sleep -Milliseconds 300
    } while ($true)
}
$targetPostgres = $receipt.targets['shared-postgres']
foreach ($scope in @('platform','enterprise')) {
    $dump = Join-Path $migrationDir "$scope.dump"
    $snapshot = Join-Path $migrationDir "$scope.redis.json"
    $sourcePG = $receipt.sources["$scope-db"]
    if ($receipt.done -notcontains "$scope-snapshot") {
        Invoke-LocalDocker exec $sourcePG pg_dump -U $scope -d $scope --format=custom --no-owner --no-acl -f /tmp/frogim-shared-migration.dump
        Invoke-LocalDocker cp "${sourcePG}:/tmp/frogim-shared-migration.dump" "$dump.partial"
        Move-Item -LiteralPath "$dump.partial" -Destination $dump -Force
        if (Test-Path -LiteralPath "$snapshot.partial") { Remove-Item -LiteralPath "$snapshot.partial" }
        Invoke-LocalDocker run --rm --pull never --network "frogim-tenancy-local_$scope" --read-only --cap-drop ALL --security-opt no-new-privileges --tmpfs /tmp --env-file (Join-Path $sharedDir "$scope-source-redis.env") --mount "type=bind,source=$migrationDir,target=/snapshots" --entrypoint /opt/frogim/redis-database frogim/tenancy-local:workspace -mode export -file "/snapshots/$scope.redis.json.partial"
        Move-Item -LiteralPath "$snapshot.partial" -Destination $snapshot -Force
        $receipt.files["$scope.dump"] = (Get-FileHash -LiteralPath $dump -Algorithm SHA256).Hash
        $receipt.files["$scope.redis.json"] = (Get-FileHash -LiteralPath $snapshot -Algorithm SHA256).Hash
        Complete-Step "$scope-snapshot"
    }
    foreach ($file in @("$scope.dump","$scope.redis.json")) {
        if ((Get-FileHash -LiteralPath (Join-Path $migrationDir $file) -Algorithm SHA256).Hash -ne $receipt.files[$file]) { throw 'Migration snapshot hash changed.' }
    }
    if ($receipt.done -notcontains "$scope-postgres") {
        $tables = Invoke-LocalDocker exec $targetPostgres psql -X -qAt -U shared_admin -d $scope -c "SELECT count(*) FROM pg_tables WHERE schemaname='public'"
        if ($tables -ne '0') { throw 'Target PostgreSQL database is not empty; inspect the receipt before retrying.' }
        Invoke-LocalDocker cp $dump "${targetPostgres}:/tmp/frogim-shared-$scope.dump"
        Invoke-LocalDocker exec $targetPostgres pg_restore -U shared_admin "--role=$scope" -d $scope --single-transaction --exit-on-error --no-owner --no-acl --no-comments "/tmp/frogim-shared-$scope.dump"
        Complete-Step "$scope-postgres"
    }
    if ($receipt.done -notcontains "$scope-redis") {
        Invoke-LocalDocker run --rm --pull never --network frogim-shared-default_data --read-only --cap-drop ALL --security-opt no-new-privileges --tmpfs /tmp --env-file (Join-Path $sharedDir "$scope-target-redis.env") --mount "type=bind,source=$migrationDir,target=/snapshots,readonly" --entrypoint /opt/frogim/redis-database frogim/tenancy-local:workspace -mode import -file "/snapshots/$scope.redis.json"
        Complete-Step "$scope-redis"
    }
    # Compare every original identity, without emitting account data or secrets.
    $identityTable = if ($scope -eq 'platform') { 'platform_accounts' } else { 'im_users' }
    $sourceIDs = @(Invoke-LocalDocker exec $sourcePG psql -X -qAt -U $scope -d $scope -c "SELECT id FROM $identityTable ORDER BY id")
    $targetIDs = @(Invoke-LocalDocker exec $targetPostgres psql -X -qAt -U $scope -d $scope -c "SELECT id FROM $identityTable ORDER BY id")
    if (($sourceIDs -join "`n") -cne ($targetIDs -join "`n")) { throw 'Migrated identity set differs from source.' }
    Write-Host "Verified $scope identities: $($targetIDs.Count)"
}
foreach ($source in $receipt.sources.Values) { Invoke-LocalDocker stop --timeout 30 $source | Out-Null }
Complete-Step 'completed'
Write-Host 'Shared PostgreSQL/Redis are ready. Original containers, volumes, credentials and snapshots are retained.'
