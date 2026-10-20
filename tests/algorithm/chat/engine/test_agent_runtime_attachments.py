from __future__ import annotations

from lazymind.chat.engine.agent_runtime import (
    AgentRole,
    normalize_attachments,
    render_attachment_content,
)


def test_attachment_renderer_marks_current_turn_and_deduplicates_names(tmp_path) -> None:
    first = tmp_path / 'first' / 'design.png'
    second = tmp_path / 'second' / 'design.png'
    first.parent.mkdir()
    second.parent.mkdir()
    first.write_bytes(b'a')
    second.write_bytes(b'bb')

    attachments = normalize_attachments(
        {'2': [str(first), str(second)], '1': ['/missing/history.txt']},
        current_turn_seq=2,
    )
    rendered = render_attachment_content(
        attachments,
        role=AgentRole.CHAT,
        current_turn_seq=2,
    )

    assert 'Turn 2 [CURRENT]' in rendered
    assert 'design.png' in rendered
    assert 'design-1.png' in rendered
    assert rendered.count('design.png') == 1


def test_attachment_names_are_deduplicated_only_within_each_turn(tmp_path) -> None:
    current = tmp_path / 'current' / 'design.png'
    historical = tmp_path / 'historical' / 'design.png'
    current.parent.mkdir()
    historical.parent.mkdir()
    current.write_bytes(b'current')
    historical.write_bytes(b'historical')

    attachments = normalize_attachments({
        '2': [str(current)],
        '1': [str(historical)],
    }, current_turn_seq=2)

    assert [item.display_name for item in attachments] == ['design.png', 'design.png']


def test_current_small_spreadsheet_is_inlined_but_historical_one_is_not(tmp_path) -> None:
    current = tmp_path / 'current.csv'
    historical = tmp_path / 'historical.csv'
    current.write_text('name,value\ncurrent,7\n', encoding='utf-8')
    historical.write_text('name,value\nhistorical,9\n', encoding='utf-8')
    attachments = normalize_attachments(
        {'2': [str(current)], '1': [str(historical)]},
        current_turn_seq=2,
    )

    rendered = render_attachment_content(
        attachments,
        role=AgentRole.CHAT,
        current_turn_seq=2,
    )

    assert 'Current-turn spreadsheet content' in rendered
    assert 'current,7' in rendered
    assert 'historical,9' not in rendered


def test_large_spreadsheet_context_tells_model_to_use_python(tmp_path) -> None:
    path = tmp_path / 'large.csv'
    path.write_text(
        'id,value\n' + ''.join(f'{index},{index}\n' for index in range(201)),
        encoding='utf-8',
    )
    attachments = normalize_attachments({'3': [str(path)]}, current_turn_seq=3)

    rendered = render_attachment_content(
        attachments,
        role=AgentRole.CHAT,
        current_turn_seq=3,
    )

    assert 'Large spreadsheet detected' in rendered
    assert 'Python execution tool with pandas' in rendered
    assert '200,200' not in rendered


def test_inline_spreadsheet_escapes_prompt_template_placeholders(tmp_path) -> None:
    path = tmp_path / 'prompt.csv'
    path.write_text('name,value\ninput,{query}\n', encoding='utf-8')
    attachments = normalize_attachments({'1': [str(path)]}, current_turn_seq=1)

    rendered = render_attachment_content(
        attachments,
        role=AgentRole.CHAT,
        current_turn_seq=1,
    )

    assert '{ query }' in rendered
    assert '{query}' not in rendered
