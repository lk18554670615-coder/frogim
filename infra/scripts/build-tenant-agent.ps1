#requires -Version 7.0
$ErrorActionPreference = 'Stop'
$agentBuildRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$agentDockerEndpoint = if ($IsWindows) { 'npipe:////./pipe/dockerDesktopLinuxEngine' } else { 'unix:///var/run/docker.sock' }
$engineOS = (& docker --host $agentDockerEndpoint info --format '{{.OSType}}').Trim()
if ($LASTEXITCODE -ne 0 -or $engineOS -ne 'linux') { throw 'The local Linux Docker engine is required.' }
$oldGoOS = $env:GOOS
$oldGoArch = $env:GOARCH
$oldCgo = $env:CGO_ENABLED
Push-Location $agentBuildRoot
try {
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    & go -C server build -trimpath -o ../build/tenancy-local/bin/tenant-agent ./cmd/tenant-agent
    if ($LASTEXITCODE -ne 0) { throw 'Agent compilation failed.' }
    & docker --host $agentDockerEndpoint build --platform linux/amd64 -f infra/tenancy/Dockerfile.agent -t frogim/tenant-agent:local .
    if ($LASTEXITCODE -ne 0) { throw 'Agent image build failed.' }
    # Building does not start an agent, mount a socket, or change existing peers.
    & docker --host $agentDockerEndpoint image inspect frogim/tenant-agent:local --format '{{index .RepoDigests 0}}'
    if ($LASTEXITCODE -ne 0) { throw 'Immutable agent image reference unavailable.' }
} finally {
    $env:GOOS = $oldGoOS; $env:GOARCH = $oldGoArch; $env:CGO_ENABLED = $oldCgo
    Pop-Location
}
