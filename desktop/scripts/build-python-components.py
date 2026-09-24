#!/usr/bin/env python3
"""Split optional distributions from the resolved algorithm environment into ZIPs."""

import argparse
import hashlib
import importlib.metadata as metadata
import importlib.util
import json
import os
from pathlib import Path
import platform
import sys
import sysconfig
from urllib.parse import urlparse
import zipfile

from packaging.requirements import Requirement
from packaging.utils import canonicalize_name


# Shared readers and scientific libraries remain in core for attachments/Skills.
SEEDS = {
    'rag': {'milvus-lite', 'pymilvus', 'spacy', 'bm25s', 'pystemmer', 'sentencepiece', 'nltk'},
}


def digest(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    return (json.dumps(value, sort_keys=True, separators=(',', ':')) + '\n').encode()


def dependency_groups(distributions, protected):
    edges = {}
    for name, dist in distributions.items():
        edges[name] = set()
        for text in dist.requires or ():
            req = Requirement(text)
            if req.marker is None or req.marker.evaluate({'extra': ''}):
                edges[name].add(canonicalize_name(req.name))
    groups = {}
    for component, seeds in SEEDS.items():
        missing = seeds - distributions.keys()
        if missing:
            raise RuntimeError(f'{component} seed dependencies missing: {sorted(missing)}')
        selected = set(seeds)
        while True:
            expanded = selected | (set().union(*(edges.get(name, set()) for name in selected)) - protected)
            expanded &= distributions.keys()
            if expanded == selected:
                break
            selected = expanded
        groups[component] = selected
    # Shared dependencies stay in core. Repeatedly retain dependencies of every
    # retained distribution; never leave a base distribution missing a dependency.
    shared = {name for names in groups.values() for name in names
              if sum(name in group for group in groups.values()) > 1}
    for selected in groups.values():
        selected.difference_update(shared)
    while True:
        optional = set.union(*groups.values())
        required = set().union(*(edges[name] for name in distributions.keys() - optional))
        changed = False
        for selected in groups.values():
            retained = selected & required
            if retained:
                selected.difference_update(retained)
                changed = True
        if not changed:
            break
    for component, seeds in SEEDS.items():
        if not seeds <= groups[component]:
            raise RuntimeError(f'{component} is required by core: {sorted(seeds - groups[component])}')
    return groups


def distribution_files(dist, site):
    if dist.files is None:
        raise RuntimeError(f'Missing RECORD for {dist.metadata["Name"]}')
    result = []
    for entry in dist.files:
        path = Path(os.path.abspath(dist.locate_file(entry)))
        # Console entry points remain in the bundled venv. Milvus uses its
        # metadata entry point via the bundled interpreter after activation.
        if not path.is_relative_to(site) or not path.is_file():
            continue
        if path.is_symlink() or not path.resolve().is_relative_to(site.resolve()):
            raise RuntimeError(f'Unsupported symlink in component: {path}')
        result.append(path)
        if path.suffix == '.py':
            # Imports may create bytecode absent from RECORD, including when
            # pruning is disabled for a size comparison. Move it with its source.
            for cached in (path.parent / '__pycache__').glob(path.stem + '.*.pyc'):
                if cached.is_symlink():
                    raise RuntimeError(f'Unsupported bytecode symlink: {cached}')
                result.append(cached)
            if path.with_suffix('.pyc').is_file():
                result.append(path.with_suffix('.pyc'))
    return result


DEFAULT_BASE_URL = 'https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/'
DEFAULT_FALLBACK_BASE_URL = 'https://huggingface.co/datasets/LazyAGI/LazyMind/resolve/main/'


def split(runtime, output, base_url=DEFAULT_BASE_URL, slim_providers=False):
    # Windows TEMP may use an 8.3 alias (e.g. CUISHA~1); compare canonical paths
    # against the resolved runtime root rather than rejecting the same directory.
    site = Path(sysconfig.get_path('purelib')).resolve()
    if not site.is_relative_to((runtime / 'deps/python/algorithm').resolve()):
        raise RuntimeError('Run this script with the staged algorithm Python, not the system Python')
    if base_url and urlparse(base_url).scheme != 'https':
        raise ValueError('Component download base URL must use HTTPS')
    bootstrap_versions = {canonicalize_name(d.metadata['Name']): d.version
                          for d in metadata.distributions(path=[str(site)]) if d.metadata.get('Name')}
    if slim_providers:
        spec = importlib.util.spec_from_file_location('desktop_profile',
                                                     Path(__file__).with_name('desktop-python-profile.py'))
        profile = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(profile)
        source = runtime / 'app/algorithm/lazyllm'
        if not source.is_dir():
            source = Path(__file__).resolve().parents[2] / 'algorithm/lazyllm'
        profile.verify_source(source)
        profile.remove_providers(runtime, site)
    distributions = {canonicalize_name(d.metadata['Name']): d
                     for d in metadata.distributions(path=[str(site)]) if d.metadata.get('Name')}
    requirements = Path(__file__).resolve().parents[2] / 'algorithm/requirements.txt'
    protected = {canonicalize_name(Requirement(line.strip()).name)
                 for line in requirements.read_text().splitlines() if line.strip() and not line.lstrip().startswith('#')}
    protected -= set.union(*SEEDS.values())
    groups = dependency_groups(distributions, protected)
    if slim_providers and 'grpcio' not in groups['rag']:
        raise RuntimeError('gRPC is still required by the desktop base; review the dependency boundary')
    full_versions = {name: dist.version for name, dist in sorted(distributions.items())}
    identity = {'schemaVersion': 1, 'platform': sys.platform if sys.platform != 'win32' else 'windows',
                'arch': 'amd64' if platform.machine().lower() in {'amd64', 'x86_64'} else 'arm64',
                'pythonAbi': f'cp{sys.version_info.major}{sys.version_info.minor}'}
    # Include resolved versions and RECORD contents, binding components to the
    # exact base environment, including rebuilt wheels with the same version.
    records = {name: digest((d.read_text('RECORD') or '').encode()) for name, d in distributions.items()}
    identity['baseFingerprint'] = digest(encoded({'identity': identity, 'versions': full_versions, 'records': records}))
    output.mkdir(parents=True, exist_ok=True)
    catalog = {**identity, 'components': {}}
    if slim_providers:
        catalog['desktopProfile'] = 'slim-providers-v1'
    planned = {}
    owners = {}
    for name, dist in distributions.items():
        for path in distribution_files(dist, site):
            owners.setdefault(path, set()).add(name)
    for component, names in groups.items():
        paths = sorted({p for name in names for p in distribution_files(distributions[name], site)})
        if any(not owners[path] <= names for path in paths):
            raise RuntimeError(f'{component} shares distribution files with core or another component')
        manifest = {**identity, 'id': component, 'packages': {name: full_versions[name] for name in sorted(names)}}
        contents = {str(p.relative_to(site)).replace('\\', '/'): digest(p.read_bytes()) for p in paths}
        manifest['revision'] = digest(encoded({'manifest': manifest, 'files': contents}))
        filename = (f'lazymind-python-{component}-{identity["platform"]}-{identity["arch"]}-'
                    f'{identity["pythonAbi"]}-{manifest["revision"][:16]}.zip')
        archive = output / filename
        with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as bundle:
            for path in paths:
                info = zipfile.ZipInfo('site-packages/' + path.relative_to(site).as_posix())
                info.compress_type = zipfile.ZIP_DEFLATED
                info.external_attr = (path.stat().st_mode & 0o777) << 16
                bundle.writestr(info, path.read_bytes())
            bundle.writestr(zipfile.ZipInfo('bundle-manifest.json'), encoded(manifest))
        catalog['components'][component] = {
            **manifest, 'filename': filename, 'sha256': digest(archive.read_bytes()),
            'sizeBytes': archive.stat().st_size, 'unpackedBytes': sum(p.stat().st_size for p in paths),
            'url': base_url.rstrip('/') + '/' + filename if base_url else '',
            'fallbackUrl': DEFAULT_FALLBACK_BASE_URL + filename,
        }
        planned[component] = paths
    # Do not remove anything until all complete, verifiable archives exist.
    for paths in planned.values():
        for path in paths:
            path.unlink()
    # Empty namespace directories must not make find_spec report missing
    # optional packages as installed. Keep shared/nonempty namespace directories.
    for directory, _, _ in os.walk(site, topdown=False):
        path = Path(directory)
        if path != site and not path.is_symlink() and not any(path.iterdir()):
            path.rmdir()
    target = runtime / 'config/python-components.json'
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(encoded(catalog))
    (output / 'python-components.json').write_bytes(encoded(catalog))
    (output / 'SHA256SUMS').write_text(''.join(f'{v["sha256"]}  {v["filename"]}\n'
                                              for v in catalog['components'].values()))
    (output / 'algorithm-requirements.lock').write_text(
        ''.join(f'{name}=={version}\n' for name, version in sorted(bootstrap_versions.items())), encoding='utf-8')
    print(json.dumps(catalog, indent=2))
    return catalog


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('runtime', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--slim-providers', action='store_true',
                        help='Remove unused desktop providers and include gRPC in the newly generated RAG bundle')
    args = parser.parse_args()
    split(args.runtime.resolve(), args.output.resolve(),
          os.environ.get('LAZYMIND_PYTHON_COMPONENT_BASE_URL') or DEFAULT_BASE_URL, args.slim_providers)
