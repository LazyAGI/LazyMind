"""Run native source-to-target probes for one platform's verified CI plan."""

import argparse
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def verify_pairs(plan, current, baseline, output, platform, unsigned_test, target_components_dir=None):
    reports = output / 'evidence' / platform
    reports.mkdir(parents=True)
    probe = Path(__file__).with_name('verify-update-format.py')
    pairs = json.loads((plan / 'pairs.json').read_text(encoding='utf-8'))
    with tempfile.TemporaryDirectory() as scratch:
        for pair in pairs:
            source = pair['sourceReleaseId']
            target = pair['targetReleaseId']
            source_record = output / 'records' / platform / source / 'release.json'
            source_record.parent.mkdir(parents=True)
            command = [sys.executable, str(probe), '--target', str(current / target),
                       '--output', str(reports / (source + '.json'))]
            if unsigned_test:
                command.extend(['--unsigned-test', '--target-components-dir', str(current / 'python-components')])
            elif target_components_dir is not None:
                command.extend(['--target-components-dir', str(target_components_dir)])
            local_source = baseline / source
            use_build_artifact = pair['baseline'] and (local_source / 'release.json').is_file()
            if use_build_artifact:
                command.extend(['--source', str(local_source)])
                if unsigned_test:
                    command.extend(['--source-components-dir', str(baseline / 'python-components')])
            else:
                published = Path(scratch) / 'source.json'
                published.write_text(json.dumps(pair['sourceRelease']), encoding='utf-8')
                command.extend(['--published-source', str(published), '--source-record-output', str(source_record)])
            print(f'Verifying {source} -> {target}', flush=True)
            subprocess.run(command, check=True)
            if pair['baseline'] and json.loads((reports / (source + '.json')).read_text(encoding='utf-8')).get('passed') is not True:
                raise ValueError('The latest published source must pass native compatibility verification')
            if use_build_artifact:
                shutil.copyfile(local_source / 'release.json', source_record)
    shutil.copyfile(plan / 'expected-policy-sha256', output / (platform + '-policy-sha256'))
    targets = list(current.glob('*/release.json'))
    if len(targets) != 1:
        raise ValueError('Expected exactly one current native release for the installed upgrade smoke')
    shutil.copyfile(targets[0], output / (platform + '-target.json'))
    baseline_pairs = [pair for pair in pairs if pair['baseline']]
    if len(baseline_pairs) > 1 or (pairs and not baseline_pairs):
        raise ValueError('Expected exactly one verified baseline for this platform')
    (output / (platform + '-baseline.json')).write_text(json.dumps(baseline_pairs), encoding='utf-8')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ['plan', 'current', 'baseline', 'output']:
        parser.add_argument('--' + name, required=True, type=Path)
    parser.add_argument('--platform', required=True, choices=['macos', 'windows'])
    parser.add_argument('--target-components-dir', type=Path)
    parser.add_argument('--unsigned-test', action='store_true')
    verify_pairs(**vars(parser.parse_args()))
