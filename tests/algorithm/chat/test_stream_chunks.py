import base64
import json

import pytest

from lazymind.chat.service.utils.streaming import (
    MAX_LINE_BYTES, log_and_emit_frame, sse_line,
)


@pytest.mark.parametrize('key', ['text', 'canonical_messages'])
def test_large_tool_results_are_lossless_bounded_lines(key):
    text = '中文😀"\\\n' * 90000
    data = {key: text if key == 'text' else [{'role': 'tool', 'content': text}]}
    wire = log_and_emit_frame(data, 0.25, 'query', 'session')
    lines = [line for line in wire.splitlines() if line]
    assert len(lines) > 1
    chunks = [json.loads(line)['stream_chunk'] for line in lines]
    assert all(len(line.encode('utf-8')) <= MAX_LINE_BYTES for line in lines)
    assert len({chunk['id'] for chunk in chunks}) == 1
    assert [chunk['index'] for chunk in chunks] == list(range(len(chunks)))
    assert all(chunk['total'] == len(chunks) for chunk in chunks)
    raw = b''.join(base64.b64decode(chunk['payload'], validate=True) for chunk in chunks)
    assert all(chunk['size'] == len(raw) for chunk in chunks)
    assert json.loads(raw) == {'code': 200, 'msg': 'success', 'data': data, 'cost': 0.25}


def test_small_frame_preserves_wire_format():
    payload = {'code': 200, 'data': {'text': '你好'}}
    assert sse_line(payload) == json.dumps(payload, ensure_ascii=False) + '\n\n'


def test_reassembly_limit_is_explicit(monkeypatch):
    from lazymind.chat.service.utils import streaming
    monkeypatch.setattr(streaming, 'MAX_MESSAGE_BYTES', 100)
    with pytest.raises(ValueError, match='reassembly limit'):
        sse_line({'text': 'a' * 101})


@pytest.mark.parametrize('length', [MAX_LINE_BYTES - 12, MAX_LINE_BYTES - 11, 524288])
def test_encoded_line_boundary(length):
    payload = {'text': 'x' * length}
    wire = sse_line(payload)
    lines = [line for line in wire.splitlines() if line]
    assert all(len(line.encode()) <= MAX_LINE_BYTES for line in lines)
    if len(json.dumps(payload).encode()) <= MAX_LINE_BYTES:
        assert json.loads(lines[0]) == payload
    else:
        assert json.loads(b''.join(base64.b64decode(json.loads(line)['stream_chunk']['payload'])
                                   for line in lines)) == payload


def test_whole_deck_event_preserves_both_result_frames(tmp_path):
    from pptx import Presentation
    from lazymind.chat.engine.tools.file_resources.pptx_template_fill import inspect_template
    from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

    path = tmp_path / 'synthetic-45.pptx'
    deck = Presentation()
    for index in range(45):
        slide = deck.slides.add_slide(deck.slide_layouts[1])
        slide.shapes.title.text = f'第 {index + 1} 页'
        slide.placeholders[1].text = '讨论稿内容中文😀"\\\n' * 250
    deck.save(path)
    result = inspect_template(path, all_pages=True, cache_workspace=tmp_path / 'cache')
    assert len(result['pages']) == 45
    frames = AgentEventFrameTranslator(query='按页总结').feed({
        'tag': 'tool_results',
        'tool_results': [{'id': 'ppt-call', 'name': 'pptx_analyzer', 'result': result}],
    })
    assert any(frame.get('canonical_messages') for frame in frames)
    assert any('tool_result' in (frame.get('text') or '') for frame in frames)
    for frame in frames:
        raw = json.dumps({'code': 200, 'msg': 'success', 'data': frame, 'cost': 0})
        assert len(raw.encode()) > 512 * 1024
        lines = [line for line in log_and_emit_frame(frame, 0, '', '').splitlines() if line]
        assert all(len(line.encode()) <= MAX_LINE_BYTES for line in lines)
        restored = b''.join(base64.b64decode(json.loads(line)['stream_chunk']['payload']) for line in lines)
        assert json.loads(restored) == json.loads(raw)
