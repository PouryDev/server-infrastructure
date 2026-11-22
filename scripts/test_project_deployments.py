import copy
import hashlib
import io
import json
import pathlib
import tarfile
import tempfile
import unittest
from unittest.mock import patch

from project_deployments import Projects, preserve_limits, validate_manifest, check_required_environment, MissingEnvironment


def manifest():
    return {'name': 'testapp', 'services': {'testapp-api': {
        'image': 'testapp-api:sha-one', 'user': '65532:65532', 'read_only': True,
        'cap_drop': ['ALL'], 'security_opt': ['no-new-privileges:true'], 'networks': {'infra': None},
        'mem_limit': '128m', 'cpus': .5, 'healthcheck': {'test': ['CMD', '/api', 'healthcheck']},
        'volumes': [{'type': 'bind', 'source': '/srv/data/apps/testapp/uploads', 'target': '/data/uploads'}],
        'env_file': [{'path': '.env', 'required': True}],
    }}, 'networks': {'infra': {'name': 'infra', 'external': True}}}


class DeploymentTests(unittest.TestCase):
    def test_required_destination_env_reports_only_missing_names(self):
        doc = manifest()
        spec = doc['services']['testapp-api']
        spec['labels'] = {'infra.manager.required-env': 'APP_KEY, PUBLIC_URL'}
        spec['environment'] = {'APP_KEY': 'secret-do-not-print', 'PUBLIC_URL': ''}
        with self.assertRaises(MissingEnvironment) as error: check_required_environment(doc)
        self.assertEqual(str(error.exception), 'Missing required destination environment variables: PUBLIC_URL')
        spec['environment']['PUBLIC_URL'] = 'https://example.com'
        check_required_environment(doc)
        spec['labels']['infra.manager.required-env'] = 'secret with spaces'
        with self.assertRaisesRegex(ValueError, '^Invalid required environment name$'): check_required_environment(doc)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.apps = self.root / 'apps'; self.apps.mkdir()
        self.data = self.root / 'data'; self.data.mkdir()
        (self.root / 'config/nginx/conf.d').mkdir(parents=True)
        self.runner = Projects.__new__(Projects)
        self.runner.root, self.runner.apps, self.runner.data = self.root, self.apps, self.data
        self.runner.state = self.root / 'state'; self.runner.state.mkdir()

    def test_contract_remaps_only_project_data_and_keeps_env_on_destination(self):
        raw = manifest()
        result = validate_manifest(raw, 'testapp', self.data)
        service = result['services']['testapp-api']
        self.assertEqual(service['volumes'][0]['source'], str(self.data / 'apps/testapp/uploads'))
        self.assertEqual(service['env_file'], ['.env', {'path': '.runtime.env', 'format': 'raw'}])
        self.assertEqual(raw, manifest())

    def test_unsafe_compose_is_rejected_before_loading_any_image(self):
        for key, value in [('ports', ['80:80']), ('privileged', True), ('cap_add', ['SYS_ADMIN']),
                           ('network_mode', 'host'), ('user', '0'), ('read_only', False),
                           ('image', 'nginx:latest'), ('networks', {'infra': {'aliases': ['postgres']}}),
                           ('env_file', ['/etc/shadow'])]:
            with self.subTest(key=key):
                raw = manifest(); raw['services']['testapp-api'][key] = value
                with self.assertRaises(ValueError): validate_manifest(raw, 'testapp', self.data)
        for source in ('/var/run/docker.sock', '/srv/data/apps/other/uploads', '/srv/data/apps/testapp/../other'):
            raw = manifest(); raw['services']['testapp-api']['volumes'][0]['source'] = source
            with self.assertRaises(ValueError): validate_manifest(raw, 'testapp', self.data)

    def test_destination_limits_win_and_new_services_keep_bundle_limits(self):
        doc = manifest(); doc['services']['testapp-worker'] = copy.deepcopy(doc['services']['testapp-api'])
        live = {'testapp-api': {'mem_limit': 512 * 1024 ** 2, 'cpus': 1.75, 'memswap_limit': 768 * 1024 ** 2, 'pids_limit': 100}}
        doc['services']['testapp-api']['cpu_quota'] = 50000
        preserve_limits(doc, live)
        self.assertEqual(doc['services']['testapp-api']['mem_limit'], 512 * 1024 ** 2)
        self.assertEqual(doc['services']['testapp-api']['cpus'], 1.75)
        self.assertNotIn('cpu_quota', doc['services']['testapp-api'])
        self.assertEqual(doc['services']['testapp-worker']['cpus'], .5)

    def test_domain_collisions_include_aliases_and_wildcards(self):
        conf = self.root / 'config/nginx/conf.d/other.conf'
        conf.write_text('server { server_name one.example.com two.example.com *.private.example.com; }')
        for domain in ('one.example.com', 'two.example.com', 'app.private.example.com'):
            with self.assertRaises(ValueError): self.runner.check_domain('testapp', domain)
        self.runner.check_domain('testapp', 'free.example.com')

    def test_catalog_reports_the_destination_edge_port(self):
        self.runner.edge_port = 8088
        self.assertEqual(self.runner.catalog()['edgePort'], 8088)

    def test_failed_nginx_validation_restores_the_previous_file(self):
        original = 'server { server_name old.example.com; set $client http://testapp-api:8080; }'
        conf = self.root / 'config/nginx/conf.d/testapp.conf'; conf.write_text(original)
        with patch('project_deployments.run', side_effect=[RuntimeError('test failed'), '']):
            with self.assertRaisesRegex(RuntimeError, 'restored'):
                self.runner.route('testapp', 'app.example.com', 'testapp-api', 8080)
        self.assertEqual(conf.read_text(), original)

    def test_domain_edit_preserves_existing_api_routes_aliases_and_custom_settings(self):
        api = 'server { server_name api.example.com; set $api http://testapp-api:8080; }'
        conf = self.root / 'config/nginx/conf.d/testapp.conf'
        conf.write_text('server { server_name old.example.com www.example.com; set $client http://testapp-web:80; location /api { proxy_pass http://testapp-api:8080; } expires 1d; }\n' + api)
        with patch('project_deployments.run'):
            self.runner.route('testapp', 'new.example.com', 'testapp-web', 8081)
        text = conf.read_text()
        self.assertIn(api, text)
        self.assertIn('server_name new.example.com www.example.com;', text)
        self.assertIn('http://testapp-web:8081;', text)
        self.assertIn('location /api { proxy_pass http://testapp-api:8080; } expires 1d;', text)

    def test_env_only_save_never_rewrites_or_reloads_existing_routes(self):
        original = 'server { server_name old.example.com; set $client http://testapp-api:8080; }'
        conf = self.root / 'config/nginx/conf.d/testapp.conf'; conf.write_text(original)
        with patch('project_deployments.run') as command:
            self.runner.route('testapp', 'old.example.com', 'testapp-api', 8080)
            command.assert_not_called()
        self.assertEqual(conf.read_text(), original)

    def test_save_preserves_shared_credentials_and_literal_env_and_revision(self):
        directory = self.apps / 'testapp'; directory.mkdir()
        (directory / '.manager').mkdir()
        (directory / '.env').write_text('DATABASE_URL=destination-secret\n')
        (directory / '.runtime.env').write_text('UNCHANGED=keep-me\nDELETE=old\n')
        (directory / '.manager/candidate.json').write_text(json.dumps(manifest()))
        before = self.runner.revision(directory)
        request = {'name': 'testapp', 'domain': 'new.example.com', 'service': 'testapp-api', 'port': 8080,
            'environment': {'PASSWORD': "literal$secret'\""}, 'removeEnv': ['DELETE']}
        with patch.object(self.runner, 'route'):
            self.runner.save(request, directory, lambda _: None)
        self.assertEqual((directory / '.env').read_text(), 'DATABASE_URL=destination-secret\n')
        self.assertEqual(self.runner.read_env(directory), {'UNCHANGED': 'keep-me', 'PASSWORD': "literal$secret'\""})
        self.assertNotEqual(self.runner.revision(directory), before)
        detail = self.runner.detail('testapp')
        self.assertIn('PASSWORD', detail['envKeys'])
        self.assertNotIn('literal', json.dumps(detail))
        self.assertEqual(detail['domain'], 'new.example.com')

    def test_env_not_changed_when_nginx_rejects_settings(self):
        directory = self.apps / 'testapp'; directory.mkdir(); (directory / '.manager').mkdir()
        (directory / '.env').write_text('DATABASE_URL=destination-secret\n')
        (directory / '.runtime.env').write_text('OLD=value\n')
        (directory / '.manager/candidate.json').write_text(json.dumps(manifest()))
        with patch.object(self.runner, 'route', side_effect=RuntimeError('nginx failed')):
            with self.assertRaises(RuntimeError):
                self.runner.save({'name': 'testapp', 'domain': 'app.example.com', 'service': 'testapp-api', 'port': 8080, 'environment': {'NEW': 'secret'}}, directory, lambda _: None)
        self.assertEqual((directory / '.runtime.env').read_text(), 'OLD=value\n')

    def test_native_launch_stages_routing_until_containers_are_healthy(self):
        directory = self.apps / 'testapp'; directory.mkdir(); (directory / '.manager').mkdir()
        (directory / '.env').write_text('DATABASE_URL=destination-secret\n')
        (directory / '.manager/candidate.json').write_text(json.dumps(manifest()))
        with patch.object(self.runner, 'route') as route:
            self.runner.save({'name': 'testapp', 'domain': 'app.example.com', 'service': 'testapp-api', 'port': 8080}, directory, lambda _: None, route_now=False)
            route.assert_not_called()
        self.assertEqual(json.loads((directory / '.manager/project.json').read_text())['domain'], 'app.example.com')

    def test_failed_first_release_is_never_adopted_as_a_healthy_rollback(self):
        directory = self.apps / 'testapp'; directory.mkdir(); (directory / '.manager').mkdir()
        (directory / '.env').write_text('DATABASE_URL=destination-secret\n')
        (directory / 'compose.yaml').write_text(json.dumps(manifest()))
        (directory / '.manager/candidate.json').write_text(json.dumps(manifest()))
        (directory / '.manager/project.json').write_text(json.dumps({'name': 'testapp'}))
        with patch.object(self.runner, 'snapshot') as snapshot, patch.object(self.runner, 'route'):
            self.runner.save({'name': 'testapp', 'domain': 'app.example.com', 'service': 'testapp-api', 'port': 8080}, directory, lambda _: None)
            snapshot.assert_not_called()

    def test_upload_rejects_traversal_and_extra_files(self):
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode='w') as archive:
            for name in ('project.json', 'images.tar', '../.env'):
                entry = tarfile.TarInfo(name); entry.size = 1; archive.addfile(entry, io.BytesIO(b'x'))
        data = buffer.getvalue()
        with self.assertRaises(ValueError): self.runner.upload('testapp', io.BytesIO(data), len(data))
        self.assertFalse(list(self.runner.state.glob('package-*')))

    def test_oci_index_cannot_import_another_projects_image_tag(self):
        config = json.dumps({'os': 'linux', 'architecture': 'amd64'}).encode()
        digest = hashlib.sha256(config).hexdigest()
        files = {
            'blobs/sha256/' + digest: config,
            'manifest.json': json.dumps([{'Config': 'blobs/sha256/' + digest, 'RepoTags': ['testapp-api:sha-one'], 'Layers': []}]).encode(),
            'index.json': json.dumps({'manifests': [{'annotations': {'io.containerd.image.name': 'other-project-api:production'}}]}).encode(),
        }
        archive = self.root / 'hostile-images.tar'
        with tarfile.open(archive, 'w') as stream:
            for name, content in files.items():
                member = tarfile.TarInfo(name); member.size = len(content)
                stream.addfile(member, io.BytesIO(content))
        with patch('project_deployments.subprocess.run') as docker:
            with self.assertRaisesRegex(ValueError, 'unexpected'):
                self.runner.inspect_images(archive, manifest(), 'testapp')
            docker.assert_not_called()

    def test_foreign_symlink_project_is_rejected(self):
        (self.apps / 'testapp').symlink_to(self.data)
        with self.assertRaises(ValueError): self.runner.directory('testapp')

    def test_env_newlines_reserved_names_and_unknown_fields_are_rejected(self):
        req = {'name': 'testapp', 'key': 'a' * 36, 'revision': '', 'domain': 'app.example.com', 'service': 'testapp-api', 'port': 8080}
        self.runner.validate_request(req, 'save')
        for changes in ({'environment': {'PASSWORD': 'secret\nINJECT=x'}}, {'name': 'infra'}, {'shell': 'touch /etc/passwd'}):
            with self.assertRaises(ValueError): self.runner.validate_request({**req, **changes}, 'save')


if __name__ == '__main__': unittest.main()
