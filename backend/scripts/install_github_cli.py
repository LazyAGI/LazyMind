"""Install the pinned Linux GitHub CLI without executing a cross-architecture binary."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import re
import tarfile
import urllib.request


def install(release_path, arch, destination):
    machines = {'arm64': 183, 'amd64': 62}
    if arch not in machines:
        raise ValueError('Unsupported GitHub CLI architecture')
    release = json.loads(Path(release_path).read_text())
    version = release['version']
    expected = release['archive_sha256']['linux-' + arch]
    if not re.fullmatch(r'\d+\.\d+\.\d+', version) or not re.fullmatch('[0-9a-f]{64}', expected):
        raise ValueError('Invalid GitHub CLI release metadata')
    package = f'gh_{version}_linux_{arch}'
    url = f'https://github.com/cli/cli/releases/download/v{version}/{package}.tar.gz'
    with urllib.request.urlopen(url, timeout=120) as response:
        archive = response.read(64 * 1024 * 1024 + 1)
    if len(archive) > 64 * 1024 * 1024 or hashlib.sha256(archive).hexdigest() != expected:
        raise ValueError('GitHub CLI archive checksum mismatch')
    with tarfile.open(fileobj=io.BytesIO(archive), mode='r:gz') as tar:
        contents = {}
        for relative in ('bin/gh', 'LICENSE'):
            members = [entry for entry in tar.getmembers() if entry.name == f'{package}/{relative}']
            if len(members) != 1 or not members[0].isfile() or members[0].size > 128 * 1024 * 1024:
                raise ValueError('Invalid GitHub CLI archive member')
            with tar.extractfile(members[0]) as entry:
                contents[relative] = entry.read()
    binary = contents['bin/gh']
    if (len(binary) < 64 or binary[:7] != b'\x7fELF\x02\x01\x01'
            or int.from_bytes(binary[18:20], 'little') != machines[arch]):
        raise ValueError('GitHub CLI ELF architecture mismatch')
    if not contents['LICENSE']:
        raise ValueError('GitHub CLI license is missing')
    destination = Path(destination)
    executable = destination / 'bin/gh'
    license_path = destination / 'licenses/gh/LICENSE'
    executable.parent.mkdir(parents=True, exist_ok=True)
    license_path.parent.mkdir(parents=True, exist_ok=True)
    executable.write_bytes(binary)
    executable.chmod(0o755)
    executable.with_name('gh.sha256').write_text(hashlib.sha256(binary).hexdigest() + '\n')
    license_path.write_bytes(contents['LICENSE'])


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--release', required=True)
    parser.add_argument('--arch', required=True)
    parser.add_argument('--destination', required=True)
    args = parser.parse_args()
    install(args.release, args.arch, args.destination)
