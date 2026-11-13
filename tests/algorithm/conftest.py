"""
Pytest fixtures for algorithm tests.
Add algorithm and backend paths for imports.
"""
import os
import sys

import pytest

_root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
_algo = os.path.join(_root, 'algorithm')
if _algo not in sys.path:
    sys.path.insert(0, _algo)

# Worker subprocesses need the same source root as the pytest process.
_pythonpath = os.environ.get('PYTHONPATH', '').split(os.pathsep)
if _algo not in _pythonpath:
    os.environ['PYTHONPATH'] = os.pathsep.join([_algo, *filter(None, _pythonpath)])


@pytest.fixture(autouse=True)
def isolate_ppt_resource_prefetch(request, monkeypatch):
    # Workflow tests exercise preparation without starting real cloud downloads.
    # The resource suite tests the actual transport and background executor.
    if request.node.path.name != 'test_presentation_resources.py':
        monkeypatch.setattr('lazymind.presentation_resources.prefetch', lambda: None)


@pytest.fixture
def ppt_asset_library(tmp_path_factory, monkeypatch):
    """Small original test drawings stand in for cloud payloads, never the resolver."""
    import hashlib
    import json
    from PIL import Image
    from lazymind import presentation_resources

    root = tmp_path_factory.mktemp('cloud-assets')
    data = {'sources': {}, 'assets': []}
    for collection, name in [('fluent-3d', 'clipboard'), ('fluent-color', 'clipboard-24')]:
        filename = collection + '--' + name
        png = root / (filename + '.png')
        Image.new('RGB', (160, 80), 'blue').save(png)
        svg = root / (filename + '.svg')
        svg.write_text('<svg xmlns="http://www.w3.org/2000/svg" width="160" height="80"'
                       ' viewBox="0 0 160 80" preserveAspectRatio="xMidYMid meet">'
                       '<rect width="160" height="80" fill="blue"/></svg>')
        license_file = collection + '.txt'
        (root / license_file).write_text('Original test drawing. ' * 20)
        data['sources'][collection] = {'name': collection, 'style': collection, 'license': 'MIT',
                                       'license_file': license_file, 'source_url': 'https://github.com/example/test'}
        data['assets'].append({'name': collection + ':' + name, 'collection': collection, 'style': collection,
                               'tags': ['需求分析', '需求', '素材'], 'width': 160, 'height': 80,
                               'png': png.name, 'png_sha256': hashlib.sha256(png.read_bytes()).hexdigest(),
                               **({'svg': svg.name, 'svg_sha256': hashlib.sha256(svg.read_bytes()).hexdigest()}
                                  if collection == 'fluent-color' else {})})
    (root / 'catalog.json').write_text(json.dumps(data))
    real = presentation_resources.ensure_resource
    monkeypatch.setattr(presentation_resources, 'ensure_resource',
                        lambda kind: root if kind == 'assets' else real(kind))

    module = sys.modules.get('presentation_assets')
    clear = module.catalog.cache_clear if module else lambda: None
    clear()
    try:
        yield root
    finally:
        clear()
