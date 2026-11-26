"""Authenticate an explicitly located public test round before building its next app."""

import argparse
import importlib.util
from pathlib import Path
import subprocess
import tempfile
from urllib.request import build_opener

spec = importlib.util.spec_from_file_location('ci_plan', Path(__file__).with_name('prepare-update-ci-plan.py'))
plan = importlib.util.module_from_spec(spec)
spec.loader.exec_module(plan)


def verify(config_path, validator, fetch=None):
    config, _ = plan.read_json(config_path)
    if config['mode'] != 'unsigned-test' or 'https://modelscope.cn' not in {o.rstrip('/') for o in config['allowedOrigins']}:
        raise plan.PlanError('Expected the configured isolated ModelScope test source')
    plan.publisher.destination(config['policyURL'], 'https://modelscope.cn/datasets/')
    if fetch is None:
        opener = build_opener(plan.publisher.PinnedRedirects(config['allowedOrigins']))
        fetch = lambda url: opener.open(url, timeout=60)
    envelope = plan.publisher.read_remote_policy(fetch, config['policyURL'])
    if envelope is None:
        raise plan.PlanError('No signed in-place baseline exists at this locator. Start a new round.')
    with tempfile.TemporaryDirectory() as temp:
        previous = Path(temp) / 'policy.json'
        previous.write_bytes(envelope)
        return plan.verified_policy(validator, previous, config_path, source_only=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target-config', type=Path, required=True)
    parser.add_argument('--validator', type=Path, required=True)
    args = parser.parse_args()
    try:
        source = verify(args.target_config, args.validator)
    except plan.PlanError as exc:
        raise SystemExit(str(exc)) from None
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        raise SystemExit('Could not authenticate the requested public test baseline') from None
    print('Verified published baseline ' + source['releaseId'])
