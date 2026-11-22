#!/usr/bin/env python3
"""Package already-built project images and Compose, without env files or data."""
import argparse
import io
import json
import os
import pathlib
import re
import subprocess
import tarfile
import tempfile


def package(project, tag, output):
    project = pathlib.Path(project).resolve()
    spec = (project / 'ship.conf').read_text()
    def value(key):
        match = re.search(r'^' + key + r'=[\"\']?([a-zA-Z0-9_./-]+)[\"\']?\s*$', spec, re.M)
        if not match:
            raise ValueError('ship.conf needs a literal ' + key)
        return match[1]
    name = value('APP')
    if not re.fullmatch(r'[a-z][a-z0-9-]{1,30}', name):
        raise ValueError('Invalid APP')
    if not re.fullmatch(r'[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}', tag) or tag == 'latest':
        raise ValueError('Use the immutable tag of your built images')
    compose = (project / value('COMPOSE_FILE')).resolve()
    if not compose.is_relative_to(project):
        raise ValueError('COMPOSE_FILE must be inside the project')
    with tempfile.TemporaryDirectory(prefix='manager-package-') as staging:
        stage = pathlib.Path(staging)
        (stage / 'empty.env').touch()
        # Deliberately ignore the project's local .env and the calling shell's app settings.
        env = {k: v for k, v in os.environ.items() if k in ('PATH', 'HOME', 'DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_CONFIG', 'XDG_RUNTIME_DIR')}
        env.update(TAG=tag, APP_DATA_DIR='/srv/data/apps/' + name)
        raw = subprocess.check_output(['docker', 'compose', '--profile', '*', '--env-file', str(stage / 'empty.env'),
            '--project-directory', str(project), '-f', str(compose), 'config', '--format', 'json',
            '--no-env-resolution', '--no-path-resolution'], env=env)
        config = json.loads(raw)
        refs = sorted({s['image'] for s in config['services'].values()})
        if any(not re.fullmatch(re.escape(name) + r'-[a-z0-9-]+:[a-zA-Z0-9_.-]+', ref) or ref.endswith(':latest') for ref in refs):
            raise ValueError('Every image must have the project prefix and an immutable tag')
        subprocess.run(['docker', 'image', 'inspect', *refs], stdout=subprocess.DEVNULL, check=True)
        subprocess.run(['docker', 'image', 'save', '-o', str(stage / 'images.tar'), *refs], check=True)
        output = pathlib.Path(output).resolve()
        # Exclusive creation prevents silently replacing a previous release package.
        fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'wb') as stream, tarfile.open(fileobj=stream, mode='w') as archive:
            metadata = json.dumps({'version': 1, 'name': name, 'compose': config}).encode()
            member = tarfile.TarInfo('project.json'); member.size = len(metadata); member.mode = 0o600
            archive.addfile(member, io.BytesIO(metadata))
            archive.add(stage / 'images.tar', arcname='images.tar', recursive=False)
        print(str(output))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('project')
    parser.add_argument('--tag', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    package(args.project, args.tag, args.output)
