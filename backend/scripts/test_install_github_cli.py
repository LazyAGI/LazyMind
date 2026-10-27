"""Installer contract with synthetic archives; it never fetches or executes GitHub binaries."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name('install_github_cli.py')
REPO = Path(__file__).resolve().parents[2]
VERSION = '2.102.1'
OFFICIAL_SHA = {
    'linux-arm64': '5019b3cc6e8a03bb4d88600f8dbda2add8e6407705c159b0c63151036e77a1af',
    'linux-amd64': '77b9e297e94c50336823d0f84de7ef1be60cba704601fa0d98f129f47a94987b',
}


def archive(arch='arm64', *, machine=None, license_present=True, binary_link=False):
    header = bytearray(64)
    header[:7] = b'\x7fELF\x02\x01\x01'
    header[18:20] = (machine or {'arm64': 183, 'amd64': 62}[arch]).to_bytes(2, 'little')
    content = io.BytesIO()
    with tarfile.open(fileobj=content, mode='w:gz') as tar:
        info = tarfile.TarInfo(f'gh_{VERSION}_linux_{arch}/bin/gh')
        info.mode = 0o755
        if binary_link:
            info.type = tarfile.SYMTYPE
            info.linkname = '/fixture/outside'
            tar.addfile(info)
        else:
            info.size = len(header)
            tar.addfile(info, io.BytesIO(header))
        if license_present:
            data = b'fixture license text'
            info = tarfile.TarInfo(f'gh_{VERSION}_linux_{arch}/LICENSE')
            info.size = len(data)
            tar.addfile(info, io.BytesIO(data))
    return content.getvalue(), bytes(header)


class GitHubCLIInstallerTest(unittest.TestCase):
    def setUp(self):
        # Until implemented, loading this proposed script is an explicit missing interface.
        spec = importlib.util.spec_from_file_location('github_cli_installer_candidate', SCRIPT)
        self.installer = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.installer)
        self.temp = tempfile.TemporaryDirectory(prefix='github-cli-install-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.destination = self.root / 'installed'
        self.release = self.root / 'release.json'

    def install(self, arch='arm64', *, bad_checksum=False, **options):
        payload, binary = archive(arch, **options)
        self.release.write_text(json.dumps({'version': VERSION, 'archive_sha256': {
            'linux-' + arch: ('0' * 64 if bad_checksum else hashlib.sha256(payload).hexdigest()),
        }}))
        with patch('urllib.request.urlopen', return_value=io.BytesIO(payload)) as fetch:
            self.installer.install(self.release, arch, self.destination)
        requested = fetch.call_args.args[0]
        url = requested if isinstance(requested, str) else requested.full_url
        self.assertEqual(
            url, f'https://github.com/cli/cli/releases/download/v{VERSION}/gh_{VERSION}_linux_{arch}.tar.gz',
        )
        return binary

    def test_installs_matching_binary_digest_license_and_executable_for_both_architectures(self):
        for arch in ('arm64', 'amd64'):
            with self.subTest(arch=arch):
                self.destination = self.root / arch
                expected = self.install(arch)
                binary = self.destination / 'bin/gh'
                self.assertEqual(binary.read_bytes(), expected)
                self.assertTrue(binary.stat().st_mode & 0o111)
                self.assertEqual(binary.with_name('gh.sha256').read_text().strip(), hashlib.sha256(expected).hexdigest())
                self.assertEqual((self.destination / 'licenses/gh/LICENSE').read_bytes(), b'fixture license text')

    def test_archive_hash_mismatch_installs_nothing(self):
        with self.assertRaises((ValueError, RuntimeError)):
            self.install(bad_checksum=True)
        self.assertFalse((self.destination / 'bin/gh').exists())

    def test_wrong_elf_architecture_installs_nothing(self):
        with self.assertRaises((ValueError, RuntimeError)):
            self.install('arm64', machine=62)
        self.assertFalse((self.destination / 'bin/gh').exists())

    def test_missing_license_is_not_a_successful_install(self):
        with self.assertRaises((ValueError, RuntimeError)):
            self.install(license_present=False)
        self.assertFalse((self.destination / 'bin/gh').exists())

    def test_archive_symlink_is_never_followed_or_installed(self):
        with self.assertRaises((ValueError, RuntimeError)):
            self.install(binary_link=True)
        self.assertFalse((self.destination / 'bin/gh').exists())

    def test_unsupported_architecture_stops_before_network_access(self):
        self.release.write_text(json.dumps({'version': VERSION, 'archive_sha256': {}}))
        with patch('urllib.request.urlopen') as fetch, self.assertRaises((ValueError, RuntimeError)):
            self.installer.install(self.release, 'unsupported', self.destination)
        fetch.assert_not_called()


class GitHubCLIImageContractTest(unittest.TestCase):
    def test_release_metadata_contains_independently_observed_official_linux_checksums(self):
        release = json.loads((REPO / 'backend/auth-service/github-cli-release.json').read_text())
        self.assertEqual(release['version'], VERSION)
        self.assertEqual({key: release['archive_sha256'].get(key) for key in OFFICIAL_SHA}, OFFICIAL_SHA)

    def test_dockerfile_installs_for_target_architecture_and_selects_that_binary(self):
        dockerfile = (REPO / 'backend/auth-service/Dockerfile').read_text()
        self.assertIn('ARG TARGETARCH', dockerfile)
        self.assertIn('install_github_cli.py', dockerfile)
        self.assertIn('github-cli-release.json', dockerfile)
        self.assertIn('LAZYMIND_GITHUB_CLI_PATH=/opt/lazymind/github-cli/bin/gh', dockerfile)
        self.assertIn('TARGETARCH', dockerfile.split('install_github_cli.py')[-1])


if __name__ == '__main__':
    unittest.main()
