#!/usr/bin/env python3
"""Update one storefront image, run its migrations, and restore its prior manifest on health failure."""
import json,os,pathlib,re,subprocess,sys,time
import importlib.util
spec=importlib.util.spec_from_file_location('storefront_provision',pathlib.Path(__file__).with_name('storefront-provision.py'))
provision=importlib.util.module_from_spec(spec);spec.loader.exec_module(provision)
name,image=sys.argv[1:]
if not re.fullmatch(r'[a-z][a-z0-9-]{1,30}',name) or not re.fullmatch(r'storefront-api:[a-zA-Z0-9_.-]+',image):raise SystemExit('Invalid name/image')
env=provision.env_values();root=pathlib.Path(env['APPS_DIR'])/name;manifest=root/'compose.yaml'
if not (root/'storefront.env').exists():raise SystemExit('Not a provisioned storefront')
import fcntl
with open(root/'.release.lock','a+') as lock:
 fcntl.flock(lock,fcntl.LOCK_EX)
 old=manifest.read_text();model=json.loads(old);tag=image.split(':',1)[1];ref=name+'-api:'+tag
 provision.run(['docker','image','tag',image,ref])
 for service in model['services'].values():service['image']=ref
 archive=root/'releases'/str(time.time_ns());archive.mkdir(parents=True);provision.atomic(archive/'compose.previous.json',old)
 provision.atomic(manifest,json.dumps(model,indent=2))
 try:provision.run(['docker','compose','-f',str(manifest),'up','-d','--wait','--wait-timeout','120','--pull','never'],cwd=root)
 except Exception:
  provision.atomic(manifest,old)
  try:provision.run(['docker','compose','-f',str(manifest),'up','-d','--wait','--wait-timeout','120','--pull','never'],cwd=root)
  except Exception:raise SystemExit('Release failed; rollback also failed. Inspect this instance.')
  raise SystemExit('Release failed; previous image restored. Database migrations were not reversed.')
 print(json.dumps({'status':'ready','instance':name,'image':ref,'rollbackManifest':str(archive/'compose.previous.json')}))
