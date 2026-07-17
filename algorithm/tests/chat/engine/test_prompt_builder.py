from __future__ import annotations

import pytest

from lazymind.chat.engine.agent_runtime import AgentRole, PromptBuilder


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
            'state', 'Plugin State', 'Step A is ready.', 'backend',
            priority=20, authoritative=True, content_kind='state',
        )
        .input(content='Please continue.', source='user')
        .build()
    )

    assert bundle.system_prompt == 'Base policy.'
    assert [section.section_id for section in bundle.sections] == ['base', 'state', 'artifact']
    assert '### Runtime Context' in bundle.current_input
    assert '#### Plugin State [AUTHORITATIVE]' in bundle.current_input
    assert bundle.current_input.endswith('### User Instruction\n\nPlease continue.')


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
    assert bundle.current_input == '### Task Objective\n\nDo the work.'
