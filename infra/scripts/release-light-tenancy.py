"""Reviewed Linux-only cutover driver. Invoke one phase at a time.

Assets and private state live below --root. Existing business mounts are retained.
This driver never downloads data, prunes assets, or restores after opening.
"""
import argparse, copy, datetime, hashlib, json, os, pathlib, secrets, shutil, subprocess, urllib.parse, time

parser=argparse.ArgumentParser()
parser.add_argument('phase',choices=['prepare','rehearse','freeze','adopt','open','check','rollback'])
parser.add_argument('--root',required=True)
args=parser.parse_args()
ROOT=pathlib.Path(args.root).resolve()
if ROOT.parent!=pathlib.Path('/data/frogim/releases') or not ROOT.name.startswith('light-'):
    raise SystemExit('release root outside allowed directory')
os.umask(0o077)
for folder in ['ops','config','backups','build','gateway']: (ROOT/folder).mkdir(parents=True,exist_ok=True)
PG='frogim-shared-default-shared-postgres-1'
API='frogim-single-api-1'
GATEWAY='frogim-single-gateway-1'
SOURCE='single_main_33d9ea2'
TARGET='platform_light'
REHEARSAL_SOURCE='light_rehearsal_enterprise'
REHEARSAL_TARGET='light_rehearsal_platform'

def run(cmd,input=None):
    r=subprocess.run(cmd,input=input,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    if r.returncode:
        (ROOT/'ops'/'last-error-private.log').write_bytes(r.stderr)
        raise RuntimeError('command failed: '+cmd[0]+'; private diagnostic saved')
    return r.stdout
def write(path,value):
    temp=path.with_suffix(path.suffix+'.tmp')
    temp.write_text(json.dumps(value,ensure_ascii=False,indent=2))
    temp.replace(path)
def read(path): return json.loads(path.read_text())
def inspect(name): return json.loads(run(['docker','inspect',name]))[0]
def environment(container): return dict(x.split('=',1) for x in container['Config']['Env'] if '=' in x)
PGUSER=environment(inspect(PG))['POSTGRES_USER']
def sql(db,query): return run(['docker','exec',PG,'psql','-X','-v','ON_ERROR_STOP=1','-U',PGUSER,'-d',db,'-At','-c',query]).decode().strip()
def sha(path):
    h=hashlib.sha256()
    with path.open('rb') as f:
        while b:=f.read(1024*1024): h.update(b)
    return h.hexdigest()
def compose(path,*cmd): return run(['docker','compose','-f',str(path),*cmd])
def state(phase,extra=None): write(ROOT/'ops'/'state.json',dict(phase=phase,at=datetime.datetime.now(datetime.timezone.utc).isoformat(),**(extra or {})))
def require(phase):
    if read(ROOT/'ops'/'state.json')['phase']!=phase: raise RuntimeError('incorrect cutover phase')
def escaped(v,reverse=False):
    if isinstance(v,str):return v.replace('$$','$') if reverse else v.replace('$','$$')
    if isinstance(v,list):return [escaped(x,reverse) for x in v]
    if isinstance(v,dict):return {k:escaped(x,reverse) for k,x in v.items()}
    return v
def with_db(url,name):
    p=urllib.parse.urlsplit(url);return urllib.parse.urlunsplit(p._replace(path='/'+name))
def create_db(name,owner):
    if sql('postgres',"SELECT count(*) FROM pg_database WHERE datname='%s'"%name)!='0': raise RuntimeError('new database already exists: '+name)
    if not owner.replace('_','').isalnum():raise RuntimeError('unsupported database owner')
    sql('postgres','CREATE DATABASE "'+name+'" OWNER "'+owner+'"')
def tool(config,phase,label):
    output=run(['docker','run','--rm','--network','frogim-shared-default_data','--entrypoint','/opt/frogim/light-tenancy-import','-v',str(config)+':/private/import.json:ro',read(ROOT/'ops'/'images.json')['api'],'-config','/private/import.json','-phase',phase])
    data=json.loads(output);write(ROOT/'ops'/(label+'.json'),data);print(label,json.dumps(data),flush=True);return data
def fingerprints(db):
    names=['im_users','im_friendships','im_conversations','im_messages','im_members','im_groups','im_media']
    return {name:sql(db,'SELECT count(*)||\'|\'||COALESCE(md5(string_agg(row_to_json(t)::text,\'\' ORDER BY row_to_json(t)::text)),md5(\'\')) FROM '+name+' t') for name in names}
def wait_health(name):
    end=time.monotonic()+120
    while time.monotonic()<end:
        status=inspect(name)['State']
        if status.get('Health',{}).get('Status')=='healthy':return
        if status['Status']=='exited': raise RuntimeError('service exited: '+name)
        time.sleep(2)
    raise RuntimeError('service readiness timeout: '+name)

def check_gateway():
    run(['docker','exec',GATEWAY,'caddy','validate','--config','/config/light/Caddyfile','--adapter','caddyfile'])

def restore_database(name,dump):
    # This function only restores a named database, never the shared PG volume.
    if name not in [SOURCE,'light_rehearsal_restore']:raise RuntimeError('restore database outside approved scope')
    cfg=read(ROOT/'config'/'import.json');owner=urllib.parse.unquote(urllib.parse.urlsplit(cfg['sourceDatabaseUrl']).username)
    sql('postgres',"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='%s' AND pid<>pg_backend_pid()"%name)
    sql('postgres','DROP DATABASE "'+name+'"');create_db(name,owner)
    run(['docker','exec','-i',PG,'pg_restore','--exit-on-error','--no-owner','--no-acl','-U',PGUSER,'-d',name],dump.read_bytes())

if args.phase=='prepare':
    if (ROOT/'ops'/'state.json').exists():raise RuntimeError('release already prepared; use its recorded next phase')
    names=[API,GATEWAY,'frogim-single-im-1','frogim-single-livekit-1','frogim-single-minio-1',PG,'frogim-shared-default-shared-redis-1']
    snapshots={name:inspect(name) for name in names};write(ROOT/'ops'/'containers-before.json',snapshots)
    pointer=read(pathlib.Path('/data/frogim/active-deployment.json'))
    if pointer.get('architecture')!='single-enterprise' or pointer.get('database')!=SOURCE:raise RuntimeError('active deployment changed')
    write(ROOT/'ops'/'pointer-before.json',pointer)
    before=pathlib.Path(snapshots[GATEWAY]['Config']['Labels']['com.docker.compose.project.config_files'])
    shutil.copy2(before,ROOT/'ops'/'compose-before.json')
    c=escaped(read(before),True)
    env=environment(snapshots[API]);dburl=env['IM_DATABASE_URL'];owner=urllib.parse.unquote(urllib.parse.urlsplit(dburl).username)
    if urllib.parse.urlsplit(dburl).path!='/'+SOURCE:raise RuntimeError('business database changed')
    (ROOT/'backups'/'rehearsal-source.dump').write_bytes(run(['docker','exec',PG,'pg_dump','-U',PGUSER,'-d',SOURCE,'-Fc']))
    print('snapshot bytes', (ROOT/'backups'/'rehearsal-source.dump').stat().st_size,flush=True)
    certs=ROOT/'config'/'certs';certs.mkdir(exist_ok=True)
    run(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-days','365','-subj','/CN=light-tenancy-control-ca','-keyout',str(certs/'ca.key'),'-out',str(certs/'ca.pem')])
    for host,identity in [('platform','platform'),('api','enterprise-a')]:
        folder=certs/host;folder.mkdir(exist_ok=True)
        run(['openssl','req','-newkey','rsa:3072','-nodes','-subj','/CN='+identity,'-keyout',str(folder/'key.pem'),'-out',str(folder/'request.csr')])
        (folder/'extensions').write_text('subjectAltName=DNS:'+host+'\nextendedKeyUsage=serverAuth,clientAuth\n')
        run(['openssl','x509','-req','-days','365','-in',str(folder/'request.csr'),'-CA',str(certs/'ca.pem'),'-CAkey',str(certs/'ca.key'),'-CAcreateserial','-extfile',str(folder/'extensions'),'-out',str(folder/'cert.pem')])
        shutil.copy2(certs/'ca.pem',folder/'ca.pem')
    base=snapshots[API]['Image'];gateway_base=snapshots[GATEWAY]['Image']
    run(['docker','tag',base,'frogim/light-base:20261001'])
    (ROOT/'build'/'Dockerfile').write_text('FROM frogim/light-base:20261001\nCOPY im-server /opt/frogim/im-server\nCOPY light-tenancy-import /opt/frogim/light-tenancy-import\nCOPY platform-ui /srv/platform\n')
    run(['docker','build','--pull=false','-t','frogim/light-api:20261001',str(ROOT/'build')])
    image=json.loads(run(['docker','image','inspect','frogim/light-api:20261001']))[0]['Id']
    write(ROOT/'ops'/'images.json',dict(api=image,gateway=gateway_base,previousApi=base))
    credentials=read(ROOT/'config'/'operator-input.json')
    password_hash=run(['docker','run','--rm','-i','--entrypoint','/opt/frogim/light-tenancy-import',image,'-hash-password-stdin'],credentials['password'].encode()).decode()
    secret=secrets.token_urlsafe(48)
    platform_env=dict(IM_MODE='platform',IM_ADDR=':8080',IM_ENV='production',IM_DEV_MODE='false',IM_DATABASE_URL=with_db(dburl,TARGET),IM_JWT_SECRET=secret,IM_ADMIN_USERNAME=credentials['username'],IM_ADMIN_PASSWORD_HASH=password_hash,IM_PLATFORM_FIXED_OTP_CODE='123456',IM_ALLOWED_ORIGINS='https://18.163.165.233',IM_PLATFORM_STATIC_DIR='/srv/platform',IM_CONTROL_ADDR=':8443',IM_CONTROL_CA='/certs/ca.pem',IM_CONTROL_CERT='/certs/cert.pem',IM_CONTROL_KEY='/certs/key.pem')
    write(ROOT/'config'/'credentials.json',credentials)
    common_certs=lambda host:[str(certs/host)+':/certs:ro']
    c['networks']['control']={'internal':True}
    p=copy.deepcopy(c['services']['api']);p.update(image=image,entrypoint=['/opt/frogim/im-server'],environment=platform_env,volumes=common_certs('platform'),networks=['business','control','shared-data'],ports=[]);p.pop('depends_on',None)
    p['volumes'].append(str(ROOT/'config'/'import.json')+':/private/import.json:ro')
    c['services']['platform']=p
    env.update(IM_MODE='enterprise',IM_TENANT_ID='enterprise-a',IM_PLATFORM_URL='https://platform:8443',IM_ENTERPRISE_API_URL='https://18.163.165.233',IM_ENTERPRISE_MEDIA_URL='https://18.163.165.233',IM_CONTROL_ADDR=':8443',IM_CONTROL_CA='/certs/ca.pem',IM_CONTROL_CERT='/certs/cert.pem',IM_CONTROL_KEY='/certs/key.pem',IM_LIVEKIT_URL='wss://18.163.165.233/livekit',IM_LIVEKIT_API_URL='http://livekit:7880',IM_ENV='production',IM_DEV_MODE='false',IM_DEV_ALLOW_CONTAINER_BIND='false',IM_IP_TEST_ONLY='false',IM_SEED_DEMO='false',IM_JWT_SECRET=secrets.token_urlsafe(48))
    env.pop('IM_S3_ANDROID_PUBLIC_ENDPOINT',None)
    c['services']['api'].update(image=image,environment=env)
    c['services']['api']['networks'].append('control');c['services']['api']['volumes']+=common_certs('api')
    tenant=dict(id='enterprise-a',name='客户A企业',code='A',enabled=False,isDefault=True,controlUrl='https://api:8443',services=dict(apiBaseUrl='https://18.163.165.233',mediaBaseUrl='https://18.163.165.233',imWsUrl=env['IM_WUKONG_WS_URL'],imTcpUrl=env['IM_WUKONG_TCP_URL'],callSignalUrl=env['IM_LIVEKIT_URL']))
    cfg=dict(sourceDatabaseUrl=dburl,platformDatabaseUrl=with_db(dburl,TARGET),tenant=tenant)
    write(ROOT/'config'/'import.json',cfg)
    original=pathlib.Path(next(m['Source'] for m in snapshots[GATEWAY]['Mounts'] if m['Destination']=='/config/Caddyfile')).read_text()
    begin=original.index('    @legacy_version ');end=original.index('    @backend ',begin)
    original=original[:begin]+'''    handle /platform {
      redir /platform/ 302
    }
    handle /platform/* { reverse_proxy platform:8080 }
    handle /v2/config/version { reverse_proxy platform:8080 }
'''+original[end:]
    begin=original.index('    @rtc ');end=original.index('    @private ',begin)
    original=original[:begin]+'''    @old_rtc path /rtc /rtc/*
    handle @old_rtc {
      respond "not found" 404
    }
    @calls path /livekit /livekit/*
    handle @calls {
      reverse_proxy api:8080
    }
'''+original[end:]
    # Fixed entry clears attacker-supplied forwarding headers before either API.
    original=original.replace('reverse_proxy platform:8080 }','reverse_proxy platform:8080 {\n      header_up -X-Frogim-*\n      header_up X-Forwarded-For {remote_host}\n      header_up X-Forwarded-Proto https\n    }\n    }')
    original=original.replace('header Content-Type "text/html; charset=utf-8"\n      file_server','header Content-Type "text/html; charset=utf-8"\n      try_files {path} {path}.html\n      file_server')
    (ROOT/'gateway'/'Caddyfile.active').write_text(original)
    route=original.index('  route {');opening=original.index('{',route);depth=1;i=opening+1
    while depth:
        depth+=(original[i]=='{')-(original[i]=='}');i+=1
    maintenance=original[:opening+1]+'\n    header Cache-Control no-store\n    respond "服务升级维护中，请稍后重试" 503\n  '+original[i-1:]
    (ROOT/'gateway'/'Caddyfile.maintenance').write_text(maintenance)
    shutil.copy2(ROOT/'gateway'/'Caddyfile.active',ROOT/'gateway'/'Caddyfile')
    legal_source=next(m['Source'] for m in snapshots[GATEWAY]['Mounts'] if m['Destination']=='/srv/legal')
    shutil.copytree(legal_source,ROOT/'legal',dirs_exist_ok=True)
    (ROOT/'legal'/'upgrade.html').write_text('<!doctype html><meta charset="utf-8"><title>客户端升级说明</title><h1>服务已升级为统一平台认证</h1><p>现有账号和历史数据保留，请重新登录。</p><p>本次先开放网页版；Android、iOS 新版本后续提供，旧移动端停止业务访问。</p><p><a href="/app/">打开网页版</a></p>')
    volumes=[v for v in c['services']['gateway']['volumes'] if not v.endswith(':/config/Caddyfile:ro') and not v.endswith(':/config/Caddyfile') and not v.endswith(':/srv/legal:ro')]
    volumes += [str(ROOT/'gateway')+':/config/light:ro',str(ROOT/'web')+':/srv/web:ro']
    volumes.append(str(ROOT/'legal')+':/srv/legal:ro')
    c['services']['gateway']['volumes']=volumes
    c['services']['gateway']['entrypoint']=['/usr/bin/caddy','run','--config','/config/light/Caddyfile','--adapter','caddyfile']
    c['services']['gateway']['healthcheck']['test']=['CMD','/usr/bin/caddy','validate','--config','/config/light/Caddyfile','--adapter','caddyfile']
    write(ROOT/'compose.json',escaped(c))
    os.chmod(ROOT/'gateway',0o755)
    for path in (ROOT/'gateway').iterdir():os.chmod(path,0o644)
    uid=int(run(['docker','run','--rm','--entrypoint','id',image,'-u']))
    gid=int(run(['docker','run','--rm','--entrypoint','id',image,'-g']))
    for host in ['platform','api']:
        folder=certs/host;os.chown(folder,uid,gid)
        for path in folder.iterdir():os.chown(path,uid,gid)
    os.chown(ROOT/'config'/'import.json',uid,gid)
    compose(ROOT/'compose.json','config','-q')
    for service in ['platform','api']:
        compose(ROOT/'compose.json','run','--rm','--no-deps','--entrypoint','/opt/frogim/light-tenancy-import',service,'-check-config')
    # No active services changed during preparation.
    for file in ['Caddyfile.active','Caddyfile.maintenance']:
        run(['docker','run','--rm','--entrypoint','/usr/bin/caddy','-v',str(ROOT/'gateway')+':/config/light:ro','-v','/data/linli-im/shared/letsencrypt:/etc/letsencrypt:ro',gateway_base,'validate','--config','/config/light/'+file,'--adapter','caddyfile'])
    for name in [REHEARSAL_SOURCE,REHEARSAL_TARGET]:create_db(name,owner)
    run(['docker','exec','-i',PG,'pg_restore','--exit-on-error','--no-owner','--no-acl','-U',PGUSER,'-d',REHEARSAL_SOURCE],(ROOT/'backups'/'rehearsal-source.dump').read_bytes())
    rehearsal=copy.deepcopy(cfg);rehearsal['sourceDatabaseUrl']=with_db(dburl,REHEARSAL_SOURCE);rehearsal['platformDatabaseUrl']=with_db(dburl,REHEARSAL_TARGET)
    write(ROOT/'config'/'rehearsal.json',rehearsal)
    state('prepared')
    print('prepared',image,flush=True)

elif args.phase=='rehearse':
    require('prepared');start=time.monotonic()
    before=fingerprints(REHEARSAL_SOURCE)
    for phase in ['preflight','import','import','verify']:tool(ROOT/'config'/'rehearsal.json',phase,'rehearsal-'+phase)
    if before!=fingerprints(REHEARSAL_SOURCE):raise RuntimeError('business data changed during rehearsal')
    # Restore into a third isolated DB, verify the exact original snapshot.
    cfg=read(ROOT/'config'/'import.json');owner=urllib.parse.unquote(urllib.parse.urlsplit(cfg['sourceDatabaseUrl']).username)
    restore='light_rehearsal_restore';create_db(restore,owner)
    run(['docker','exec','-i',PG,'pg_restore','--exit-on-error','--no-owner','--no-acl','-U',PGUSER,'-d',restore],(ROOT/'backups'/'rehearsal-source.dump').read_bytes())
    if before!=fingerprints(restore):raise RuntimeError('backup restoration mismatch')
    # Exercise the same drop/recreate/restore procedure needed for in-place rollback.
    sql(restore,"UPDATE im_users SET signature=signature||'rehearsal rollback'")
    restore_database(restore,ROOT/'backups'/'rehearsal-source.dump')
    if before!=fingerprints(restore):raise RuntimeError('cutover rollback rehearsal mismatch')
    write(ROOT/'ops'/'rehearsal-completed.json',dict(seconds=round(time.monotonic()-start,2),businessFingerprints=before,backupSHA256=sha(ROOT/'backups'/'rehearsal-source.dump'),restoreVerified=True))
    state('rehearsed');print('isolated import/retry/restore passed',flush=True)

elif args.phase=='freeze':
    require('rehearsed')
    if shutil.disk_usage(ROOT).free<10*1024**3:raise RuntimeError('insufficient backup and rollback space')
    for name in [API,GATEWAY,'frogim-single-im-1','frogim-single-livekit-1','frogim-single-minio-1']:
        if not inspect(name)['State']['Running']:raise RuntimeError('active deployment changed')
    state('freezing')
    # Switch only the entry to maintenance, then stop all business writers.
    shutil.copy2(ROOT/'gateway'/'Caddyfile.maintenance',ROOT/'gateway'/'Caddyfile')
    compose(ROOT/'compose.json','up','-d','--no-deps','gateway')
    compose(ROOT/'ops'/'compose-before.json','stop','api','im','livekit','minio')
    (ROOT/'backups'/'enterprise-final.dump').write_bytes(run(['docker','exec',PG,'pg_dump','-U',PGUSER,'-d',SOURCE,'-Fc']))
    write(ROOT/'ops'/'business-before.json',fingerprints(SOURCE))
    redis=inspect('frogim-shared-default-shared-redis-1');r_env=environment(redis)
    run(['docker','exec','-e','REDISCLI_AUTH='+r_env['REDIS_PASSWORD'],'frogim-shared-default-shared-redis-1','redis-cli','SAVE'])
    snapshots=read(ROOT/'ops'/'containers-before.json')
    paths={m['Source'] for name in ['frogim-single-im-1','frogim-single-minio-1','frogim-shared-default-shared-redis-1'] for m in snapshots[name]['Mounts'] if m['Destination'] in ['/data','/config/wk.yaml']}
    paths.update([str(ROOT/'ops'),str(ROOT/'config'),str(ROOT/'backups'/'enterprise-final.dump'),read(ROOT/'ops'/'pointer-before.json')['releaseRoot']+'/config'])
    key=ROOT/'backups'/'encryption.key';key.write_text(secrets.token_urlsafe(48))
    archive=ROOT/'backups'/'cutover-backup.tar.enc'
    tar=subprocess.Popen(['tar','-cf','-','--absolute-names',*sorted(paths)],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    encrypt=subprocess.run(['openssl','enc','-aes-256-cbc','-salt','-pbkdf2','-pass','file:'+str(key),'-out',str(archive)],stdin=tar.stdout,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    tar.stdout.close();tar.wait()
    if tar.returncode or encrypt.returncode:raise RuntimeError('full backup archive failed')
    decrypt=subprocess.Popen(['openssl','enc','-d','-aes-256-cbc','-pbkdf2','-pass','file:'+str(key),'-in',str(archive)],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    listing=subprocess.run(['tar','-tf','-'],stdin=decrypt.stdout,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE);decrypt.stdout.close();decrypt.wait()
    if listing.returncode or decrypt.returncode:raise RuntimeError('encrypted backup unreadable')
    write(ROOT/'ops'/'backup-manifest.json',dict(archiveSHA256=sha(archive),archiveBytes=archive.stat().st_size,databaseSHA256=sha(ROOT/'backups'/'enterprise-final.dump'),serverOnly=True,archiveVerified=True))
    state('frozen');print('maintenance enabled; final backup verified',flush=True)

elif args.phase=='adopt':
    require('frozen');cfg=read(ROOT/'config'/'import.json');owner=urllib.parse.unquote(urllib.parse.urlsplit(cfg['sourceDatabaseUrl']).username)
    if not (ROOT/'ops'/'target-created.json').exists():
        create_db(TARGET,owner);write(ROOT/'ops'/'target-created.json',dict(database=TARGET))
    for phase in ['preflight','import','verify']:tool(ROOT/'config'/'import.json',phase,'final-'+phase)
    if fingerprints(SOURCE)!=read(ROOT/'ops'/'business-before.json'):raise RuntimeError('business data changed')
    # Identity import verified before any intentional authentication revocations.
    compose(ROOT/'compose.json','up','-d','--no-deps','platform');wait_health('frogim-single-platform-1')
    compose(ROOT/'compose.json','up','-d','--no-deps','api','im','livekit','minio')
    for name in [API,'frogim-single-im-1','frogim-single-livekit-1','frogim-single-minio-1']:wait_health(name)
    state('adopted');print('import verified; enterprise and platform ready behind maintenance',flush=True)

elif args.phase=='open':
    require('adopted')
    if not (ROOT/'release-manifest.json').exists():raise RuntimeError('release not pinned')
    output=run(['docker','exec','frogim-single-platform-1','/opt/frogim/light-tenancy-import','-config','/private/import.json','-phase','revoke'])
    write(ROOT/'ops'/'revocation.json',json.loads(output))
    expected=sql(SOURCE,'SELECT count(*) FROM im_users')
    actual=sql(SOURCE,"SELECT count(*) FROM lp_offline WHERE completed AND operation_id LIKE 'cutover:enterprise-a:%'")
    if expected!=actual or sql(SOURCE,'SELECT count(*) FROM lp_identity WHERE active')!='0':raise RuntimeError('account revocation not complete')
    # Version history starts from actual existing mobile policies, never a fictitious APK.
    policies=json.loads(sql(SOURCE,"SELECT COALESCE(json_agg(json_build_object('platform',platform,'policy',json_build_object('minimumVersion',minimum_version,'latestVersion',latest_version,'forceUpdate',force_update,'downloadUrl',download_url,'releaseNotes',release_notes))),'[]') FROM im_client_version_policies"))
    for row in policies:
        if row['platform'] not in ['android','ios','web']:continue
        policy=row['policy'];policy['releaseNotes']='本次先开放网页版，旧移动端停止业务访问；新移动端后续提供。'+(policy.get('releaseNotes') or '')
        if row['platform']=='web':continue
        encoded=json.dumps(policy,ensure_ascii=False).replace("'","''")
        sql(TARGET,"INSERT INTO lp_versions(platform,policy) VALUES('%s','%s') ON CONFLICT DO NOTHING"%(row['platform'],encoded))
    web=dict(minimumVersion='1.0.16',latestVersion='1.0.16',forceUpdate=False,downloadUrl='https://18.163.165.233/app/',releaseNotes='轻量平台统一认证；注册企业码选填。')
    sql(TARGET,"INSERT INTO lp_versions(platform,policy) VALUES('web','%s') ON CONFLICT DO NOTHING"%json.dumps(web,ensure_ascii=False).replace("'","''"))
    sql(TARGET,"INSERT INTO lp_version_history(platform,version,policy,actor,reason,source) SELECT platform,version,policy,'admin','轻量架构发布导入现行版本策略','baseline' FROM lp_versions ON CONFLICT DO NOTHING")
    # Back up the imported platform independently; never restore the whole shared instance.
    (ROOT/'backups'/'platform-preopen.dump').write_bytes(run(['docker','exec',PG,'pg_dump','-U',PGUSER,'-d',TARGET,'-Fc']))
    state('opening',dict(manifestSHA256=sha(ROOT/'release-manifest.json')))
    sql(TARGET,"BEGIN; UPDATE lp_tenants SET enabled=true WHERE id='enterprise-a' AND NOT enabled; INSERT INTO lp_audit(actor,action,object_id,reason,result) VALUES('admin','release.open','enterprise-a','轻量平台正式开服','success'); COMMIT")
    shutil.copy2(ROOT/'gateway'/'Caddyfile.active',ROOT/'gateway'/'Caddyfile')
    check_gateway();run(['docker','exec',GATEWAY,'caddy','reload','--config','/config/light/Caddyfile','--adapter','caddyfile'])
    old_root=pathlib.Path(read(ROOT/'ops'/'pointer-before.json')['releaseRoot'])
    renewal=(old_root/'renew-certificate.sh').read_text().replace('/config/Caddyfile','/config/light/Caddyfile')
    (ROOT/'renew-certificate.sh').write_text(renewal);os.chmod(ROOT/'renew-certificate.sh',0o700)
    dropin=pathlib.Path('/etc/systemd/system/qingwa-cert-renew.service.d/light-tenancy.conf')
    dropin.parent.mkdir(exist_ok=True);dropin.write_text('[Service]\nExecStart=\nExecStart='+str(ROOT/'renew-certificate.sh')+'\n')
    run(['systemctl','daemon-reload'])
    manifest=read(ROOT/'release-manifest.json')
    pointer=dict(architecture='lightweight-multitenant',commit=manifest['commit'],tag=manifest['tag'],compose=str(ROOT/'compose.json'),database=SOURCE,platformDatabase=TARGET,releaseRoot=str(ROOT),webVersion='1.0.16+8022')
    write(pathlib.Path('/data/frogim/active-deployment.json'),pointer)
    state('opened');write(ROOT/'ops'/'deployment-completed.json',dict(**pointer,openedAt=datetime.datetime.now(datetime.timezone.utc).isoformat(),importReport=read(ROOT/'ops'/'final-verify.json'),backup=read(ROOT/'ops'/'backup-manifest.json'),revocation=read(ROOT/'ops'/'revocation.json'),images=read(ROOT/'ops'/'images.json'),rehearsal=read(ROOT/'ops'/'rehearsal-completed.json')))
    print('Web open; mobile/audio-video/push receipt pending',flush=True)

elif args.phase=='rollback':
    phase=read(ROOT/'ops'/'state.json')['phase']
    if phase not in ['freezing','frozen','adopted']:raise RuntimeError('automatic rollback forbidden after opening or unconfirmed opening')
    compose(ROOT/'compose.json','stop','api','platform','im','livekit','minio')
    if phase!='freezing':
        restore_database(SOURCE,ROOT/'backups'/'enterprise-final.dump')
        if fingerprints(SOURCE)!=read(ROOT/'ops'/'business-before.json'):raise RuntimeError('rollback business verification failed')
    compose(ROOT/'ops'/'compose-before.json','up','-d','--no-deps','api','im','livekit','minio','gateway')
    for name in [API,GATEWAY,'frogim-single-im-1','frogim-single-livekit-1','frogim-single-minio-1']:wait_health(name)
    write(pathlib.Path('/data/frogim/active-deployment.json'),read(ROOT/'ops'/'pointer-before.json'))
    state('rolled-back');print('previous deployment restored before opening',flush=True)

elif args.phase=='check':
    for name in [API,GATEWAY,'frogim-single-platform-1','frogim-single-im-1','frogim-single-livekit-1','frogim-single-minio-1',PG,'frogim-shared-default-shared-redis-1']:wait_health(name)
    results={p:run(['curl','--silent','--show-error','--max-time','15','-o','/dev/null','-w','%{http_code}','https://18.163.165.233'+p]).decode() for p in ['/app/','/platform/','/platform/ready','/ready','/rtc/','/internal/directory/key']}
    if any(results[p]!='200' for p in ['/app/','/platform/','/platform/ready','/ready']):raise RuntimeError('public availability failure')
    write(ROOT/'ops'/'availability.json',results);print(json.dumps(results),flush=True)
