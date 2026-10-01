param([switch]$SkipBuild,[switch]$SkipClientBuild,[switch]$RefreshConfig,[string]$Flutter='C:/Users/lee/fvm/versions/3.44.8/bin/flutter.bat')
$ErrorActionPreference='Stop'
if($PSVersionTable.PSVersion.Major -lt 7){throw 'Run with PowerShell 7 (pwsh).'}
$root=Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
Set-Location -LiteralPath $root
function AssertCommand{if($LASTEXITCODE -ne 0){throw "Command failed with exit $LASTEXITCODE"}}
function WaitReady($url){$until=(Get-Date).AddSeconds(60);do{try{if((Invoke-WebRequest -Uri $url -TimeoutSec 3).StatusCode -eq 200){return}}catch{};Start-Sleep -Milliseconds 500}while((Get-Date) -lt $until);throw "Not ready: $url"}
if($RefreshConfig){go -C server run ./cmd/light-tenancy-local -root .. -refresh-config}else{go -C server run ./cmd/light-tenancy-local -root ..};AssertCommand
if(!$SkipBuild){
 $oldOS=$env:GOOS;$oldCGO=$env:CGO_ENABLED
 try{
  $env:GOOS='linux';$env:CGO_ENABLED='0'
  go -C server build -o ../build/light-tenancy/im-server ./cmd/server;AssertCommand
  go -C tools/wukong-policy-plugin build -o ../../build/light-tenancy/plugins/wk.plugin.im-policy-linux-amd64.wkp .;AssertCommand
 }finally{$env:GOOS=$oldOS;$env:CGO_ENABLED=$oldCGO}
 Set-Content -LiteralPath build/light-tenancy/.dockerignore -Value "*`n!im-server" -Encoding utf8
 docker build -t frogim/light-tenancy:local -f infra/light-tenancy/Dockerfile build/light-tenancy;AssertCommand
 Push-Location -LiteralPath apps/admin
 $oldMode=$env:VITE_PLATFORM_MODE
 try{
  $env:VITE_PLATFORM_MODE='true';npm run build -- --base=/platform/ --outDir=../../build/light-tenancy/platform-ui;AssertCommand
  Remove-Item Env:VITE_PLATFORM_MODE -ErrorAction SilentlyContinue
  npm run build -- --base=/admin/ --outDir=../../build/light-tenancy/enterprise-ui;AssertCommand
 }finally{$env:VITE_PLATFORM_MODE=$oldMode;Pop-Location}
}
if(!$SkipClientBuild){
 Push-Location -LiteralPath apps/mobile
 try{& $Flutter build web --no-pub --release --base-href / --no-web-resources-cdn --dart-define=APP_ENV=development --dart-define=PLATFORM_BASE_URL=http://127.0.0.1:18700/platform --dart-define=ENABLE_DEMO=false;AssertCommand}finally{Pop-Location}
}
$compose=Join-Path $root 'build/light-tenancy/compose.json'
docker compose -f $compose up -d postgres-a postgres-b redis-a redis-b platform;AssertCommand
WaitReady 'http://127.0.0.1:18700/ready'
$session=New-Object Microsoft.PowerShell.Commands.WebRequestSession
$base='http://127.0.0.1:18700/platform/admin'
$null=Invoke-RestMethod -Uri "$base/auth/login" -Method Post -ContentType application/json -Body '{"username":"admin","password":"LocalAdmin123!"}' -WebSession $session
$tenants=Invoke-RestMethod -Uri "$base/tenants" -WebSession $session
for($i=0;$i -lt 2;$i++){
 $id=@('a','b')[$i];$tenant="enterprise-$id"
 if($tenants.id -contains $tenant){
  $existing=$tenants|Where-Object id -eq $tenant
  $expected="ws://127.0.0.1:$($i+18701)/livekit"
  if($existing.services.callSignalUrl -ne $expected){$existing.services.callSignalUrl=$expected;$existing|Add-Member -NotePropertyName reason -NotePropertyValue '本机通话撤权入口更新' -Force;$existing|Add-Member -NotePropertyName confirmed -NotePropertyValue $true -Force;$null=Invoke-RestMethod -Uri "$base/tenants/$tenant" -Method Patch -ContentType application/json -Body ($existing|ConvertTo-Json -Depth 8) -WebSession $session}
  continue
 }
 $body=@{id=$tenant;name="测试企业$($id.ToUpperInvariant())";code=$id.ToUpperInvariant();enabled=$true;version=0;isDefault=($i -eq 0);controlUrl="https://${tenant}:8443";reason='建立隔离本机验收目录';confirmed=$true;services=@{apiBaseUrl="http://127.0.0.1:$($i+18701)";imWsUrl="ws://127.0.0.1:$($i+18750)";imTcpUrl="tcp://127.0.0.1:$($i+18740)";callSignalUrl="ws://127.0.0.1:$($i+18701)/livekit";mediaBaseUrl="http://127.0.0.1:$($i+18701)"}}|ConvertTo-Json -Depth 8
 $null=Invoke-RestMethod -Uri "$base/tenants" -Method Post -ContentType application/json -Body $body -WebSession $session
}
docker compose -f $compose up -d minio-a minio-b im-a im-b rtc-a rtc-b enterprise-a enterprise-b;AssertCommand
WaitReady 'http://127.0.0.1:18701/ready';WaitReady 'http://127.0.0.1:18702/ready'
go -C server run ./cmd/light-tenancy-local -root .. -init-media;AssertCommand
# These are synthetic local fixtures. No production data or credentials are used.
$users=(Invoke-RestMethod -Uri "$base/users/query" -Method Post -ContentType application/json -Body '{"query":"1380000000","page":1}' -WebSession $session).items
for($i=1;$i -le 2;$i++){
 $phone="1380000000$i";if($users.phone -contains $phone){continue}
 $body=@{phone=$phone;password='LocalUser123!';name=@('验收甲','验收乙')[$i-1];inviteCode='A';reason='本机验收测试账号';confirmed=$true;version=0}|ConvertTo-Json
 $null=Invoke-RestMethod -Uri "$base/users" -Method Post -ContentType application/json -Body $body -WebSession $session
}
docker compose -f $compose up -d web-a web-b;AssertCommand
if($RefreshConfig){
 foreach($name in @('frogim-light-web-a-1','frogim-light-web-b-1')){
  docker exec $name caddy reload --config /config/Caddyfile --adapter caddyfile;AssertCommand
 }
}
Write-Host 'Local platform: http://127.0.0.1:18700/platform/ (admin / LocalAdmin123!)'
Write-Host 'Local Web: http://127.0.0.1:18780/ and :18781/ (13800000001 or 13800000002 / LocalUser123!)'
Write-Host 'Enterprise codes A/B. Development OTP: 123456. No production changes.'
