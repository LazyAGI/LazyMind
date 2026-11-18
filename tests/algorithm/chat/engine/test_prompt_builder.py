from __future__ import annotations

import pytest

from lazymind.chat.engine.agent_runtime import AgentRole, PromptBuilder


@pytest.mark.parametrize('skip', [True, lambda: True])
def test_runtime_skip_condition_omits_inactive_document_contract(skip):
    bundle = (PromptBuilder.for_role(AgentRole.CHAT)
              .runtime('inactive-document', 'Active Document', 'document_id=None',
                       'backend.document_preview', skip_if=skip)
              .input('Read the selected library', source='user')
              .build())
    assert 'document_id=None' not in bundle.current_input


def test_prompt_builder_renders_stable_sections_and_boundaries() -> None:
    bundle = (
        PromptBuilder.for_role(AgentRole.CHAT)
        .system(
            'base', '', 'Base policy.', 'platform', priority=10,
        )
        .runtime(
            'artifact', 'Artifacts', 'draft.md', 'database',
            priority=30, content_kind='reference',
        )
        .runtime(
            'state', 'Workflow State', 'Step A is ready.', 'backend',
            priority=20, authoritative=True, content_kind='state',
        )
        .input(content='Please continue.', source='user')
        .build()
    )

    assert bundle.system_prompt == 'Base policy.'
    assert [section.section_id for section in bundle.sections] == ['base', 'state', 'artifact']
    assert bundle.current_input.index('Step A is ready.') < bundle.current_input.index('draft.md')
    assert bundle.current_input.index('draft.md') < bundle.current_input.index('Please continue.')


def test_prompt_builder_ignores_empty_sections_and_rejects_duplicate_ids() -> None:
    builder = PromptBuilder.for_role(AgentRole.CHAT)
    builder.system('empty', '', '', 'platform')
    builder.system('skipped', '', 'ignored', 'platform', skip_if=lambda: True)
    builder.system('base', '', 'A', 'platform')
    with pytest.raises(ValueError, match='duplicate prompt section_id'):
        builder.runtime(
            'base', 'State', 'B', 'backend',
        )
    assert [section.section_id for section in builder.build().sections] == ['base']


def test_prompt_builder_keeps_input_boundary_without_runtime_context() -> None:
    bundle = (
        PromptBuilder.for_role(AgentRole.SUBAGENT)
        .input(content='Do the work.', source='task')
        .build()
    )
    assert bundle.input_content == 'Do the work.'
    assert bundle.input_content in bundle.current_input
    assert bundle.system_prompt == ''


def test_prompt_builder_renders_after_input_guard_after_user_instruction() -> None:
    bundle = (
        PromptBuilder.for_role(AgentRole.CHAT)
        .runtime(
            'guard', 'Tool Guard', 'Call the tool.', 'tool.registry',
            authoritative=True, placement='after_input',
        )
        .input('Ask one at a time.', source='user')
        .build()
    )

    assert bundle.current_input.index('Ask one at a time.') < bundle.current_input.index(
        'Call the tool.'
    )
    assert bundle.input_content == 'Ask one at a time.'
