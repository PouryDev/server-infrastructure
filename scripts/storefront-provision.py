#!/usr/bin/env python3
"""Provision a single named storefront, with a durable resumable journal.
Input is JSON over stdin, never shell fragments. Secrets are generated on the target.
Run through make storefront-create. Source .env is read only by the existing provisioner.
"""
import contextlib, fcntl, json, os, pathlib, re, secrets, subprocess, sys, tempfile, time
ROOT = pathlib.Path(__file__).resolve().parents[1]

def atomic(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, 'w') as stream:
            stream.write(data); stream.flush(); os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary): os.unlink(temporary)

def run(args, *, cwd=ROOT, input=None):
    environment = dict(os.environ)
    bypass = environment.get("NO_PROXY", environment.get("no_proxy", ""))
    environment["NO_PROXY"] = environment["no_proxy"] = ",".join(filter(None, [bypass, "localhost", "127.0.0.1", "::1"]))
    p = subprocess.run(args, cwd=cwd, input=input, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=180, env=environment)
    if p.returncode:
        # Commands can contain credentials in diagnostics: never return their output to the browser.
        raise RuntimeError('Operation failed: '+args[0]+' (inspect target infrastructure logs)')
    return p.stdout

def env_values():
    # Read only the deployment paths/image mirror, never print or serialize the infra credentials.
    output = run(['bash','-c','set -a; . ./.env; python3 -c \'import json,os; print(json.dumps({k:os.environ.get(k, "") for k in ["APPS_DIR","DATA_DIR","REGISTRY"]}))\''])
    return json.loads(output)

def main():
    req = json.load(sys.stdin)
    if set(req)-{'name','domain','title','theme','ownerPhone','adminPhone','demo','image','modules','smsKey','smsTemplate','accent'}: raise ValueError('Unknown storefront option')
    name=req.get('name',''); domain=req.get('domain',''); theme=req.get('theme','atelier')
    if not re.fullmatch(r'[a-z][a-z0-9-]{1,30}',name) or name in {'infra','manager','postgres','redis','rabbitmq','nginx','storefront'}:raise ValueError('Invalid store name')
    if not re.fullmatch(r'(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}',domain):raise ValueError('Invalid domain')
    catalog=json.loads((ROOT/'storefront/catalog.json').read_text())
    if theme not in {t['id'] for t in catalog['themes']}:raise ValueError('Invalid theme')
    if not re.fullmatch(r'#[0-9a-fA-F]{6}',req.get('accent','#17655d')):raise ValueError('Invalid accent color')
    for k in ('ownerPhone','adminPhone'):
        if not re.fullmatch(r'\+989[0-9]{9}',req.get(k,'')):raise ValueError('Iranian owner/admin phones are required')
    if req['ownerPhone']==req['adminPhone']:raise ValueError('Owner and admin must have different phone numbers')
    demo=bool(req.get('demo',False))
    if demo and not domain.endswith('.localhost'):raise ValueError('Demo mode is only allowed on .localhost')
    if not demo and (not req.get('smsKey') or not req.get('smsTemplate')):raise ValueError('SMS credentials and OTP template are required for live stores')
    image=req.get('image','')
    if not re.fullmatch(r'storefront-api:[a-zA-Z0-9_.-]+',image):raise ValueError('Choose a built immutable storefront image')
    env=env_values(); apps=pathlib.Path(env['APPS_DIR']); data=pathlib.Path(env['DATA_DIR']); root=apps/name
    # One global lock covers shared nginx/probe changes; no lost updates between stores.
    lockdir=apps/'.storefront';lockdir.mkdir(exist_ok=True)
    with open(lockdir/'provision.lock','a+') as lock:
      fcntl.flock(lock,fcntl.LOCK_EX)
      journal=lockdir/(name+'.json')
      if journal.exists():
        state=json.loads(journal.read_text())
        if state['domain']!=domain or state['ownerPhone']!=req['ownerPhone']:raise ValueError('Store identity conflicts with existing provision journal')
        if state.get('done'):print(json.dumps({'status':'ready','url':state['origin'],'name':name}));return
      else:
        if (root/'.env').exists() or (ROOT/'config/nginx/conf.d'/f'{name}.conf').exists():raise ValueError('Name is already used by another project')
        for conf in (ROOT/'config/nginx/conf.d').glob('*.conf'):
          if domain in re.findall(r'server_name\s+([^;]+);',conf.read_text()):raise ValueError('Domain already belongs to another project')
        state={'name':name,'domain':domain,'ownerPhone':req['ownerPhone'],'origin':('http://' if domain.endswith('.localhost') else 'https://')+domain,'stage':'new','createdAt':int(time.time())}
        atomic(journal,json.dumps(state))
      def stage(value):
        state['stage']=value;atomic(journal,json.dumps(state));print(json.dumps({'phase':value}),flush=True)
      if state['stage']=='new':
        stage('provisioning')
        # Existing infrastructure command supplies DB/Redis/RabbitMQ isolation and credentials.
        run(['bash','scripts/new-project.sh',name])
        stage('resources-ready')
      if state['stage']=='provisioning':
        # Never replay a partially executed credential-creation script automatically.
        if not (root/'.env').exists():raise RuntimeError('Partial provisioning requires inspecting allocated resources before retry')
        stage('resources-ready')
      config=root/'storefront.env'
      if not config.exists():
        owner_password=secrets.token_urlsafe(18); admin_password=secrets.token_urlsafe(18)
        values={'STORE_INSTANCE':name,'STORE_ORIGIN':state['origin'],'STORE_SECRET':secrets.token_hex(32),'STORE_UPLOADS':'/data/uploads','STORE_DEMO':str(demo).lower(),'STORE_OWNER_PHONE':req['ownerPhone'],'STORE_OWNER_PASSWORD':owner_password,'STORE_ADMIN_PHONE':req['adminPhone'],'STORE_ADMIN_PASSWORD':admin_password,'STORE_INITIAL_THEME':theme,'STORE_INITIAL_ACCENT':req.get('accent','#17655d'),'STORE_INITIAL_TITLE':req.get('title',name),'STORE_INITIAL_MODULES':','.join(req.get('modules',['blog','attachments','discounts','analytics','wishlist'])),'KAVENEGAR_API_KEY':req.get('smsKey',''),'KAVENEGAR_TEMPLATE':req.get('smsTemplate',''),'GAPCHI_IDENTITY_SECRET':''}
        if any('\n' in str(v) or '\r' in str(v) for v in values.values()):raise ValueError('Configuration must be single-line')
        atomic(config,'\n'.join(k+'='+str(v) for k,v in values.items())+'\n')
      run(['docker','run','--rm','-v',str(data/'apps'/name)+':/data',env.get('REGISTRY','docker.arvancloud.ir')+'/library/alpine:3.24','mkdir','-p','/data/uploads'])
      run(['docker','run','--rm','-v',str(data/'apps'/name)+':/data',env.get('REGISTRY','docker.arvancloud.ir')+'/library/alpine:3.24','chown','-R','65532:65532','/data'])
      tag=image.split(':',1)[1];instance_image=name+'-api:'+tag
      run(['docker','image','tag',image,instance_image])
      common={'image':instance_image,'env_file':['.env',{'path':'storefront.env','format':'raw'}],'read_only':True,'tmpfs':['/tmp'],'user':'65532:65532','cap_drop':['ALL'],'security_opt':['no-new-privileges:true'],'networks':['infra'],'mem_limit':'256m','cpus':0.75}
      manifest={'name':name,'services':{name+'-migrate':dict(common,entrypoint=['/migrate'],command=['up'],restart='no'),name+'-api':dict(common,environment={'OTEL_SERVICE_NAME':name+'-api'},volumes=[str(data/'apps'/name/'uploads')+':/data/uploads'],depends_on={name+'-migrate':{'condition':'service_completed_successfully'}},restart='unless-stopped',stop_grace_period='15s',logging={'driver':'json-file','options':{'max-size':'10m','max-file':'3'}},healthcheck={'test':['CMD','/api','healthcheck'],'interval':'10s','timeout':'3s','retries':3,'start_period':'20s'})},'networks':{'infra':{'external':True}}}
      atomic(root/'compose.yaml',json.dumps(manifest,indent=2))
      stage('starting')
      run(['docker','compose','-f',str(root/'compose.yaml'),'up','-d','--wait','--wait-timeout','120','--pull','never'],cwd=root)
      stage('routing')
      conf=ROOT/'config/nginx/conf.d'/f'{name}.conf'
      old=conf.read_text() if conf.exists() else None
      text='server {\n listen 80;\n server_name '+domain+';\n client_max_body_size 20m;\n set $upstream_client http://'+name+'-api:8080;\n location / { proxy_pass $upstream_client; include /etc/nginx/snippets/proxy.conf; }\n}\n'
      atomic(conf,text);os.chmod(conf,0o644)
      try:
        run(['docker','compose','exec','-T','nginx','nginx','-t']);run(['docker','compose','exec','-T','nginx','nginx','-s','reload'])
      except Exception:
        if old is None:conf.unlink(missing_ok=True)
        else:atomic(conf,old)
        raise
      state['done']=True;stage('ready')
      print(json.dumps({'status':'ready','url':state['origin'],'name':name}))
if __name__=='__main__':
  try:main()
  except Exception as exc:
    print(json.dumps({'error':str(exc)}),file=sys.stderr);sys.exit(1)
