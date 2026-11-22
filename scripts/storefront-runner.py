#!/usr/bin/env python3
"""Authenticated internal provisioning runner; never exposed to the public network."""
import concurrent.futures, hashlib, hmac, http.server, json, os, pathlib, re, subprocess, threading, time
import urllib.parse
from project_deployments import Projects, MAX_UPLOAD
ROOT=pathlib.Path(os.environ.get('STOREFRONT_INFRA_ROOT','/infra'))
DATA=pathlib.Path('/data'); DATA.mkdir(exist_ok=True)
TOKEN=os.environ.get('MANAGER_GATEWAY_TOKEN','')
POOL=concurrent.futures.ThreadPoolExecutor(max_workers=1)
LOCK=threading.Lock()
PROJECTS=Projects(ROOT, DATA, POOL)
def write(path,value):
 tmp=path.with_suffix('.tmp');tmp.write_text(json.dumps(value));os.chmod(tmp,0o600);os.replace(tmp,path)
def job_run(path,spec):
 job=json.loads(path.read_text());job.update(status='running',phase='preparing');write(path,job)
 try:
  p=subprocess.Popen(['make','--no-print-directory','storefront-create'],cwd=ROOT,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
  p.stdin.write(json.dumps(spec));p.stdin.close()
  for line in p.stdout:
   try:
    event=json.loads(line)
    if event.get('phase'):job['phase']=event['phase'];write(path,job)
    if event.get('url'):job['url']=event['url']
   except ValueError:pass
  err=p.stderr.read();rc=p.wait(timeout=600)
  if rc:raise RuntimeError('Provisioning stopped; inspect the recorded phase and infrastructure logs before retrying')
  job.update(status='ready',phase='ready')
 except Exception as exc:job.update(status='failed',message=str(exc))
 write(path,job)
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def send(self,code,obj):
  data=json.dumps(obj).encode();self.send_response(code);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
 def do_GET(self):
  if self.path=='/healthz':return self.send(200,{'status':'ok'})
  if not self.auth():return
  if self.path=='/projects/catalog':
   try:return self.send(200,PROJECTS.catalog())
   except Exception:return self.send(503,{'error':'Project catalog unavailable; inspect project configuration on the server'})
  if self.path=='/catalog':
   try:
    result=subprocess.run(['docker','image','ls','--filter','reference=storefront-api:*','--format','{{.Repository}}:{{.Tag}}'],capture_output=True,text=True,timeout=10,check=True)
    images=sorted({x for x in result.stdout.splitlines() if re.fullmatch(r'storefront-api:[a-zA-Z0-9_.-]+',x) and not x.endswith(':latest')},reverse=True)
    catalog=json.loads((ROOT/'storefront/catalog.json').read_text())
    jobs=[json.loads(p.read_text()) for p in sorted(DATA.glob('*.json'),reverse=True)][:100]
    journals=pathlib.Path(os.environ.get('STOREFRONT_APPS_ROOT','/apps'))/'.storefront'
    if journals.exists():
     for path in journals.glob('*.json'):
      state=json.loads(path.read_text())
      if not any(j['name']==state['name'] for j in jobs):jobs.append({'id':'existing-'+state['name'],'name':state['name'],'domain':state['domain'],'status':'ready' if state.get('done') else 'interrupted','phase':state['stage'],'url':state.get('origin',''),'createdAt':state.get('createdAt',0)})
    catalog.update(images=images,jobs=jobs);return self.send(200,catalog)
   except Exception:return self.send(503,{'error':'Storefront catalog unavailable'})
  return self.send(404,{'error':'Unknown runner endpoint'})
 def auth(self):
  if len(TOKEN)<32 or not hmac.compare_digest(self.headers.get('Authorization',''),'Bearer '+TOKEN):self.send(401,{'error':'Unauthorized'});return False
  return True
 def do_POST(self):
  if not self.auth():return
  if self.path.startswith('/projects/'):
   try:
    url=urllib.parse.urlsplit(self.path)
    n=int(self.headers.get('Content-Length','0'))
    if url.path=='/projects/upload':
     name=urllib.parse.parse_qs(url.query).get('project',[''])[0]
     if not 0<n<=MAX_UPLOAD:raise ValueError('Package exceeds the 2 GiB upload limit')
     return self.send(200,PROJECTS.upload(name,self.rfile,n))
    if not 0<n<=1024*1024:raise ValueError('Invalid deployment request size')
    req=json.loads(self.rfile.read(n))
    if url.path=='/projects/preview':return self.send(200,PROJECTS.preview(req))
    if url.path=='/projects/job':return self.send(200,PROJECTS.job(req.get('id')))
    if url.path not in ('/projects/save','/projects/deploy','/projects/launch'):return self.send(404,{'error':'Unknown project operation'})
    return self.send(202,PROJECTS.enqueue(url.path.rsplit('/',1)[1],req))
   except (ValueError,TypeError,KeyError) as exc:return self.send(422,{'error':str(exc)})
   except Exception:return self.send(503,{'error':'Project operation failed; inspect server state before retrying'})
  if self.path!='/create':return self.send(404,{'error':'Unknown runner endpoint'})
  try:
   n=int(self.headers.get('Content-Length','0'))
   if n<1 or n>4096:raise ValueError('Invalid body size')
   req=json.loads(self.rfile.read(n));key=req.pop('key','')
   if not re.fullmatch(r'[a-f0-9-]{36}',key):raise ValueError('Invalid request key')
   if not re.fullmatch(r'[a-z][a-z0-9-]{1,30}',req.get('name','')):raise ValueError('Invalid store name')
   fingerprint=hashlib.sha256(json.dumps(req,sort_keys=True).encode()).hexdigest();path=DATA/(key+'.json')
   with LOCK:
    if path.exists():
     job=json.loads(path.read_text())
     if job['fingerprint']!=fingerprint:raise ValueError('Request key already belongs to another configuration')
     return self.send(200,job)
    for p in DATA.glob('*.json'):
     other=json.loads(p.read_text())
     if other['name']==req['name'] and other['status'] in ('running','queued','ready'):raise ValueError('A store or job already uses this name')
    job={'id':key,'name':req['name'],'domain':req.get('domain'),'status':'queued','phase':'queued','createdAt':int(time.time()),'fingerprint':fingerprint}
    write(path,job);POOL.submit(job_run,path,req)
   return self.send(202,job)
  except (ValueError,TypeError) as exc:return self.send(400,{'error':str(exc)})
# Outcomes of in-flight jobs are inspected, never blindly replayed after a crash.
for path in DATA.glob('*.json'):
 job=json.loads(path.read_text())
 if job.get('status') in ('running','queued'):job.update(status='interrupted',message='Runner restarted. Inspect provision journal before resuming.');write(path,job)
http.server.ThreadingHTTPServer(('0.0.0.0',8080),Handler).serve_forever()
