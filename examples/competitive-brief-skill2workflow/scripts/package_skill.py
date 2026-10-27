#!/usr/bin/env python3
"""Build a pinned, dependency-complete marketing Skill locally; never install/run it.

Upstream bodies stay outside Git. Use --source-root for an offline pinned checkout,
or --fetch for three bounded public reads. An output directory must be new.
"""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import posixpath
import re
from urllib.parse import unquote, urlsplit
from urllib.request import build_opener, HTTPRedirectHandler, Request
from zipfile import ZipFile, ZipInfo, ZIP_STORED

CASE = Path(__file__).resolve().parents[1]
LOCK = json.loads((CASE / 'source-lock.json').read_text())
ALIAS = 'competitive-brief-marketing'
OLD_LINK = '../../CONNECTORS.md'
NEW_LINK = 'references/CONNECTORS.md'
CHANGE_NOTE = '<!-- Local packaging adaptation: marketing alias and bundled CONNECTORS link only; see ORIGIN.json and NOTICE.txt. -->\n'


def sha256(raw):
    return hashlib.sha256(raw).hexdigest()


def verify_source(raw, spec):
    blob = hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest()
    if (len(raw), blob, sha256(raw)) != (spec['bytes'], spec['blob'], spec['sha256']):
        raise ValueError('Pinned source mismatch: ' + spec['path'])


def check_links(files):
    """Check inline Markdown file links in this pinned package (no external reads)."""
    for name, raw in files.items():
        if not name.endswith('.md'):
            continue
        for target in re.findall(r'!?\[[^\]\n]*\]\(([^)\s]+)\)', raw.decode()):
            parsed = urlsplit(target)
            if parsed.scheme or parsed.netloc or not parsed.path:
                continue
            decoded = unquote(parsed.path)
            resolved = posixpath.normpath(posixpath.join(str(PurePosixPath(name).parent), decoded))
            if decoded.startswith('/') or resolved == '..' or resolved.startswith('../') or resolved not in files:
                raise ValueError(f'Unresolved package dependency: {name} -> {target}')


def assemble(inputs):
    for key in ('primary', 'dependency', 'license'):
        verify_source(inputs[key], LOCK[key])
    original = inputs['primary'].decode()
    if original.count(OLD_LINK) != 1 or original.count('name: competitive-brief\n') != 1:
        raise ValueError('Unexpected upstream layout')
    adapted = original.replace('name: competitive-brief\n', f'name: {ALIAS}\n', 1)
    adapted = adapted.replace('`/competitive-brief`', f'`/{ALIAS}`')
    adapted = adapted.replace(OLD_LINK, NEW_LINK, 1)
    # Leave the frontmatter first for ordinary Skill installers.
    end = adapted.index('\n---\n', 4) + len('\n---\n')
    adapted = adapted[:end] + '\n' + CHANGE_NOTE + adapted[end:]
    origin = {
        'repository': LOCK['repository'], 'commit': LOCK['commit'],
        'primary': LOCK['primary'], 'dependency': LOCK['dependency'], 'license': LOCK['license'],
        'package_name': ALIAS,
        'changes': ['Skill name and slash trigger use marketing alias',
                    'CONNECTORS link rewritten to bundled references/CONNECTORS.md',
                    'Added modification notice; other instructions unchanged'],
        'packaged_skill_sha256': sha256(adapted.encode()),
        'execution_status': 'not_run: no install, model conversion or execution by this builder',
        'connector_configuration': 'Catalog text only; no .mcp.json, servers, tokens or permissions installed',
    }
    notice = (f'Derived from Anthropic {LOCK["repository"]}, commit {LOCK["commit"]}.\n'
              f'Original Skill: {LOCK["primary"]["path"]}\n'
              f'Dependency: {LOCK["dependency"]["path"]}\n'
              'The upstream LICENSE is copied byte-for-byte, including its trailing text.\n'
              'Local modifications: marketing-specific name/trigger, dependency link and this notice.\n'
              'No other upstream instructions changed. No affiliation or endorsement claimed.\n'
              'CONNECTORS describes categories; copying it does not provision any connector.\n'
              'This is a portable example package, not a fix to any official installer.\n')
    files = {'SKILL.md': adapted.encode(), NEW_LINK: inputs['dependency'],
             'LICENSE': inputs['license'], 'NOTICE.txt': notice.encode(),
             'ORIGIN.json': (json.dumps(origin, ensure_ascii=False, indent=2, sort_keys=True) + '\n').encode()}
    check_links(files)
    return files


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError('Unexpected redirect; recheck the pinned source')


def inputs_from(source_root=None):
    inputs = {}
    opener = build_opener(NoRedirect)
    for key in ('primary', 'dependency', 'license'):
        spec = LOCK[key]
        if source_root is not None:
            raw = (source_root / spec['path']).read_bytes()
        else:
            url = f'https://raw.githubusercontent.com/{LOCK["repository"]}/{LOCK["commit"]}/{spec["path"]}'
            with opener.open(Request(url, headers={'User-Agent': 'LazyMind-pinned-example'}), timeout=30) as r:
                raw = r.read(65537)
        if len(raw) > 65536:
            raise ValueError('Source exceeds bounded size')
        verify_source(raw, spec)
        inputs[key] = raw
    return inputs


def build(out, inputs):
    files = assemble(inputs)
    out.mkdir(parents=True, exist_ok=False)
    package = out / ALIAS
    for name, raw in files.items():
        path = package / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(raw)
    archive = out / (ALIAS + '.zip')
    with ZipFile(archive, 'w', compression=ZIP_STORED) as z:
        for name, raw in sorted(files.items()):
            info = ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.create_system = 3
            info.external_attr = 0o100644 << 16
            z.writestr(info, raw)
    receipt = {'mode': 'local dependency packaging only; no runtime E2E',
               'source_commit': LOCK['commit'], 'source_path': LOCK['primary']['path'],
               'archive': archive.name, 'archive_sha256': sha256(archive.read_bytes()),
               'files': {n: {'bytes': len(r), 'sha256': sha256(r)} for n, r in sorted(files.items())}}
    (out / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
    return receipt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument('--source-root', type=Path, help='Pinned upstream checkout or verified files; no network')
    mode.add_argument('--fetch', action='store_true', help='Read three pinned public files; stop on any HTTP error')
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    receipt = build(args.out, inputs_from(args.source_root))
    print(json.dumps(receipt, indent=2))


if __name__ == '__main__':
    main()
