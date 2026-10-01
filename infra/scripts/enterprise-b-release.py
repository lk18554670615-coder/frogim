"""Linux release phases for the approved, empty enterprise B deployment.

Invoke one phase at a time. Never migrates A data, switches users or deletes old roots.
Private diagnostics and snapshots stay on the server; stdout contains summaries only.
"""
import argparse
import copy
import datetime
import hashlib
import json
import os
import pathlib
import secrets
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.request

A_ROOT = pathlib.Path('/data/frogim/releases/light-20261001-c83107e')
B_ROOT = pathlib.Path('/data/frogim/enterprise-b')
A_IP, B_IP = '172.31.36.243', '172.31.37.107'
ORIGINS = 'https://18.163.165.233,https://43.198.32.187'
PROJECT = 'frogim-enterprise-b'


def run(cmd, data=None, diagnostic=None):
    result = subprocess.run(cmd, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        if diagnostic:
            pathlib.Path(diagnostic).write_bytes(result.stderr)
        raise RuntimeError('command failed: ' + cmd[0] + '; inspect private server diagnostic')
    return result.stdout


def write(path, value):
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_suffix(path.suffix + '.tmp')
    temp.write_text(json.dumps(value, ensure_ascii=False, indent=2))
    temp.replace(path)


def read(path):
    return json.loads(pathlib.Path(path).read_text())


def inspect(name):
    return json.loads(run(['docker', 'inspect', name]))[0]


def env(container):
    return dict(v.split('=', 1) for v in container['Config']['Env'] if '=' in v)


def escaped(value, reverse=False):
    if isinstance(value, str):
        return value.replace('$$', '$') if reverse else value.replace('$', '$$')
    if isinstance(value, list):
        return [escaped(v, reverse) for v in value]
    if isinstance(value, dict):
        return {k: escaped(v, reverse) for k, v in value.items()}
    return value


def sha(path):
    h = hashlib.sha256()
    with pathlib.Path(path).open('rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def compose(root, *arguments):
    return run(['docker', 'compose', '-p', PROJECT if root == B_ROOT else 'frogim-single',
                '-f', str(root / 'compose.json'), *arguments], diagnostic=root / 'ops/last-error-private.log')


def wait_health(name):
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        state = inspect(name)['State']
        if state.get('Health', {}).get('Status') == 'healthy':
            return
        if state['Status'] == 'exited':
            raise RuntimeError('service exited: ' + name)
        time.sleep(2)
    raise RuntimeError('readiness timed out: ' + name)


def permissions_b():
    """Keep private files restricted while making them readable by the image UID."""
    root = B_ROOT
    c = read(root / 'compose.json')
    for service, paths in [('api', ['config/certs', 'data/plugins']), ('im', ['data/im', 'data/logs', 'data/plugins', 'config/wk.yaml']), ('livekit', ['config/livekit.yaml'])]:
        image = json.loads(run(['docker', 'image', 'inspect', c['services'][service]['image']]))[0]
        user = c['services'][service].get('user') or image['Config'].get('User') or '0'
        uid = int(user.split(':')[0])
        gid = int(user.split(':')[1]) if ':' in user else uid
        for relative in paths:
            path = root / relative
            for item in [path, *(path.rglob('*') if path.is_dir() else [])]:
                os.chown(item, uid, gid)
                item.chmod(0o700 if item.is_dir() else (0o550 if item.suffix == '.wkp' else 0o600))
    print('B private configuration ownership matches immutable image users')


def firewall(root, own, peer, ports, subnets):
    """Filter original destination before Docker DNAT; retain unrelated rules."""
    lines = ['#!/bin/sh', 'set -eu',
             'iptables -N DOCKER-USER 2>/dev/null || true',
             'iptables -N FROGIM_CONTROL 2>/dev/null || true',
             'iptables -F FROGIM_CONTROL',
             'iptables -C DOCKER-USER -j FROGIM_CONTROL 2>/dev/null || iptables -I DOCKER-USER 1 -j FROGIM_CONTROL']
    for port in ports:
        base = f'iptables -A FROGIM_CONTROL -p tcp -m conntrack --ctorigdst {own} --ctorigdstport {port}'
        for source in [own + '/32', peer + '/32', *subnets]:
            lines.append(base + f' -s {source} -j ACCEPT')
        lines.append(base + ' -j DROP')
    lines.append('iptables -A FROGIM_CONTROL -j RETURN')
    script = root / 'ops/control-firewall.sh'
    script.write_text('\n'.join(lines) + '\n')
    script.chmod(0o700)
    run(['/bin/sh', str(script)])
    unit = pathlib.Path('/etc/systemd/system/frogim-control-firewall.service')
    unit.write_text('[Unit]\nDescription=Private enterprise mTLS Docker firewall\nAfter=network-pre.target\nBefore=docker.service\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=' + str(script) + '\n[Install]\nWantedBy=multi-user.target\n')
    run(['systemctl', 'daemon-reload'])
    run(['systemctl', 'enable', 'frogim-control-firewall.service'])
    # UFW also covers connections terminating on the host rather than Docker.
    for port in ports:
        run(['ufw', 'allow', 'from', peer, 'to', own, 'port', str(port), 'proto', 'tcp'])


def snapshot(root, names):
    target = root / 'ops/before'
    if target.exists():
        raise RuntimeError('snapshot already exists; do not overwrite rollback evidence')
    target.mkdir(parents=True)
    write(target / 'containers.json', {n: inspect(n) for n in names})
    (target / 'iptables.rules').write_bytes(run(['iptables-save']))
    (target / 'ufw.txt').write_bytes(run(['ufw', 'status', 'numbered']))
    for name in ['qingwa-backup.service', 'qingwa-backup.timer', 'qingwa-cert-renew.service', 'qingwa-cert-renew.timer']:
        result = subprocess.run(['systemctl', 'cat', name], capture_output=True)
        (target / (name + '.txt')).write_bytes(result.stdout)


def package_a():
    root = A_ROOT / 'ops/enterprise-b-release'
    root.mkdir(exist_ok=True)
    snapshot(root, ['frogim-single-api-1', 'frogim-single-platform-1', 'frogim-single-gateway-1'])
    shutil.copy2(A_ROOT / 'compose.json', root / 'ops/before/compose.json')
    shutil.copytree(A_ROOT / 'config/certs', root / 'ops/before/certs')
    pg = inspect('frogim-shared-default-shared-postgres-1')
    (root / 'ops/before/platform.dump').write_bytes(run(['docker', 'exec', pg['Name'][1:], 'pg_dump', '-U', env(pg)['POSTGRES_USER'], '-d', 'platform_light', '-Fc']))
    bundle = root / 'bundle'
    bundle.mkdir()
    c = escaped(read(A_ROOT / 'compose.json'), True)
    services = {}
    for name in ['api', 'im', 'livekit', 'minio', 'gateway']:
        services[name] = {key: copy.deepcopy(c['services'][name][key]) for key in ['image', 'entrypoint', 'healthcheck'] if key in c['services'][name]}
    for name, container in [('postgres', 'frogim-shared-default-shared-postgres-1'), ('redis', 'frogim-shared-default-shared-redis-1')]:
        services[name] = {'image': inspect(container)['Image']}
    ae = env(inspect('frogim-single-api-1'))
    config = {key: ae[key] for key in ['IM_WUKONG_PLUGIN_TRUSTED_KEYS', 'IM_WUKONG_PLUGIN_ALLOWLIST', 'IM_PUSH_PROVIDER', 'IM_GETUI_APP_ID', 'IM_GETUI_APP_KEY', 'IM_GETUI_MASTER_SECRET'] if key in ae}
    write(bundle / 'template.json', {'services': services, 'apiConfig': config})
    (bundle / 'wk.yaml').write_bytes(pathlib.Path('/data/frogim/releases/main-33d9ea2-20261001/config/wk.yaml').read_bytes())
    (bundle / 'livekit.yaml').write_text(pathlib.Path('/data/frogim/releases/main-33d9ea2-20261001/config/livekit.yaml').read_text().replace('18.163.165.233', '43.198.32.187').replace('7882-7885', '7882-7889'))
    gateway = (A_ROOT / 'gateway/Caddyfile').read_text()
    begin, end = gateway.index('    handle /platform {'), gateway.index('    @backend ')
    gateway = gateway[:begin] + '''    handle /v2/config/version {
      reverse_proxy https://18.163.165.233 {
        header_up Host 18.163.165.233
        header_up -Authorization
        header_up -Cookie
      }
    }
''' + gateway[end:]
    (bundle / 'Caddyfile').write_text(gateway.replace('18.163.165.233/fullchain', '43.198.32.187/fullchain').replace('18.163.165.233/privkey', '43.198.32.187/privkey').replace('default_sni 18.163.165.233', 'default_sni 43.198.32.187').replace('redir https://18.163.165.233{uri}', 'redir https://43.198.32.187{uri}'))
    for folder in ['web', 'legal']:
        shutil.copytree(A_ROOT / folder, bundle / folder)
    shutil.copy2(A_ROOT / 'config/certs/ca.pem', bundle / 'ca.pem')
    shutil.copy2('/data/frogim/releases/main-33d9ea2-20261001/data/plugins/wk.plugin.im-policy-linux-amd64.wkp', bundle / 'wk.plugin.im-policy-linux-amd64.wkp')
    images = sorted({s['image'] for s in services.values()})
    for tag in ['minio/mc:frogim-pinned-20260929', 'certbot/certbot:latest']:
        images.append(json.loads(run(['docker', 'image', 'inspect', tag]))[0]['Id'])
    write(bundle / 'images.json', {'services': {k: v['image'] for k, v in services.items()}, 'mc': images[-2], 'certbot': images[-1]})
    with (root / 'images.tar').open('wb') as output:
        subprocess.run(['docker', 'save', *images], stdout=output, check=True)
    with tarfile.open(root / 'assets.tar.gz', 'w:gz') as archive:
        archive.add(bundle, arcname='bundle')
    write(root / 'artifacts.json', {p.name: {'sha256': sha(p), 'bytes': p.stat().st_size} for p in [root / 'images.tar', root / 'assets.tar.gz']})
    print(json.dumps(read(root / 'artifacts.json')))


def prepare_b():
    root = B_ROOT
    if (root / 'compose.json').exists():
        raise RuntimeError('B already prepared; use recorded next phase')
    if run(['docker', 'ps', '-q']).strip():
        raise RuntimeError('B has running containers; repeat read-only preflight before continuing')
    snapshot(root, [])
    for folder in ['config/certs', 'config/gateway', 'config/acme-webroot', 'certificates', 'data/postgres', 'data/redis', 'data/media', 'data/im', 'data/logs', 'data/plugins', 'downloads', 'backups']:
        (root / folder).mkdir(parents=True, exist_ok=True)
    artifacts = read(root / 'artifacts.json')
    for name, evidence in artifacts.items():
        if sha(root / name) != evidence['sha256']:
            raise RuntimeError('artifact digest mismatch')
    run(['docker', 'load', '-i', str(root / 'images.tar')], diagnostic=root / 'ops/last-error-private.log')
    with tarfile.open(root / 'assets.tar.gz') as archive:
        archive.extractall(root, filter='data')
    template = read(root / 'bundle/template.json')
    services = template['services']
    token = lambda: secrets.token_urlsafe(48)
    pg, redis, minioroot, minioapp, jwt, manager, policy, imtoken, rtc = [token() for _ in range(9)]
    images = read(root / 'bundle/images.json')
    password = sys.stdin.buffer.read().strip()
    if not 6 <= len(password) <= 32:
        raise RuntimeError('supply approved B administrator password on stdin (6–32 bytes)')
    pw_hash = run(['docker', 'run', '--rm', '-i', '--entrypoint', '/opt/frogim/light-tenancy-import', services['api']['image'], '-hash-password-stdin'], password).decode().strip()
    ae = dict(template['apiConfig'], IM_MODE='enterprise', IM_TENANT_ID='enterprise-b', IM_ENV='production', IM_DEV_MODE='false', IM_ADDR=':8080', IM_ALLOWED_ORIGINS=ORIGINS,
              IM_PLATFORM_URL='https://' + A_IP + ':8443', IM_CONTROL_ADDR=':8443', IM_CONTROL_CERT='/certs/cert.pem', IM_CONTROL_KEY='/certs/key.pem', IM_CONTROL_CA='/certs/ca.pem',
              IM_ENTERPRISE_API_URL='https://43.198.32.187', IM_ENTERPRISE_MEDIA_URL='https://43.198.32.187', IM_DATABASE_URL=f'postgres://enterprise:{pg}@postgres:5432/enterprise_b?sslmode=disable',
              IM_REDIS_URL=f'redis://:{redis}@redis:6379/0', IM_JWT_SECRET=jwt, IM_ADMIN_USERNAME='admin', IM_ADMIN_ID='enterprise-b-admin', IM_ADMIN_PASSWORD_HASH=pw_hash,
              IM_S3_ENDPOINT='minio:9000', IM_S3_PUBLIC_ENDPOINT='43.198.32.187', IM_S3_PUBLIC_SECURE='true', IM_S3_BUCKET='nexachat-media', IM_S3_ACCESS_KEY='enterprise-b-media', IM_S3_SECRET_KEY=minioapp, IM_S3_REGION='us-east-1',
              IM_WUKONG_ENABLED='true', IM_WUKONG_API_URL='http://im:5001', IM_WUKONG_MANAGER_URL='http://im:5300', IM_WUKONG_MANAGER_TOKEN=manager, IM_WUKONG_TOKEN_SECRET=imtoken,
              IM_WUKONG_POLICY_SECRET=policy, IM_WUKONG_GRPC_ADDR=':6970', IM_WUKONG_TCP_URL='tcp://43.198.32.187:5100', IM_WUKONG_WS_URL='wss://43.198.32.187/im', IM_WUKONG_PLUGIN_DIR='/plugins',
              IM_LIVEKIT_ENABLED='true', IM_LIVEKIT_URL='wss://43.198.32.187/livekit', IM_LIVEKIT_API_URL='http://livekit:7880', IM_LIVEKIT_API_KEY='enterprise-b-livekit', IM_LIVEKIT_API_SECRET=rtc,
              IM_TRUST_PROXY='true', IM_LOG_LEVEL='warn', IM_HTTP_LOG_SUCCESS_SAMPLE_RATE='0', IM_SEED_DEMO='false')
    services['api'].update(environment=ae, entrypoint=['/opt/frogim/im-server'], ports=[B_IP + ':8443:8443'], volumes=[str(root / 'config/certs') + ':/certs:ro', str(root / 'data/plugins') + ':/plugins'], networks=['business', 'data', 'edge'], depends_on={'postgres': {'condition': 'service_healthy'}, 'redis': {'condition': 'service_healthy'}})
    services['im'].update(environment=dict(IM_WUKONG_POLICY_SECRET=policy, IM_WUKONG_POLICY_URL='http://api:8080/internal/wukong/policy/check', WK_EXTERNAL_IP='43.198.32.187', WK_EXTERNAL_WSADDR='wss://43.198.32.187/im', WK_EXTERNAL_TCPADDR='43.198.32.187:5100', WK_MANAGERTOKEN=manager, WK_TOKENAUTHON='true', WK_DATASOURCE_ADDR='http://api:8080/internal/wukong/datasource', WK_WEBHOOK_GRPCADDR='api:6970'), ports=['5100:5100'], volumes=[str(root / 'data/im') + ':/data', str(root / 'data/logs') + ':/logs', str(root / 'data/plugins') + ':/data/plugins', str(root / 'config/wk.yaml') + ':/config/wk.yaml:ro'], networks=['business', 'edge'])
    services['livekit'].update(environment={'LIVEKIT_KEYS': 'enterprise-b-livekit: ' + rtc}, ports=['7881:7881', '7882-7889:7882-7889/udp'], volumes=[str(root / 'config/livekit.yaml') + ':/config/livekit.yaml:ro'], networks=['business', 'edge'])
    services['minio'].update(environment={'MINIO_ROOT_USER': 'enterprise-b-root', 'MINIO_ROOT_PASSWORD': minioroot}, command=['server', '/data', '--console-address', ':9001'], volumes=[str(root / 'data/media') + ':/data'], networks=['business'])
    services['postgres'].update(environment={'POSTGRES_USER': 'enterprise', 'POSTGRES_PASSWORD': pg, 'POSTGRES_DB': 'enterprise_b'}, volumes=[str(root / 'data/postgres') + ':/var/lib/postgresql/data'], networks=['data'], healthcheck={'test': ['CMD', 'pg_isready', '-U', 'enterprise', '-d', 'enterprise_b'], 'interval': '5s', 'timeout': '3s', 'retries': 20})
    services['redis'].update(environment={'REDIS_PASSWORD': redis}, command=['sh', '-c', 'exec redis-server --appendonly yes --requirepass "$REDIS_PASSWORD"'], volumes=[str(root / 'data/redis') + ':/data'], networks=['data'], healthcheck={'test': ['CMD-SHELL', 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping'], 'interval': '5s', 'timeout': '3s', 'retries': 20})
    services['gateway'].update(ports=['80:80', '443:443'], volumes=[str(root / 'certificates') + ':/etc/letsencrypt:ro', str(root / 'config/acme-webroot') + ':/var/www/certbot:ro', str(root / 'config/gateway') + ':/config/light:ro', str(root / 'bundle/web') + ':/srv/web:ro', str(root / 'bundle/legal') + ':/srv/legal:ro', str(root / 'downloads') + ':/srv/downloads:ro'], networks=['business', 'edge'])
    for service in services.values():
        service.update(restart='unless-stopped', logging={'driver': 'json-file', 'options': {'max-size': '10m', 'max-file': '3'}})
    for name in ['api', 'im', 'livekit']:
        services[name]['user'] = '0:0'
    config = {'name': PROJECT, 'services': services, 'networks': {name: {'internal': name != 'edge', 'ipam': {'config': [{'subnet': f'192.168.{64 + index}.0/24'}]}} for index, name in enumerate(['business', 'data', 'edge'])}}
    write(root / 'compose.json', escaped(config))
    shutil.copy2(root / 'bundle/ca.pem', root / 'config/certs/ca.pem')
    shutil.copy2(root / 'bundle/wk.plugin.im-policy-linux-amd64.wkp', root / 'data/plugins/wk.plugin.im-policy-linux-amd64.wkp')
    for src, dst in [('wk.yaml', 'config/wk.yaml'), ('livekit.yaml', 'config/livekit.yaml'), ('Caddyfile', 'config/gateway/Caddyfile')]:
        shutil.copy2(root / 'bundle' / src, root / dst)
    for folder in ['bundle/web', 'bundle/legal']:
        for path in (root / folder).rglob('*'):
            path.chmod(0o755 if path.is_dir() else 0o644)
        (root / folder).chmod(0o755)
    run(['openssl', 'req', '-newkey', 'rsa:3072', '-nodes', '-subj', '/CN=enterprise-b', '-keyout', str(root / 'config/certs/key.pem'), '-out', str(root / 'config/certs/request.csr')], diagnostic=root / 'ops/last-error-private.log')
    firewall(root, B_IP, A_IP, [8443], ['192.168.64.0/24', '192.168.65.0/24', '192.168.66.0/24'])
    compose(root, 'config', '--quiet')
    write(root / 'ops/prepared.json', {'at': now(), 'services': list(services), 'composeSha256': sha(root / 'compose.json'), 'images': images, 'admin': 'admin', 'dataImported': False})
    print('B prepared: seven services, fresh data, private key generated on B')


def connect_a():
    root = A_ROOT / 'ops/enterprise-b-release'
    before = root / 'ops/before'
    c = escaped(read(A_ROOT / 'compose.json'), True)
    expected = read(before / 'containers.json')
    for name in ['api', 'platform']:
        container = 'frogim-single-' + name + '-1'
        if inspect(container)['Image'] != expected[container]['Image']:
            raise RuntimeError('image changed since preflight')
    certs = A_ROOT / 'config/certs'
    for name, identity in [('platform', 'platform'), ('api', 'enterprise-a')]:
        folder = certs / name
        run(['openssl', 'req', '-new', '-key', str(folder / 'key.pem'), '-subj', '/CN=' + identity, '-out', str(folder / 'private-ip.csr')])
        (folder / 'private-ip.ext').write_text(f'subjectAltName=DNS:{name},IP:{A_IP}\nextendedKeyUsage=serverAuth,clientAuth\n')
        run(['openssl', 'x509', '-req', '-days', '365', '-in', str(folder / 'private-ip.csr'), '-CA', str(certs / 'ca.pem'), '-CAkey', str(certs / 'ca.key'), '-CAcreateserial', '-extfile', str(folder / 'private-ip.ext'), '-out', str(folder / 'cert.next.pem')])
        run(['openssl', 'verify', '-CAfile', str(certs / 'ca.pem'), str(folder / 'cert.next.pem')])
        shutil.copy2(folder / 'cert.next.pem', folder / 'cert.pem')
    c['services']['platform']['ports'] = [A_IP + ':8443:8443']
    if 'edge' not in c['services']['platform']['networks']:
        # Docker does not publish ports for a container on internal-only networks.
        c['services']['platform']['networks'].append('edge')
    c['services']['api']['ports'] = ['127.0.0.1:18810:8080', A_IP + ':8444:8443']
    for name in ['api', 'platform']:
        c['services'][name]['environment']['IM_ALLOWED_ORIGINS'] = ORIGINS
    firewall(root, A_IP, B_IP, [8443, 8444], ['172.22.0.0/16', '172.29.0.0/16', '172.30.0.0/16', '192.168.0.0/20'])
    write(A_ROOT / 'compose.json', escaped(c))
    for name in ['platform', 'api']:
        try:
            compose(A_ROOT, 'up', '-d', '--no-deps', name)
            wait_health('frogim-single-' + name + '-1')
        except Exception:
            shutil.copy2(before / 'compose.json', A_ROOT / 'compose.json')
            for leaf in ['api', 'platform']:
                shutil.copy2(before / 'certs' / leaf / 'cert.pem', certs / leaf / 'cert.pem')
            compose(A_ROOT, 'up', '-d', '--no-deps', 'platform', 'api')
            raise
    write(root / 'ops/connected.json', {'at': now(), 'composeSha256': sha(A_ROOT / 'compose.json'), 'imagesUnchanged': True})
    print('A/platform connected; images and business addresses preserved')


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('phase', choices=['package-a', 'prepare-b', 'permissions-b', 'connect-a'])
    args = parser.parse_args()
    os.umask(0o077)
    {'package-a': package_a, 'prepare-b': prepare_b, 'permissions-b': permissions_b, 'connect-a': connect_a}[args.phase]()
