param([switch]$Faults)
$ErrorActionPreference='Stop'
$root=Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
Set-Location -LiteralPath $root
$platform='http://127.0.0.1:18700/platform'
$admin=New-Object Microsoft.PowerShell.Commands.WebRequestSession
$evidence=[System.Collections.Generic.List[object]]::new()
function Check($name,$ok){if(!$ok){throw "FAILED: $name"};$evidence.Add(@{check=$name;result='passed';at=(Get-Date).ToUniversalTime().ToString('o')});Write-Host "PASS $name"}
function Request($url,$body=$null,$token='',$method='POST',$session=$null){
 $args=@{Uri=$url;Method=$method;SkipHttpErrorCheck=$true;ContentType='application/json';Headers=@{'X-Client-Platform'='web'}}
 if($body -ne $null){$args.Body=ConvertTo-Json -InputObject $body -Depth 15 -Compress}
 if($token){$args.Headers.Authorization="Bearer $token"};if($session){$args.WebSession=$session}
 $r=Invoke-WebRequest @args;$data=$null;if($r.Content){try{$data=$r.Content|ConvertFrom-Json -Depth 20}catch{}}
 return @{status=[int]$r.StatusCode;data=$(if($data.data){$data.data}else{$data})}
}
function Admin($path,$body=$null,$method='POST'){Request "$platform/admin$path" $body '' $(if($null -eq $body){'GET'}else{$method}) $admin}
function Login($phone){$g=Request "$platform/v2/auth/password-login" @{phone=$phone;password='LocalUser123!'};Check 'directory login' ($g.status -eq 200);$api=$g.data.enterprise.tenant.services.apiBaseUrl;$s=Request "$api/v2/auth/enterprise-session" @{ticket=$g.data.enterprise.ticket};Check 'direct enterprise exchange' ($s.status -eq 200);return @{grant=$g.data;api=$api;session=$s.data;phone=$phone;uid=$s.data.user.id}}
function SwitchUser($uid,$to){$u=(Admin "/users/$uid").data.user;Admin "/users/$uid/switch" @{tenantId=$to;version=$u.assignmentVersion;confirmed=$true;reason='本机隔离验收'} }
try{
 $r=Admin '/auth/login' @{username='admin';password='LocalAdmin123!'};Check 'admin login' ($r.status -eq 200)
 $suffix=Get-Random -Minimum 100000 -Maximum 999999;$phone='13888'+$suffix
 $reg=Request "$platform/v2/auth/register" @{phone=$phone;password='LocalUser123!';code='123456';name='隔离验收';inviteCode='A'}
 Check 'enterprise code registration' ($reg.status -eq 200 -and $reg.data.enterprise.tenant.id -eq 'enterprise-a')
 $a=Login $phone;$uid=$a.uid
 $services=$a.grant.enterprise.tenant.services
 Check 'directory includes verified IM RTC media and configuration version' ($services.imWsUrl -eq 'ws://127.0.0.1:18750' -and $services.imTcpUrl -eq 'tcp://127.0.0.1:18740' -and $services.callSignalUrl -eq 'ws://127.0.0.1:18701/livekit' -and $services.mediaBaseUrl -eq $a.api -and $a.grant.enterprise.tenant.version -ge 1 -and $a.grant.enterprise.user.assignmentVersion -ge 1)
 $replay=Request "$($a.api)/v2/auth/enterprise-session" @{ticket=$a.grant.enterprise.ticket};Check 'ticket replay rejected' ($replay.status -eq 401)
 $bypass=Request "$($a.api)/v2/auth/password-login" @{phone=$phone;password='LocalUser123!'};Check 'enterprise independent login disabled' ($bypass.status -eq 409)
 $business=Request "$platform/v2/users/me" $null $a.grant.accessToken 'GET';Check 'platform has no business routing' ($business.status -eq 404)
 $png=[Convert]::FromBase64String('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j5xkAAAAASUVORK5CYII=')
 $presign=Request "$($a.api)/v2/media/presign" @{mime='image/png';fileName='avatar.png';size=$png.Length} $a.session.accessToken
 Check 'enterprise media prepare' ($presign.status -eq 201)
 $put=Invoke-WebRequest -Uri $presign.data.uploadUrl -Method PUT -Body $png -ContentType image/png -SkipHttpErrorCheck
 Check 'direct media upload' ($put.StatusCode -eq 200)
 $checksum=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($png)).ToLowerInvariant()
 $complete=Request "$($a.api)/v2/media/$($presign.data.mediaId)/complete" @{checksum=$checksum} $a.session.accessToken
 Check 'media completion' ($complete.status -eq 200)
 $profile=Request "$($a.api)/v2/users/me" @{name='A验收资料';avatarMediaId=$presign.data.mediaId;signature='A保留签名'} $a.session.accessToken PATCH
 Check 'enterprise profile update' ($profile.status -eq 200)
 $mediaLink=Request "$($a.api)/v2/media/$($presign.data.mediaId)/url" $null $a.session.accessToken GET
 Check 'media URL uses revocable enterprise entry' ($mediaLink.status -eq 200 -and $mediaLink.data.url -eq "$($a.api)/v2/media/$($presign.data.mediaId)/content")
 $read=Invoke-WebRequest -Uri $mediaLink.data.url -Headers @{Authorization="Media $($a.session.mediaAccessToken)"} -SkipHttpErrorCheck
 Check 'current media session reads bytes without redirect' ($read.StatusCode -eq 200 -and !$read.Headers.Location -and $read.RawContentLength -eq $png.Length)
 $enterpriseAdmin=New-Object Microsoft.PowerShell.Commands.WebRequestSession
 $enterpriseLogin=Request "$($a.api)/v2/admin/auth/login" @{username='admin';password='LocalAdmin123!'} '' POST $enterpriseAdmin
 Check 'enterprise admin login remains available' ($enterpriseLogin.status -eq 200)
 $adminUser=Request "$($a.api)/v2/admin/users/$uid" $null $enterpriseLogin.data.accessToken GET
 $adminAvatar="$($a.api)$($adminUser.data.user.avatarUrl)"
 $read=Invoke-WebRequest -Uri $adminAvatar -SkipHttpErrorCheck
 Check 'enterprise permanent media URL alone is not authority' ($read.StatusCode -eq 401)
 $read=Invoke-WebRequest -Uri "$adminAvatar`?viewer=$uid" -Headers @{Authorization="Media $($a.session.mediaAccessToken)"} -SkipHttpErrorCheck
 Check 'native avatar uses current media credential' ($read.StatusCode -eq 200)
 $read=Invoke-WebRequest -Uri $adminAvatar -WebSession $enterpriseAdmin -SkipHttpErrorCheck
 Check 'enterprise admin thumbnail uses scoped cookie' ($read.StatusCode -eq 200 -and $read.RawContentLength -eq $png.Length)
 $until=(Get-Date).AddSeconds(20);do{$detail=(Admin "/users/$uid").data;if($detail.user.profile.avatarMediaId -eq $presign.data.mediaId){break};Start-Sleep -Milliseconds 500}while((Get-Date) -lt $until)
 Check 'profile snapshot synced' ($detail.user.profile.avatarMediaId -eq $presign.data.mediaId)
 if($Faults){
  docker compose -f build/light-tenancy/compose.json stop enterprise-a | Out-Null
  $failed=SwitchUser $uid 'enterprise-b';Check 'unreachable source rejects switch' ($failed.status -eq 409)
  $u=(Admin "/users/$uid").data.user;Check 'unreachable preserves source' ($u.tenantId -eq 'enterprise-a' -and !$u.pendingOperation)
  docker compose -f build/light-tenancy/compose.json start enterprise-a | Out-Null
  Start-Sleep -Seconds 3
  docker compose -f build/light-tenancy/compose.json stop minio-b | Out-Null
  $failed=SwitchUser $uid 'enterprise-b';Check 'avatar copy failure leaves pending operation' ($failed.status -eq 503 -and $failed.data.operationId)
  $pending=Request "$platform/v2/auth/password-login" @{phone=$phone;password='LocalUser123!'};Check 'pending pauses new login' ($pending.status -eq 409)
  docker compose -f build/light-tenancy/compose.json start minio-b | Out-Null
  docker compose -f build/light-tenancy/compose.json restart platform | Out-Null
  Start-Sleep -Seconds 4
  $switch=Admin "/operations/$($failed.data.operationId)/retry" @{confirmed=$true;reason='恢复本机媒体存储后续跑';version=1}
 }else{$switch=SwitchUser $uid 'enterprise-b'}
 Check 'A to B completed' ($switch.status -eq 200 -and $switch.data.phase -eq 'done')
 $old=Request "$($a.api)/v2/users/me" $null $a.session.accessToken GET;Check 'old enterprise token revoked' ($old.status -eq 401 -or $old.status -eq 403)
 $read=Invoke-WebRequest -Uri $mediaLink.data.url -Headers @{Authorization="Media $($a.session.mediaAccessToken)"} -SkipHttpErrorCheck
 Check 'old media session revoked after enterprise switch' ($read.StatusCode -eq 401 -or $read.StatusCode -eq 403)
 $read=Invoke-WebRequest -Uri "$adminAvatar`?viewer=$uid" -Headers @{Authorization="Media $($a.session.mediaAccessToken)"} -SkipHttpErrorCheck
 Check 'old avatar media credential revoked after enterprise switch' ($read.StatusCode -eq 401 -or $read.StatusCode -eq 403)
 $late=Request "$($a.api)/v2/media/$($presign.data.mediaId)/complete" @{checksum=$checksum} $a.session.accessToken
 Check 'late upload completion rejected after enterprise switch' ($late.status -eq 401 -or $late.status -eq 403)
 $b=Login $phone;Check 'directory returned B services' ($b.api -eq 'http://127.0.0.1:18702' -and $b.uid -eq $uid)
 $bProfile=Request "$($b.api)/v2/users/me" $null $b.session.accessToken GET
 Check 'first B account initialized and avatar copied' ($bProfile.data.name -eq 'A验收资料' -and $bProfile.data.avatarMediaId -and $bProfile.data.avatarMediaId -ne $presign.data.mediaId)
 $bAvatar=$bProfile.data.avatarMediaId
 $update=Request "$($b.api)/v2/users/me" @{name='B独立资料';signature='B独立签名'} $b.session.accessToken PATCH;Check 'B profile edit' ($update.status -eq 200)
 $back=SwitchUser $uid 'enterprise-a';Check 'B to A completed' ($back.status -eq 200)
 $aa=Login $phone;$aProfile=Request "$($aa.api)/v2/users/me" $null $aa.session.accessToken GET
 Check 'A original profile and avatar preserved' ($aProfile.data.name -eq 'A验收资料' -and $aProfile.data.avatarMediaId -eq $presign.data.mediaId)
 $again=SwitchUser $uid 'enterprise-b';Check 'return to existing B' ($again.status -eq 200)
 $bb=Login $phone;$existing=Request "$($bb.api)/v2/users/me" $null $bb.session.accessToken GET
 Check 'existing B profile never overwritten' ($existing.data.name -eq 'B独立资料' -and $existing.data.avatarMediaId -eq $bAvatar)
 $final=SwitchUser $uid 'enterprise-a';Check 'test account restored to A' ($final.status -eq 200)
}finally{
 if($Faults){docker compose -f build/light-tenancy/compose.json start platform enterprise-a minio-b | Out-Null}
 $dir=Join-Path $root 'build/light-tenancy/evidence';New-Item -ItemType Directory -Force -Path $dir | Out-Null
 @{checks=$evidence;finishedAt=(Get-Date).ToUniversalTime().ToString('o');environment='local-only'} | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath (Join-Path $dir 'direct-enterprise.json') -Encoding utf8
}
