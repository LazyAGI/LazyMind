"""Run native source-to-target probes for one platform's verified CI plan."""

import argparse
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def verify_pairs(plan, current, baseline, output, platform, unsigned_test):
    reports = output / 'evidence' / platform
    reports.mkdir(parents=True)
    probe = Path(__file__).with_name('verify-update-format.py')
    with tempfile.TemporaryDirectory() as scratch:
        for pair in json.loads((plan / 'pairs.json').read_text(encoding='utf-8')):
            source = pair['sourceReleaseId']
            target = pair['targetReleaseId']
            source_record = output / 'records' / platform / source / 'release.json'
            source_record.parent.mkdir(parents=True)
            command = [sys.executable, str(probe), '--target', str(current / target),
                       '--output', str(reports / (source + '.json'))]
            if unsigned_test:
                command.extend(['--unsigned-test', '--target-components-dir', str(current / 'python-components')])
            if pair['baseline']:
                command.extend(['--source', str(baseline / source)])
                if unsigned_test:
                    command.extend(['--source-components-dir', str(baseline / 'python-components')])
            else:
                published = Path(scratch) / 'source.json'
                published.write_text(json.dumps(pair['sourceRelease']), encoding='utf-8')
                command.extend(['--published-source', str(published), '--source-record-output', str(source_record)])
            print(f'Verifying {source} -> {target}', flush=True)
            subprocess.run(command, check=True)
            if pair['baseline']:
                shutil.copyfile(baseline / source / 'release.json', source_record)
    shutil.copyfile(plan / 'expected-policy-sha256', output / (platform + '-policy-sha256'))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ['plan', 'current', 'baseline', 'output']:
        parser.add_argument('--' + name, required=True, type=Path)
    parser.add_argument('--platform', required=True, choices=['macos', 'windows'])
    parser.add_argument('--unsigned-test', action='store_true')
    verify_pairs(**vars(parser.parse_args()))
