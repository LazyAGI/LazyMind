import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from urllib.error import HTTPError
from urllib.request import Request

spec = importlib.util.spec_from_file_location('publisher_transport', Path(__file__).with_name('publish-desktop-update.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class RedirectTests(unittest.TestCase):
    def test_redirects_keep_exact_https_origin_pins(self):
        handler = module.PinnedRedirects(['https://modelscope.cn/', 'https://cdn.example.invalid'])
        request = Request('https://modelscope.cn/datasets/fixture/updates/resolve/master/package.zip')
        allowed = handler.redirect_request(request, None, 302, '', {}, 'https://cdn.example.invalid/file?signature=fixture')
        self.assertEqual(allowed.full_url, 'https://cdn.example.invalid/file?signature=fixture')
        for target in ('http://modelscope.cn/file', 'https://evil.invalid/file',
                       'https://modelscope.cn.evil.invalid/file', 'https://user:secret@modelscope.cn/file'):
            with self.assertRaises(ValueError):
                handler.redirect_request(request, None, 302, '', {}, target)


class PublicationTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.directory = Path(temp.name)
        self.repo = 'fixture/updates'
        self.base = f'https://modelscope.cn/datasets/{self.repo}/resolve/master/'
        self.policy_path = 'desktop/policy.json'
        self.previous = b'previous signed policy fixture'
        self.next = b'next signed policy fixture'
        self.remote = {}
        self.events = []
        self.after_upload = None

        release_id = 'macos-v2'
        release = {'releaseId': release_id, 'platform': 'darwin'}
        self.artifacts = {}
        artifact_dir = self.directory / 'build' / release_id
        artifact_dir.mkdir(parents=True)
        for kind, filename, content in (
            ('package', 'package.zip', b'installer bytes'),
            ('feed', 'feed.json', b'{"feed":true}'),
            ('components', 'components.json', b'{"components":[]}'),
        ):
            path = f'desktop/releases/{release_id}/{filename}'
            (artifact_dir / filename).write_bytes(content)
            self.artifacts[path] = content
            release[kind] = {
                'url': self.base + path,
                'size': len(content),
                'sha256': hashlib.sha256(content).hexdigest(),
            }
        self.policies = {
            self.previous: {'policySequence': 1, 'releases': []},
            self.next: {'policySequence': 2, 'releases': [release]},
        }
        self.config_file = self.directory / 'config.json'
        self.config_file.write_text(json.dumps({'policyURL': self.base + self.policy_path}), encoding='utf-8')
        self.policy_file = self.directory / 'policy.json'
        self.policy_file.write_bytes(self.next)

    def fetch(self, url):
        self.assertTrue(url.startswith(self.base))
        path = url.removeprefix(self.base)
        if path not in self.remote:
            raise HTTPError(url, 404, 'fixture file missing', {}, None)
        content = self.remote[path]
        self.events.append(('read', path, content))
        return io.BytesIO(content)

    def upload_file(self, *, repo_id, repo_type, path_or_fileobj, path_in_repo, revision, commit_message):
        self.assertEqual((repo_id, repo_type, revision), (self.repo, 'dataset', 'master'))
        content = Path(path_or_fileobj).read_bytes()
        self.events.append(('upload', path_in_repo, content))
        self.remote[path_in_repo] = content
        if self.after_upload is not None:
            self.after_upload()

    def publish(self, expected_policy_sha256):
        expected = {} if expected_policy_sha256 is None else {'expected_policy_sha256': expected_policy_sha256}
        module.publish(self.directory, self.policy_file, self.config_file, self.repo,
                       api=SimpleNamespace(upload_file=self.upload_file), fetch=self.fetch,
                       validate=self.policies.__getitem__, **expected)

    def test_matching_remote_state_uploads_exact_artifacts_before_policy(self):
        for previous, expected in (
            (None, 'absent'),
            (self.previous, hashlib.sha256(self.previous).hexdigest()),
            (self.previous, None),
        ):
            with self.subTest(previous=previous, expected=expected):
                self.events.clear()
                self.remote.clear()
                if previous is not None:
                    self.remote[self.policy_path] = previous
                self.publish(expected)
                uploads = [event for event in self.events if event[0] == 'upload']
                self.assertEqual(uploads[-1], ('upload', self.policy_path, self.next))
                self.assertCountEqual(uploads[:-1], [('upload', path, data) for path, data in self.artifacts.items()])
                policy_index = self.events.index(uploads[-1])
                for path, data in self.artifacts.items():
                    self.assertIn(('read', path, data), self.events[:policy_index])
                self.assertIn(('read', self.policy_path, self.next), self.events[policy_index + 1:])

    def test_unexpected_remote_head_is_rejected_before_any_upload(self):
        for before, expected in (
            ({self.policy_path: self.previous}, 'absent'),
            ({self.policy_path: self.previous}, hashlib.sha256(b'a different verified policy').hexdigest()),
            ({}, hashlib.sha256(self.previous).hexdigest()),
        ):
            with self.subTest(before=before, expected=expected):
                self.events.clear()
                self.remote = before.copy()
                with self.assertRaises(ValueError):
                    self.publish(expected)
                self.assertFalse(any(event[0] == 'upload' for event in self.events))
                self.assertEqual(self.remote, before)

    def test_remote_policy_change_after_artifact_upload_blocks_policy_write(self):
        self.remote[self.policy_path] = self.previous
        later_policy = b'policy from a concurrent publisher'

        def concurrent_publish():
            self.remote[self.policy_path] = later_policy

        self.after_upload = concurrent_publish
        with self.assertRaises(ValueError):
            self.publish(hashlib.sha256(self.previous).hexdigest())
        uploaded = [path for action, path, _ in self.events if action == 'upload']
        self.assertTrue(uploaded, 'The remote policy must change during publication')
        self.assertNotIn(self.policy_path, uploaded)
        self.assertEqual(self.remote[self.policy_path], later_policy)


if __name__ == '__main__':
    unittest.main()
