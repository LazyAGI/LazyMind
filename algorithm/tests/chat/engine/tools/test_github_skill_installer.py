import io
import json
import zipfile

import requests
import pytest

from lazymind.chat.engine.tools.infra import github_skill_installer as installer_mod
from lazymind.chat.engine.tools.infra.github_skill_installer import GitHubSkillInstaller


def _response(url, *, status=200, json_data=None, content=b''):
    response = requests.Response()
    response.status_code = status
    response.url = url
    response.reason = 'OK' if status < 400 else 'error'
    response._content = json.dumps(json_data).encode() if json_data is not None else content
    response.raw = io.BytesIO(response._content)
    response.headers['Content-Type'] = 'application/json' if json_data is not None else 'application/zip'
    return response


def _zip_bytes(files):
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w') as archive:
        for path, content in files.items():
            archive.writestr(path, content)
    return output.getvalue()


def _zip_with_symlink(path, target):
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w') as archive:
        archive.writestr(
            'repo-main/SKILL.md',
            '---\nname: example\ndescription: Example.\n---\nUse this skill.\n',
        )
        info = zipfile.ZipInfo(path)
        info.create_system = 3
        info.external_attr = 0o120777 << 16
        archive.writestr(info, target)
    return output.getvalue()


class _FakeSession:
    def __init__(self, responses):
        self.responses = list(responses)
        self.calls = []

    def get(self, url, **kwargs):
        self.calls.append((url, kwargs))
        return self.responses.pop(0)


def test_prepare_repository_root_installs_complete_normalized_package():
    archive = _zip_bytes({
        'example-main/SKILL.md': (
            b'---\nname: example-skill\ndescription: Example.\ncategory: upstream\n'
            b'github_url: https://example.test/old\nlicense: MIT\n---\nUse this skill.\n'
        ),
        'example-main/scripts/run.py': b'print("ok")\n',
        'example-main/assets/logo.bin': b'\x00\x01',
        'example-main/.github/workflows/ci.yml': b'ignored',
        'example-main/scripts/__pycache__/run.pyc': b'ignored',
        'example-main/.DS_Store': b'ignored',
    })
    session = _FakeSession([
        _response('https://api.github.com/repos/Owner/example', json_data={'default_branch': 'main'}),
        _response('https://codeload.github.com/Owner/example/zip/main', content=archive),
    ])

    package = GitHubSkillInstaller(session=session).prepare(
        'https://www.github.com/Owner/example.git/?utm_source=test#readme'
    )

    assert package.name == 'example-skill'
    assert package.category == 'external'
    assert package.source.canonical_url == 'https://github.com/Owner/example/tree/main'
    assert package.source.identity == ('owner/example', '')
    assert package.files['scripts/run.py'] == b'print("ok")\n'
    assert package.files['assets/logo.bin'] == b'\x00\x01'
    assert '.github/workflows/ci.yml' not in package.files
    assert 'scripts/__pycache__/run.pyc' not in package.files
    assert '.DS_Store' not in package.files
    skill_md = package.files['SKILL.md'].decode()
    assert 'category: external' in skill_md
    assert 'github_url: https://github.com/Owner/example/tree/main' in skill_md
    assert 'license: MIT' in skill_md
    assert 'https://example.test/old' not in skill_md
    assert skill_md.endswith('Use this skill.\n')
    assert session.calls[0][0] == 'https://api.github.com/repos/Owner/example'
    assert session.calls[1][0] == 'https://codeload.github.com/Owner/example/zip/main'


def test_prepare_tree_url_resolves_longest_slash_ref_and_exact_skill_path():
    archive = _zip_bytes({
        'repo-feature-foo/skills/example/SKILL.md': (
            b'---\nname: example\ndescription: Example.\n---\nUse this skill.\n'
        ),
        'repo-feature-foo/skills/example/references/guide.md': b'guide\n',
        'repo-feature-foo/skills/other/SKILL.md': b'not selected\n',
    })
    session = _FakeSession([
        _response('candidate', status=422),
        _response('resolved', json_data={'sha': 'abc'}),
        _response('archive', content=archive),
    ])

    package = GitHubSkillInstaller(session=session).prepare(
        'https://github.com/Owner/repo/tree/feature/foo/skills/example',
        category='engineering',
    )

    assert package.category == 'engineering'
    assert package.source.ref == 'feature/foo'
    assert package.source.skill_path == 'skills/example'
    assert package.source.identity == ('owner/repo', 'skills/example')
    assert package.source.canonical_url == (
        'https://github.com/Owner/repo/tree/feature/foo/skills/example'
    )
    assert set(package.files) == {'SKILL.md', 'references/guide.md'}
    assert session.calls[0][0].endswith('/commits/feature%2Ffoo%2Fskills')
    assert session.calls[1][0].endswith('/commits/feature%2Ffoo')
    assert session.calls[2][0].endswith('/zip/feature%2Ffoo')


@pytest.mark.parametrize(
    ('archive', 'error'),
    [
        (
            _zip_bytes({
                'repo-main/SKILL.md': (
                    b'---\nname: example\ndescription: Example.\n---\nUse this skill.\n'
                ),
                'repo-main/../escaped.txt': b'no',
            }),
            'Unsafe ZIP path',
        ),
        (
            _zip_with_symlink('repo-main/scripts/run.py', '../outside.py'),
            'Symbolic links are not allowed',
        ),
        (
            _zip_bytes({
                'repo-main/SKILL.md': (
                    b'---\nname: example\ndescription: Example.\n---\nUse this skill.\n'
                ),
                'repo-main/references//guide.md': b'no',
            }),
            'Unsafe ZIP path',
        ),
        (
            _zip_bytes({
                'repo-main/SKILL.md': (
                    b'---\nname: example\ndescription: Example.\n---\nUse this skill.\n'
                ),
                'repo-main/references/./guide.md': b'no',
            }),
            'Unsafe ZIP path',
        ),
    ],
)
def test_prepare_rejects_unsafe_archive_entries(archive, error):
    session = _FakeSession([
        _response('metadata', json_data={'default_branch': 'main'}),
        _response('archive', content=archive),
    ])

    with pytest.raises(ValueError, match=error):
        GitHubSkillInstaller(session=session).prepare('https://github.com/owner/repo')


@pytest.mark.parametrize('github_url', [
    'http://github.com/owner/repo',
    'https://raw.githubusercontent.com/owner/repo/main/SKILL.md',
    'https://user@github.com/owner/repo',
    'https://github.com:443/owner/repo',
    'https://github.com/owner/repo/blob/main/SKILL.md',
    'https://github.com/owner/repo/tree/main',
    'https://github.com/owner/repo/tree/main/%2E%2E/secret',
])
def test_resolve_source_rejects_unsupported_or_unsafe_urls(github_url):
    with pytest.raises(ValueError):
        GitHubSkillInstaller(session=_FakeSession([])).resolve_source(github_url)


@pytest.mark.parametrize(
    ('skill_md', 'error'),
    [
        (b'\xff\xfe', 'valid UTF-8'),
        (b'---\nname: example\n---\nBody\n', "non-empty 'description'"),
        (b'---\nname: example\ndescription: Example.\n---\n', 'markdown content'),
        (b'---\n- item\n---\nBody\n', 'must be a mapping'),
    ],
)
def test_prepare_rejects_invalid_skill_md(skill_md, error):
    archive = _zip_bytes({'repo-main/SKILL.md': skill_md})
    session = _FakeSession([
        _response('metadata', json_data={'default_branch': 'main'}),
        _response('archive', content=archive),
    ])

    with pytest.raises(ValueError, match=error):
        GitHubSkillInstaller(session=session).prepare('https://github.com/owner/repo')


@pytest.mark.parametrize(
    ('limit_name', 'limit_value', 'files', 'error'),
    [
        (
            '_MAX_FILE_BYTES',
            100,
            {
                'repo-main/SKILL.md': (
                    b'---\nname: example\ndescription: Example.\n---\nUse this skill.\n'
                ),
                'repo-main/assets/large.bin': b'x' * 101,
            },
            '10 MiB limit',
        ),
        (
            '_MAX_EXPANDED_BYTES',
            70,
            {
                'repo-main/SKILL.md': (
                    b'---\nname: example\ndescription: Example.\n---\nUse this skill.\n'
                ),
                'repo-main/assets/data.bin': b'x' * 20,
            },
            'expanded size limit',
        ),
        (
            '_MAX_FILES',
            1,
            {
                'repo-main/SKILL.md': (
                    b'---\nname: example\ndescription: Example.\n---\nUse this skill.\n'
                ),
                'repo-main/references/guide.md': b'guide',
            },
            'file limit',
        ),
        (
            '_MAX_SKILL_MD_BYTES',
            10,
            {
                'repo-main/SKILL.md': (
                    b'---\nname: example\ndescription: Example.\n---\nUse this skill.\n'
                ),
            },
            'loading limit',
        ),
    ],
)
def test_prepare_enforces_expanded_package_limits(monkeypatch, limit_name, limit_value, files, error):
    monkeypatch.setattr(installer_mod, limit_name, limit_value)
    session = _FakeSession([
        _response('metadata', json_data={'default_branch': 'main'}),
        _response('archive', content=_zip_bytes(files)),
    ])

    with pytest.raises(ValueError, match=error):
        GitHubSkillInstaller(session=session).prepare('https://github.com/owner/repo')


def test_prepare_enforces_streaming_download_limit(monkeypatch):
    monkeypatch.setattr(installer_mod, '_MAX_DOWNLOAD_BYTES', 5)
    session = _FakeSession([
        _response('metadata', json_data={'default_branch': 'main'}),
        _response('archive', content=b'123456'),
    ])

    with pytest.raises(ValueError, match='download limit'):
        GitHubSkillInstaller(session=session).prepare('https://github.com/owner/repo')
