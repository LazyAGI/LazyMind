"""Select the existing Skill lock verification mode for a pull request."""
import json
import subprocess
import sys


def remote_source_sections(text):
    """Ignore only the repository's plain, top-level bundled_skills block.

    Unfamiliar YAML layouts use full verification. Anchors/tags in the local
    block could also affect remote sources, so they must not be ignored.
    """
    lines = text.splitlines(keepends=True)
    starts = [index for index, line in enumerate(lines) if line.rstrip() == 'bundled_skills:']
    if len(starts) != 1:
        return None
    start = starts[0]
    end = start + 1
    while end < len(lines) and (not lines[end].strip() or lines[end][0].isspace()):
        end += 1
    if any(char in ''.join(lines[start:end]) for char in '&*!'):
        return None
    return ''.join(lines[:start] + lines[end:])


def remote_lock(text):
    catalog = json.loads(text)
    catalog['skills'] = [
        entry for entry in catalog['skills']
        if not entry['source_url'].startswith('builtin://')
    ]
    return catalog


def verification_mode(base):
    protected = [
        'skills/patches', 'skills/featured',
        'backend/core/cmd/builtin-skill-bundle',
        'backend/core/skillv2/builtin', 'backend/core/skillv2/metadata',
        'backend/core/skillv2/skillpackage', 'backend/core/skillv2/skillpatch',
        'backend/core/skillv2/sourceurl',
    ]
    changed = subprocess.run([
        'git', 'diff', '--quiet', base, 'HEAD', '--',
        *(f':(top){path}' for path in protected),
        # Presentation assets are validated by the frozen catalog build.
        ':(top,glob,exclude)skills/featured/*/assets/**',
    ])
    if changed.returncode:
        return 'full'

    def read(revision, path):
        return subprocess.check_output(['git', 'show', f'{revision}:{path}'], text=True)

    sources_path = 'skills/builtin-sources.yaml'
    before = remote_source_sections(read(base, sources_path))
    after = remote_source_sections(read('HEAD', sources_path))
    if before is None or after is None or before != after:
        return 'full'
    lock_path = 'skills/builtin-skills.lock.json'
    if remote_lock(read(base, lock_path)) != remote_lock(read('HEAD', lock_path)):
        return 'full'
    return 'local'


if __name__ == '__main__':
    print(verification_mode(sys.argv[1]))
