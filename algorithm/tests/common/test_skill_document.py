from __future__ import annotations

import pytest

from lazymind.common.skill_document import (
    SkillDocumentError,
    require_valid_skill_document,
)


def test_require_valid_skill_document_returns_immutable_parsed_document():
    content = (
        '---\n'
        'name: example-skill\n'
        'description: Example skill.\n'
        'category: writing\n'
        'options:\n'
        '  tags:\n'
        '    - reusable\n'
        '---\n'
        '# Steps\n\nUse the skill.\n'
    )

    document = require_valid_skill_document(content, expected_name='example-skill')

    assert document.metadata == {
        'name': 'example-skill',
        'description': 'Example skill.',
        'category': 'writing',
        'options': {'tags': ('reusable',)},
    }
    assert document.body == '# Steps\n\nUse the skill.\n'
    with pytest.raises(TypeError):
        document.metadata['name'] = 'renamed'
    with pytest.raises(TypeError):
        document.metadata['options']['new'] = True
    with pytest.raises(AttributeError):
        document.metadata['options']['tags'].append('mutable')


@pytest.mark.parametrize(
    ('content', 'expected_code', 'expected_field'),
    [
        ('', 'empty_content', None),
        ('No frontmatter.\n', 'missing_frontmatter', None),
        ('---\nname: [broken\n---\nBody.\n', 'invalid_frontmatter_yaml', None),
        ('---\n- list item\n---\nBody.\n', 'frontmatter_not_mapping', None),
        ('---\ndescription: Missing name.\n---\nBody.\n', 'missing_field', 'name'),
        ('---\nname: valid\n---\nBody.\n', 'missing_field', 'description'),
        (
            '---\nname: 123\ndescription: Numeric name.\n---\nBody.\n',
            'invalid_field_type',
            'name',
        ),
        (
            '---\nname: valid\ndescription: [not, text]\n---\nBody.\n',
            'invalid_field_type',
            'description',
        ),
        ('---\nname: ""\ndescription: Empty name.\n---\nBody.\n', 'missing_field', 'name'),
        ('---\nname: valid\ndescription: ""\n---\nBody.\n', 'missing_field', 'description'),
        ('---\nname: invalid/name\ndescription: Invalid name.\n---\nBody.\n', 'invalid_name', 'name'),
        (
            f'---\nname: valid\ndescription: {"x" * 1025}\n---\nBody.\n',
            'description_too_long',
            'description',
        ),
        ('---\nname: valid\ndescription: Empty body.\n---\n\n', 'empty_body', None),
    ],
)
def test_require_valid_skill_document_rejects_invalid_documents_with_structured_errors(
    content,
    expected_code,
    expected_field,
):
    with pytest.raises(SkillDocumentError) as exc_info:
        require_valid_skill_document(content)

    assert exc_info.value.code == expected_code
    assert exc_info.value.field == expected_field


def test_require_valid_skill_document_rejects_unexpected_name():
    content = '---\nname: actual\ndescription: Example.\n---\nBody.\n'

    with pytest.raises(SkillDocumentError) as exc_info:
        require_valid_skill_document(content, expected_name='expected')

    assert exc_info.value.code == 'name_mismatch'
    assert exc_info.value.field == 'name'


def test_parse_error_preserves_body_after_recognized_frontmatter_delimiters():
    content = '---\nname: [broken\n---\nBody after broken metadata.\n'

    with pytest.raises(SkillDocumentError) as exc_info:
        require_valid_skill_document(content)

    assert exc_info.value.code == 'invalid_frontmatter_yaml'
    assert exc_info.value.body == 'Body after broken metadata.\n'
