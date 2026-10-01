"""Explicit certificate, directory and backup operations; no user switching."""
import argparse
import fcntl
import importlib.util
import json
import pathlib
import shutil
import ssl
import subprocess
import sys
import tarfile
import time
import urllib.request

spec = importlib.util.spec_from_file_location('release', pathlib.Path(__file__).with_name('enterprise-b-release.py'))
r = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r)


def http(url, body=None, token='', context=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    request = urllib.request.Request(url, data=json.dumps(body).encode() if body is not None else None, headers=headers)
    with urllib.request.urlopen(request, timeout=20, context=context) as response:
        return json.load(response)


def mtls(certs):
    context = ssl.create_default_context(cafile=str(certs / 'ca.pem'))
    context.minimum_version = ssl.TLSVersion.TLSv1_3
    context.load_cert_chain(str(certs / 'cert.pem'), str(certs / 'key.pem'))
    return context


def sign_b():
    folder = r.A_ROOT / 'ops/enterprise-b-release/b-certificate'
    folder.mkdir(exist_ok=True)
    csr = sys.stdin.buffer.read()
    (folder / 'request.csr').write_bytes(csr)
    subject = r.run(['openssl', 'req', '-in', str(folder / 'request.csr'), '-noout', '-subject']).decode()
    if subject.strip() not in ['subject=CN = enterprise-b', 'subject=CN=enterprise-b']:
        raise RuntimeError('unexpected CSR identity')
    (folder / 'extensions').write_text('subjectAltName=DNS:api,IP:172.31.37.107\nextendedKeyUsage=serverAuth,clientAuth\n')
    ca = r.A_ROOT / 'config/certs'
    r.run(['openssl', 'x509', '-req', '-days', '365', '-in', str(folder / 'request.csr'), '-CA', str(ca / 'ca.pem'), '-CAkey', str(ca / 'ca.key'), '-CAcreateserial', '-extfile', str(folder / 'extensions'), '-out', str(folder / 'cert.pem')])
    r.run(['openssl', 'verify', '-CAfile', str(ca / 'ca.pem'), str(folder / 'cert.pem')])
    print('B CSR signed; CA private key remained on A')


def issue_or_renew(issue=False):
    root = r.B_ROOT
    image = r.read(root / 'bundle/images.json')['certbot']
    temporary = None
    try:
        if issue:
            temporary = subprocess.Popen([sys.executable, '-m', 'http.server', '80', '--bind', '0.0.0.0', '--directory', str(root / 'config/acme-webroot')], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            time.sleep(1)
            if temporary.poll() is not None:
                raise RuntimeError('temporary ACME listener could not start')
        command = ['docker', 'run', '--rm', '--pull', 'never', '-v', str(root / 'certificates') + ':/etc/letsencrypt', '-v', str(root / 'config/acme-webroot') + ':/var/www/certbot', image]
        command += ['certonly', '--non-interactive', '--agree-tos', '--register-unsafely-without-email', '--preferred-profile', 'shortlived', '--webroot', '--webroot-path', '/var/www/certbot', '--ip-address', '43.198.32.187'] if issue else ['renew', '--quiet', '--no-random-sleep-on-renew', '--cert-name', '43.198.32.187']
        r.run(command, diagnostic=root / 'ops/last-error-private.log')
        cert = root / 'certificates/live/43.198.32.187/fullchain.pem'
        r.run(['openssl', 'x509', '-in', str(cert), '-noout', '-checkip', '43.198.32.187'])
        r.run(['openssl', 'x509', '-in', str(cert), '-noout', '-checkend', '43200'])
        if not issue:
            for verb in ['validate', 'reload']:
                r.run(['docker', 'exec', 'frogim-enterprise-b-gateway-1', 'caddy', verb, '--config', '/config/light/Caddyfile', '--adapter', 'caddyfile'])
        r.write(root / 'ops/certificate.json', {'at': r.now(), 'sha256': r.sha(cert), 'ip': '43.198.32.187', 'validated': True})
        print('B IP certificate validated' + (' and gateway reloaded' if not issue else ''))
    finally:
        if temporary:
            temporary.terminate()
            temporary.wait(timeout=5)


def init_media():
    root = r.B_ROOT
    c = r.escaped(r.read(root / 'compose.json'), True)
    me = c['services']['minio']['environment']
    ae = c['services']['api']['environment']
    policy = {'Version': '2012-10-17', 'Statement': [{'Effect': 'Allow', 'Action': ['s3:GetBucketLocation', 's3:ListBucket', 's3:ListBucketMultipartUploads'], 'Resource': ['arn:aws:s3:::nexachat-media']}, {'Effect': 'Allow', 'Action': ['s3:GetObject', 's3:PutObject', 's3:DeleteObject', 's3:AbortMultipartUpload', 's3:ListMultipartUploadParts'], 'Resource': ['arn:aws:s3:::nexachat-media/*']}]}
    r.write(root / 'config/media-policy.json', policy)
    command = 'mc mb --ignore-existing root/nexachat-media && mc admin user add root "$APP_USER" "$APP_SECRET" && mc admin policy create root enterprise-b-media /policy.json && mc admin policy attach root enterprise-b-media --user "$APP_USER"'
    r.run(['docker', 'run', '--rm', '--network', 'frogim-enterprise-b_business', '-e', f"MC_HOST_root=http://{me['MINIO_ROOT_USER']}:{me['MINIO_ROOT_PASSWORD']}@minio:9000", '-e', 'APP_USER=' + ae['IM_S3_ACCESS_KEY'], '-e', 'APP_SECRET=' + ae['IM_S3_SECRET_KEY'], '-v', str(root / 'config/media-policy.json') + ':/policy.json:ro', '--entrypoint', '/bin/sh', r.read(root / 'bundle/images.json')['mc'], '-c', command], diagnostic=root / 'ops/last-error-private.log')
    print('B bucket and restricted application media identity initialized')


def admin():
    credentials = r.read(r.A_ROOT / 'config/operator-input.json')
    response = http('https://18.163.165.233/platform/admin/auth/login', credentials)
    return response['token']


def directory(enable=False):
    token = admin()
    base = 'https://18.163.165.233/platform/admin'
    tenants = http(base + '/tenants', token=token)
    a = next(t for t in tenants if t['id'] == 'enterprise-a')
    if not a['isDefault'] or not a['enabled'] or len([t for t in tenants if t['isDefault']]) != 1:
        raise RuntimeError('default A state changed')
    r.write(r.A_ROOT / 'ops/enterprise-b-release/directory-before.json', tenants) if not (r.A_ROOT / 'ops/enterprise-b-release/directory-before.json').exists() else None
    if a['controlUrl'] != 'https://172.31.36.243:8444':
        a.update(controlUrl='https://172.31.36.243:8444', reason='企业 B 跨服务器接入：A 私网控制地址', confirmed=True)
        a = http_patch(base + '/tenants/enterprise-a', a, token)
    services = {'apiBaseUrl': 'https://43.198.32.187', 'imWsUrl': 'wss://43.198.32.187/im', 'imTcpUrl': 'tcp://43.198.32.187:5100', 'callSignalUrl': 'wss://43.198.32.187/livekit', 'mediaBaseUrl': 'https://43.198.32.187'}
    b = next((t for t in tenants if t['id'] == 'enterprise-b'), None)
    if not b:
        b = http(base + '/tenants', {'id': 'enterprise-b', 'name': '客户B企业', 'code': 'B', 'enabled': False, 'isDefault': False, 'version': 0, 'controlUrl': 'https://172.31.37.107:8443', 'services': services, 'reason': '登记新建独立企业 B，保持停止登录', 'confirmed': True}, token)
    if b['services'] != services or b['controlUrl'] != 'https://172.31.37.107:8443' or b['isDefault']:
        raise RuntimeError('B directory differs from approved deployment')
    if enable:
        proof = r.read(r.A_ROOT / 'ops/enterprise-b-release/connectivity.json')
        if not proof.get('privateMTLS') or not proof.get('publicControlBlocked'):
            raise RuntimeError('network acceptance not recorded; keep B disabled')
        context = mtls(r.A_ROOT / 'config/certs/platform')
        for tenant in [a, b]:
            ready = http(tenant['controlUrl'] + '/internal/directory/ready', {}, context=context)
            if ready != {'status': 'ready', 'tenantId': tenant['id'], 'services': tenant['services']}:
                raise RuntimeError('authenticated deployment evidence differs')
        if not b['enabled']:
            b.update(enabled=True, reason='企业 B 独立部署及私网身份、地址和可用性已校对，开放新登录', confirmed=True)
            b = http_patch(base + '/tenants/enterprise-b', b, token)
    r.write(r.A_ROOT / 'ops/enterprise-b-release/directory-current.json', http(base + '/tenants', token=token))
    print('B directory ' + ('enabled' if enable else 'registered disabled') + '; A remains sole default; no user assignment changed')


def http_patch(url, body, token):
    request = urllib.request.Request(url, data=json.dumps(body).encode(), method='PATCH', headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
    with urllib.request.urlopen(request, timeout=20) as response:
        return json.load(response)


def backup():
    root = r.B_ROOT
    lock = (root / 'ops/backup.lock').open('w')
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    target = root / 'backups' / time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())
    target.mkdir()
    c = r.escaped(r.read(root / 'compose.json'), True)
    started = []
    try:
        for service in ['api', 'im']:
            name = 'frogim-enterprise-b-' + service + '-1'
            if r.inspect(name)['State']['Running']:
                r.compose(root, 'stop', service)
                started.append(service)
        (target / 'enterprise.dump').write_bytes(r.run(['docker', 'exec', 'frogim-enterprise-b-postgres-1', 'pg_dump', '-U', 'enterprise', '-d', 'enterprise_b', '-Fc']))
        r.run(['docker', 'exec', '-e', 'REDISCLI_AUTH=' + c['services']['redis']['environment']['REDIS_PASSWORD'], 'frogim-enterprise-b-redis-1', 'redis-cli', 'SAVE'])
        for service in ['redis', 'minio']:
            if r.inspect('frogim-enterprise-b-' + service + '-1')['State']['Running']:
                r.compose(root, 'stop', service)
                started.append(service)
        with tarfile.open(target / 'data-config.tar.gz', 'w:gz') as archive:
            for path in ['data/redis', 'data/im', 'data/media', 'data/plugins', 'config', 'compose.json', 'certificates', 'bundle/images.json']:
                archive.add(root / path, arcname=path)
        r.write(target / 'complete.json', {'at': r.now(), 'files': {p.name: r.sha(p) for p in target.iterdir() if p.is_file()}, 'scope': 'enterprise-b-only'})
    finally:
        if started:
            r.compose(root, 'start', *started)
            for service in started:
                r.wait_health('frogim-enterprise-b-' + service + '-1')
    completed = sorted(p for p in (root / 'backups').iterdir() if p.name.endswith('Z') and (p / 'complete.json').exists())
    for old in completed[:-7]:
        if old.parent != root / 'backups' or r.read(old / 'complete.json').get('scope') != 'enterprise-b-only':
            raise RuntimeError('backup retention scope changed')
        shutil.rmtree(old)
    print('B consistent backup completed:', target.name)


def restore_check():
    root = r.B_ROOT
    target = sorted(p for p in (root / 'backups').iterdir() if (p / 'complete.json').exists())[-1]
    evidence = r.read(target / 'complete.json')
    for name, digest in evidence['files'].items():
        if r.sha(target / name) != digest:
            raise RuntimeError('backup digest mismatch')
    isolated = root / 'ops/restore-check' / target.name
    isolated.mkdir(parents=True)
    with tarfile.open(target / 'data-config.tar.gz') as archive:
        archive.extractall(isolated, filter='data')
    name = 'frogim-enterprise-b-restore-check'
    image = r.read(root / 'bundle/images.json')['services']['postgres']
    r.run(['docker', 'run', '-d', '--name', name, '--network', 'none', '-e', 'POSTGRES_USER=enterprise', '-e', 'POSTGRES_PASSWORD=' + r.secrets.token_urlsafe(48), '-e', 'POSTGRES_DB=restore_check', image])
    try:
        for _ in range(30):
            if subprocess.run(['docker', 'exec', name, 'pg_isready', '-U', 'enterprise', '-d', 'restore_check'], capture_output=True).returncode == 0:
                break
            time.sleep(1)
        r.run(['docker', 'exec', '-i', name, 'pg_restore', '--exit-on-error', '--no-acl', '-U', 'enterprise', '-d', 'restore_check'], (target / 'enterprise.dump').read_bytes())
        count = r.run(['docker', 'exec', name, 'psql', '-U', 'enterprise', '-d', 'restore_check', '-Atc', "SELECT (SELECT count(*) FROM lp_config WHERE tenant_id='enterprise-b')||'|'||(SELECT count(*) FROM im_users)||'|'||(SELECT count(*) FROM lp_identity)"]).decode().strip()
        r.write(root / 'ops/restore-check.json', {'at': r.now(), 'backup': target.name, 'checksums': True, 'isolatedPostgresRestored': True, 'configTenantUsersIdentities': count, 'archiveExtracted': True, 'productionDataOverwritten': False})
    finally:
        r.run(['docker', 'rm', '-fv', name])
    print('B backup restored in isolated database and directory; current data untouched')


def timers():
    script = r.B_ROOT / 'enterprise-b-ops.py'
    for name, command, schedule in [('qingwa-cert-renew', 'renew', '*-*-* 00,12:00:00'), ('qingwa-backup', 'backup', '*-*-* 04:30:00')]:
        folder = pathlib.Path('/etc/systemd/system') / (name + '.service.d')
        folder.mkdir(exist_ok=True)
        (folder / 'enterprise-b.conf').write_text('[Service]\nExecStart=\nExecStart=/usr/bin/python3 ' + str(script) + ' ' + command + '\n')
        folder = pathlib.Path('/etc/systemd/system') / (name + '.timer.d')
        folder.mkdir(exist_ok=True)
        (folder / 'enterprise-b.conf').write_text('[Timer]\nOnCalendar=\nOnCalendar=' + schedule + '\nRandomizedDelaySec=300\nPersistent=true\n')
    r.run(['systemctl', 'daemon-reload'])
    r.run(['systemctl', 'enable', '--now', 'qingwa-cert-renew.timer', 'qingwa-backup.timer'])
    print('B certificate checks every 12 hours; daily backups retain seven complete copies')


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('phase', choices=['sign-b', 'issue', 'renew', 'init-media', 'register', 'enable', 'backup', 'restore-check', 'timers'])
    args = parser.parse_args()
    r.os.umask(0o077)
    {'sign-b': sign_b, 'issue': lambda: issue_or_renew(True), 'renew': issue_or_renew, 'init-media': init_media, 'register': directory, 'enable': lambda: directory(True), 'backup': backup, 'restore-check': restore_check, 'timers': timers}[args.phase]()
