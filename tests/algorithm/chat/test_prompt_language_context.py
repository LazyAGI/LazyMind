from lazymind.chat.engine.prompts.system_prompt import (
    build_standard_prompt_bundle,
    build_system_prompt,
)


def test_response_language_policy_defaults_include_product_locale():
    bundle = build_standard_prompt_bundle(False)

    assert 'zh-CN' in bundle.system_prompt


def test_system_prompt_injects_soul_profile_preference():
    prompt = build_system_prompt(
        False,
        soul='---\nschema_version: 1\nidentity:\n  name: "LazyMind"\n---\n',
        profile='---\nschema_version: 1\nidentity:\n  preferred_name: "Alice"\n---\n',
        preference=(
            '---\nschema_version: 1\nupdated_at: 2026-07-20\n---\n'
            '# Preference Index\n'
            '- name: pref.response.detail\n'
            '  summary: Prefer concise answers.\n'
            '  ref: references/response.md\n'
        ),
    )

    assert 'Alice' in prompt
    assert 'pref.response.detail' in prompt


def test_precise_current_time_lives_in_runtime_context():
    morning = build_standard_prompt_bundle(
        False,
        environment_context={
            'locale': 'zh-CN',
            'time': {'now': '2026-05-11T01:15:30.000Z', 'timezone': 'Asia/Shanghai'},
        },
    )
    evening = build_standard_prompt_bundle(
        False,
        environment_context={
            'locale': 'zh-CN',
            'time': {'now': '2026-05-11T15:48:00.000Z', 'timezone': 'Asia/Shanghai'},
        },
    )

    assert morning.system_prompt == evening.system_prompt
    assert '09:15:30' in morning.current_input
    assert '23:48:00' in evening.current_input
    assert '09:15:30' not in morning.system_prompt
    assert '23:48:00' not in evening.system_prompt
    assert '2026-05-11' in morning.system_prompt


def test_unparseable_time_is_omitted_from_system_and_kept_in_runtime():
    bundle = build_standard_prompt_bundle(
        False,
        environment_context={
            'locale': 'zh-CN',
            'time': {'now': 'Monday morning', 'timezone': 'Asia/Shanghai'},
        },
    )

    assert 'Monday morning' not in bundle.system_prompt
    assert 'Monday morning' in bundle.current_input
