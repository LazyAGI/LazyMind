#!/usr/bin/env python3
"""Publish verified desktop update files, then one signed policy. No private key is needed."""
import argparse
import hashlib
import json
import logging
import os
from pathlib import Path
import re
import subprocess
import tempfile
from urllib.error import HTTPError
from urllib.parse import urlsplit, unquote
from urllib.request import HTTPRedirectHandler, build_opener


MAX_POLICY_BYTES = 1024 * 1024
log = logging.getLogger('desktop-update-publication')


class PinnedRedirects(HTTPRedirectHandler):
    def __init__(self, origins):
        self.origins = {origin.rstrip('/') for origin in origins}

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        parsed = urlsplit(newurl)
        if (parsed.scheme != 'https' or parsed.username or parsed.password or parsed.fragment
                or f'{parsed.scheme}://{parsed.netloc}' not in self.origins):
            raise ValueError('Untrusted update download redirect')
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def destination(url, base):
    if not isinstance(url, str) or not url.startswith(base):
        raise ValueError('Policy/artifact URL does not match upload repository')
    parsed = urlsplit(url)
    relative = url[len(base):]
    if (parsed.query or parsed.fragment or unquote(relative) != relative or '\\' in relative
            or any(part in ('', '.', '..') for part in relative.split('/'))):
        raise ValueError('Unsafe publication path')
    return relative


def read_remote_policy(fetch, url):
    try:
        with fetch(url) as stream:
            data = stream.read(MAX_POLICY_BYTES + 1)
            if len(data) > MAX_POLICY_BYTES:
                raise ValueError('Remote policy too large')
            return data
    except HTTPError as error:
        if error.code == 404:
            return None
        raise


def digest(stream, expected):
    size, hashed = 0, hashlib.sha256()
    while chunk := stream.read(min(1024 * 1024, expected['size'] - size + 1)):
        size += len(chunk)
        if size > expected['size']:
            raise ValueError('Artifact size mismatch')
        hashed.update(chunk)
    if size != expected['size'] or hashed.hexdigest() != expected['sha256']:
        raise ValueError('Artifact size/hash mismatch; immutable files cannot be replaced')


def publish(directory, policy_path, config_path, repo, *, api=None, fetch=None, validate=None, validator=None,
            expected_policy_sha256=None):
    log.info('Validating publication inputs')
    directory = Path(directory).resolve()
    config = json.loads(Path(config_path).read_text())
    envelope = Path(policy_path).read_bytes()
    if len(envelope) > MAX_POLICY_BYTES:
        raise ValueError('Policy too large')
    if validate is None:
        if not validator:
            raise ValueError('Offline policy validator is required')
        def check(raw, historical=False):
            with tempfile.TemporaryDirectory(prefix='lm-policy-check-') as temp:
                filename = Path(temp) / 'policy.json'
                filename.write_bytes(raw)
                command = [str(validator), 'verify', '--config', str(config_path), '--envelope', str(filename)]
                if historical:
                    command.append('--historical')
                result = subprocess.run(command,
                                        capture_output=True, text=True, timeout=30)
                if result.returncode:
                    raise ValueError('Policy signature or contract verification failed')
                return json.loads(result.stdout)
        validate = check
        validate_history = lambda raw: check(raw, historical=True)
    else:
        validate_history = validate
    policy = validate(envelope)
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*', repo):
        raise ValueError('Invalid ModelScope repository')
    base = f'https://modelscope.cn/datasets/{repo}/resolve/master/'
    policy_url = config['policyURL']
    policy_destination = destination(policy_url, base)
    if fetch is None:
        opener = build_opener(PinnedRedirects(config['allowedOrigins']))
        fetch = lambda url: opener.open(url, timeout=60)
    def remote_exists(artifact):
        try:
            with fetch(artifact['url']) as stream:
                digest(stream, artifact)
            return True
        except HTTPError as error:
            if error.code == 404:
                return False
            raise
    log.info('Checking the published policy')
    old = read_remote_policy(fetch, policy_url)
    if expected_policy_sha256 is not None:
        actual = 'absent' if old is None else hashlib.sha256(old).hexdigest()
        if expected_policy_sha256 != actual:
            raise ValueError('Remote policy does not match the publication plan')
    if old is not None:
        previous = validate_history(old)
        if envelope != old and policy['policySequence'] <= previous['policySequence']:
            raise ValueError('Policy sequence regression or conflict')
    # Validate every local file and destination before the first remote mutation.
    log.info('Verifying local release artifacts')
    items, seen = [], set()
    for release in policy['releases']:
        rid = release['releaseId']
        if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,127}', rid):
            raise ValueError('Invalid release ID')
        for kind, filename in [('package', 'package.zip' if release['platform'] == 'darwin' else 'package.exe'),
                               ('feed', 'feed.json'), ('components', 'components.json')]:
            artifact = release[kind]
            target = destination(artifact['url'], base)
            if not target.endswith(f'/releases/{rid}/{filename}') or target in seen or target == policy_destination:
                raise ValueError('Invalid or duplicate immutable destination')
            seen.add(target)
            files = list(directory.glob(f'**/{rid}/{filename}'))
            if len(files) > 1:
                raise ValueError('Duplicate local release files')
            local = files[0] if files else None
            if local is not None:
                if local.is_symlink() or not local.is_file() or not local.resolve().is_relative_to(directory):
                    raise ValueError('Unsafe local artifact path')
                with local.open('rb') as stream:
                    digest(stream, artifact)
            items.append((artifact, target, local))
    log.info('Verifying existing remote release artifacts')
    missing = []
    for artifact, target, local in items:
        if not remote_exists(artifact):
            if local is None:
                raise ValueError('Historical artifact missing remotely and locally')
            missing.append((artifact, target, local))
    if api is None:
        token = os.environ.get('MODELSCOPE_API_TOKEN')
        if not token:
            raise ValueError('MODELSCOPE_API_TOKEN is required')
        from modelscope_hub import HubApi
        api = HubApi(token=token, endpoint='https://modelscope.cn')
    def upload(source, target):
        api.upload_file(repo_id=repo, repo_type='dataset', path_or_fileobj=str(source), path_in_repo=target,
                        revision='master', commit_message='Publish desktop update artifact')
    for artifact, target, local in missing:
        log.info('Uploading immutable %s', local.name)
        upload(local, target)
        log.info('Reading back immutable %s', local.name)
        if not remote_exists(artifact):
            raise ValueError('Uploaded artifact is not publicly downloadable')
    # Workflows serialize this operation. Detect intervening external publishers too.
    log.info('Rechecking the previous policy')
    if read_remote_policy(fetch, policy_url) != old:
        raise ValueError('Remote policy changed during publication; retry with a new plan')
    if envelope == old:
        return
    with tempfile.TemporaryDirectory(prefix='lm-policy-publish-') as temp:
        snapshot = Path(temp) / 'policy.json'
        snapshot.write_bytes(envelope)
        log.info('Uploading signed policy')
        upload(snapshot, policy_destination)
    log.info('Reading back signed policy')
    if read_remote_policy(fetch, policy_url) != envelope:
        raise ValueError('Published policy readback mismatch')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, required=True)
    parser.add_argument('--policy', type=Path, required=True)
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--repo', required=True)
    parser.add_argument('--validator', type=Path, required=True)
    parser.add_argument('--expected-policy-sha256', help="Required prior policy SHA-256, or 'absent' for the first release")
    args = parser.parse_args()
    log.setLevel(logging.INFO)
    log.addHandler(logging.StreamHandler())
    try:
        publish(args.directory, args.policy, args.config, args.repo, validator=args.validator.resolve(),
                expected_policy_sha256=args.expected_policy_sha256)
    except Exception:
        # SDK/network errors may contain temporary signed URLs or credentials.
        raise SystemExit('Desktop update publication failed; policy must not be assumed published. Reconcile remote state before retrying.') from None
    print('Desktop update files and signed policy publicly verified.')
