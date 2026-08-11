from lazymind.common.skill.paths import relative_to_package


def test_relative_to_package_does_not_treat_double_slash_drive_as_uri():
    assert relative_to_package(
        'C://Users/test/skills/pkg',
        'C://Users/test/skills/pkg/references/doc.md',
    ) == 'references/doc.md'
