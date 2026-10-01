"""Linux-only Web static hotfix. No API restart, identity mutation or migration."""
import argparse
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import datetime

p = argparse.ArgumentParser()
p.add_argument('phase', choices=['prepare', 'publish', 'verify', 'rollback'])
p.add_argument('--root', required=True)
p.add_argument('--stage', required=True)
a = p.parse_args()
root, stage = pathlib.Path(a.root).resolve(), pathlib.Path(a.stage).resolve()
if root.parent != pathlib.Path('/data/frogim/releases') or not root.name.startswith('light-') or stage.parent != root / 'build' or not stage.name.startswith('web-startup-'):
    raise SystemExit('outside approved release paths')
os.umask(0o077)
pointer_file = pathlib.Path('/data/frogim/active-deployment.json')
pointer = json.loads(pointer_file.read_text())
if pointer.get('releaseRoot') != str(root) or pointer.get('architecture') != 'lightweight-multitenant':
    raise SystemExit('active deployment changed')
manifest = json.loads((stage / 'frontend/web-release.json').read_text())
rid = manifest['releaseId']
if len(rid) != 16 or any(c not in '0123456789abcdef' for c in rid): raise SystemExit('invalid resource version')
web = root / 'web'
record = root / 'ops' / ('web-startup-' + rid + '.json')
backup = root / 'backups' / ('web-startup-' + rid)
gateway = 'frogim-single-gateway-1'
shells = ['app_startup.js', 'app_startup.js.gz', 'flutter_bootstrap.js', 'flutter_bootstrap.js.gz', 'flutter_service_worker.js', 'flutter_service_worker.js.gz', 'version.json', 'version.json.gz', 'web-release.json', 'index.html.gz', 'index.html']

def run(argv):
    result = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
    if result.returncode:
        (root / 'ops/web-startup-error.private.log').write_bytes(result.stderr)
        raise RuntimeError('Web availability/configuration check failed')
    return result.stdout

def sha(path): return hashlib.sha256(path.read_bytes()).hexdigest()
def tree(path):
    return hashlib.sha256('\n'.join(p.relative_to(path).as_posix()+':'+sha(p) for p in sorted((p for p in path.rglob('*') if p.is_file()), key=lambda p:p.relative_to(path).as_posix())).encode()).hexdigest()
def write(path, value):
    temp = path.with_name(path.name + '.tmp')
    temp.write_text(json.dumps(value, ensure_ascii=False, indent=2))
    temp.replace(path)
def atomic_copy(source, target):
    temp = target.with_name(target.name + '.tmp')
    shutil.copyfile(source, temp); temp.chmod(0o644); temp.replace(target)
def reload():
    run(['docker', 'exec', gateway, 'caddy', 'reload', '--config', '/config/light/Caddyfile', '--adapter', 'caddyfile'])
def rollback(info):
    current = json.loads(pointer_file.read_text())
    if current.get('commit') != info['previousPointer']['commit'] or current.get('webRelease', {}).get('releaseId', rid) != rid:
        raise RuntimeError('a newer deployment blocks Web rollback')
    for name, existed in info['previousFiles'].items():
        if existed: atomic_copy(backup / name, web / name)
        elif (web / name).exists(): (web / name).unlink()
    for name in ['Caddyfile', 'Caddyfile.active']: atomic_copy(backup / name, root / 'gateway' / name)
    reload(); write(pointer_file, info['previousPointer'])
    info['phase'] = 'rolled-back'; write(record, info)

def verify():
    origin = 'https://18.163.165.233'
    def fetch(path): return run(['curl', '--fail', '--silent', '--show-error', '--max-time', '30', origin + path])
    if ('releases/' + rid + '/') not in fetch('/app/').decode(): raise RuntimeError('HTML resource version differs')
    paths = ['main.dart.js', 'assets/assets/fonts/NotoSansSC-Regular.otf', 'assets/assets/fonts/NotoColorEmoji.ttf', 'canvaskit/chromium/canvaskit.wasm']
    results = {}
    for path in paths:
        url = origin + '/app/releases/' + rid + '/' + path
        raw = run(['curl', '--fail', '--silent', '--show-error', '--head', '-H', 'Accept-Encoding: gzip', url]).decode()
        headers = dict(line.split(':',1) for line in raw.splitlines() if ':' in line)
        headers = {k.lower():v.strip() for k,v in headers.items()}
        if 'immutable' not in headers.get('cache-control','') or headers.get('content-encoding') != 'gzip': raise RuntimeError('immutable compression headers missing')
        result = run(['curl', '--silent', '--show-error', '-H', 'Accept-Encoding: gzip', '-H', 'If-None-Match: '+headers['etag'], '-o', '/dev/null', '-w', '%{http_code}|%{size_download}', url]).decode()
        if result != '304|0': raise RuntimeError('conditional static response differs')
        results[path] = dict(cache=headers['cache-control'], compressedBytes=int(headers['content-length']), conditional=result)
    if hashlib.sha256(fetch('/app/releases/' + rid + '/main.dart.js')).hexdigest() != manifest['mainSHA256']: raise RuntimeError('public script digest differs')
    if manifest.get('version'):
        for path in ['/app/version.json', '/app/releases/' + rid + '/version.json']:
            version = json.loads(fetch(path))
            if version.get('version') != manifest['version'] or version.get('build_number') != manifest['buildNumber']:
                raise RuntimeError('public Web version differs from release manifest')
        from urllib.parse import urlencode
        policy = json.loads(fetch('/platform/v2/config/version?' + urlencode(dict(platform='web', version=manifest['version'], installId='web-release-validation'))))
        if policy.get('currentVersion') != manifest['version'] or policy.get('forceUpdate') or policy.get('updateAvailable'):
            raise RuntimeError('Web release is behind the published version policy')
    for path in ['/ready', '/platform/ready', '/platform/']:
        if run(['curl','--silent','--show-error','--max-time','15','-o','/dev/null','-w','%{http_code}',origin+path]).decode() != '200': raise RuntimeError('service unavailable')
    return results

if a.phase == 'prepare':
    if record.exists(): raise SystemExit('this Web release already has a receipt')
    runtime = stage / 'frontend/releases' / rid
    if tree(runtime) != manifest['runtimeTreeSHA256']: raise SystemExit('runtime digest differs')
    previous = {name:(web / name).exists() for name in shells}
    backup.mkdir()
    for name, existed in previous.items():
        if existed: shutil.copy2(web / name, backup / name)
    for name in ['Caddyfile','Caddyfile.active']: shutil.copy2(root / 'gateway' / name, backup / name)
    text = (root / 'gateway/Caddyfile.active').read_text()
    start = text.index('    handle_path /app/* {')
    opening = text.index('{',start); end = opening+1; depth = 1
    while depth:
        depth += (text[end]=='{')-(text[end]=='}'); end += 1
    candidate = text[:start] + (stage / 'web-static.caddy.template').read_text().rstrip() + text[end:]
    config = root / 'gateway' / ('Caddyfile.web-' + rid)
    config.write_text(candidate); config.chmod(0o644)
    run(['docker','exec',gateway,'caddy','validate','--config','/config/light/'+config.name,'--adapter','caddyfile'])
    target = web / 'releases' / rid
    target.parent.mkdir(exist_ok=True); target.parent.chmod(0o755)
    if target.exists():
        if tree(target) != manifest['runtimeTreeSHA256']: raise SystemExit('immutable URL already has different content')
    else: shutil.copytree(runtime,target)
    write(record, dict(phase='prepared', releaseId=rid, previousPointer=pointer, previousFiles=previous, candidate=str(config), manifest=manifest, sourceCommit=json.loads((stage / 'source.json').read_text())['commit']))
    print('Web candidate validated; running application unchanged')
elif a.phase == 'publish':
    info = json.loads(record.read_text())
    if info['phase'] != 'prepared': raise SystemExit('Web release is not prepared')
    info['phase']='publishing'; write(record,info)
    try:
        for name in shells:
            source = stage / 'frontend' / name
            if source.exists(): atomic_copy(source, web / name)
            elif (web / name).exists(): (web / name).unlink()
        for name in ['Caddyfile','Caddyfile.active']: atomic_copy(pathlib.Path(info['candidate']),root / 'gateway' / name)
        reload(); info['availability']=verify()
        info['phase']='published'; info['publishedAt']=datetime.datetime.now(datetime.timezone.utc).isoformat(); write(record,info)
        pointer['webRelease']=dict(releaseId=rid,commit=info['sourceCommit'],receipt=str(record)); write(pointer_file,pointer)
        print(json.dumps({k:info[k] for k in ['phase','releaseId','sourceCommit','publishedAt','availability']},ensure_ascii=False))
    except Exception:
        rollback(info); raise
elif a.phase == 'rollback': rollback(json.loads(record.read_text()))
else: print(json.dumps(verify(),ensure_ascii=False))
