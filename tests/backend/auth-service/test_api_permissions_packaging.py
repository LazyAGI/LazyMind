import importlib.util
import json
import re
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
EXTRACTOR = ROOT / 'backend' / 'scripts' / 'extract_api_permissions.py'
DOCKERFILE = ROOT / 'backend' / 'auth-service' / 'Dockerfile'


def _load_extractor():
    spec = importlib.util.spec_from_file_location('api_permission_extractor', EXTRACTOR)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def _scan_entries(scan_root: Path) -> dict[tuple[str, str], tuple[str, ...]]:
    extractor = _load_extractor()
    entries = []
    for go_file in extractor.collect_files(scan_root, {'scripts', 'core', 'vendor'}, '.go'):
        entries.extend(extractor.extract_from_go_file(go_file))
    return {
        (entry['method'], entry['path']): tuple(entry.get('permissions') or [])
        for entry in entries
        if entry['path'].startswith('/api/scan') and entry.get('permissions')
    }


def test_auth_image_packages_scan_permissions(tmp_path):
    dockerfile = DOCKERFILE.read_text(encoding='utf-8')
    assert re.search(r'^COPY scan-control-plane /scan-control-plane$', dockerfile, re.MULTILINE)

    extraction_block = dockerfile.split(
        'RUN python3 /app/scripts/extract_api_permissions.py',
        1,
    )[1].split('\n\n', 1)[0]
    assert '/scan-control-plane' in extraction_block

    output = tmp_path / 'api_permissions.json'
    permission_groups = tmp_path / 'permission_groups.yaml'
    sources = [
        ROOT / 'backend' / 'core',
        ROOT / 'backend' / 'auth-service',
        ROOT / 'backend' / 'channel-gateway',
        ROOT / 'backend' / 'scan-control-plane',
    ]
    subprocess.run(
        [
            sys.executable,
            str(EXTRACTOR),
            '--output',
            str(output),
            '--sync-permission-groups',
            str(permission_groups),
            '--exclude',
            'scripts,core,vendor',
            *(str(source) for source in sources),
        ],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )

    permission_map = json.loads(output.read_text(encoding='utf-8'))
    actual = {
        (entry['method'], entry['path']): tuple(entry.get('permissions') or [])
        for entry in permission_map
        if entry['path'].startswith('/api/scan') and entry.get('permissions')
    }
    expected = _scan_entries(ROOT / 'backend' / 'scan-control-plane')

    assert actual == expected
    assert {'scan.read', 'scan.write', 'user.admin'} == {
        permission
        for permissions in actual.values()
        for permission in permissions
    }
