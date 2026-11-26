"""Exercise real publication destinations and predecessor selection without writing to a service."""
import json
import os
from pathlib import Path
import runpy
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

HERE = Path(__file__).resolve().parent
publisher = runpy.run_path(str(HERE / 'publish-python-components.py'))


class WindowsSignedPublication(unittest.TestCase):
    def test_component_upload_destinations(self):
        repo, tag, prefix = 'fixture/updates', 'c' * 64, 'updates/windows-test/trial-1/RAG'
        base = f'https://modelscope.cn/datasets/{repo}/resolve/master/{prefix}/tags/{tag}/'
        entry = dict(platform='windows', id='rag', tag=tag, sha256='a' * 64, revision='b' * 64,
                     filename='rag.zip', url=base + 'rag.zip', catalogUrl=base + 'python-components.json')
        with tempfile.TemporaryDirectory() as tmp:
            folder, api, verify = Path(tmp), Mock(), publisher['verify']
            (folder / 'python-components.json').write_text(json.dumps({'components': {'rag': entry}}))
            # Bundle integrity has its own tests; record every real uploader destination.
            with patch.object(verify, 'check_archive'), patch.object(verify, 'extract_bundle', side_effect=lambda a, d, e: d.mkdir()), \
                    patch.object(verify, 'resolve_catalog_entry', return_value=entry), patch.object(verify, 'acquire_bundle'):
                for forbidden in ['RAG', 'updates/unsigned-test/round-1/RAG', 'updates/windows-test/../RAG']:
                    wrong = {**entry, 'url': entry['url'].replace(prefix, forbidden),
                             'catalogUrl': entry['catalogUrl'].replace(prefix, forbidden)}
                    (folder / 'python-components.json').write_text(json.dumps({'components': {'rag': wrong}}))
                    with self.assertRaises((ValueError, RuntimeError, AssertionError)):
                        publisher['publish'](folder, repo, api=api, signing_mode='windows-test', prefix=forbidden)
                    api.upload_file.assert_not_called()
                (folder / 'python-components.json').write_text(json.dumps({'components': {'rag': entry}}))
                publisher['publish'](folder, repo, api=api, signing_mode='windows-test', prefix=prefix)
            self.assertEqual([(c.kwargs['repo_id'], c.kwargs['path_in_repo']) for c in api.upload_file.call_args_list],
                             [(repo, f'{prefix}/tags/{tag}/{name}') for name in ['rag.zip', 'SHA256SUMS', 'python-components.json']])

    def test_windows_a_has_no_predecessor_and_b_selects_installed_windows_a(self):
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp)
            def save(name, obj):
                dest = work / name
                dest.parent.mkdir(exist_ok=True, parents=True)
                dest.write_text(json.dumps(obj))
            def lanes():
                output = work / 'output'
                output.write_text('')
                subprocess.run([sys.executable, str(HERE / 'run-published-upgrade-smoke.py'), 'matrix', str(work), 'windows'],
                               env={**os.environ, 'GITHUB_OUTPUT': str(output)}, check=True, capture_output=True)
                return json.loads(output.read_text().split('=', 1)[1])
            source = {'releaseId': 'windows-A'}
            save('windows-target.json', {'config': {'platform': 'windows'}, 'release': source})
            save('windows-baseline.json', [])
            self.assertEqual(lanes(), [])
            save('windows-target.json', {'config': {'platform': 'windows'}, 'release': {'releaseId': 'windows-B'}})
            save('windows-baseline.json', [{'baseline': True, 'sourceReleaseId': 'windows-A', 'targetReleaseId': 'windows-B', 'sourceRelease': source}])
            save('records/windows/windows-A/release.json', {'release': source})
            save('evidence/windows/windows-A.json', {'passed': True, 'sourceReleaseId': 'windows-A', 'targetReleaseId': 'windows-B'})
            self.assertEqual(lanes(), [{'platform': 'windows'}])


if __name__ == '__main__':
    unittest.main()
