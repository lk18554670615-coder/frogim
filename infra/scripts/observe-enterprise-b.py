"""Read-only, aggregate observation of enterprise B; no identities or raw logs."""
import argparse
import datetime
import json
import pathlib
import re
import shutil
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--since', default='')
args = parser.parse_args()
root = pathlib.Path('/data/frogim/enterprise-b')
receipt = json.loads((root / 'ops/deployment-completed.json').read_text())
since = args.since or receipt['openedAt']
datetime.datetime.fromisoformat(since.replace('Z', '+00:00'))


def run(command):
    result = subprocess.run(command, capture_output=True, timeout=30)
    if result.returncode:
        raise RuntimeError('read-only observation failed: ' + command[0])
    return result.stdout.decode(errors='replace')


health = {}
logs = {}
for service in ['gateway', 'api', 'im', 'livekit', 'minio', 'postgres', 'redis']:
    name = 'frogim-enterprise-b-' + service + '-1'
    container = json.loads(run(['docker', 'inspect', name]))[0]
    state = container['State']
    health[service] = {'running': state['Running'], 'health': state.get('Health', {}).get('Status', 'unknown'), 'restarts': container['RestartCount'], 'image': container['Image'], 'logBytes': pathlib.Path(container['LogPath']).stat().st_size if pathlib.Path(container['LogPath']).exists() else None}
    if service in ['api', 'im', 'gateway']:
        result = subprocess.run(['docker', 'logs', '--since', since, name], capture_output=True, timeout=30)
        if result.returncode:
            raise RuntimeError('log observation unavailable')
        lines = (result.stdout + result.stderr).decode(errors='replace').splitlines()
        logs[service] = {'lines': len(lines), 'errors': sum(bool(re.search(r'"level"\s*:\s*"(?:error|fatal|ERROR|FATAL)"|\bpanic:', line)) for line in lines), 'authenticationErrors': sum(bool(re.search(r'"status"\s*:\s*(?:401|403)\b|AUTH_REJECTED|IDENTITY_REJECTED|TOKEN_REJECTED', line)) for line in lines)}

availability = {path: run(['curl', '--silent', '--show-error', '--max-time', '10', '-o', '/dev/null', '-w', '%{http_code}', 'https://43.198.32.187' + path]) for path in ['/app/', '/admin/', '/ready']}
sql = "SELECT json_build_object('users',(SELECT count(*) FROM im_users),'activeIdentities',(SELECT count(*) FROM lp_identity WHERE active),'profileSyncFailures',(SELECT count(*) FROM lp_identity WHERE sync_error<>''),'incompleteRevocations',(SELECT count(*) FROM lp_offline WHERE NOT completed),'pendingPush',(SELECT count(*) FROM im_push_outbox WHERE status IN ('pending','processing')),'newFailedPush',(SELECT count(*) FROM im_push_outbox WHERE status='failed' AND created_at>='%s'::timestamptz),'databaseConnections',(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()))" % datetime.datetime.fromisoformat(receipt['openedAt']).isoformat()
database = json.loads(run(['docker', 'exec', '-e', 'PGOPTIONS=-c default_transaction_read_only=on', 'frogim-enterprise-b-postgres-1', 'psql', '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '-U', 'enterprise', '-d', 'enterprise_b', '-c', sql]))
connections = {}
for service in ['api', 'im']:
    rows = run(['docker', 'exec', 'frogim-enterprise-b-' + service + '-1', 'cat', '/proc/net/tcp', '/proc/net/tcp6'])
    connections[service] = sum(len(line.split()) > 3 and line.split()[3] == '01' for line in rows.splitlines())
timers = {}
for name in ['qingwa-backup', 'qingwa-cert-renew']:
    timers[name] = {'active': run(['systemctl', 'is-active', name + '.timer']).strip(), 'correctTarget': str(root / 'enterprise-b-ops.py') in run(['systemctl', 'show', name + '.service', '-p', 'ExecStart', '--value'])}
disk = shutil.disk_usage(root)
data_bytes = int(run(['du', '-sb', str(root / 'data')]).split()[0])
issues = [service + ' not healthy' for service, value in health.items() if not value['running'] or value['health'] != 'healthy']
issues += [path + ' unavailable' for path, status in availability.items() if status != '200']
issues += [name + ' timer target unavailable' for name, value in timers.items() if value['active'] != 'active' or not value['correctTarget']]
if database['profileSyncFailures'] or database['incompleteRevocations']:
    issues.append('unfinished sync or revocation requires review')
if disk.free < 10 * 1024**3:
    issues.append('less than 10 GiB free disk')
print(json.dumps({'checkedAt': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'logWindowStart': since, 'releaseCommit': receipt['commit'], 'health': health, 'availability': availability, 'database': database, 'logCounts': logs, 'establishedTCP': connections, 'timers': timers, 'disk': {'total': disk.total, 'used': disk.used, 'free': disk.free}, 'dataBytes': data_bytes, 'issues': issues}, ensure_ascii=False))
