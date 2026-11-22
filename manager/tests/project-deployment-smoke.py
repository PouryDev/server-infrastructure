#!/usr/bin/env python3
"""Run inside manager-storefront; creates and removes only manager-deploy-check.
Real Docker/Compose/nginx checks, with no application data or shared DB changes.
"""
import concurrent.futures
import io
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tarfile
import tempfile
import uuid

sys.path.insert(0, '/infra/scripts')
from project_deployments import Projects, atomic

name = 'manager-deploy-check'
root = pathlib.Path(os.environ['STOREFRONT_INFRA_ROOT'])
pool = concurrent.futures.ThreadPoolExecutor(max_workers=1)
runner = Projects(root, '/data', pool)
directory = runner.apps / name
if directory.exists(): raise RuntimeError('Smoke project already exists; inspect before rerunning')
ref = name + '-api:smoke-v1'
base = 'docker.arvancloud.ir/nginx:1.30.6-alpine'
subprocess.run(['docker', 'image', 'tag', base, ref], check=True)
manifest = {'name': name, 'services': {name + '-api': {
    'image': ref, 'entrypoint': ['/bin/sh', '-ec'],
    'command': ["mkdir -p /tmp/www; env > /tmp/www/index.html; printf 'pid /tmp/nginx.pid; events {} http { access_log off; error_log /dev/stderr; client_body_temp_path /tmp/client; proxy_temp_path /tmp/proxy; fastcgi_temp_path /tmp/fastcgi; uwsgi_temp_path /tmp/uwsgi; scgi_temp_path /tmp/scgi; server { listen 8080; root /tmp/www; location /healthz { return 200 ok; } } }' > /tmp/nginx.conf; exec nginx -c /tmp/nginx.conf -g 'daemon off;'"],
    'environment': {'SMOKE_VALUE': 'compose-default'},
    'user': '65532:65532', 'read_only': True, 'tmpfs': ['/tmp'], 'cap_drop': ['ALL'],
    'security_opt': ['no-new-privileges:true'], 'networks': ['infra'], 'mem_limit': '64m', 'cpus': .25,
    'restart': 'no', 'healthcheck': {'test': ['CMD', '/usr/bin/wget', '-q', '-O', '/dev/null', 'http://127.0.0.1:8080/healthz'], 'interval': '1s', 'timeout': '1s', 'retries': 2},
}}, 'networks': {'infra': {'external': True}}}

def fetch(domain):
    return subprocess.check_output(['docker', 'exec', 'infra-nginx-1', 'wget', '-qO-', '--header', 'Host: ' + domain, 'http://127.0.0.1/'], text=True)

def save(domain, value, package):
    runner.save({'name': name, 'domain': domain, 'service': name + '-api', 'port': 8080, 'packageID': package,
        'environment': {'SMOKE_VALUE': value}}, directory, lambda _: None)

try:
    # Exercise the exact uploaded archive format, nested image validation and import.
    with tempfile.TemporaryDirectory(dir=runner.state) as temp:
        temp = pathlib.Path(temp)
        subprocess.run(['docker', 'image', 'save', '-o', str(temp / 'images.tar'), ref], check=True)
        with tarfile.open(temp / 'bundle.tar', 'w') as archive:
            data = json.dumps({'version': 1, 'name': name, 'compose': manifest}).encode()
            member = tarfile.TarInfo('project.json'); member.size = len(data)
            archive.addfile(member, io.BytesIO(data)); archive.add(temp / 'images.tar', arcname='images.tar')
        with open(temp / 'bundle.tar', 'rb') as stream:
            pack = runner.upload(name, stream, (temp / 'bundle.tar').stat().st_size)
    directory.mkdir(); atomic(directory / '.env', '# Isolated smoke test; no shared credentials\n')
    save('manager-deploy-check.localhost', 'first$literal', pack['id'])
    runner.deploy({'name': name, 'key': str(uuid.uuid4())}, directory, lambda _: None)
    assert 'SMOKE_VALUE=first$literal' in fetch('manager-deploy-check.localhost')
    container = subprocess.check_output(['docker', 'ps', '-q', '--filter', 'label=com.docker.compose.project=' + name], text=True).strip()
    subprocess.run(['docker', 'update', '--memory', '96m', '--memory-swap', '192m', '--cpus', '.6', '--pids-limit', '64', container], check=True, stdout=subprocess.DEVNULL)
    save('manager-deploy-check-new.localhost', 'second$literal', pack['id'])
    runner.deploy({'name': name, 'key': str(uuid.uuid4())}, directory, lambda _: None)
    assert 'SMOKE_VALUE=second$literal' in fetch('manager-deploy-check-new.localhost')
    container = subprocess.check_output(['docker', 'ps', '-q', '--filter', 'label=com.docker.compose.project=' + name], text=True).strip()
    host = json.loads(subprocess.check_output(['docker', 'inspect', container], text=True))[0]['HostConfig']
    assert host['Memory'] == 96 * 1024 ** 2 and host['NanoCpus'] == 600000000 and host['PidsLimit'] == 64, host
    # A bad release must recover healthy containers, destination env AND live limits.
    save('manager-deploy-check-new.localhost', 'bad-env', pack['id'])
    candidate = directory / '.manager/candidate.json'
    broken = json.loads(candidate.read_text()); broken['services'][name + '-api']['entrypoint'] = ['/bin/false']
    atomic(candidate, json.dumps(broken))
    try: runner.deploy({'name': name, 'key': str(uuid.uuid4())}, directory, lambda _: None)
    except RuntimeError as exc: assert 'previous containers and env restored' in str(exc), str(exc)
    else: raise AssertionError('Bad deployment unexpectedly succeeded')
    assert 'SMOKE_VALUE=second$literal' in fetch('manager-deploy-check-new.localhost')
    container = subprocess.check_output(['docker', 'ps', '-q', '--filter', 'label=com.docker.compose.project=' + name], text=True).strip()
    host = json.loads(subprocess.check_output(['docker', 'inspect', container], text=True))[0]['HostConfig']
    assert host['Memory'] == 96 * 1024 ** 2 and host['NanoCpus'] == 600000000 and host['PidsLimit'] == 64
    runner.save({'name': name, 'domain': 'manager-deploy-check-new.localhost', 'service': name + '-api', 'port': 9999,
        'packageID': pack['id'], 'environment': {'SMOKE_VALUE': 'wrong-route'}}, directory, lambda _: None)
    assert 'SMOKE_VALUE=second$literal' in fetch('manager-deploy-check-new.localhost')
    try: runner.deploy({'name': name, 'key': str(uuid.uuid4())}, directory, lambda _: None)
    except RuntimeError as exc: assert 'previous containers and env restored' in str(exc), str(exc)
    else: raise AssertionError('An unhealthy public route was reported as a successful deployment')
    assert 'SMOKE_VALUE=second$literal' in fetch('manager-deploy-check-new.localhost')
    print('PASS: upload, deploy, literal env, domains, owner limits, container rollback and public-route rollback')
except Exception:
    subprocess.run(['docker', 'compose', '-p', name, '-f', str(directory / 'compose.yaml'), 'ps', '-a'])
    subprocess.run(['docker', 'compose', '-p', name, '-f', str(directory / 'compose.yaml'), 'logs', '--tail', '10'])
    raise
finally:
    subprocess.run(['docker', 'compose', '-p', name, '-f', str(directory / 'compose.yaml'), 'down', '--remove-orphans'], capture_output=True)
    conf = root / 'config/nginx/conf.d' / (name + '.conf'); conf.unlink(missing_ok=True)
    subprocess.run(['docker', 'compose', 'exec', '-T', 'nginx', 'nginx', '-s', 'reload'], cwd=root, capture_output=True)
    shutil.rmtree(directory, ignore_errors=True)
    for folder in runner.state.glob('package-*'):
        identity = folder / 'identity.json'
        if identity.exists() and json.loads(identity.read_text())['name'] == name: shutil.rmtree(folder)
    subprocess.run(['docker', 'image', 'rm', ref], capture_output=True)
    pool.shutdown()
