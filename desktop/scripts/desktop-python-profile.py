#!/usr/bin/env python3
"""Prepare the next desktop dependency boundary without changing cloud requirements."""
import argparse
import importlib.abc
import importlib.metadata as metadata
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from unittest.mock import patch

from packaging.requirements import Requirement
from packaging.utils import canonicalize_name

# The SDK entry only cleans older cached environments; new dependency locks omit it.
REMOVED = {'volcengine-python-sdk', 'opensearch-py', 'opensearch-protobufs'}


def load_components():
    spec = importlib.util.spec_from_file_location('components', Path(__file__).with_name('build-python-components.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def removal_plan(site):
    components = load_components()
    distributions = {canonicalize_name(d.metadata['Name']): d for d in metadata.distributions(path=[str(site)])}
    selected = REMOVED & distributions.keys()
    for name, dist in distributions.items():
        if name in selected:
            continue
        for text in dist.requires or ():
            req = Requirement(text)
            if (req.marker is None or req.marker.evaluate({'extra': ''})) and canonicalize_name(req.name) in selected:
                raise RuntimeError(f'{name} still requires {req.name}; refusing desktop removal')
    owners = {}
    for name, dist in distributions.items():
        for file in components.distribution_files(dist, site):
            owners.setdefault(file, set()).add(name)
    files = {p for name in selected for p in components.distribution_files(distributions[name], site)}
    if any(not owners[p] <= selected for p in files):
        raise RuntimeError('Desktop provider distributions share files with retained dependencies')
    roots = {site / p.relative_to(site).parts[0] for p in files}
    for root in roots:
        # Refuse unknown files and directory links instead of leaving partial packages.
        if root.is_symlink():
            raise RuntimeError(f'Linked distribution root: {root}')
        if root.is_dir():
            for directory, dirs, names in os.walk(root, followlinks=False):
                if any((Path(directory) / n).is_symlink() for n in dirs + names):
                    raise RuntimeError(f'Linked distribution content: {directory}')
                if any(Path(directory) / n not in files for n in names):
                    raise RuntimeError(f'Unowned distribution content: {directory}')
    return files, {name: distributions[name].version for name in sorted(selected)}


def remove_providers(runtime, site):
    files, versions = removal_plan(site)
    report = {'removedDistributions': versions, 'removedBytes': sum(p.stat().st_size for p in files),
              'measurement': 'uncompressed bytes, not installer download savings'}
    # File moves are rolled back on failure. Only RECORD-owned files are removed.
    with tempfile.TemporaryDirectory(prefix='desktop-providers-', dir=runtime.parent) as temp:
        moved = []
        try:
            for path in sorted(files):
                backup = Path(temp) / path.relative_to(site)
                backup.parent.mkdir(parents=True, exist_ok=True)
                path.replace(backup)
                moved.append((path, backup))
        except BaseException:
            for path, backup in reversed(moved):
                path.parent.mkdir(parents=True, exist_ok=True)
                backup.replace(path)
            raise
    for directory, _, _ in os.walk(site, topdown=False):
        path = Path(directory)
        if path != site and not path.is_symlink() and not any(path.iterdir()):
            path.rmdir()
    destination = runtime / 'config/desktop-python-profile.json'
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
    return report


def verify_source(source):
    subprocess.run([sys.executable, '-I', '-B', str(Path(__file__).resolve()), '--verify-source', str(source)],
                   check=True, timeout=90)


def smoke(source):
    class NoSDK(importlib.abc.MetaPathFinder):
        def find_spec(self, fullname, path=None, target=None):
            if fullname.startswith('volcenginesdk'):
                raise ImportError('Volcengine SDK must not be required by desktop Doubao')

    sys.meta_path.insert(0, NoSDK())
    if not (source / 'lazyllm/module/llms/onlinemodule/supplier/doubao.py').is_file():
        raise RuntimeError(f'LazyLLM source not found: {source}')
    sys.path.insert(0, str(source))
    import requests
    from lazyllm.module.llms.onlinemodule.multimodal import OnlineMultiModalModule
    from lazyllm.module.llms.onlinemodule.supplier import doubao

    calls = []

    def send(session, request, **kwargs):
        calls.append((request.method, request.url))
        result = requests.Response()
        result.status_code = 200
        result._content_consumed = True
        if request.url.startswith('https://output.invalid/'):
            assert 'Authorization' not in request.headers
            result._content = b'media'
            return result
        assert request.headers['Authorization'] == 'Bearer smoke-key'
        if request.method == 'POST':
            body = json.loads(request.body)
            assert body['model'] == 'smoke-model'
        if request.url.endswith('/images/generations'):
            body = {'data': [{'url': 'https://output.invalid/image'}]}
        elif request.method == 'POST' and request.url.endswith('/contents/generations/tasks'):
            body = {'id': 'smoke-task'}
        elif request.method == 'GET' and request.url.endswith('/contents/generations/tasks/smoke-task'):
            body = {'status': 'succeeded', 'content': {'video_url': 'https://output.invalid/video'}}
        else:
            raise AssertionError(f'Unexpected request {request.method} {request.url}')
        result._content = json.dumps(body).encode()
        return result

    with patch.object(requests.sessions.Session, 'send', send), \
         patch.object(doubao, 'bytes_to_file', return_value=['/smoke/output']):
        for kind in ('text2image', 'text2video'):
            model = OnlineMultiModalModule(source='doubao', type=kind, model='smoke-model',
                                           api_key='smoke-key', url='https://ark.invalid/api/v3')
            assert '/smoke/output' in model('smoke')
    assert len(calls) == 5
    print('DOUBAO_HTTP_WITHOUT_SDK_OK')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verify-source', type=Path, required=True)
    smoke(parser.parse_args().verify_source.resolve())
