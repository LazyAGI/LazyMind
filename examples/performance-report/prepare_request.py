"""Build a pinned adapted Skill ZIP and a fresh Hosted Conversion request."""
import argparse
import base64
import hashlib
import io
import json
from pathlib import Path
import urllib.request
import uuid
import zipfile


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parent
    origin = json.loads((root / 'ORIGIN.json').read_text())
    upstream = f"https://raw.githubusercontent.com/anthropics/knowledge-work-plugins/{origin['commit']}/"
    files = {}
    for target, source in [('SKILL.md', origin['path']),
                           ('references/CONNECTORS.md', origin['connector_path']),
                           ('LICENSE', origin['license_path'])]:
        with urllib.request.urlopen(upstream + source, timeout=30) as response:
            data = response.read()
        if target == 'SKILL.md':
            if digest(data) != origin['original_skill_sha256']:
                raise ValueError('Upstream Skill hash mismatch')
            data = data.replace(b'../../CONNECTORS.md', b'references/CONNECTORS.md')
            data = data.replace(b'name: performance-report\n',
                                b'name: performance-report-offline-20261102\n', 1)
        if digest(data) != origin['files_sha256'][target]:
            raise ValueError(f'Hash mismatch: {target}')
        files[target] = data
    files['ORIGIN.json'] = (root / 'ORIGIN.json').read_bytes()
    archive = io.BytesIO()
    with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_STORED) as output:
        for name, content in sorted(files.items()):
            info = zipfile.ZipInfo(name, date_time=(2026, 11, 2, 0, 0, 0))
            info.external_attr = 0o644 << 16
            output.writestr(info, content)
    scope = (root / 'performance-data.md').read_text()
    inputs = []
    for material in ['performance_data', 'report_scope']:
        data = scope.encode()
        inputs.append({'material_id': material, 'name': material + '.md',
                       'mime_type': 'text/plain',
                       'content_base64': base64.b64encode(data).decode(),
                       'content_hash': 'sha256:' + digest(data)})
    request_id = 'performance-example-' + str(uuid.uuid4())
    request = {'agent_type': 'codex',
               'skill': {'name': 'performance-report-offline-20261102',
                         'zip_base64': base64.b64encode(archive.getvalue()).decode()},
               'task_description': scope,
               'input_files': inputs, 'idempotency_key': request_id,
               'external_thread_id': request_id,
               'config': {'reuse_workflow': False}}
    args.output.mkdir(parents=True, exist_ok=True)
    # Never overwrite a request that might already have been submitted.
    with (args.output / 'request.json').open('x') as output:
        json.dump(request, output, ensure_ascii=False, indent=2)
    with (args.output / 'skill.zip').open('xb') as output:
        output.write(archive.getvalue())
    print('ZIP SHA256:', digest(archive.getvalue()))
    print('Request:', args.output / 'request.json')


if __name__ == '__main__':
    main()
