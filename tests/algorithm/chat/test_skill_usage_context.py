from lazymind.chat.engine.prompts import resolve_task_profile
from lazymind.chat.engine.agent_runtime.models import AgentRole
from lazymind.chat.engine.agent_runtime.prompt_builder import PromptBuilder
from lazymind.chat.engine.tools.skill_listing import render_loaded_skills


def test_task_profile_does_not_reinterpret_host_skill_selection():
    profile = resolve_task_profile(
        '不要使用 alpha，paper', enable_llm_fallback=False,
        explicit_resources={
            'skill_names': ['external/paper'],
            'mentions': [{'resource_type': 'skill', 'resource_ref': 'external/paper', 'display_name': 'paper'}],
        },
    )
    assert profile.explicit_resources.skill_names == ('external/paper',)
    assert profile.excluded_resources.skill_names == ()


def test_explicit_l2_is_appended_without_changing_system_prefix():
    content = '---\nname: paper\ndescription: Write papers\ntags: [academic]\n---\nFollow these steps.\n'

    def build(loaded):
        builder = PromptBuilder.for_role(AgentRole.CHAT)
        builder.system('base', 'Assistant', 'System instructions', 'platform')
        builder.runtime('loaded', 'Selected skills', render_loaded_skills(loaded, ['external/paper']),
                        'backend.skill_usage', authoritative=True, content_kind='instruction')
        return builder.input(content='Write the paper', source='user').build()
    original = build([])
    selected = build([{'skill_key': 'external/paper', 'revision_id': 'rev1', 'content': content}])
    assert selected.system_prompt == original.system_prompt
    assert content.strip() in selected.current_input
