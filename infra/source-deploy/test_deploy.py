"""Targeted deployment tests; no production network or data."""
import copy
import importlib.util
import json
import pathlib
import subprocess
import base64
import shutil
import tempfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('deploy',pathlib.Path(__file__).parents[1]/'scripts/deploy-light-tenancy.py')
d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)


def fixture(mode='platform-enterprise'):
    return {'mode':mode,'project':'frogim-example','root':'/data/frogim/example','repository':'https://example.com/source.git','ref':'main','public_origin':'https://203.0.113.10','platform_origin':'https://203.0.113.10','private_ip':'172.31.1.10','tenant_id':'enterprise-a','tenant_name':'客户A企业','tenant_code':'A','admin_user':'admin','platform_admin_user':'admin','tls_mode':'files','tls_cert':'/private/fullchain.pem','tls_key':'/private/privkey.pem','push_provider':'getui','getui_app_id':'fixture-id','getui_app_key':'fixture-key','getui_master_secret':'fixture-master-secret-long','control_sources':['172.31.1.11/32'],'otp_mode':'fixed','fixed_otp':'123456','platform_private_ip':'172.31.1.10','platform_ssh_host':'172.31.1.10','platform_ssh_user':'root','platform_ca_dir':'/data/frogim/main/config/ca'}


def secrets_():
    keys=('pg','redis','minio','s3','jwt','manager','im','policy','rtc','platform_jwt','admin_hash','platform_hash','plugin_public','vapid_public','vapid_private')
    return dict.fromkeys(keys,'fixture-value')


class DeploymentTests(unittest.TestCase):
    def test_mode_data_and_ports(self):
        for mode,count in [('platform-enterprise',8),('enterprise',7)]:
            c=d.validate(fixture(mode));plan=d.services(c,secrets_(),{'app':'fixture:api','gateway':'fixture:gateway','im':'fixture:im'})
            self.assertEqual(len(plan['services']),count)
            for name in ('postgres','redis','minio'):self.assertNotIn('ports',plan['services'][name])
            self.assertEqual(plan['services']['api']['environment']['IM_MODE'],'enterprise')
            self.assertEqual(plan['services']['api']['user'],'0:0')
            self.assertTrue(plan['services']['api']['ports'][0].startswith('172.31.1.10:'))
            if count==8:
                platform=plan['services']['platform']['environment']
                self.assertTrue(platform['IM_DATABASE_URL'].endswith('/platform_light'))
                self.assertNotIn('IM_REDIS_URL',platform)
                self.assertEqual(platform['IM_PLATFORM_FIXED_OTP_CODE'],'123456')
                self.assertEqual(platform['IM_DEV_MODE'],'false')
                self.assertEqual(plan['services']['api']['environment']['IM_PLATFORM_URL'],'https://platform:8443')
            else:self.assertNotIn('platform',plan['services'])

    def test_sms_config_is_independent(self):
        c=fixture();c.update(otp_mode='sms',otp_webhook_url='https://sms.example.com/send',otp_webhook_token='a'*32)
        plan=d.services(d.validate(c),secrets_(),{'app':'x','gateway':'y','im':'z'})
        e=plan['services']['platform']['environment']
        self.assertNotIn('IM_PLATFORM_FIXED_OTP_CODE',e);self.assertEqual(e['IM_DEV_MODE'],'false')

    def test_input_rejection(self):
        for key,value in [('root','/'),('root','/data/../'),('public_origin','http://host'),('public_origin','https://host/platform'),('private_ip','8.8.8.8'),('ref','--upload-pack=x'),('tenant_id','a; rm -rf /'),('control_sources',['0.0.0.0/0']),('push_provider','log')]:
            c=fixture();c[key]=value
            with self.subTest(key=key):
                with self.assertRaises(ValueError):d.validate(c)

    def test_gateway_scope_and_cache(self):
        a=d.gateway(fixture());b=d.gateway(fixture('enterprise'))
        self.assertIn('reverse_proxy platform:8080',a);self.assertIn('root * /srv/web',a)
        self.assertNotIn('root * /srv/web',b);self.assertIn('@chat path / /app /app/* /web /web/*',b)
        self.assertIn('header_up -Authorization',b);self.assertIn('header_up -Cookie',b)
        self.assertIn('/internal/*',a);self.assertIn('/rtc/*',a)
        self.assertIn('request>headers>Authorization delete',a)
        self.assertNotIn('@immutable path /assets/',a)

    def test_dollar_escaping_roundtrip(self):
        plan={'environment':{'PASSWORD':'$2a$12$fixture'},'test':['sh','-c','echo "$PASSWORD"']}
        self.assertEqual(d.escape(plan)['environment']['PASSWORD'],'$$2a$$12$$fixture')
        self.assertEqual(plan['environment']['PASSWORD'],'$2a$12$fixture')

    def test_firewall_covers_both_host_and_docker(self):
        value=d.firewall_script('fixture','172.31.1.10',[8443,8444],['172.31.1.11/32','172.28.0.0/16'])
        self.assertIn('INPUT -j',value);self.assertIn('DOCKER-USER -j',value)
        self.assertIn('--ctorigdstport 8443',value);self.assertIn('--dport 8443',value)
        self.assertIn('-j DROP',value)
        peer=d.firewall_script('peer','172.31.1.10',[8443],['172.31.1.11/32'],True)
        self.assertNotIn(' -F ',peer);self.assertNotIn('-j DROP',peer)

    def test_failed_stage_retries_and_completed_stage_skips(self):
        with tempfile.TemporaryDirectory() as folder:
            obj=object.__new__(d.Deployment);obj.root=pathlib.Path(folder);(obj.root/'ops').mkdir();obj.state={'done':[]}
            with self.assertRaises(RuntimeError):obj.step('build',lambda:(_ for _ in ()).throw(RuntimeError('interrupted')))
            self.assertEqual(obj.state['done'],[])
            calls=[];obj.step('build',lambda:calls.append(1));obj.step('build',lambda:calls.append(2))
            self.assertEqual(calls,[1]);self.assertEqual(d.read(obj.root/'ops/state.json')['done'],['build'])

    def test_sensitive_inputs_not_persisted_and_resume_config_locked(self):
        with tempfile.TemporaryDirectory() as folder:
            c=fixture();c['root']=folder;c['_platform_password']='private-password';c['_admin_password']='private-password'
            obj=d.Deployment(c)
            saved=d.read(obj.root/'config/deployment.json')
            self.assertNotIn('_platform_password',saved);self.assertNotIn('_admin_password',saved)
            d.Deployment(saved)
            saved['tenant_code']='OTHER'
            with self.assertRaises(ValueError):d.Deployment(saved)

    def test_directory_acknowledgement_loss_does_not_register_twice(self):
        with tempfile.TemporaryDirectory() as folder:
            c=fixture();c['root']=folder;c['_platform_password']='FixtureAdmin123!'
            obj=d.Deployment(c);db=[];calls=[];proofs=[]
            def http(path,body=None,token='',method=None):
                calls.append((path,method))
                if path=='/auth/login':return {'token':'synthetic-token'}
                if path=='/tenants' and body is None:return copy.deepcopy(db)
                if path=='/tenants':
                    body.update(version=1,isDefault=True);db.append(copy.deepcopy(body));return copy.deepcopy(body)
                db[0].update(enabled=True,version=2)
                raise RuntimeError('confirmation lost')
            obj.http=http;obj.verify_directory=lambda *a:proofs.append(1);obj.check_public=lambda:None
            with self.assertRaises(RuntimeError):obj.register()
            obj.platform_password='FixtureAdmin123!';obj.register()
            self.assertEqual(len(db),1);self.assertTrue(db[0]['enabled'])
            self.assertEqual(sum(x[0]=='/tenants/enterprise-a' for x in calls),1)
            self.assertEqual(len(proofs),2)

    def test_backup_never_stops_all_when_no_writers_running(self):
        with tempfile.TemporaryDirectory() as folder:
            c=fixture();c['root']=folder;obj=d.Deployment(c)
            for name in ('redis','media','im','plugins'):(obj.root/'data'/name).mkdir(parents=True)
            d.write(obj.root/'compose.json',{});d.write(obj.root/'ops/images.json',{})
            calls=[]
            def compose(*args,**kwargs):
                calls.append(args)
                return b'postgres redis minio gateway\n' if args[0]=='ps' else b'fixture-dump'
            obj.compose=compose;obj.backup()
            self.assertNotIn(('stop',),calls)
            self.assertIn(('stop','redis','minio'),calls)
            self.assertNotIn(('stop','postgres'),calls)

    def test_startup_handles_api_im_readiness_dependency(self):
        for mode in ('platform-enterprise','enterprise'):
            with tempfile.TemporaryDirectory() as folder:
                c=fixture(mode);c['root']=folder;obj=d.Deployment(c);started=set()
                obj.compose=lambda *args:started.update(args[2:])
                obj.init_media=lambda:None
                def ready(name):
                    self.assertIn(name,started)
                    if name=='api':self.assertIn('im',started,'API cannot become ready before IM starts')
                    if name=='gateway' and mode=='platform-enterprise':self.assertIn('platform',started)
                obj.wait=ready;obj.start_base();obj.start()
                self.assertEqual('platform' in started,mode=='platform-enterprise')

    @unittest.skipUnless(shutil.which('git'),'Git required')
    def test_source_fetch_is_shallow_and_pinned_on_resume(self):
        with tempfile.TemporaryDirectory() as folder:
            remote=pathlib.Path(folder)/'repo';remote.mkdir()
            def git(*args):return subprocess.check_output(['git','-C',str(remote),*args],stderr=subprocess.DEVNULL)
            git('init','-b','main');git('config','user.email','fixture@example.invalid');git('config','user.name','fixture')
            path=remote/'infra/source-deploy/Dockerfile.api';path.parent.mkdir(parents=True);path.write_text('FROM alpine\n')
            git('add','.');git('commit','-m','fixture');first=git('rev-parse','HEAD').decode().strip()
            c=fixture();c.update(root=str(pathlib.Path(folder)/'deployment'),repository=str(remote))
            obj=d.Deployment(c);obj.checkout()
            self.assertEqual(obj.state['commit'],first)
            self.assertTrue((obj.source/'.git/shallow').exists())
            path.write_text('FROM alpine:3.22\n');git('add','.');git('commit','-m','new-head')
            obj.checkout()
            self.assertEqual(obj.run(['git','-C',str(obj.source),'rev-parse','HEAD']).decode().strip(),first)

    @unittest.skipUnless(shutil.which('openssl'),'OpenSSL required')
    def test_real_control_certificates_and_public_key_pair(self):
        with tempfile.TemporaryDirectory() as folder:
            c=fixture();c['root']=folder
            obj=d.Deployment(c)
            for who in ('api','platform'):(obj.root/'config/certs'/who).mkdir(parents=True)
            obj.control_certificates()
            key=(obj.root/'config/certs/api/key.pem').read_bytes()
            obj.control_certificates()
            self.assertEqual(key,(obj.root/'config/certs/api/key.pem').read_bytes())
            public=obj.root/'config/public-tls';public.mkdir()
            cert=obj.root/'fixture-cert.pem';pk=obj.root/'fixture-key.pem'
            obj.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','2','-subj','/CN=fixture','-addext','subjectAltName=IP:203.0.113.10','-keyout',str(pk),'-out',str(cert)])
            obj.c.update(tls_cert=str(cert),tls_key=str(pk));obj.public_certificate()
            self.assertEqual(cert.read_bytes(),(public/'fullchain.pem').read_bytes())
            installed_key=(public/'privkey.pem').read_bytes()
            calls=[];obj.compose=lambda *args:calls.append(args)
            obj.public_certificate(renew=True)
            self.assertTrue(any('reload' in x for x in calls))
            wrong=obj.root/'wrong-key.pem';obj.run(['openssl','genpkey','-algorithm','RSA','-pkeyopt','rsa_keygen_bits:2048','-out',str(wrong)])
            obj.c['tls_key']=str(wrong)
            with self.assertRaises(RuntimeError):obj.public_certificate()
            self.assertEqual(installed_key,(public/'privkey.pem').read_bytes())


if __name__=='__main__':unittest.main()
