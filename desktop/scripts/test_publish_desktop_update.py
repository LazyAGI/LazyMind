"""Publication order/immutable-file tests. Real policy crypto is covered by Go client tests."""
import copy
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from urllib.error import HTTPError

spec = importlib.util.spec_from_file_location('publish_desktop', Path(__file__).with_name('publish-desktop-update.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = 'fixture/updates'
        self.base = 'https://modelscope.cn/datasets/fixture/updates/resolve/master/'
        self.config = {'policyURL': self.base + 'updates/stable.json', 'allowedOrigins': ['https://modelscope.cn']}
        self.config_path = self.root / 'config.json'
        self.config_path.write_text(json.dumps(self.config))
        self.policy_path = self.root / 'policy.json'
        self.policy_path.write_bytes(b'fixture signed envelope')
        release = {'releaseId': 'r2', 'platform': 'darwin'}
        (self.root / 'r2').mkdir()
        for kind, name in [('package', 'package.zip'), ('feed', 'feed.json'), ('components', 'components.json')]:
            data = ('fixture ' + kind).encode()
            (self.root / 'r2' / name).write_bytes(data)
            release[kind] = {'url': self.base + 'updates/releases/r2/' + name, 'size': len(data), 'sha256': hashlib.sha256(data).hexdigest()}
        self.policy = {'policySequence': 2, 'releases': [release]}
        self.remote = {}
        self.events = []
        self.fail_upload = None
        self.corrupt_upload = False

    def validate(self, raw):
        self.events.append(('validate', raw))
        if raw == b'fixture signed envelope':
            return copy.deepcopy(self.policy)
        if raw == b'old signed envelope':
            return {'policySequence': 1, 'releases': []}
        raise ValueError('invalid signature')

    def fetch(self, url):
        self.events.append(('fetch', url))
        if url not in self.remote:
            raise HTTPError(url, 404, 'missing', {}, None)
        return io.BytesIO(self.remote[url])

    def upload_file(self, **kwargs):
        name = kwargs['path_in_repo']
        self.events.append(('upload', name))
        if name == self.fail_upload:
            raise RuntimeError('upload failure')
        self.assertEqual(kwargs['repo_id'], self.repo)
        self.assertEqual(kwargs['repo_type'], 'dataset')
        self.assertEqual(kwargs['revision'], 'master')
        raw = Path(kwargs['path_or_fileobj']).read_bytes()
        self.remote[self.base + name] = b'corrupt' if self.corrupt_upload else raw

    def publish(self, **kwargs):
        return module.publish(self.root, self.policy_path, self.config_path, self.repo,
                              api=self, fetch=self.fetch, validate=self.validate, **kwargs)

    def test_files_verified_before_policy_and_identical_retry_is_safe(self):
        self.publish()
        uploads = [name for action, name in self.events if action == 'upload']
        self.assertEqual(uploads[-1], 'updates/stable.json')
        self.assertEqual(len(uploads), 4)
        self.assertEqual(self.events[0][0], 'validate')
        policy_upload = self.events.index(('upload', 'updates/stable.json'))
        for kind in ('package', 'feed', 'components'):
            url = self.policy['releases'][0][kind]['url']
            artifact_upload = self.events.index(('upload', url.removeprefix(self.base)))
            self.assertIn(('fetch', url), self.events[artifact_upload + 1:policy_upload])
        self.assertIn(('fetch', self.config['policyURL']), self.events[policy_upload + 1:])
        self.events.clear()
        self.publish()
        self.assertFalse(any(action == 'upload' for action, _ in self.events))

    def test_local_hash_mismatch_uploads_nothing(self):
        (self.root / 'r2/components.json').write_bytes(b'wrong')
        with self.assertRaisesRegex(ValueError, 'hash|size|mismatch'):
            self.publish()
        self.assertFalse(any(action == 'upload' for action, _ in self.events))

    def test_existing_different_artifact_is_never_overwritten(self):
        self.remote[self.policy['releases'][0]['components']['url']] = b'old content'
        with self.assertRaisesRegex(ValueError, 'immutable|mismatch'):
            self.publish()
        self.assertFalse(any(action == 'upload' for action, _ in self.events))

    def test_failed_upload_or_readback_never_publishes_policy(self):
        for corrupt in [False, True]:
            self.events.clear(); self.remote.clear()
            self.corrupt_upload = corrupt
            self.fail_upload = None if corrupt else 'updates/releases/r2/feed.json'
            with self.assertRaises((RuntimeError, ValueError)):
                self.publish()
            self.assertNotIn(('upload', 'updates/stable.json'), self.events)

    def test_remote_policy_signature_and_sequence_must_be_valid(self):
        self.remote[self.config['policyURL']] = b'invalid envelope'
        with self.assertRaisesRegex(ValueError, 'signature'):
            self.publish()
        self.assertFalse(any(action == 'upload' for action, _ in self.events))
        self.remote[self.config['policyURL']] = b'old signed envelope'
        self.policy['policySequence'] = 1
        with self.assertRaisesRegex(ValueError, 'sequence|conflict'):
            self.publish()

    def test_older_sequence_cannot_replace_remote_policy(self):
        self.remote[self.config['policyURL']] = b'old signed envelope'
        original = self.validate
        self.validate = lambda raw: {'policySequence': 3, 'releases': []} if raw == b'old signed envelope' else original(raw)
        with self.assertRaisesRegex(ValueError, 'sequence|conflict'):
            self.publish()
        self.assertFalse(any(action == 'upload' for action, _ in self.events))

    def test_final_policy_readback_corruption_is_reported(self):
        original = self.upload_file
        def upload(**kwargs):
            original(**kwargs)
            if kwargs['path_in_repo'] == 'updates/stable.json':
                self.remote[self.config['policyURL']] = b'corrupt'
        self.upload_file = upload
        with self.assertRaises(ValueError):
            self.publish()
        self.assertIn(('upload', 'updates/stable.json'), self.events)
        self.assertIn(('fetch', self.config['policyURL']), self.events[self.events.index(('upload', 'updates/stable.json')) + 1:])

    def test_missing_or_corrupt_historical_asset_blocks_policy(self):
        (self.root / 'r2/components.json').unlink()
        url = self.policy['releases'][0]['components']['url']
        for data in (None, b'wrong'):
            self.events.clear(); self.remote.clear()
            if data is not None:
                self.remote[url] = data
            with self.assertRaises((ValueError, HTTPError)):
                self.publish()
            self.assertFalse(any(action == 'upload' for action, _ in self.events))

    def test_remote_permission_error_is_not_treated_as_missing(self):
        original = self.fetch
        def fetch(url):
            if url.endswith('components.json'):
                raise HTTPError(url, 403, 'forbidden', {}, None)
            return original(url)
        self.fetch = fetch
        with self.assertRaises(HTTPError):
            self.publish()
        self.assertFalse(any(action == 'upload' for action, _ in self.events))

    def test_target_repository_and_paths_cannot_escape(self):
        self.policy['releases'][0]['package']['url'] = 'https://modelscope.cn/datasets/other/repo/resolve/master/x.zip'
        with self.assertRaises(ValueError):
            self.publish()
        self.assertFalse(any(action == 'upload' for action, _ in self.events))

    def test_policy_changed_during_upload_aborts_pointer_write(self):
        original = self.upload_file
        def upload(**kwargs):
            original(**kwargs)
            self.remote[self.config['policyURL']] = b'old signed envelope'
        self.upload_file = upload
        with self.assertRaisesRegex(ValueError, 'changed|concurrent'):
            self.publish()
        self.assertNotIn(('upload', 'updates/stable.json'), self.events)

    def test_missing_local_old_release_requires_successful_remote_hash_check(self):
        for artifact in self.policy['releases'][0].values():
            if isinstance(artifact, dict):
                name = artifact['url'].rsplit('/', 1)[-1]
                self.remote[artifact['url']] = (self.root / 'r2' / name).read_bytes()
                (self.root / 'r2' / name).unlink()
        self.publish()
        self.assertEqual([name for action, name in self.events if action == 'upload'], ['updates/stable.json'])


if __name__ == '__main__':
    unittest.main()
