"""Linux read-only observation. Prints aggregate JSON; never prints logs or identities."""
import argparse
import datetime
import json
import pathlib
import re
import shutil
import subprocess

p = argparse.ArgumentParser()
p.add_argument('--root', required=True)
p.add_argument('--since', default='')
a = p.parse_args()
root = pathlib.Path(a.root).resolve()
if root.parent != pathlib.Path('/data/frogim/releases') or not root.name.startswith('light-'):
    raise SystemExit('invalid release directory')

def command(argv):
    result = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=45)
    if result.returncode:
        raise RuntimeError('observation command failed: ' + argv[0])
    return result.stdout.decode(errors='replace')

receipt = json.loads((root / 'ops/deployment-completed.json').read_text())
pointer = json.loads(pathlib.Path('/data/frogim/active-deployment.json').read_text())
if pointer.get('architecture') != 'lightweight-multitenant' or pointer.get('commit') != receipt['commit']:
    raise SystemExit('active deployment changed; do not apply old observation assumptions')
since = a.since or receipt['openedAt']
datetime.datetime.fromisoformat(since.replace('Z', '+00:00'))
now = datetime.datetime.now(datetime.timezone.utc).isoformat()
names = ['frogim-single-' + n + '-1' for n in ['api', 'platform', 'gateway', 'im', 'livekit', 'minio']]
names += ['frogim-shared-default-shared-postgres-1', 'frogim-shared-default-shared-redis-1']
containers = {n: json.loads(command(['docker', 'inspect', n]))[0] for n in names}
health = {n: dict(running=c['State']['Running'], health=c['State'].get('Health', {}).get('Status', 'unknown'), restarts=c['RestartCount'], logBytes=pathlib.Path(c['LogPath']).stat().st_size if c.get('LogPath') and pathlib.Path(c['LogPath']).exists() else None) for n, c in containers.items()}
availability = {path: command(['curl', '--silent', '--show-error', '--max-time', '15', '-o', '/dev/null', '-w', '%{http_code}', 'https://18.163.165.233' + path]) for path in ['/app/', '/platform/', '/ready', '/platform/ready']}
pg = 'frogim-shared-default-shared-postgres-1'
env = dict(v.split('=', 1) for v in containers[pg]['Config']['Env'])
def sql(db, query):
    return json.loads(command(['docker', 'exec', '-e', 'PGOPTIONS=-c default_transaction_read_only=on', pg, 'psql', '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '-U', env['POSTGRES_USER'], '-d', db, '-c', query]))
platform = sql(receipt['platformDatabase'], "SELECT json_build_object('pendingOperations',(SELECT count(*) FROM lp_operations WHERE phase<>'done'),'pendingOverTenMinutes',(SELECT count(*) FROM lp_operations WHERE phase<>'done' AND updated_at<now()-interval '10 minutes'),'profileSyncFailures',(SELECT count(*) FROM lp_memberships WHERE sync_error<>''),'currentConnections',(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()))")
# Only count new outbox entries; legacy failed entries are not new provider failures.
opened = datetime.datetime.fromisoformat(receipt['openedAt']).isoformat()
enterprise = sql(receipt['database'], "SELECT json_build_object('pendingPush',(SELECT count(*) FROM im_push_outbox WHERE status IN ('pending','processing')),'newFailedPush',(SELECT count(*) FROM im_push_outbox WHERE status='failed' AND created_at>='%s'::timestamptz),'activeIdentities',(SELECT count(*) FROM lp_identity WHERE active),'incompleteRevocations',(SELECT count(*) FROM lp_offline WHERE NOT completed),'currentConnections',(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()))" % opened)
logs = {}
for name in ['frogim-single-api-1', 'frogim-single-platform-1', 'frogim-single-gateway-1']:
    # Docker writes container stderr to process stderr; capture both without exposing it.
    result = subprocess.run(['docker', 'logs', '--since', since, name], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=45)
    if result.returncode:
        raise RuntimeError('container log observation unavailable')
    lines = (result.stdout + result.stderr).decode(errors='replace').splitlines()
    logs[name] = dict(lines=len(lines), errors=sum(bool(re.search(r'"level"\s*:\s*"(?:error|fatal)"|\b(?:ERROR|FATAL|panic:)\b', line)) for line in lines), authenticationErrors=sum(bool(re.search(r'"status"\s*:\s*(?:401|403)\b|AUTH_REJECTED|IDENTITY_REJECTED|TOKEN_REJECTED', line)) for line in lines))
redis = 'frogim-shared-default-shared-redis-1'
redis_env = dict(v.split('=', 1) for v in containers[redis]['Config']['Env'])
redis_info = command(['docker', 'exec', '-e', 'REDISCLI_AUTH=' + redis_env['REDIS_PASSWORD'], redis, 'redis-cli', 'INFO'])
redis_counts = {key: int(re.search(r'^' + key + r':(\d+)', redis_info, re.M).group(1)) for key in ['connected_clients', 'used_memory']}
tcp = {}
for name in ['frogim-single-im-1', 'frogim-single-api-1']:
    table = command(['docker', 'exec', name, 'cat', '/proc/net/tcp', '/proc/net/tcp6'])
    tcp[name] = sum(len(line.split()) > 3 and line.split()[3] == '01' for line in table.splitlines())
timer = command(['systemctl', 'is-active', 'qingwa-cert-renew.timer']).strip()
renewal = command(['systemctl', 'show', 'qingwa-cert-renew.service', '-p', 'ExecStart', '--value'])
disk = shutil.disk_usage(root)
issues = [n + ' not healthy' for n, v in health.items() if not v['running'] or v['health'] != 'healthy']
issues += [path + ' unavailable' for path, status in availability.items() if status != '200']
if timer != 'active' or str(root / 'renew-certificate.sh') not in renewal:
    issues.append('certificate renewal target changed')
if platform['pendingOverTenMinutes'] or enterprise['incompleteRevocations']:
    issues.append('unfinished operation needs review')
if disk.free < 10 * 1024**3:
    issues.append('less than 10 GiB free space')
print(json.dumps(dict(checkedAt=now, logWindowStart=since, releaseCommit=receipt['commit'], health=health, availability=availability, platform=platform, enterprise=enterprise, logCounts=logs, redis=redis_counts, establishedTCP=tcp, disk=dict(total=disk.total, used=disk.used, free=disk.free), certificateTimer=timer, issues=issues), ensure_ascii=False))
