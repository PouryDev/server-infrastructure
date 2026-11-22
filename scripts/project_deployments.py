"""Same-host project deployment adapter for the authenticated manager runner.

Only project-scoped bundles/settings are accepted. No shell or arbitrary host paths
come from HTTP. Credentials stay on the destination, outside uploaded bundles.
"""
import contextlib
import fcntl
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import tarfile
import tempfile
import time
import uuid

MAX_UPLOAD = 2 * 1024 ** 3
NAME = re.compile(r'[a-z][a-z0-9-]{1,30}')
RESERVED = {'infra', 'manager', 'postgres', 'pgbouncer', 'redis', 'rabbitmq', 'nginx', 'otel-collector', 'prometheus', 'grafana', 'tempo'}
DOMAIN = re.compile(r'(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}')
ENV_KEY = re.compile(r'[A-Za-z_][A-Za-z0-9_]{0,127}')

class MissingEnvironment(ValueError):
    pass


def check_required_environment(manifest):
    missing = set()
    for spec in manifest.get('services', {}).values():
        required = spec.get('labels', {}).get('infra.manager.required-env', '')
        if not isinstance(required, str) or len(required) > 4096:
            raise ValueError('Invalid required environment declaration')
        for key in filter(None, (part.strip() for part in required.split(','))):
            if not ENV_KEY.fullmatch(key): raise ValueError('Invalid required environment name')
            if not spec.get('environment', {}).get(key): missing.add(key)
    if missing:
        raise MissingEnvironment('Missing required destination environment variables: ' + ', '.join(sorted(missing)))


def atomic(path, value, mode=0o600):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(dir=path.parent)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'w') as stream:
            stream.write(value); stream.flush(); os.fsync(stream.fileno())
        os.replace(temp, path)
    finally:
        if os.path.exists(temp): os.unlink(temp)


def run(args, cwd, timeout=180, input=None):
    result = subprocess.run(args, cwd=cwd, input=input, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        # Compose and database commands can emit secrets. Never relay their output.
        raise RuntimeError('Command failed: ' + args[0] + '; inspect the server operation logs')
    return result.stdout


def valid_name(name):
    if not isinstance(name, str) or not NAME.fullmatch(name) or name in RESERVED:
        raise ValueError('Invalid or reserved project name')
    return name


def primary_route(text, name):
    # Keep offsets into the original file while ignoring commented examples.
    masked = re.sub(r'#[^\n]*', lambda match: ' ' * len(match[0]), text)
    domain = re.search(r'\bserver_name\s+([a-z0-9.-]+)[^;]*;', masked)
    upstream = re.search(r'http://(' + re.escape(name) + r'-[a-z0-9-]+):([0-9]+)', masked)
    return domain, upstream


def preserve_limits(manifest, live):
    """Destination runtime choices win over incoming Compose/override values."""
    fields = {'mem_limit', 'memswap_limit', 'mem_reservation', 'cpus', 'cpu_quota', 'cpu_period',
              'cpu_shares', 'cpuset', 'pids_limit'}
    for name, values in live.items():
        if name not in manifest['services']: continue
        spec = manifest['services'][name]
        for key in fields: spec.pop(key, None)
        resources = spec.get('deploy', {}).get('resources', {})
        for key in ('limits', 'reservations'):
            for resource in ('cpus', 'memory', 'pids'):
                resources.get(key, {}).pop(resource, None)
        spec.update(values)
    return manifest


def validate_manifest(raw, name, data):
    """Enforce the shared-infrastructure contract before any Docker load/start."""
    valid_name(name)
    if not isinstance(raw, dict): raise ValueError('Compose manifest is required')
    doc = json.loads(json.dumps(raw))
    doc = {k: v for k, v in doc.items() if not k.startswith('x-')}
    if set(doc) - {'name', 'services', 'networks'} or not isinstance(doc.get('services'), dict) or not doc['services'] or len(doc['services']) > 24:
        raise ValueError('Bundle must contain project services and the external infra network only')
    net = doc.get('networks', {})
    if set(net) != {'infra'} or net['infra'].get('external') is not True or net['infra'].get('name', 'infra') != 'infra':
        raise ValueError('Use only the external infra network')
    allowed = {'image', 'entrypoint', 'command', 'environment', 'env_file', 'volumes', 'mem_limit', 'memswap_limit',
        'cpus', 'cpu_shares', 'read_only', 'tmpfs', 'user', 'cap_drop', 'security_opt', 'restart', 'stop_grace_period',
        'stop_signal', 'logging', 'healthcheck', 'networks', 'depends_on', 'profiles', 'init', 'working_dir', 'expose', 'labels'}
    services = doc['services']
    for service, spec in services.items():
        if not re.fullmatch(re.escape(name) + r'-[a-z0-9-]+', service) or not isinstance(spec, dict) or set(spec) - allowed:
            raise ValueError('Unsupported service option or missing project prefix: ' + service)
        image = spec.get('image', '')
        if not re.fullmatch(re.escape(name) + r'-[a-z0-9-]+:[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}', image) or image.endswith(':latest'):
            raise ValueError('Use immutable project-prefixed images: ' + service)
        networks = spec.get('networks', {})
        if isinstance(networks, list): networks = dict.fromkeys(networks)
        if set(networks) != {'infra'} or networks['infra'] not in (None, {}):
            raise ValueError('Service must join infra without aliases: ' + service)
        if str(spec.get('user')) not in ('65532', '65532:65532') or spec.get('read_only') is not True or spec.get('cap_drop') != ['ALL']:
            raise ValueError('Use uid 65532, read_only and cap_drop ALL: ' + service)
        if not any(x in ('no-new-privileges', 'no-new-privileges:true') for x in spec.get('security_opt', [])) or any(x not in ('no-new-privileges', 'no-new-privileges:true') for x in spec.get('security_opt', [])):
            raise ValueError('Use only no-new-privileges security option: ' + service)
        mem = spec.get('mem_limit', 0)
        if isinstance(mem, str):
            match = re.fullmatch(r'([0-9]+)([kmg]?)b?', mem.lower())
            mem = int(match[1]) * {'': 1, 'k': 1024, 'm': 1024 ** 2, 'g': 1024 ** 3}[match[2]] if match else 0
        if not 16 * 1024 ** 2 <= mem <= 2 * 1024 ** 3 or not 0 < float(spec.get('cpus', 0)) <= 4:
            raise ValueError('Set bounded memory and CPU for every service: ' + service)
        for mount in spec.get('volumes', []):
            if not isinstance(mount, dict) or mount.get('type') != 'bind' or set(mount) - {'type', 'source', 'target', 'read_only', 'bind'}:
                raise ValueError('Use project data bind mounts only')
            source = pathlib.PurePosixPath(mount.get('source', ''))
            expected = pathlib.PurePosixPath('/srv/data/apps') / name
            actual = pathlib.PurePosixPath(data) / 'apps' / name
            if '..' in source.parts or not (source.is_relative_to(expected) or source.is_relative_to(actual)):
                raise ValueError('Bind sources must stay inside this project data directory')
            suffix = source.relative_to(expected) if source.is_relative_to(expected) else source.relative_to(actual)
            mount['source'] = str(actual / suffix)
            if not pathlib.PurePosixPath(mount.get('target', '')).is_absolute(): raise ValueError('Use absolute container mount targets')
        for item in spec.get('tmpfs', []):
            if not isinstance(item, str) or not item.startswith('/tmp') or '..' in item:
                raise ValueError('Scratch mounts must stay under /tmp')
        if any(x not in services for x in spec.get('depends_on', {})):
            raise ValueError('Dependencies must belong to the same project')
        labels = spec.get('labels', {})
        if not isinstance(labels, dict) or any(x.startswith('com.docker.') for x in labels):
            raise ValueError('Reserved Docker labels are forbidden')
        files = spec.get('env_file', [])
        for item in files:
            path = item.get('path', '') if isinstance(item, dict) else item
            if path not in ('.env', '.runtime.env'):
                raise ValueError('Only destination .env and .runtime.env can be referenced')
        # Runtime env is raw: dollar signs and quotes in secrets remain literal.
        spec['env_file'] = ['.env', {'path': '.runtime.env', 'format': 'raw'}]
        if not spec.get('profiles') and not spec.get('healthcheck'):
            raise ValueError('Long-running services need a healthcheck: ' + service)
    doc['name'] = name
    return doc


class Projects:
    def __init__(self, root, state, pool, token=''):
        self.root = pathlib.Path(root)
        self.state = pathlib.Path(state) / 'projects'
        self.state.mkdir(parents=True, exist_ok=True)
        self.pool = pool
        # Read deployment paths only, never the infra's credentials into public state.
        raw = run(['bash', '-c', 'set -a; . ./.env; python3 -c \'import os,json; print(json.dumps({k:os.environ.get(k, "") for k in ("APPS_DIR","DATA_DIR","NGINX_HTTP_PORT","REGISTRY")}))\''], self.root)
        paths = json.loads(raw)
        self.apps, self.data = pathlib.Path(paths['APPS_DIR']), pathlib.Path(paths['DATA_DIR'])
        self.edge_port = int(paths.get('NGINX_HTTP_PORT') or 80)
        for path in self.state.glob('job-*.json'):
            job = json.loads(path.read_text())
            if job['status'] in ('queued', 'running'):
                job.update(status='interrupted', message='Runner restarted; inspect the last phase before retrying')
                atomic(path, json.dumps(job))

    @contextlib.contextmanager
    def lock(self):
        # Also used by storefront provisioning; serialize all nginx changes.
        folder = self.apps / '.storefront'; folder.mkdir(exist_ok=True)
        with open(folder / 'provision.lock', 'a+') as stream:
            fcntl.flock(stream, fcntl.LOCK_EX)
            yield

    def directory(self, name):
        path = self.apps / valid_name(name)
        if path.is_symlink() or not path.resolve().is_relative_to(self.apps.resolve()):
            raise ValueError('Project path must not be a symlink')
        for relative in ('.env', '.runtime.env', 'compose.yaml', '.manager', '.manager/project.json', '.manager/candidate.json'):
            if (path / relative).is_symlink(): raise ValueError('Project configuration must not contain symlinks')
        return path

    def compose(self, directory):
        if not (directory / 'compose.yaml').exists(): return {'services': {}}
        return json.loads(run(['docker', 'compose', '--profile', '*', '--project-directory', str(directory), '-f', str(directory / 'compose.yaml'),
            'config', '--format', 'json', '--no-interpolate', '--no-env-resolution', '--no-path-resolution'], directory))

    def revision(self, directory):
        digest = hashlib.sha256()
        for path in [directory / '.manager/project.json', directory / '.manager/candidate.json', directory / '.runtime.env',
                     directory / 'compose.yaml', self.root / 'config/nginx/conf.d' / (directory.name + '.conf')]:
            digest.update(str(path).encode()); digest.update(path.read_bytes() if path.exists() else b'')
        return digest.hexdigest()

    def detail(self, name):
        directory = self.directory(name)
        stored = directory / '.manager/project.json'
        config = json.loads(stored.read_text()) if stored.exists() else {'name': name, 'domain': '', 'service': '', 'port': 8080, 'migrationService': '', 'packageID': ''}
        candidate = directory / '.manager/candidate.json'
        manifest = json.loads(candidate.read_text()) if candidate.exists() else self.compose(directory)
        if not stored.exists():
            conf = self.root / 'config/nginx/conf.d' / (name + '.conf')
            if conf.exists():
                text = conf.read_text()
                domain, upstream = primary_route(text, name)
                if domain: config['domain'] = domain[1]
                if upstream: config.update(service=upstream[1], port=int(upstream[2]))
        config.update(revision=self.revision(directory), services=list(manifest.get('services', {})),
            images=sorted({s.get('image', '') for s in manifest.get('services', {}).values()}),
            envKeys=sorted(self.read_env(directory)), provisioned=(directory / '.env').exists(),
            deployed=(directory / 'compose.yaml').exists(), configured=stored.exists())
        return config

    def catalog(self):
        projects = []
        for directory in sorted(self.apps.iterdir()):
            if directory.is_dir() and NAME.fullmatch(directory.name) and directory.name not in RESERVED and ((directory / '.env').exists() or (directory / '.manager').exists()):
                projects.append(self.detail(directory.name))
        jobs = [self.public_job(json.loads(p.read_text())) for p in self.state.glob('job-*.json')]
        jobs.sort(key=lambda x: x['createdAt'], reverse=True)
        return {'projects': projects, 'jobs': jobs[:100], 'appsDir': str(self.apps), 'dataDir': str(self.data), 'maxUploadBytes': MAX_UPLOAD, 'edgePort': getattr(self, 'edge_port', 80)}

    def read_env(self, directory):
        result = {}
        path = directory / '.runtime.env'
        if path.exists():
            for line in path.read_text().splitlines():
                key, sep, value = line.partition('=')
                if sep and ENV_KEY.fullmatch(key): result[key] = value
        return result

    def upload(self, name, stream, size):
        valid_name(name)
        if not 0 < size <= MAX_UPLOAD: raise ValueError('Package exceeds the 2 GiB upload limit')
        with self.lock():
            if shutil.disk_usage(self.state).free < size * 2 + 256 * 1024 ** 2: raise ValueError('Insufficient free space for this package')
            identity = str(uuid.uuid4())
            directory = self.state / ('package-' + identity); directory.mkdir(mode=0o700)
            try:
                archive = directory / 'bundle.tar'
                with open(archive, 'xb') as target:
                    os.chmod(archive, 0o600)
                    remaining = size
                    while remaining:
                        chunk = stream.read(min(1024 * 1024, remaining))
                        if not chunk: raise ValueError('Upload was interrupted')
                        target.write(chunk); remaining -= len(chunk)
                with tarfile.open(archive, 'r:') as bundle:
                    members = []
                    for member in bundle:
                        members.append(member)
                        if len(members) > 2: raise ValueError('Package contains unexpected files')
                    if len(members) != 2 or {x.name for x in members} != {'project.json', 'images.tar'} or any(not x.isfile() for x in members):
                        raise ValueError('Choose a package created with manager-package')
                    metadata_member = next(x for x in members if x.name == 'project.json')
                    image_member = next(x for x in members if x.name == 'images.tar')
                    if metadata_member.size > 512 * 1024: raise ValueError('Compose metadata is too large')
                    metadata = json.load(bundle.extractfile(metadata_member))
                    if metadata.get('version') != 1 or metadata.get('name') != name: raise ValueError('Package project does not match the selected name')
                    manifest = validate_manifest(metadata['compose'], name, self.data)
                    with open(directory / 'images.tar', 'xb') as images:
                        shutil.copyfileobj(bundle.extractfile(image_member), images)
                self.inspect_images(directory / 'images.tar', manifest, name)
                atomic(directory / 'compose.json', json.dumps(manifest))
                atomic(directory / 'identity.json', json.dumps({'name': name}))
                archive.unlink()
                return {'id': identity, 'name': name, 'services': list(manifest['services']), 'images': sorted({s['image'] for s in manifest['services'].values()})}
            except Exception:
                shutil.rmtree(directory)
                raise

    def inspect_images(self, archive, manifest, name):
        wanted = {s['image'] for s in manifest['services'].values()}
        found = {}
        with tarfile.open(archive, 'r:') as images:
            seen = set()
            for member in images:
                if member.name in seen or len(seen) >= 50000: raise ValueError('Ambiguous or excessive image archive members')
                seen.add(member.name)
            def descriptor_ids(descriptor, config_id, depth=0):
                # Docker's containerd store reports an OCI manifest/index digest as
                # .Id; the classic store reports the config digest. Verify the
                # content-addressed chain instead of assuming either store layout.
                digest = descriptor.get('digest', '')
                if depth > 4 or not re.fullmatch(r'sha256:[a-f0-9]{64}', digest): return set()
                try: member = images.getmember('blobs/sha256/' + digest.split(':')[1])
                except KeyError: return set()  # unexported architectures in a multi-arch index
                if not member.isfile() or member.size > 4 * 1024 ** 2: raise ValueError('Invalid OCI descriptor')
                content = images.extractfile(member).read()
                if 'sha256:' + hashlib.sha256(content).hexdigest() != digest: raise ValueError('OCI descriptor digest mismatch')
                value = json.loads(content)
                if value.get('config', {}).get('digest') == config_id: return {digest}
                children = set()
                for child in value.get('manifests', []): children |= descriptor_ids(child, config_id, depth + 1)
                return children | {digest} if children else set()
            index = {}
            if 'index.json' in images.getnames():
                info = images.getmember('index.json')
                if not info.isfile() or info.size > 128 * 1024: raise ValueError('Invalid OCI index')
                index = json.load(images.extractfile(info))
            indexed = {}
            for descriptor in index.get('manifests', []):
                annotations = descriptor.get('annotations', {})
                ref = annotations.get('io.containerd.image.name', annotations.get('org.opencontainers.image.ref.name', ''))
                if ref.startswith('docker.io/library/'): ref = ref[len('docker.io/library/'):]
                if ref not in wanted or ref in indexed: raise ValueError('OCI index contains unexpected or duplicate image tags')
                indexed[ref] = descriptor
            try: info = images.getmember('manifest.json')
            except KeyError: raise ValueError('Image archive must be produced by docker image save')
            if not info.isfile() or info.size > 128 * 1024: raise ValueError('Invalid Docker image manifest')
            entries = json.load(images.extractfile(info))
            for entry in entries:
                tags = entry.get('RepoTags') or []
                config = images.getmember(entry['Config'])
                if not config.isfile() or config.size > 4 * 1024 ** 2: raise ValueError('Invalid image config')
                raw = images.extractfile(config).read()
                image_id = 'sha256:' + hashlib.sha256(raw).hexdigest()
                accepted_ids = {image_id}
                for descriptor in index.get('manifests', []): accepted_ids |= descriptor_ids(descriptor, image_id)
                details = json.loads(raw)
                if details.get('os') != 'linux' or details.get('architecture') != 'amd64': raise ValueError('Build images for linux/amd64')
                if not tags: raise ValueError('Every image must have an immutable project tag')
                for ref in tags:
                    if ref not in wanted or not ref.startswith(name + '-') or ref.endswith(':latest') or ref in found:
                        raise ValueError('Archive contains unexpected or duplicate image tags')
                    if ref in indexed and not descriptor_ids(indexed[ref], image_id): raise ValueError('OCI index does not match its project image config')
                    existing = subprocess.run(['docker', 'image', 'inspect', ref, '--format', '{{.Id}}'], capture_output=True, text=True)
                    if existing.returncode == 0 and existing.stdout.strip() not in accepted_ids:
                        raise ValueError('An existing image tag has different contents; build with a new immutable tag')
                    found[ref] = accepted_ids
        if set(found) != wanted: raise ValueError('Package does not contain every Compose image')
        return found

    def package(self, identity, name):
        if not re.fullmatch(r'[a-f0-9-]{36}', identity): raise ValueError('Invalid package ID')
        directory = self.state / ('package-' + identity)
        if not (directory / 'identity.json').exists() or json.loads((directory / 'identity.json').read_text())['name'] != name:
            raise ValueError('Upload a package belonging to this project')
        return directory, json.loads((directory / 'compose.json').read_text())

    def validate_request(self, req, action):
        allowed = {'name', 'key', 'revision'} | ({'domain', 'service', 'port', 'migrationService', 'packageID', 'environment', 'removeEnv'} if action in ('save', 'launch') else set())
        if action == 'launch': allowed.add('manifest')
        if not isinstance(req, dict) or set(req) - allowed: raise ValueError('Unknown deployment option')
        valid_name(req.get('name'))
        if not isinstance(req.get('key'), str) or not re.fullmatch(r'[a-f0-9-]{36}', req['key']): raise ValueError('Invalid request key')
        if not isinstance(req.get('revision', ''), str) or len(req.get('revision', '')) > 64: raise ValueError('Invalid revision')
        if action in ('save', 'launch'):
            if not DOMAIN.fullmatch(req.get('domain', '')): raise ValueError('Enter a domain without scheme, port or path')
            if not isinstance(req.get('port'), int) or not 1 <= req['port'] <= 65535: raise ValueError('Invalid upstream port')
            env = req.get('environment', {})
            removed = req.get('removeEnv', [])
            if not isinstance(env, dict) or not isinstance(removed, list) or len(env) + len(removed) > 200: raise ValueError('Too many environment edits')
            for key, value in env.items():
                if not ENV_KEY.fullmatch(key) or not isinstance(value, str) or len(value) > 32768 or any(c in value for c in '\r\n\0'):
                    raise ValueError('Use valid environment keys and single-line values')
            if any(not isinstance(key, str) or not ENV_KEY.fullmatch(key) for key in removed): raise ValueError('Invalid environment deletion')

    def preview(self, req):
        self.validate_request(req, 'launch')
        manifest = validate_manifest(req.get('manifest'), req['name'], self.data)
        if req.get('service') not in manifest['services'] or manifest['services'][req['service']].get('profiles'):
            raise ValueError('Choose an always-running HTTP service')
        if req.get('migrationService') and req['migrationService'] not in manifest['services']:
            raise ValueError('Migration service is missing from the source Compose file')
        self.check_domain(req['name'], req['domain'])
        directory = self.directory(req['name'])
        revision = self.revision(directory) if directory.exists() else ''
        if req.get('revision', '') != revision: raise ValueError('Project settings changed; refresh before saving')
        return {'revision': revision, 'envKeys': sorted(self.read_env(directory)), 'appsDir': str(self.apps), 'dataDir': str(self.data)}

    def job(self, key):
        if not isinstance(key, str) or not re.fullmatch(r'[a-f0-9-]{36}', key): raise ValueError('Invalid job ID')
        path = self.state / ('job-' + key + '.json')
        if not path.exists(): raise ValueError('Deployment job was not found')
        return self.public_job(json.loads(path.read_text()))

    def enqueue(self, action, req):
        self.validate_request(req, action)
        if action == 'launch': self.preview(req)
        with self.lock():
            path = self.state / ('job-' + req['key'] + '.json')
            fingerprint = hashlib.sha256(json.dumps({'action': action, **req}, sort_keys=True).encode()).hexdigest()
            if path.exists():
                job = json.loads(path.read_text())
                if job['fingerprint'] != fingerprint: raise ValueError('Request key already used for different settings')
                return self.public_job(job)
            if any(json.loads(p.read_text())['status'] in ('queued', 'running') for p in self.state.glob('job-*.json')):
                raise ValueError('Another project deployment is active')
            directory = self.directory(req['name'])
            expected = self.revision(directory) if directory.exists() else ''
            if req.get('revision', '') != expected: raise ValueError('Project settings changed; refresh before saving')
            job = {'id': req['key'], 'name': req['name'], 'action': action, 'phase': 'queued', 'status': 'queued', 'createdAt': int(time.time()), 'fingerprint': fingerprint}
            atomic(path, json.dumps(job))
            self.pool.submit(self.execute, path, req)
            return self.public_job(job)

    @staticmethod
    def public_job(job): return {k: v for k, v in job.items() if k != 'fingerprint'}

    def execute(self, path, req):
        job = json.loads(path.read_text())
        def phase(value):
            job.update(status='running', phase=value); atomic(path, json.dumps(job))
        try:
            with self.lock():
                directory = self.directory(req['name'])
                expected = self.revision(directory) if directory.exists() else ''
                if req.get('revision', '') != expected: raise ValueError('Project settings changed; refresh before saving')
                directory.mkdir(exist_ok=True)
                (directory / '.manager').mkdir(exist_ok=True, mode=0o700)
                # Same lock filename as the shipping adapter.
                with open(directory / '.manager/deploy.lock', 'a+') as project_lock:
                    fcntl.flock(project_lock, fcntl.LOCK_EX)
                    if job['action'] in ('save', 'launch'): self.save(req, directory, phase, route_now=job['action'] == 'save')
                    if job['action'] in ('deploy', 'launch'): self.deploy(req, directory, phase)
            job.update(status='ready', phase='ready', message='')
        except Exception as exc:
            job.update(status='failed', message=str(exc))
        atomic(path, json.dumps(job))

    def check_domain(self, name, domain):
        for conf in (self.root / 'config/nginx/conf.d').glob('*.conf'):
            if conf.name == name + '.conf': continue
            # Tokenize every server_name directive, including aliases on one line.
            names = [token for line in re.findall(r'server_name\s+([^;]+);', conf.read_text()) for token in line.split()]
            if domain in names or any(x.startswith('*.') and domain.endswith(x[1:]) for x in names):
                raise ValueError('Domain is already routed by another project')

    def route(self, name, domain, service, port, force_reload=False):
        self.check_domain(name, domain)
        conf = self.root / 'config/nginx/conf.d' / (name + '.conf')
        previous = conf.read_text() if conf.exists() else None
        text = '# Managed project: ' + name + '\nserver {\n listen 80;\n server_name ' + domain + ';\n set $project_upstream http://' + service + ':' + str(port) + ';\n location / {\n  proxy_pass $project_upstream;\n  include /etc/nginx/snippets/proxy.conf;\n }\n}\n'
        if previous is not None:
            old_domain, old_upstream = primary_route(previous, name)
            if not old_domain or not old_upstream:
                raise ValueError('Existing nginx routing cannot be safely adopted; configure its primary project upstream first')
            if domain != old_domain[1]:
                other_names = [token for line in re.findall(r'server_name\s+([^;]+);', previous) for token in line.split()][1:]
                if domain in other_names: raise ValueError('Domain already belongs to another route of this project')
            edits = [(old_domain.start(1), old_domain.end(1), domain),
                     (old_upstream.start(), old_upstream.end(), 'http://' + service + ':' + str(port))]
            text = previous
            for start, end, value in sorted(edits, reverse=True): text = text[:start] + value + text[end:]
            if text == previous:
                if force_reload:
                    run(['docker', 'compose', 'exec', '-T', 'nginx', 'nginx', '-t'], self.root)
                    run(['docker', 'compose', 'exec', '-T', 'nginx', 'nginx', '-s', 'reload'], self.root)
                return
        atomic(conf, text, 0o644)
        try:
            run(['docker', 'compose', 'exec', '-T', 'nginx', 'nginx', '-t'], self.root)
            run(['docker', 'compose', 'exec', '-T', 'nginx', 'nginx', '-s', 'reload'], self.root)
        except Exception:
            if previous is None: conf.unlink(missing_ok=True)
            else: atomic(conf, previous, 0o644)
            run(['docker', 'compose', 'exec', '-T', 'nginx', 'nginx', '-s', 'reload'], self.root)
            raise RuntimeError('Nginx validation/reload failed; previous domain configuration restored')

    def snapshot(self, directory, destination):
        destination.mkdir(parents=True, exist_ok=True, mode=0o700)
        for relative in ('compose.yaml', '.runtime.env', 'manager.override.yaml'):
            source = directory / relative
            if source.exists(): atomic(destination / relative, source.read_text())
            else: (destination / relative).unlink(missing_ok=True)
        conf = self.root / 'config/nginx/conf.d' / (directory.name + '.conf')
        if conf.exists(): atomic(destination / 'nginx.conf', conf.read_text())
        else: (destination / 'nginx.conf').unlink(missing_ok=True)

    def restore_route(self, name, snapshot):
        conf = self.root / 'config/nginx/conf.d' / (name + '.conf')
        if (snapshot / 'nginx.conf').exists(): atomic(conf, (snapshot / 'nginx.conf').read_text(), 0o644)
        else: conf.unlink(missing_ok=True)
        run(['docker', 'compose', 'exec', '-T', 'nginx', 'nginx', '-t'], self.root)
        run(['docker', 'compose', 'exec', '-T', 'nginx', 'nginx', '-s', 'reload'], self.root)

    def verify_route(self, domain):
        script = '''import sys,urllib.request
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs): return None
request=urllib.request.Request('http://nginx/healthz',headers={'Host':sys.argv[1],'X-Forwarded-Proto':'http' if sys.argv[1].endswith('.localhost') else 'https'})
with urllib.request.build_opener(NoRedirect).open(request,timeout=3) as response:
 assert response.status==200
'''
        for attempt in range(3):
            try:
                run(['docker', 'run', '--rm', '--network', 'infra', '--read-only', '--user', '65532:65532',
                    '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--memory', '64m', '--cpus', '0.2',
                    '--entrypoint', 'python3', 'infra-storefront-provisioner:local', '-c', script, domain], self.root, timeout=15)
                return
            except Exception:
                if attempt == 2: raise RuntimeError('Project HTTP route is unhealthy; check the selected upstream service and port')
                time.sleep(.5)

    def live_limits(self, name):
        ids = run(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=' + name], self.root).split()
        if not ids: return {}
        containers = json.loads(run(['docker', 'inspect', *ids], self.root))
        result = {}
        for container in containers:
            labels = container['Config'].get('Labels') or {}
            if labels.get('com.docker.compose.oneoff', '').lower() == 'true': continue
            service = labels.get('com.docker.compose.service', '')
            if not service.startswith(name + '-'): continue
            host = container['HostConfig']
            values = {'mem_limit': host.get('Memory', 0), 'memswap_limit': host.get('MemorySwap', 0),
                'mem_reservation': host.get('MemoryReservation', 0), 'cpu_shares': host.get('CpuShares', 0),
                'cpuset': host.get('CpusetCpus', ''), 'pids_limit': host.get('PidsLimit') or 0}
            nano = host.get('NanoCpus', 0)
            if nano:
                values['cpus'] = nano / 1e9
            elif host.get('CpuQuota', 0) > 0:
                values.update(cpu_quota=host['CpuQuota'], cpu_period=host.get('CpuPeriod') or 100000)
            else: values['cpus'] = 0
            if service in result and result[service] != values:
                raise ValueError('Replicas have different resource limits; make them consistent before deploying')
            result[service] = values
        return result

    def prepare_data(self, manifest, name):
        base = self.data / 'apps' / name
        for spec in manifest['services'].values():
            for mount in spec.get('volumes', []):
                if not isinstance(mount, dict) or mount.get('type') != 'bind': continue
                path = pathlib.Path(mount['source'])
                if not path.resolve().is_relative_to(base.resolve()): raise ValueError('Project data bind contains an escaping symlink')
                if path.exists(): continue
                if mount.get('bind', {}).get('create_host_path') is False: raise ValueError('Create the project data bind before deployment')
                path.mkdir(parents=True, exist_ok=True)
                # The broker itself has no CHOWN capability. A bounded helper
                # changes only newly created directories, never existing data.
                run(['docker', 'run', '--rm', '--network', 'none', '--read-only', '--memory', '32m', '--cpus', '0.1',
                    '--cap-drop', 'ALL', '--cap-add', 'CHOWN', '--cap-add', 'DAC_OVERRIDE',
                    '-v', str(path) + ':/data', '--entrypoint', 'chown', 'infra-storefront-provisioner:local', '65532:65532', '/data'], self.root)

    def save(self, req, directory, phase, route_now=True):
        name = req['name']
        self.check_domain(name, req['domain'])
        if req.get('manifest'):
            manifest = validate_manifest(req['manifest'], name, self.data)
        elif req.get('packageID'):
            _, manifest = self.package(req['packageID'], name)
        elif (directory / '.manager/candidate.json').exists():
            manifest = json.loads((directory / '.manager/candidate.json').read_text())
        else: manifest = self.compose(directory)
        # Existing projects can retain additional env files; destination overrides
        # are appended to every service, and local package env is never imported.
        for spec in manifest.get('services', {}).values():
            files = spec.get('env_file', [])
            files = [f for f in files if (f.get('path') if isinstance(f, dict) else f) != '.runtime.env']
            spec['env_file'] = files + [{'path': '.runtime.env', 'format': 'raw'}]
        services = manifest.get('services', {})
        service = req.get('service', '')
        migration = req.get('migrationService', '')
        if service not in services or not service.startswith(name + '-') or services[service].get('profiles'):
            raise ValueError('Choose an always-running HTTP service from this project')
        if migration and (migration not in services or migration == service): raise ValueError('Choose a separate migration service')
        if migration:
            manifest['services'][migration]['profiles'] = ['tools']
        if not (directory / '.env').exists():
            journal = directory / '.manager/provision.json'
            if journal.exists(): raise RuntimeError('Partial provisioning requires inspecting shared resources; it will not be replayed automatically')
            phase('provisioning'); atomic(journal, json.dumps({'startedAt': int(time.time())}))
            # Run the infrastructure's own make/script environment with its real
            # host paths. The web/runner containers retain read-only source mounts;
            # only this bounded bootstrap helper writes the project-specific
            # observability configuration required by new-project.sh.
            run(['docker', 'run', '--rm', '--network', 'host', '--read-only', '--memory', '256m', '--cpus', '0.5',
                '--cap-drop', 'ALL', '--cap-add', 'DAC_OVERRIDE', '--cap-add', 'CHOWN', '--cap-add', 'FOWNER',
                '--tmpfs', '/tmp', '-v', str(self.root) + ':' + str(self.root), '-v', str(self.apps) + ':' + str(self.apps),
                '-v', str(self.data) + ':' + str(self.data), '-v', '/var/run/docker.sock:/var/run/docker.sock',
                '-w', str(self.root), '--entrypoint', 'bash', 'infra-storefront-provisioner:local',
                'scripts/new-project.sh', name], self.root, timeout=300)
            atomic(journal, json.dumps({'complete': True}))
        active = directory / '.manager/deployments/active'
        if (directory / 'compose.yaml').exists() and not active.exists() and not (directory / '.manager/project.json').exists(): self.snapshot(directory, active)
        values = self.read_env(directory)
        values.update(req.get('environment', {}))
        for key in req.get('removeEnv', []): values.pop(key, None)
        # The destination base .env remains untouched. Only app overrides are edited.
        runtime = ''.join(key + '=' + value + '\n' for key, value in sorted(values.items()))
        phase('routing' if route_now else 'configuring')
        # No settings/env mutation if nginx rejects the domain change.
        conf = self.root / 'config/nginx/conf.d' / (name + '.conf')
        current_domain, current_upstream = primary_route(conf.read_text(), name) if conf.exists() else (None, None)
        same_upstream = current_upstream and current_upstream[1] == service and int(current_upstream[2]) == req['port']
        # New code/ports are routed only after becoming healthy. A domain-only
        # edit for the running upstream can be applied immediately and safely.
        if route_now and (not (directory / 'compose.yaml').exists() or same_upstream):
            self.route(name, req['domain'], service, req['port'])
            if active.exists() and conf.exists(): atomic(active / 'nginx.conf', conf.read_text())
        atomic(directory / '.runtime.env', runtime)
        atomic(directory / '.manager/candidate.json', json.dumps(manifest, indent=2))
        config = {k: req.get(k, '') for k in ('name', 'domain', 'service', 'port', 'migrationService', 'packageID')}
        if not config['packageID'] and not req.get('manifest') and (directory / '.manager/project.json').exists():
            config['packageID'] = json.loads((directory / '.manager/project.json').read_text()).get('packageID', '')
        atomic(directory / '.manager/project.json', json.dumps(config))

    def deploy(self, req, directory, phase):
        config_path = directory / '.manager/project.json'
        if not config_path.exists(): raise ValueError('Save project/domain settings before deploying')
        config = json.loads(config_path.read_text())
        candidate = directory / '.manager/candidate.json'
        manifest = json.loads(candidate.read_text())
        name = req['name']
        if config.get('packageID'):
            package, _ = self.package(config['packageID'], name)
            phase('images')
            expected = self.inspect_images(package / 'images.tar', manifest, name)
            run(['docker', 'image', 'load', '--quiet', '-i', str(package / 'images.tar')], self.root, timeout=600)
            for ref, identities in expected.items():
                if run(['docker', 'image', 'inspect', ref, '--format', '{{.Id}}'], self.root).strip() not in identities:
                    raise RuntimeError('Destination image verification failed')
        self.check_domain(name, config['domain'])
        phase('preserving-limits')
        live = self.live_limits(name)
        active = directory / '.manager/deployments/active'
        previous = (active / 'compose.yaml').exists()
        release = directory / '.manager/deployments' / req['key']
        self.snapshot(directory, release)
        # Keep last-known-good env separately from settings edited for this deployment.
        if previous:
            for rel in ('compose.yaml', '.runtime.env', 'manager.override.yaml'):
                if (active / rel).exists(): atomic(release / rel, (active / rel).read_text())
                else: (release / rel).unlink(missing_ok=True)
        args = ['docker', 'compose', '-p', name, '--project-directory', str(directory), '-f', str(directory / 'compose.yaml')]
        try:
            render = ['docker', 'compose', '--profile', '*', '-p', name, '--project-directory', str(directory)]
            if (directory / '.tag.next').exists(): render += ['--env-file', str(directory / '.tag.next')]
            render += ['-f', str(candidate)]
            if (directory / 'manager.override.yaml').exists(): render += ['-f', str(directory / 'manager.override.yaml')]
            manifest = json.loads(run(render + ['config', '--format', 'json', '--no-env-resolution', '--no-path-resolution'], directory))
            values = self.read_env(directory)
            for spec in manifest['services'].values():
                explicit = spec.get('environment', {})
                for key in values.keys() & explicit.keys(): explicit[key] = values[key].replace('$', '$$')
                spec['environment'] = explicit
            preserve_limits(manifest, live)
            self.prepare_data(manifest, name)
            atomic(directory / 'compose.yaml', json.dumps(manifest, indent=2))
            # The manifest now includes existing env.d wiring and destination limits.
            # Never reapply an older override on top of these captured live values.
            args = ['docker', 'compose', '-p', name, '--project-directory', str(directory), '-f', str(directory / 'compose.yaml')]
            run(args + ['config', '--quiet'], directory)
            # Resolve destination env in memory only. Report missing names, never values,
            # before running migrations or touching the current service containers.
            check_required_environment(json.loads(run(args + ['config', '--format', 'json'], directory)))
            migration = config.get('migrationService')
            if migration:
                phase('migrating')
                run(args + ['run', '--rm', '--no-deps', '--pull', 'never', migration], directory, timeout=600)
            # Migrations can take minutes. Preserve owner changes made while they
            # were running as well, immediately before recreating the services.
            phase('preserving-limits')
            live.update(self.live_limits(name))
            preserve_limits(manifest, live)
            atomic(directory / 'compose.yaml', json.dumps(manifest, indent=2))
            phase('starting')
            run(args + ['up', '-d', '--no-build', '--pull', 'never', '--wait', '--wait-timeout', '120'], directory, timeout=180)
            phase('routing')
            self.route(name, config['domain'], config['service'], config['port'], force_reload=True)
            self.verify_route(config['domain'])
            self.snapshot(directory, active)
        except Exception as exc:
            if isinstance(exc, MissingEnvironment): raise
            if previous:
                phase('rollback')
                try: live.update(self.live_limits(name))
                except Exception: pass  # restore files with the last captured limits even if Docker is unavailable
                for rel in ('compose.yaml', '.runtime.env', 'manager.override.yaml'):
                    if (release / rel).exists(): atomic(directory / rel, (release / rel).read_text())
                    else: (directory / rel).unlink(missing_ok=True)
                rollback = self.compose(directory)
                preserve_limits(rollback, live)
                atomic(directory / 'compose.yaml', json.dumps(rollback, indent=2))
                try:
                    run(args + ['up', '-d', '--no-build', '--pull', 'never', '--wait', '--wait-timeout', '120'], directory, timeout=180)
                    self.restore_route(name, active)
                    if (active / 'nginx.conf').exists():
                        restored_domain, _ = primary_route((active / 'nginx.conf').read_text(), name)
                        if restored_domain: self.verify_route(restored_domain[1])
                except Exception:
                    raise RuntimeError('Deployment and container rollback failed; inspect the server. Database migrations are never rolled back automatically') from exc
                raise RuntimeError('Deployment failed; previous containers and env restored. Database migrations are never rolled back automatically') from exc
            # Preserve diagnostics and explicitly distinguish the first deployment.
            raise RuntimeError('First deployment failed; no previous healthy release exists. Inspect the recorded phase and application logs') from exc
