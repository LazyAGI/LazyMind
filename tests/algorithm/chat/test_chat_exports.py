from lazymind.chat.service.component.chat_exports import ChatExportStream
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
from lazymind.chat.runtime_events import RunOutcome


OPEN = ':::export{title="报告" filename="报告.md"}\n'


def parse(text, chunk_size=1):
    parser = ChatExportStream()
    visible = ''.join(parser.feed(text[i:i + chunk_size]) for i in range(0, len(text), chunk_size))
    return visible, parser.finish()


def test_multiple_blocks_stream_without_markers_and_utf16_ranges():
    raw = '前言😀\n' + OPEN + '# 报告\n中文😀\n:::\n后记\n' + OPEN + '第二份\n:::\n'
    for size in (1, 7, len(raw)):
        visible, final = parse(raw, size)
        assert ':::export' not in visible
        assert len(final['exports']) == 2
        encoded = final['content'].encode('utf-16-le')
        bodies = [encoded[item['start'] * 2:item['end'] * 2].decode('utf-16-le') for item in final['exports']]
        assert bodies == ['# 报告\n中文😀\n', '第二份']


def test_invalid_and_unclosed_blocks_restore_original_text():
    for raw in (OPEN + 'unfinished', ':::export{bad}\nbody\n:::', OPEN + OPEN + 'nested\n:::\n:::'):
        _, final = parse(raw)
        assert final == {'content': raw.strip(), 'exports': []}


def test_code_fences_are_literal_and_body_fences_do_not_close_export():
    literal = '```text\n' + OPEN + 'example\n:::\n```'
    assert parse(literal)[1] == {'content': literal, 'exports': []}
    body = '```text\n:::\n```\n'
    final = parse(OPEN + body + ':::\n')[1]
    assert final['content'] == body.strip()
    assert len(final['exports']) == 1


def test_plain_markdown_streams_without_waiting_for_newline():
    parser = ChatExportStream()
    assert parser.feed('Hello ') == 'Hello '
    assert parser.feed('world') == 'world'
    assert parser.finish() == {'content': 'Hello world', 'exports': []}


def test_translator_protocol_is_opt_in_and_finalizes_post_scanner_text():
    raw = OPEN + '完整内容😀\n:::\n'
    for enabled in (False, True):
        translator = AgentEventFrameTranslator(query='写报告', enable_exports=enabled)
        frames = translator.finish(raw)
        terminal = translator.finish_run(outcome=RunOutcome.SUCCEEDED)
        visible = ''.join(frame.get('text') or '' for frame in frames)
        if enabled:
            assert ':::export' not in visible
            assert terminal['export_snapshot']['content'] == '完整内容😀'
            assert len(terminal['export_snapshot']['exports']) == 1
        else:
            assert ':::export' in visible
            assert 'export_snapshot' not in terminal


def test_export_offsets_only_include_answer_after_tool_previews():
    translator = AgentEventFrameTranslator(query='报告', enable_exports=True)
    translator.feed({'tag': 'text', 'delta': '先检索资料。'})
    translator.feed({'tag': 'tool_results', 'tool_results': []})
    translator.feed({'tag': 'text', 'delta': OPEN + '# 完整报告\n:::\n'})
    translator.finish(OPEN + '# 完整报告\n:::\n')
    snapshot = translator.finish_run(outcome=RunOutcome.SUCCEEDED)['export_snapshot']
    assert snapshot['content'] == '# 完整报告'
    assert snapshot['exports'][0]['start'] == 0
