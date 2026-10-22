#!/usr/bin/env python3
"""Mirror the exact GitHub installer artifact; never change component catalogs."""
import argparse
import hashlib
import os
from pathlib import Path


def publish(installer, host, repo, token, api=None):
    if not token:
        raise ValueError('Missing installer mirror token')
    if host not in ('ms', 'hf'):
        raise ValueError('Unsupported installer mirror')
    if not installer.is_file() or installer.suffix.lower() not in ('.exe', '.dmg') or installer.stat().st_size == 0:
        raise ValueError('Expected a nonempty EXE or DMG installer artifact')
    if any(c in installer.name for c in '\r\n\\:'):
        raise ValueError('Unsafe installer filename')
    digest = hashlib.sha256()
    with installer.open('rb') as source:
        while chunk := source.read(1024 * 1024):
            digest.update(chunk)
    if api is None:
        if host == 'ms':
            from modelscope_hub import HubApi
            api = HubApi(endpoint='https://modelscope.cn', token=token)
        else:
            from huggingface_hub import HfApi
            api = HfApi(token=token)
    api.upload_file(repo_id=repo, repo_type='dataset', path_or_fileobj=str(installer),
                    path_in_repo=f'Realease/{installer.name}',
                    revision='master' if host == 'ms' else 'main',
                    commit_message=f'Mirror installer {installer.name}')
    base = (f'https://modelscope.cn/datasets/{repo}/resolve/master/' if host == 'ms'
            else f'https://huggingface.co/datasets/{repo}/resolve/main/')
    from urllib.parse import quote
    url = base + 'Realease/' + quote(installer.name)
    print(f'Installer mirrored: {url}\nSHA-256: {digest.hexdigest()}', flush=True)
    if summary := os.environ.get('GITHUB_STEP_SUMMARY'):
        with open(summary, 'a', encoding='utf-8') as stream:
            stream.write(f'\nInstaller mirror ({host}): <{url}>\n\nSHA-256: `{digest.hexdigest()}`\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--file', type=Path, required=True)
    parser.add_argument('--host', choices=('ms', 'hf'), required=True)
    parser.add_argument('--repo', required=True)
    args = parser.parse_args()
    publish(args.file, args.host, args.repo, os.environ.get('UPLOAD_TOKEN'))
