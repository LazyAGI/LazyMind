from lazymind.chat.engine.prompts import resolve_task_profile
from lazymind.chat.engine.agent_runtime.models import AgentRole
from lazymind.chat.engine.agent_runtime.prompt_builder import PromptBuilder
from lazymind.chat.engine.tools.skill_listing import append_loaded_skill_invocations
import json


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

    def build():
        builder = PromptBuilder.for_role(AgentRole.CHAT)
        builder.system('base', 'Assistant', 'System instructions', 'platform')
        return builder.input(content='Write the paper', source='user').build()

    original = build()
    selected = build()
    history = append_loaded_skill_invocations(
        [], [{'skill_key': 'external/paper', 'revision_id': 'rev1', 'content': content}],
    )
    assert selected.system_prompt == original.system_prompt
    assert json.loads(history[1]['content'])['content'].strip() == content.strip()
