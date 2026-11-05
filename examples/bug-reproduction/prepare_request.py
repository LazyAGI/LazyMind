"""Execute only the fixed synthetic fixture twice, then package evidence for LazyMind."""
import argparse
import base64
import hashlib
import io
import json
from pathlib import Path
import platform
import subprocess
import sys
import tempfile
import urllib.request
import uuid
import zipfile


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    root = Path(__file__).resolve().parent
    args.output.mkdir(parents=True, exist_ok=True)
    if (args.output / 'request.json').exists():
        raise FileExistsError('Use a new output directory')
    fixture = (root / 'repro.py').read_bytes()
    runs = []
    with tempfile.TemporaryDirectory(prefix='synthetic-bug-') as directory:
        target = Path(directory) / 'repro.py'
        target.write_bytes(fixture)
        for number in [1, 2]:
            result = subprocess.run([sys.executable, '-I', 'repro.py'], cwd=directory,
                                    capture_output=True, text=True, timeout=10)
            if result.returncode != 1 or 'AssertionError: expected 80, actual -1900' not in result.stderr:
                raise RuntimeError('Fixture did not reproduce the expected assertion failure')
            runs.append({'run': number, 'command': [sys.executable, '-I', 'repro.py'],
                         'exit_code': result.returncode, 'stdout': result.stdout, 'stderr': result.stderr})
    evidence = {'executor': 'external example script; not LazyMind internal execution',
                'python': platform.python_version(), 'platform': platform.platform(),
                'source_sha256': digest(fixture), 'runs': runs}
    (args.output / 'execution-evidence.json').write_text(json.dumps(evidence, indent=2))
    report = ('# Synthetic discount bug\nExpected 80 for a 20 percent discount on 100; actual -1900.\n'
              'No repair requested. The fixed fixture was run twice by an external executor.\n'
              'These failing test exits prove the bug; they do not mean the reporting Workflow failed.\n'
              'Do not claim LazyMind executed commands. No additional execution is requested.\n'
              '```python\n' + fixture.decode() + '```\n```json\n' + json.dumps(evidence, indent=2) + '\n```\n')
    (args.output / 'bug-report.md').write_text(report)
    origin = json.loads((root / 'ORIGIN.json').read_text())
    files = {}
    for name, metadata in origin['files'].items():
        with urllib.request.urlopen(metadata['url'], timeout=30) as response:
            content = response.read()
        if digest(content) != metadata['sha256']:
            raise ValueError('Source hash mismatch: ' + name)
        files[name] = content
    archive = io.BytesIO()
    with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_STORED) as output:
        for name, content in sorted(files.items()):
            info = zipfile.ZipInfo(name, date_time=(2026, 11, 2, 0, 0, 0))
            info.external_attr = 0o644 << 16
            output.writestr(info, content)
    request_id = 'bug-example-' + str(uuid.uuid4())
    request = {'agent_type': 'codex', 'idempotency_key': request_id, 'external_thread_id': request_id,
               'skill': {'name': 'bug-reproduction-brief',
                         'zip_base64': base64.b64encode(archive.getvalue()).decode()},
               'config': {'reuse_workflow': False},
               'task_description': 'Produce an evidence-backed Bug Reproduction Brief from bug_report. '
                                   'Distinguish external execution from LazyMind synthesis; do not repair code.',
               'input_files': [{'material_id': 'bug_report', 'name': 'bug-report.md', 'mime_type': 'text/plain',
                                'content_base64': base64.b64encode(report.encode()).decode(),
                                'content_hash': 'sha256:' + digest(report.encode())}]}
    (args.output / 'request.json').write_text(json.dumps(request, indent=2))
    (args.output / 'skill.zip').write_bytes(archive.getvalue())
    print('Verified test exit codes:', [run['exit_code'] for run in runs])
    print('ZIP SHA256:', digest(archive.getvalue()))
    print('Request:', args.output / 'request.json')


if __name__ == '__main__':
    main()
