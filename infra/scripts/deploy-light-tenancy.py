"""Interactive Linux source deployment; no production data import or cleanup.

All durable inputs are root-private. Stages resume with identical source/secrets.
Business traffic remains direct to each enterprise. No additional resident service.
"""
import argparse
import base64
import contextlib
import datetime
import getpass
import hashlib
import ipaddress
import json
import os
import pathlib
import platform
import re
import secrets
import shlex
import shutil
import socket
import ssl
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.parse
import urllib.request
import urllib.error

HERE = pathlib.Path(__file__).resolve().parent
PG = 'postgres:17-alpine'
REDIS = 'redis:8-alpine'
MINIO = 'minio/minio:RELEASE.2025-07-23T15-54-02Z'
MC = 'minio/mc:RELEASE.2025-07-21T05-28-08Z'
RTC = 'docker.io/livekit/livekit-server@sha256:d0d1cfdbe95617647bbe91630454526c2cdd88cec83f41114b3495b444918b9a'
CERTBOT = 'certbot/certbot:v5.8.0'
IDENT = re.compile(r'^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$')


def stamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def digest(path):
    h = hashlib.sha256()
    with pathlib.Path(path).open('rb') as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def write(path, value):
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + '.tmp')
    tmp.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding='utf8')
    tmp.chmod(0o600)
    tmp.replace(path)


def read(path):
    return json.loads(pathlib.Path(path).read_text(encoding='utf8'))


def origin(value):
    u = urllib.parse.urlsplit(value.rstrip('/'))
    if u.scheme != 'https' or not u.hostname or u.username or u.password or u.path or u.query or u.fragment:
        raise ValueError('地址须为不含路径、凭据、参数的 HTTPS 地址')
    if not re.fullmatch(r'[A-Za-z0-9.-]+', u.hostname) or u.port not in (None, 443):
        raise ValueError('公网地址仅支持 IPv4/域名及 443 端口')
    return value.rstrip('/')


def validate(c):
    if c['mode'] not in ('platform-enterprise', 'enterprise'):
        raise ValueError('未知部署模式')
    if c['tls_mode'] not in ('files', 'acme'):
        raise ValueError('证书模式只能为 acme 或 files')
    if any(x in c['root'] for x in '\r\n\t%'):
        raise ValueError('部署目录不支持控制字符或 systemd 转义符')
    for name in ('project', 'tenant_id'):
        if not IDENT.fullmatch(c[name]):
            raise ValueError(name + ' 格式无效')
    if not re.fullmatch(r'[a-z0-9][a-z0-9_-]{2,40}', c['project']):
        raise ValueError('Compose 项目名须为小写字母、数字、短横线或下划线')
    if not re.fullmatch(r'[A-Za-z0-9._-]{1,64}', c['tenant_code']):
        raise ValueError('企业码格式无效')
    if not 1 <= len(c['tenant_name']) <= 120:
        raise ValueError('企业名称长度为 1–120')
    raw_root = pathlib.Path(c['root'])
    root = raw_root.resolve()
    if not raw_root.is_absolute() or root == pathlib.Path('/') or len(root.parts) < 3 or raw_root.is_symlink() or '..' in raw_root.parts:
        raise ValueError('部署目录须为独立的绝对路径，例如 /data/frogim/customer-a')
    c['public_origin'] = origin(c['public_origin'])
    c['rtc_public_ip'] = str(ipaddress.IPv4Address(c.get('rtc_public_ip') or urllib.parse.urlsplit(c['public_origin']).hostname))
    c['platform_origin'] = origin(c.get('platform_origin') or c['public_origin'])
    c['private_ip'] = str(ipaddress.IPv4Address(c['private_ip']))
    if not ipaddress.ip_address(c['private_ip']).is_private:
        raise ValueError('控制监听必须使用私网 IPv4')
    u = urllib.parse.urlsplit(c['repository'])
    if u.username and u.scheme in ('http', 'https') or u.password:
        raise ValueError('源码地址不能含凭据；使用 Git 凭据管理或 SSH')
    if c['ref'].startswith('-') or not c['ref'] or any(x in c['ref'] for x in '\r\n'):
        raise ValueError('源码 ref 无效')
    if c['repository'].startswith('-'):
        raise ValueError('源码 URL 无效')
    if c['push_provider'] not in ('getui', 'webhook'):
        raise ValueError('生产推送须选择 getui 或 webhook')
    if c['push_provider'] == 'getui' and any(not c.get(k) for k in ('getui_app_id', 'getui_app_key', 'getui_master_secret')):
        raise ValueError('个推配置不完整')
    if c['push_provider'] == 'getui' and len(c['getui_master_secret']) < 16:
        raise ValueError('个推 MasterSecret 至少16位')
    if c['push_provider'] == 'webhook' and (not c.get('push_webhook_url', '').startswith('https://') or len(c.get('push_webhook_token', '')) < 24):
        raise ValueError('推送 webhook 需要 HTTPS 地址和至少 24 位 token')
    c['control_sources'] = [str(ipaddress.IPv4Network(x, strict=False)) for x in c['control_sources']]
    if any(ipaddress.ip_network(x).prefixlen == 0 for x in c['control_sources']):
        raise ValueError('私网控制不能允许所有来源')
    if c['mode'] == 'enterprise':
        ip = str(ipaddress.IPv4Address(c['platform_private_ip']))
        if not ipaddress.ip_address(ip).is_private:
            raise ValueError('平台控制地址须为私网地址')
        c['platform_private_ip'] = ip
        if not re.fullmatch(r'[A-Za-z0-9._-]+', c['platform_ssh_user']):
            raise ValueError('SSH 用户名无效')
        if not re.fullmatch(r'[A-Za-z0-9.-]+', c['platform_ssh_host']):
            raise ValueError('SSH 主机须为 IPv4 或域名')
        if not 1 <= int(c.get('platform_ssh_port', 22)) <= 65535:
            raise ValueError('SSH 端口无效')
        if not pathlib.PurePosixPath(c['platform_ca_dir']).is_absolute():
            raise ValueError('平台 CA 目录须为绝对路径')
    else:
        if c['otp_mode'] not in ('fixed', 'sms'):
            raise ValueError('验证码模式只能为 fixed 或 sms')
        if c['otp_mode'] == 'fixed' and not re.fullmatch(r'\d{6}', c['fixed_otp']):
            raise ValueError('固定验证码须为六位数字')
        if c['otp_mode'] == 'sms' and (not c['otp_webhook_url'].startswith('https://') or len(c['otp_webhook_token']) < 24):
            raise ValueError('短信配置无效')
    return c


def ask(label, default='', secret=False):
    text = label + (f' [{default}]' if default and not secret else '') + ': '
    while True:
        value = (getpass.getpass(text) if secret else input(text)).strip() or default
        if value:
            return value


def password(label):
    while True:
        value = ask(label + '（6–32位）', secret=True)
        if 6 <= len(value.encode()) <= 32 and value == getpass.getpass('再次输入: '):
            return value
        print('长度不符合规则或两次不一致，请重试。')


def collect(root=None):
    print('1. 平台＋企业（统一 Web）\n2. 仅企业（接入已有平台，不提供 Web 聊天）')
    mode = ask('选择模式', '1')
    while mode not in ('1','2'): mode=ask('请输入 1 或 2')
    c = {'mode': 'platform-enterprise' if mode == '1' else 'enterprise'}
    c['project'] = ask('Compose 项目名', 'frogim-main' if mode == '1' else 'frogim-enterprise-b')
    c['root'] = root or ask('独立部署目录', '/data/frogim/' + c['project'])
    c['repository'] = ask('源码仓库（可用 SSH；请勿在 URL 内填密码）', 'https://gitee.com/fanxinet_fanxinet/newimceshi.git')
    c['ref'] = ask('源码分支/tag/提交', 'main')
    c['public_origin'] = ask('本机公网 HTTPS 地址，例如 https://43.198.32.187')
    try: rtc_ip=socket.gethostbyname(urllib.parse.urlsplit(c['public_origin']).hostname)
    except OSError: rtc_ip=''
    c['rtc_public_ip'] = ask('RTC 公网 IPv4（服务器地址，不填反向代理/CDN 地址）',rtc_ip)
    c['private_ip'] = ask('本机私网 IPv4')
    c['tenant_id'] = ask('企业 ID', 'enterprise-a' if mode == '1' else 'enterprise-b')
    c['tenant_name'] = ask('企业名称', '客户A企业' if mode == '1' else '客户B企业')
    c['tenant_code'] = ask('企业码', 'A' if mode == '1' else 'B')
    c['admin_user'] = ask('企业后台用户名', 'admin')
    c['_admin_password'] = password('企业后台密码')
    c['tls_mode'] = ask('公网证书：acme 自动申请 / files 已有证书', 'acme')
    if c['tls_mode'] == 'files':
        c['tls_cert'] = ask('已有完整证书链绝对路径')
        c['tls_key'] = ask('已有证书私钥绝对路径')
    else:
        c['tls_email'] = ask('ACME 联系邮箱')
    c['push_provider'] = ask('企业推送供应商：getui / webhook', 'getui')
    if c['push_provider'] == 'getui':
        for k, label in [('getui_app_id', '个推 App ID'), ('getui_app_key', '个推 App Key'), ('getui_master_secret', '个推 MasterSecret')]:
            c[k] = ask(label, secret=True)
    else:
        c['push_webhook_url'] = ask('推送 HTTPS webhook')
        c['push_webhook_token'] = ask('推送 token（至少24位）', secret=True)
    if mode == '1':
        c['platform_origin'] = c['public_origin']
        c['platform_admin_user'] = ask('平台后台用户名', 'admin')
        c['_platform_password'] = password('平台后台密码')
        c['otp_mode'] = ask('验证码：fixed 固定码 / sms 短信', 'fixed')
        if c['otp_mode'] == 'fixed':
            c['fixed_otp'] = ask('固定验证码（不启用开发模式）', '123456')
        else:
            c['otp_webhook_url'] = ask('短信 HTTPS webhook')
            c['otp_webhook_token'] = ask('短信 token（至少24位）', secret=True)
    else:
        c['platform_origin'] = ask('已有平台公网 HTTPS 地址（不含 /platform）')
        c['platform_private_ip'] = ask('已有平台私网 IPv4')
        c['platform_ssh_host'] = ask('平台 SSH 主机（可填私网 IP）', c['platform_private_ip'])
        c['platform_ssh_port'] = int(ask('平台 SSH 端口', '22'))
        c['platform_ssh_user'] = ask('平台 SSH 用户', 'root')
        c['platform_ssh_key'] = ask('本机用于连接平台的 SSH 私钥绝对路径', '/root/.ssh/id_ed25519')
        c['platform_ssh_fingerprint'] = ask('平台 SSH Ed25519 主机指纹（SHA256:...）')
        c['platform_ca_dir'] = ask('平台控制 CA 所在目录', '/data/frogim/frogim-main/config/ca')
        c['platform_admin_user'] = ask('已有平台管理员用户名', 'admin')
        c['_platform_password'] = ask('已有平台管理员密码（只在内存使用）', secret=True)
    peer = c.get('platform_private_ip', c['private_ip']) + '/32'
    c['control_sources'] = [x.strip() for x in ask('允许控制访问的私网 IP/CIDR，逗号分隔（包含平台及其他企业）', peer).split(',')]
    return validate(c)


def escape(value):
    if isinstance(value, str):
        return value.replace('$', '$$')
    if isinstance(value, dict):
        return {k: escape(v) for k, v in value.items()}
    if isinstance(value, list):
        return [escape(v) for v in value]
    return value


def gateway(c):
    host = urllib.parse.urlsplit(c['public_origin']).hostname
    forward = '\n      header_up -X-Frogim-*\n      header_up X-Forwarded-For {remote_host}\n      header_up X-Forwarded-Proto https\n'
    special = '''    handle /platform { redir /platform/ 302 }
    handle /platform/* { reverse_proxy platform:8080 }
    handle /platform-ready { rewrite * /ready
      reverse_proxy platform:8080 }
    handle /v2/config/version { reverse_proxy platform:8080 }
    handle / { header Cache-Control no-store
      redir /app/ 302 }
    @web_alias path /app /web /web/*
    handle @web_alias { redir /app/ 302 }
    handle_path /app/* {
      root * /srv/web
      @immutable path /r/*
      header @immutable Cache-Control "public, max-age=31536000, immutable"
      @mutable path /index.html /flutter_bootstrap.js /version.json /flutter_service_worker.js /linli_push_worker.js
      header @mutable Cache-Control no-store
      try_files {path} /index.html
      file_server
    }
''' if c['mode'] == 'platform-enterprise' else f'''    @chat path / /app /app/* /web /web/* /platform /platform/*
    handle @chat {{ header Cache-Control no-store
      respond "not found" 404 }}
    handle /v2/config/version {{
      reverse_proxy {c['platform_origin']} {{
        header_up Host {urllib.parse.urlsplit(c['platform_origin']).hostname}
        header_up -Authorization
        header_up -Cookie
      }}
    }}
'''
    text = '''{ admin 127.0.0.1:2019 }
:443 {
  tls /tls/fullchain.pem /tls/privkey.pem
  encode zstd gzip
  header {
    Strict-Transport-Security "max-age=31536000"
    X-Content-Type-Options nosniff
    Referrer-Policy no-referrer
    X-Frame-Options DENY
  }
  route {
''' + special + '''    @private path /internal /internal/* /metrics /rtc /rtc/* /v1 /v1/*
    handle @private { respond "not found" 404 }
    @api path /v2/* /api/* /ready /health /livekit /livekit/*
    handle @api {
      reverse_proxy api:8080 {''' + forward + '''      }
    }
    handle /im { rewrite * /
      reverse_proxy im:5200 }
    handle /admin { redir /admin/ 302 }
    handle_path /admin/* {
      root * /srv/admin
      header Cache-Control no-store
      try_files {path} /index.html
      file_server
    }
    handle /nexachat-media/* { reverse_proxy minio:9000 }
    handle_path /downloads/* { root * /srv/downloads
      header Content-Disposition attachment
      file_server }
    handle_path /legal/* { root * /srv/legal
      try_files {path} {path}.html
      file_server }
    handle { respond "not found" 404 }
  }
  log {
    output stdout
    format filter {
      request>headers>Authorization delete
      request>headers>Cookie delete
      request>uri query {
        replace token REDACTED
        replace ticket REDACTED
      }
      wrap json
    }
  }
}
:80 {
  handle /.well-known/acme-challenge/* { root * /acme
    file_server }
''' + ('''  @chat path / /app /app/* /web /web/*
  handle @chat { respond "not found" 404 }
''' if c['mode'] == 'enterprise' else '') + f'  handle {{ redir https://{host}{{uri}} 302 }}\n}}\n'
    # Caddy requires block delimiters on separate lines; placeholders stay intact.
    text = re.sub(r'\{ (?=[A-Za-z])', '{\n    ', text)
    return re.sub(r'(?<=\S) \}', '\n}', text)


def services(c, s, images):
    root = c['root']; full = c['mode'] == 'platform-enterprise'
    def mount(p, dest, ro=False):
        return root + '/' + p + ':' + dest + (':ro' if ro else '')
    defaults = {'restart': 'unless-stopped', 'logging': {'driver': 'json-file', 'options': {'max-size': '10m', 'max-file': '3'}}}
    def svc(image, **kw):
        return dict(defaults, image=image, **kw)
    health = lambda url: {'test': ['CMD', 'wget', '-q', '-O', '/dev/null', url], 'interval': '5s', 'timeout': '5s', 'retries': 30, 'start_period': '20s'}
    cert = lambda who: [mount('config/certs/' + who, '/certs', True)]
    origins = ','.join(dict.fromkeys([c['platform_origin'], c['public_origin']]))
    ae = dict(IM_MODE='enterprise', IM_ENV='production', IM_DEV_MODE='false', IM_SEED_DEMO='false', IM_ADDR=':8080', IM_TENANT_ID=c['tenant_id'], IM_PLATFORM_URL=('https://platform:8443' if full else 'https://'+c['platform_private_ip']+':8443'), IM_ENTERPRISE_API_URL=c['public_origin'], IM_ENTERPRISE_MEDIA_URL=c['public_origin'], IM_CONTROL_ADDR=':8443', IM_CONTROL_CA='/certs/ca.pem', IM_CONTROL_CERT='/certs/cert.pem', IM_CONTROL_KEY='/certs/key.pem', IM_DATABASE_URL='postgres://enterprise:'+s['pg']+'@postgres:5432/enterprise', IM_REDIS_URL='redis://:'+s['redis']+'@redis:6379/0', IM_JWT_SECRET=s['jwt'], IM_ADMIN_USERNAME=c['admin_user'], IM_ADMIN_ID=c['tenant_id']+'-admin', IM_ADMIN_PASSWORD_HASH=s['admin_hash'], IM_ALLOWED_ORIGINS=origins, IM_TRUST_PROXY='true', IM_LOG_LEVEL='warn', IM_HTTP_LOG_SUCCESS_SAMPLE_RATE='0', IM_S3_ENDPOINT='minio:9000', IM_S3_PUBLIC_ENDPOINT=urllib.parse.urlsplit(c['public_origin']).hostname, IM_S3_PUBLIC_SECURE='true', IM_S3_BUCKET='nexachat-media', IM_S3_ACCESS_KEY='enterprise-media', IM_S3_SECRET_KEY=s['s3'], IM_WUKONG_ENABLED='true', IM_WUKONG_API_URL='http://im:5001', IM_WUKONG_MANAGER_URL='http://im:5300', IM_WUKONG_MANAGER_TOKEN=s['manager'], IM_WUKONG_TOKEN_SECRET=s['im'], IM_WUKONG_POLICY_SECRET=s['policy'], IM_WUKONG_GRPC_ADDR=':6970', IM_WUKONG_TCP_URL='tcp://'+urllib.parse.urlsplit(c['public_origin']).hostname+':5100', IM_WUKONG_WS_URL=c['public_origin'].replace('https:', 'wss:')+'/im', IM_WUKONG_PLUGIN_DIR='/plugins', IM_WUKONG_PLUGIN_TRUSTED_KEYS='deployment:'+s['plugin_public'], IM_WUKONG_PLUGIN_ALLOWLIST='wk.plugin.im-policy', IM_LIVEKIT_ENABLED='true', IM_LIVEKIT_URL=c['public_origin'].replace('https:', 'wss:')+'/livekit', IM_LIVEKIT_API_URL='http://livekit:7880', IM_LIVEKIT_API_KEY='enterprise', IM_LIVEKIT_API_SECRET=s['rtc'], IM_PUSH_PROVIDER=c['push_provider'], IM_WEB_PUSH_PUBLIC_KEY=s['vapid_public'], IM_WEB_PUSH_PRIVATE_KEY=s['vapid_private'], IM_WEB_PUSH_SUBJECT='mailto:'+c.get('tls_email', 'admin@example.invalid'))
    for key in ('getui_app_id', 'getui_app_key', 'getui_master_secret', 'push_webhook_url', 'push_webhook_token'):
        if key in c:
            ae['IM_'+key.upper()] = c[key]
    sv = {
        'postgres': svc(images.get('postgres',PG), environment={'POSTGRES_USER':'enterprise','POSTGRES_PASSWORD':s['pg'],'POSTGRES_DB':'enterprise'}, volumes=[mount('data/postgres','/var/lib/postgresql/data'), mount('config/init.sql','/docker-entrypoint-initdb.d/platform.sql',True)], networks=['data'], healthcheck={'test':['CMD','pg_isready','-U','enterprise'],'interval':'5s','timeout':'3s','retries':30}),
        'redis': svc(images.get('redis',REDIS), environment={'REDIS_PASSWORD':s['redis']}, command=['sh','-c','exec redis-server --appendonly yes --requirepass "$REDIS_PASSWORD"'], volumes=[mount('data/redis','/data')], networks=['data'], healthcheck={'test':['CMD-SHELL','REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping'],'interval':'5s','timeout':'3s','retries':30}),
        'minio': svc(images.get('minio',MINIO), environment={'MINIO_ROOT_USER':'rootmedia','MINIO_ROOT_PASSWORD':s['minio']}, command=['server','/data','--console-address',':9001'], volumes=[mount('data/media','/data')], networks=['business'], healthcheck={'test':['CMD','curl','-f','http://127.0.0.1:9000/minio/health/ready'],'interval':'5s','timeout':'3s','retries':30}),
        'api': svc(images['app'], user='0:0', environment=ae, volumes=cert('api')+[mount('data/plugins','/plugins')], ports=[c['private_ip']+(':'+str(8444 if full else 8443))+':8443'], networks=['business','data'], depends_on={'postgres':{'condition':'service_healthy'},'redis':{'condition':'service_healthy'}}, healthcheck=health('http://127.0.0.1:8080/ready')),
        'im': svc(images['im'], user='0:0', environment={'WK_MODE':'release','WK_MANAGERTOKEN':s['manager'],'WK_TOKENAUTHON':'true','WK_EXTERNAL_IP':urllib.parse.urlsplit(c['public_origin']).hostname,'WK_EXTERNAL_WSADDR':ae['IM_WUKONG_WS_URL'],'WK_EXTERNAL_TCPADDR':urllib.parse.urlsplit(c['public_origin']).hostname+':5100','WK_WEBHOOK_GRPCADDR':'api:6970','WK_DATASOURCE_ADDR':'http://api:8080/internal/wukong/datasource','IM_WUKONG_POLICY_URL':'http://api:8080/internal/wukong/policy/send','IM_WUKONG_POLICY_SECRET':s['policy']}, volumes=[mount('config/wk.yaml','/root/wukongim/wk.yaml',True),mount('data/im','/root/wukongim/data'),mount('data/logs','/root/wukongim/logs'),mount('data/plugins','/root/wukongim/data/plugins')], ports=['5100:5100'], networks=['business'], healthcheck={'test':['CMD-SHELL','wget -q --header="token: $WK_MANAGERTOKEN" -O /dev/null http://127.0.0.1:5001/health'],'interval':'5s','timeout':'5s','retries':30}),
        'livekit': svc(images.get('livekit',RTC), user='0:0', command=['--config','/etc/livekit.yaml','--node-ip',c['rtc_public_ip']], environment={'LIVEKIT_KEYS':'enterprise: '+s['rtc']}, volumes=[mount('config/livekit.yaml','/etc/livekit.yaml',True)], ports=['7881:7881','7882-7889:7882-7889/udp'], networks=['business'], healthcheck=health('http://127.0.0.1:7880/')),
        'gateway': svc(images['gateway'], command=['caddy','run','--config','/config/Caddyfile','--adapter','caddyfile'], volumes=[mount('config/gateway','/config',True),mount('config/public-tls','/tls',True),mount('acme','/acme',True),mount('downloads','/srv/downloads',True),mount('legal','/srv/legal',True)], ports=['80:80','443:443'], networks=['business'], healthcheck={'test':['CMD','caddy','validate','--config','/config/Caddyfile','--adapter','caddyfile'],'interval':'10s','timeout':'5s','retries':10}),
    }
    if full:
        pe = dict(IM_MODE='platform', IM_ENV='production', IM_DEV_MODE='false', IM_ADDR=':8080', IM_DATABASE_URL='postgres://enterprise:'+s['pg']+'@postgres:5432/platform_light', IM_JWT_SECRET=s['platform_jwt'], IM_ADMIN_USERNAME=c['platform_admin_user'], IM_ADMIN_PASSWORD_HASH=s['platform_hash'], IM_ALLOWED_ORIGINS=origins, IM_PLATFORM_STATIC_DIR='/srv/platform', IM_CONTROL_ADDR=':8443', IM_CONTROL_CA='/certs/ca.pem', IM_CONTROL_CERT='/certs/cert.pem', IM_CONTROL_KEY='/certs/key.pem')
        if c['otp_mode']=='fixed': pe['IM_PLATFORM_FIXED_OTP_CODE']=c['fixed_otp']
        else: pe.update(IM_OTP_WEBHOOK_URL=c['otp_webhook_url'],IM_OTP_WEBHOOK_TOKEN=c['otp_webhook_token'])
        sv['platform']=svc(images['app'],user='0:0',environment=pe,volumes=cert('platform'),ports=[c['private_ip']+':8443:8443'],networks=['business','data'],depends_on={'postgres':{'condition':'service_healthy'}},healthcheck=health('http://127.0.0.1:8080/ready'))
    return {'name':c['project'],'services':sv,'networks':{'business':{},'data':{'internal':True}}}


class Deployment:
    def __init__(self, c):
        self.c = validate(c); self.root = pathlib.Path(c['root'])
        self.root.mkdir(parents=True, exist_ok=True); self.root.chmod(0o700)
        for d in ('ops','config','data','acme','downloads','legal','certificates','backups'):
            (self.root/d).mkdir(exist_ok=True)
        self.state = read(self.root/'ops/state.json') if (self.root/'ops/state.json').exists() else {'done':[]}
        self.source = self.root/'source'
        self.platform_password = c.get('_platform_password')
        safe = {k:v for k,v in c.items() if not k.startswith('_')}
        config_path = self.root/'config/deployment.json'
        if config_path.exists() and read(config_path) != safe:
            raise ValueError('续跑配置与原部署不同；拒绝改变已初始化环境')
        if not config_path.exists(): write(config_path, safe)
        write(self.root/'ops/state.json', self.state)

    def run(self, cmd, data=None, visible=False):
        if visible:
            result = subprocess.run(cmd, input=data)
            if result.returncode: raise RuntimeError('构建/命令失败；未继续启用企业')
            return b''
        result = subprocess.run(cmd, input=data, capture_output=True)
        if result.returncode:
            (self.root/'ops/last-error-private.log').write_bytes(result.stderr)
            raise RuntimeError('命令失败（'+cmd[0]+'）；私有诊断位于 ops/last-error-private.log')
        return result.stdout

    def compose(self, *args, data=None):
        return self.run(['docker','compose','-f',str(self.root/'compose.json'),*args],data)

    def step(self, name, fn):
        if name in self.state['done']: return
        print('执行阶段：'+name, flush=True); fn()
        self.state['done'].append(name);self.state['updatedAt']=stamp();write(self.root/'ops/state.json',self.state)

    def checkout(self):
        if not self.source.exists():
            self.run(['git','init',str(self.source)])
            self.run(['git','-C',str(self.source),'remote','add','origin',self.c['repository']])
        if self.run(['git','-C',str(self.source),'status','--porcelain']).strip():
            raise RuntimeError('源码目录存在修改，拒绝覆盖')
        if self.run(['git','-C',str(self.source),'remote','get-url','origin']).decode().strip()!=self.c['repository']:
            raise RuntimeError('已有源码仓库地址改变，拒绝续跑')
        if not self.state.get('commit'):
            self.run(['git','-C',str(self.source),'fetch','--depth','1','origin',self.c['ref']],visible=True)
            self.state['commit']=self.run(['git','-C',str(self.source),'rev-parse','FETCH_HEAD']).decode().strip()
            write(self.root/'ops/state.json',self.state)
        self.run(['git','-C',str(self.source),'checkout','--detach',self.state['commit']])
        if not (self.source/'infra/source-deploy/Dockerfile.api').exists():
            raise RuntimeError('指定源码版本不含新部署模板；选择包含本脚本的 main/tag')

    def build(self):
        tag=self.c['project']+':'+self.state['commit'][:12]
        images={'app':tag+'-api','im':tag+'-im','gateway':tag+'-gateway'}
        def build(file,image,args=()):
            proxy=[]
            for key in ('HTTP_PROXY','HTTPS_PROXY','NO_PROXY'):
                if os.getenv(key): proxy+=['--build-arg',key+'='+os.environ[key]]
            self.run(['docker','build','--pull','-f',str(self.source/file),'-t',image,*proxy,*args,str(self.source)],visible=True)
        build('infra/source-deploy/Dockerfile.api',images['app'])
        build('infra/wukongim/server-patch/Dockerfile',images['im'])
        if self.c['mode']=='platform-enterprise':
            web=tag+'-web'
            args=[]
            for k,v in {'WEB_API_BASE_URL':self.c['public_origin'],'PLATFORM_BASE_URL':self.c['platform_origin']+'/platform','WEB_BASE_HREF':'/app/','TERMS_URL':self.c['platform_origin']+'/legal/terms','PRIVACY_URL':self.c['platform_origin']+'/legal/privacy'}.items(): args+=['--build-arg',k+'='+v]
            build('apps/mobile/Dockerfile.web',web,args)
            web_args=['--build-arg','WEB_IMAGE='+web]
        else: web_args=['--build-arg','WEB_IMAGE='+images['app'],'--build-arg','WEB_SOURCE=/srv/platform']
        build('infra/source-deploy/Dockerfile.gateway',images['gateway'],['--build-arg','APP_IMAGE='+images['app'],*web_args])
        for key,image in {'postgres':PG,'redis':REDIS,'minio':MINIO,'mc':MC,'livekit':RTC,'certbot':CERTBOT}.items():
            self.run(['docker','pull',image],visible=True);images[key]=image
        self.images={k:json.loads(self.run(['docker','image','inspect',v]))[0]['Id'] for k,v in images.items()}
        write(self.root/'ops/images.json',self.images)

    def configure(self):
        c=self.c;self.images=read(self.root/'ops/images.json')
        private=self.root/'config/secrets.json'
        if private.exists(): s=read(private)
        else:
            s={k:secrets.token_urlsafe(48) for k in ('pg','redis','minio','s3','jwt','manager','im','policy','rtc','platform_jwt')}
            for field, target in [('_admin_password','admin_hash'),('_platform_password','platform_hash')]:
                if field == '_platform_password' and c['mode'] != 'platform-enterprise': continue
                value = c.pop(field, None) or password('平台初始化密码' if field == '_platform_password' else '企业初始化密码')
                s[target]=self.run(['docker','run','--rm','-i','--entrypoint','/opt/frogim/setup',self.images['app'],'-hash-password-stdin'],value.encode()).decode().strip()
                value=None
            key=self.root/'config/plugin-signing.key'
            self.run(['openssl','genpkey','-algorithm','ED25519','-out',str(key)])
            s['plugin_public']=base64.b64encode(self.run(['openssl','pkey','-in',str(key),'-pubout','-outform','DER'])[-32:]).decode()
            ec=self.run(['openssl','ecparam','-name','prime256v1','-genkey'])
            text=self.run(['openssl','ec','-text','-noout'],ec).decode()
            raw=lambda a,b:bytes.fromhex(re.sub(r'[^0-9a-f]','',text.split(a)[1].split(b)[0]))
            s['vapid_private']=base64.urlsafe_b64encode(raw('priv:','pub:')).decode().rstrip('=')
            s['vapid_public']=base64.urlsafe_b64encode(raw('pub:','ASN1 OID:')).decode().rstrip('=')
            write(private,s)
        if c['mode']=='platform-enterprise' and 'platform_hash' not in s:
            raise RuntimeError('平台初始化密码缺失')
        for d in ('certs/api','certs/platform','gateway','public-tls'): (self.root/'config'/d).mkdir(parents=True,exist_ok=True)
        for d in ('postgres','redis','media','im','logs','plugins'): (self.root/'data'/d).mkdir(exist_ok=True)
        (self.root/'config/init.sql').write_text('CREATE DATABASE platform_light;\n' if c['mode']=='platform-enterprise' else '-- enterprise only\n')
        (self.root/'config/init.sql').chmod(0o644)
        (self.root/'config/wk.yaml').write_text((self.source/'infra/wukongim/wk.yaml').read_text().replace('server:','api:'))
        (self.root/'config/livekit.yaml').write_text((self.source/'infra/livekit/livekit.yaml').read_text().replace('use_external_ip: true','use_external_ip: false'))
        (self.root/'config/gateway/Caddyfile').write_text(gateway(c))
        shutil.copytree(self.source/'infra/legal',self.root/'legal',dirs_exist_ok=True)
        plugin=self.root/'data/plugins/wk.plugin.im-policy-linux-amd64.wkp'
        data=self.run(['docker','run','--rm','--entrypoint','cat',self.images['app'],'/opt/frogim/policy.wkp'])
        if plugin.exists():
            if digest(plugin)!=hashlib.sha256(data).hexdigest(): raise RuntimeError('已存在的插件与固定源码构建不同，拒绝覆盖')
        else:
            tmp=plugin.with_suffix('.tmp');tmp.write_bytes(data);tmp.chmod(0o550);tmp.replace(plugin)
        manifest={'schemaVersion':1,'pluginNo':'wk.plugin.im-policy','name':plugin.name,'fileName':plugin.name,'version':'1.0.0','methods':['Send'],'os':'linux','arch':'amd64','sha256':hashlib.sha256(plugin.read_bytes()).hexdigest(),'size':plugin.stat().st_size,'keyId':'deployment'}
        write(self.root/'ops/plugin-manifest.json',manifest)
        sig=self.run(['openssl','pkeyutl','-sign','-rawin','-inkey',str(self.root/'config/plugin-signing.key'),'-in',str(self.root/'ops/plugin-manifest.json')])
        (self.root/'ops/plugin-signature.txt').write_text(base64.b64encode(sig).decode())
        write(self.root/'compose.json',escape(services(c,s,self.images)));write(self.root/'config/deployment.json',{k:v for k,v in c.items() if not k.startswith('_')})
        self.compose('config','--quiet')

    def ssh(self, command, data=None):
        c=self.c
        return self.run(['ssh','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(self.root/'config/known_hosts'),'-i',c['platform_ssh_key'],'-p',str(c.get('platform_ssh_port',22)),c['platform_ssh_user']+'@'+c['platform_ssh_host'],command],data)

    def control_certificates(self):
        c=self.c;ca=self.root/'config/ca';ca.mkdir(exist_ok=True)
        if c['mode']=='platform-enterprise':
            if not (ca/'ca.key').exists():
                self.run(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-days','3650','-subj','/CN=Frogim control CA','-addext','basicConstraints=critical,CA:TRUE','-addext','keyUsage=critical,keyCertSign,cRLSign','-keyout',str(ca/'ca.key'),'-out',str(ca/'ca.pem')])
            targets=[('api',c['tenant_id']),('platform','platform')]
        else:
            scan=self.run(['ssh-keyscan','-p',str(c.get('platform_ssh_port',22)),'-t','ed25519',c['platform_ssh_host']])
            fingerprint=self.run(['ssh-keygen','-lf','-'],scan).decode().split()[1]
            if fingerprint!=c['platform_ssh_fingerprint']: raise RuntimeError('平台 SSH 主机指纹不匹配')
            (self.root/'config/known_hosts').write_bytes(scan)
            ca.joinpath('ca.pem').write_bytes(self.ssh('cat '+shlex.quote(c['platform_ca_dir']+'/ca.pem')))
            targets=[('api',c['tenant_id'])]
        for name,identity in targets:
            folder=self.root/'config/certs'/name
            if not (folder/'key.pem').exists(): self.run(['openssl','req','-newkey','rsa:3072','-nodes','-subj','/CN='+identity,'-keyout',str(folder/'key.pem'),'-out',str(folder/'request.csr')])
            ext=f'subjectAltName=DNS:{name},IP:{c["private_ip"]}\nextendedKeyUsage=serverAuth,clientAuth\n'
            if c['mode']=='platform-enterprise':
                (folder/'extensions').write_text(ext)
                self.run(['openssl','x509','-req','-days','365','-in',str(folder/'request.csr'),'-CA',str(ca/'ca.pem'),'-CAkey',str(ca/'ca.key'),'-set_serial','0x'+secrets.token_hex(16),'-extfile',str(folder/'extensions'),'-out',str(folder/'cert.pem')])
            else:
                command='set -eu; d=$(mktemp -d); trap \'rm -rf "$d"\' EXIT; cat > "$d/request"; printf %s '+shlex.quote(ext)+' > "$d/ext"; openssl x509 -req -days 365 -in "$d/request" -CA '+shlex.quote(c['platform_ca_dir']+'/ca.pem')+' -CAkey '+shlex.quote(c['platform_ca_dir']+'/ca.key')+' -set_serial 0x'+secrets.token_hex(16)+' -extfile "$d/ext"'
                (folder/'cert.pem').write_bytes(self.ssh(command,(folder/'request.csr').read_bytes()))
            shutil.copy2(ca/'ca.pem',folder/'ca.pem')
            self.run(['openssl','verify','-CAfile',str(ca/'ca.pem'),str(folder/'cert.pem')])
            self.run(['openssl','x509','-in',str(folder/'cert.pem'),'-noout','-checkip',c['private_ip']])

    def public_certificate(self, renew=False):
        c=self.c;folder=self.root/'config/public-tls';host=urllib.parse.urlsplit(c['public_origin']).hostname
        if c['tls_mode']=='files':
            source_cert=pathlib.Path(c['tls_cert']);source_key=pathlib.Path(c['tls_key'])
        else:
            server=None
            try:
                if not renew:
                    server=subprocess.Popen([sys.executable,'-m','http.server','80','--bind','0.0.0.0','--directory',str(self.root/'acme')],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
                    time.sleep(1)
                    if server.poll() is not None: raise RuntimeError('80 端口不可用，无法完成证书验证')
                command=['docker','run','--rm','-v',str(self.root/'certificates')+':/etc/letsencrypt','-v',str(self.root/'acme')+':/acme',read(self.root/'ops/images.json').get('certbot',CERTBOT)]
                if renew: command+=['renew','--quiet','--no-random-sleep-on-renew']
                else:
                    command+=['certonly','--non-interactive','--agree-tos','--email',c['tls_email'],'--webroot','-w','/acme','--cert-name',host]
                    try: ipaddress.ip_address(host);is_ip=True
                    except ValueError: is_ip=False
                    command+=['--preferred-profile','shortlived','--ip-address',host] if is_ip else ['-d',host]
                self.run(command)
                live=self.root/'certificates/live'/host
                source_cert=live/'fullchain.pem';source_key=live/'privkey.pem'
            finally:
                if server: server.terminate();server.wait(timeout=10)
        # Validate a staged pair before replacing the gateway's current files.
        with tempfile.TemporaryDirectory(dir=self.root/'config') as staging:
            cert=pathlib.Path(staging)/'fullchain.pem';key=pathlib.Path(staging)/'privkey.pem'
            shutil.copy2(source_cert,cert);shutil.copy2(source_key,key)
            cert.chmod(0o600);key.chmod(0o600)
            self.run(['openssl','x509','-in',str(cert),'-noout','-checkend','43200'])
            check='-checkip' if re.fullmatch(r'\d+\.\d+\.\d+\.\d+',host) else '-checkhost'
            self.run(['openssl','x509','-in',str(cert),'-noout',check,host])
            cert_public=self.run(['openssl','x509','-in',str(cert),'-pubkey','-noout'])
            key_public=self.run(['openssl','pkey','-in',str(key),'-pubout'])
            if cert_public!=key_public: raise RuntimeError('公网证书与私钥不匹配')
            key.replace(folder/'privkey.pem');cert.replace(folder/'fullchain.pem')
        if renew:
            self.compose('exec','-T','gateway','caddy','validate','--config','/config/Caddyfile','--adapter','caddyfile')
            self.compose('exec','-T','gateway','caddy','reload','--config','/config/Caddyfile','--adapter','caddyfile')

    def network(self):
        self.compose('create')
        subnets=json.loads(self.run(['docker','network','inspect',self.c['project']+'_business']))[0]['IPAM']['Config']
        ports=[8443,8444] if self.c['mode']=='platform-enterprise' else [8443]
        script=firewall_script(self.c['project'],self.c['private_ip'],ports,self.c['control_sources']+[x['Subnet'] for x in subnets])
        path=self.root/'ops/control-firewall.sh';path.write_text(script);path.chmod(0o700)
        self.run(['/bin/sh',str(path)])
        self.unit('control-firewall',shlex.quote(str(path)),None,before_docker=True)
        if self.c['mode']=='enterprise':
            # Dedicated chain: never replace the platform's existing firewall.
            script=firewall_script(self.c['project']+'-peer',self.c['platform_private_ip'],[8443,8444],[self.c['private_ip']+'/32'],add_only=True)
            remote='/etc/frogim-'+self.c['project']+'-peer-firewall.sh'
            install='cat > '+shlex.quote(remote)+'; chmod 700 '+shlex.quote(remote)+'; /bin/sh '+shlex.quote(remote)
            self.ssh(install,script.encode())
            unit='[Unit]\nDescription=Frogim enterprise private peer\nAfter=network-pre.target\nBefore=docker.service\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart='+remote+'\n[Install]\nWantedBy=multi-user.target\n'
            path='/etc/systemd/system/frogim-'+self.c['project']+'-peer.service'
            self.ssh('cat > '+shlex.quote(path)+'; systemctl daemon-reload; systemctl enable '+shlex.quote('frogim-'+self.c['project']+'-peer.service'),unit.encode())
        print('请确保云安全组允许私网控制来源；公网业务需要 TCP 80/443/5100/7881、UDP 7882–7889。')

    def wait(self, service):
        end=time.monotonic()+180
        while time.monotonic()<end:
            ids=self.compose('ps','-q',service).decode().strip()
            if ids:
                state=json.loads(self.run(['docker','inspect',ids]))[0]['State']
                if state.get('Health',{}).get('Status')=='healthy': return
                if state['Status']=='exited': raise RuntimeError(service+' 已退出，请检查私有容器日志')
            time.sleep(2)
        raise RuntimeError(service+' readiness 超时')

    def start_base(self):
        self.compose('up','-d','postgres','redis','minio')
        for name in ('postgres','redis','minio'): self.wait(name)
        self.init_media()
        if self.c['mode']=='platform-enterprise':
            self.compose('up','-d','platform');self.wait('platform')
        # Platform public management must be reachable to register the enterprise
        # before its API can request the platform signing key through mTLS.
        self.compose('up','-d','gateway');self.wait('gateway')

    def start(self):
        names=['api','im','livekit']
        # API readiness depends on IM; both must start before waiting on either.
        self.compose('up','-d',*names)
        for name in names: self.wait(name)

    def init_media(self):
        s=read(self.root/'config/secrets.json')
        policy={'Version':'2012-10-17','Statement':[{'Effect':'Allow','Action':['s3:GetBucketLocation','s3:ListBucket','s3:ListBucketMultipartUploads'],'Resource':['arn:aws:s3:::nexachat-media']},{'Effect':'Allow','Action':['s3:GetObject','s3:PutObject','s3:DeleteObject','s3:AbortMultipartUpload','s3:ListMultipartUploadParts'],'Resource':['arn:aws:s3:::nexachat-media/*']}]}
        write(self.root/'config/media-policy.json',policy)
        self.run(['docker','run','--rm','--network',self.c['project']+'_business','-e','MC_HOST_root=http://rootmedia:'+s['minio']+'@minio:9000','-e','APP_SECRET='+s['s3'],'-v',str(self.root/'config/media-policy.json')+':/policy.json:ro','--entrypoint','/bin/sh',read(self.root/'ops/images.json').get('mc',MC),'-c','mc mb --ignore-existing root/nexachat-media && mc admin user add root enterprise-media "$APP_SECRET" && mc admin policy create root enterprise-media /policy.json && mc admin policy attach root enterprise-media --user enterprise-media'])

    def http(self, path, body=None, token='', method=None):
        headers={'Content-Type':'application/json'}
        if token: headers['Authorization']='Bearer '+token
        req=urllib.request.Request(self.c['platform_origin']+'/platform/admin'+path,data=json.dumps(body).encode() if body is not None else None,headers=headers,method=method)
        with urllib.request.urlopen(req,timeout=20) as r: return json.load(r)

    def directory_target(self):
        c=self.c
        token=getattr(self,'admin_token',None)
        if not token:
            pwd=self.platform_password or ask('平台管理员密码（仅本次请求，不保存）',secret=True)
            token=self.http('/auth/login',{'username':c['platform_admin_user'],'password':pwd})['token'];pwd=None
            self.admin_token=token;self.platform_password=None;c.pop('_platform_password',None)
        tenants=self.http('/tenants',token=token)
        services_={'apiBaseUrl':c['public_origin'],'mediaBaseUrl':c['public_origin'],'imWsUrl':c['public_origin'].replace('https:','wss:')+'/im','imTcpUrl':'tcp://'+urllib.parse.urlsplit(c['public_origin']).hostname+':5100','callSignalUrl':c['public_origin'].replace('https:','wss:')+'/livekit'}
        control='https://'+c['private_ip']+(':'+str(8444 if c['mode']=='platform-enterprise' else 8443))
        target=next((x for x in tenants if x['id']==c['tenant_id']),None)
        if target and (target['services']!=services_ or target['controlUrl']!=control or target['code']!=c['tenant_code'].upper()): raise RuntimeError('平台已有同 ID 的不同企业配置，拒绝覆盖')
        if not target:
            target=self.http('/tenants',{'id':c['tenant_id'],'name':c['tenant_name'],'code':c['tenant_code'],'enabled':False,'isDefault':False,'version':0,'services':services_,'controlUrl':control,'reason':'源码部署：登记新建企业，暂不开放登录','confirmed':True},token)
        return token,target,control,services_

    def register_disabled(self):
        _,target,_,_=self.directory_target()
        write(self.root/'ops/directory.json',target)

    def register(self):
        c=self.c;token,target,control,services_=self.directory_target()
        # Even after an activation acknowledgement was lost, prove the same
        # identity/configuration again before treating a retry as complete.
        self.verify_directory(control,services_)
        self.check_public()
        if not target['enabled']:
            target.update(enabled=True,reason='源码部署：证书、企业身份、服务地址和 readiness 已核对，启用登录',confirmed=True)
            target=self.http('/tenants/'+c['tenant_id'],target,token,method='PATCH')
        write(self.root/'ops/directory.json',target)

    def verify_directory(self, control, services_):
        c=self.c
        if c['mode']=='platform-enterprise':
            certificates=self.root/'config/certs/platform'
            proof=json.loads(self.run(['curl','--fail','--silent','--cacert',str(certificates/'ca.pem'),'--cert',str(certificates/'cert.pem'),'--key',str(certificates/'key.pem'),'-H','Content-Type: application/json','--data','{}',control+'/internal/directory/ready']))
        else:
            self.verify_remote(control, services_)
            return
        if proof!={'status':'ready','tenantId':c['tenant_id'],'services':services_}: raise RuntimeError('mTLS 企业身份/服务地址不一致，保持停止登录')

    def verify_remote(self, control, services_):
        c=self.c
        # Use the actual platform namespace, mount and certificate identity.
        code='import json,subprocess; cs=json.loads(subprocess.check_output(["docker","inspect",*subprocess.check_output(["docker","ps","-q"]).decode().split()])); p=next(x for x in cs if "IM_MODE=platform" in x["Config"]["Env"]); e=dict(v.split("=",1) for v in p["Config"]["Env"] if "=" in v); print(subprocess.check_output(["docker","run","--rm","--user","0:0","--network","container:"+p["Id"],*sum((["-v",m["Source"]+":"+m["Destination"]+":ro"] for m in p["Mounts"] if m["Destination"]=="/certs"),[]),"curlimages/curl:8.12.1","--fail","--silent","--cacert",e["IM_CONTROL_CA"],"--cert",e["IM_CONTROL_CERT"],"--key",e["IM_CONTROL_KEY"],"-H","Content-Type: application/json","--data","{}",'+repr(control+'/internal/directory/ready')+']).decode())'
        proof=json.loads(self.ssh('python3 -c '+shlex.quote(code)))
        if proof!={'status':'ready','tenantId':c['tenant_id'],'services':services_}: raise RuntimeError('mTLS 企业身份/服务地址不一致，保持停止登录')

    def check_public(self):
        paths=['/ready','/admin/']+(['/platform/','/platform-ready','/app/'] if self.c['mode']=='platform-enterprise' else [])
        for path in paths:
            with urllib.request.urlopen(self.c['public_origin']+path,timeout=20) as r:
                if r.status!=200: raise RuntimeError('公网可用性检查失败')
        if self.c['mode']=='enterprise':
            for path in ('/','/app/','/web/'):
                try: urllib.request.urlopen(self.c['public_origin']+path,timeout=20)
                except urllib.error.HTTPError as e:
                    if e.code==404: continue
                raise RuntimeError('企业聊天入口未关闭')
        for name in services(self.c,read(self.root/'config/secrets.json'),read(self.root/'ops/images.json'))['services']: self.wait(name)

    def unit(self, name, command, schedule, before_docker=False):
        name=self.c['project']+'-'+name
        path=pathlib.Path('/etc/systemd/system')
        text='[Unit]\nDescription=Frogim '+name+'\n'+('After=network-pre.target\nBefore=docker.service\n' if before_docker else 'After=docker.service\n')+'[Service]\nType=oneshot\nExecStart='+command+'\n'
        if not schedule: text+='RemainAfterExit=yes\n[Install]\nWantedBy=multi-user.target\n'
        (path/(name+'.service')).write_text(text)
        if schedule: (path/(name+'.timer')).write_text('[Unit]\nDescription=Frogim '+name+'\n[Timer]\nOnCalendar='+schedule+'\nPersistent=true\nRandomizedDelaySec=300\n[Install]\nWantedBy=timers.target\n')
        self.run(['systemctl','daemon-reload']);self.run(['systemctl','enable',*(['--now'] if schedule else []),name+('.timer' if schedule else '.service')])

    def timers(self):
        shutil.copy2(HERE/'deploy-light-tenancy.py',self.root/'ops/deploy.py')
        cmd='/usr/bin/python3 '+shlex.quote(str(self.root/'ops/deploy.py'))+' --root '+shlex.quote(str(self.root))+' --operation '
        self.unit('backup',cmd+'backup','*-*-* 04:30:00')
        if self.c['tls_mode']=='acme': self.unit('cert-renew',cmd+'renew','*-*-* 00,12:00:00')

    def backup(self):
        target=self.root/'backups'/datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');target.mkdir()
        running=self.compose('ps','--services','--status','running').decode().split()
        if 'postgres' not in running:raise RuntimeError('数据库未运行，未开始备份或停止其他服务')
        writers=[x for x in ('api','platform','im','livekit') if x in running]
        try:
            if writers:self.compose('stop',*writers)
            for db in ['enterprise']+(['platform_light'] if self.c['mode']=='platform-enterprise' else []):
                (target/(db+'.dump')).write_bytes(self.compose('exec','-T','postgres','pg_dump','-U','enterprise','-d',db,'-Fc'))
            if 'redis' in running:self.compose('exec','-T','redis','sh','-c','REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli SAVE')
            stores=[x for x in ('redis','minio') if x in running]
            if stores:self.compose('stop',*stores)
            with tarfile.open(target/'data-config.tar.gz','w:gz') as a:
                for folder in ('config','certificates','downloads','legal','data/redis','data/media','data/im','data/plugins','compose.json','ops/images.json','ops/state.json'): a.add(self.root/folder,arcname=folder)
        finally:
            restart=[x for x in [*writers,'redis','minio'] if x in running]
            if restart:self.compose('up','-d',*restart)
        files={p.name:digest(p) for p in target.iterdir()}
        write(target/'complete.json',{'at':stamp(),'project':self.c['project'],'files':files})
        completed=sorted(x for x in (self.root/'backups').iterdir() if (x/'complete.json').exists())
        for old in completed[:-7]:
            if old.parent==self.root/'backups' and read(old/'complete.json')['project']==self.c['project']: shutil.rmtree(old)

    def deploy(self):
        for name, fn in [('source',self.checkout),('build',self.build),('configuration',self.configure),('control-certificates',self.control_certificates),('public-certificate',self.public_certificate),('network',self.network),('startup-base',self.start_base),('directory-registration',self.register_disabled),('startup',self.start),('directory',self.register),('availability',self.check_public),('timers',self.timers)]: self.step(name,fn)
        write(self.root/'ops/completed.json',{'at':stamp(),'commit':self.state['commit'],'mode':self.c['mode'],'services':list(read(self.root/'compose.json')['services']),'publicOrigin':self.c['public_origin'],'platformOrigin':self.c['platform_origin'],'composeSha256':hashlib.sha256((self.root/'compose.json').read_bytes()).hexdigest()})
        print('部署完成：企业后台 '+self.c['public_origin']+'/admin/')
        print('平台后台 '+self.c['platform_origin']+'/platform/；统一聊天 '+self.c['platform_origin']+'/app/')


def firewall_script(project, own, ports, sources, add_only=False):
    chain='FG'+hashlib.sha256(project.encode()).hexdigest()[:12]
    lines=['#!/bin/sh','set -eu','iptables -N '+chain+' 2>/dev/null || true']
    if not add_only: lines+=['iptables -F '+chain]
    for port in ports:
        for source in [own+'/32',*sources]:
            for prefix in ['-p tcp -d '+own+' --dport '+str(port),'-p tcp -m conntrack --ctorigdst '+own+' --ctorigdstport '+str(port)]:
                rule=prefix+' -s '+source+' -j ACCEPT'
                lines+=['iptables -C '+chain+' '+rule+' 2>/dev/null || iptables -A '+chain+' '+rule]
        if not add_only:
            lines+=['iptables -A '+chain+' -p tcp -d '+own+' --dport '+str(port)+' -j DROP','iptables -A '+chain+' -p tcp -m conntrack --ctorigdst '+own+' --ctorigdstport '+str(port)+' -j DROP']
    lines+=['iptables -N DOCKER-USER 2>/dev/null || true']
    for parent in ('INPUT','DOCKER-USER'): lines+=['iptables -C '+parent+' -j '+chain+' 2>/dev/null || iptables -I '+parent+' 1 -j '+chain]
    return '\n'.join(lines)+'\n'


def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--root');p.add_argument('--config',help='root-private JSON; otherwise interactive');p.add_argument('--yes',action='store_true');p.add_argument('--operation',choices=['deploy','backup','renew','check'],default='deploy');args=p.parse_args()
    if platform.system()!='Linux' or os.geteuid()!=0: raise SystemExit('只支持 Linux root')
    if platform.machine() not in ('x86_64','amd64'): raise SystemExit('现有悟空补丁只支持 Linux amd64')
    os.umask(0o077)
    if args.root and (pathlib.Path(args.root)/'config/deployment.json').exists(): c=read(pathlib.Path(args.root)/'config/deployment.json')
    elif args.config: c=read(args.config)
    else: c=collect(args.root)
    c=validate(c)
    if args.operation=='deploy':
        print('部署 '+c['mode']+' → '+c['root']+'，企业 '+c['tenant_name']+'；源码 '+c['ref'])
        if not args.yes and input('确认开始构建和部署？[y/N] ').lower()!='y': return
        root=pathlib.Path(c['root'])
        if root.exists() and any(root.iterdir()) and not (root/'ops/state.json').exists(): raise SystemExit('目录非空且无本工具阶段记录，拒绝接管已有数据')
        for port in (80,443,5100,7881):
            if not (root/'ops/state.json').exists():
                with socket.socket() as sock:
                    try:sock.bind(('0.0.0.0',port))
                    except OSError:raise SystemExit(str(port)+' 端口已占用，拒绝覆盖现有服务')
        if shutil.disk_usage(root.parent if root.parent.exists() else '/').free < 12*1024**3: raise SystemExit('可用磁盘不足 12 GiB')
    d=Deployment(c)
    import fcntl
    with (d.root/'ops/deploy.lock').open('w') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        if args.operation=='deploy': d.deploy()
        elif args.operation=='backup':d.backup()
        elif args.operation=='renew':d.public_certificate(renew=True)
        else:d.check_public()


if __name__=='__main__':
    try:main()
    except KeyboardInterrupt:raise SystemExit('已中断；保留阶段与数据，可使用 --root 续跑')
    except Exception as e:
        # Provider errors can carry credentials or user data; never print bodies.
        if isinstance(e,(ValueError,RuntimeError)): print(str(e),file=sys.stderr)
        else: print('操作失败：'+type(e).__name__+'；保留数据和阶段记录，请查看私有诊断。',file=sys.stderr)
        raise SystemExit(1)
