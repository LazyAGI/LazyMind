from __future__ import annotations

import pytest

from lazymind.chat.engine.tools.infra.github_skill_installer import (
    GitHubSkillInstaller,
    GitHubSkillSource,
)
from lazymind.common.skill_document import require_valid_skill_document


class _FixtureInstaller(GitHubSkillInstaller):
    def __init__(self, files):
        super().__init__()
        self.files = files
        self.source = GitHubSkillSource(
            owner='owner',
            repo='repo',
            ref='main',
            skill_path='skills/example',
            canonical_url='https://github.com/owner/repo/tree/main/skills/example',
        )

    def resolve_source(self, github_url):
        return self.source

    def _download_archive(self, source, temp_dir):
        return 'unused.zip'

    def _read_package(self, archive_path, skill_path):
        return self.files


def test_prepare_github_skill_preserves_metadata_and_adds_source_url():
    files = {
        'SKILL.md': (
            '---\n'
            'name: example\n'
            'description: Example skill.\n'
            'category: writing\n'
            'tags:\n'
            '  - reusable\n'
            '---\n'
            'Use the skill.\n'
        ).encode(),
        'references/doc.md': b'Reference.\n',
    }

    package = _FixtureInstaller(files).prepare('https://github.com/owner/repo')
    document = require_valid_skill_document(
        package.files['SKILL.md'].decode(),
        expected_name='example',
    )

    assert package.name == 'example'
    assert package.description == 'Example skill.'
    assert document.metadata['category'] == 'writing'
    assert document.metadata['tags'] == ('reusable',)
    assert document.metadata['github_url'] == package.source.canonical_url
    assert package.files['references/doc.md'] == b'Reference.\n'


@pytest.mark.parametrize(
    ('frontmatter', 'message'),
    [
        ('name: 123\ndescription: Numeric name.', "field 'name' must be a string"),
        ('name: example\ndescription: [not, text]', "field 'description' must be a string"),
    ],
)
def test_prepare_github_skill_rejects_non_string_required_metadata(frontmatter, message):
    files = {'SKILL.md': f'---\n{frontmatter}\n---\nBody.\n'.encode()}

    with pytest.raises(ValueError, match=message):
        _FixtureInstaller(files).prepare('https://github.com/owner/repo')
