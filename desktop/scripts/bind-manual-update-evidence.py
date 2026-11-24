"""Bind the dispatcher's v2 plan to native reports from each target's build run."""
import json
from pathlib import Path
import sys


def bind_evidence(plan, builds, verification):
    targets = {}
    for run in builds.iterdir():
        for platform, os_name in [('macos', 'darwin'), ('windows', 'windows')]:
            artifact = run / f'desktop-update-{platform}-{run.name}'
            for path in artifact.glob('*/release.json'):
                record = json.loads(path.read_bytes())
                release = record['release']
                rid = release['releaseId']
                if rid != path.parent.name or release['platform'] != os_name or rid in targets:
                    raise ValueError('Unexpected or duplicate build release')
                archive = verification / run.name / f'desktop-format-evidence-{platform}-{run.name}'
                targets[rid] = (release['updaterProtocol'], archive / 'evidence' / platform)

    def report(source, target):
        if source not in targets or target not in targets:
            raise ValueError('Missing source or target build record')
        # Source IDs must be actual release directory names. The signer then
        # checks the report contents against both releases and the component hash.
        path = targets[target][1] / (source + '.json')
        if not path.is_file() or path.is_symlink() or path.resolve() != path.absolute():
            raise ValueError('Missing native compatibility report for the target build and platform')
        return str(path.absolute())

    plan.pop('repairFormatEvidence', None)
    for edge in plan.get('edges') or []:
        edge.pop('formatEvidence', None)
        if targets[edge['targetReleaseId']][0] == 2:
            edge['formatEvidence'] = report(edge['sourceReleaseId'], edge['targetReleaseId'])
    for repair in plan.get('repairAcceptances') or []:
        source, failed, target = (repair[k] for k in ('sourceReleaseId', 'failedTargetReleaseId', 'repairReleaseId'))
        key = '/'.join([source, failed, target])
        plan.setdefault('repairFormatEvidence', {})[key] = report(source, target)
    return plan


if __name__ == '__main__':
    try:
        plan = bind_evidence(json.load(sys.stdin), Path('publication'), Path('verification'))
    except (OSError, ValueError, TypeError, KeyError):
        raise SystemExit('Could not bind the manual plan to its build records and native evidence') from None
    json.dump(plan, sys.stdout)
