from lazymind.chat.service.chat_service import (
    _parse_upload_event_frames,
    _uploaded_document_names,
)
from lazymind.chat.service.component import AgentEventFrameTranslator


def test_uploaded_document_names_keep_whitelist_only():
    names = _uploaded_document_names({
        '1': ['/tmp/paper.pdf', '/tmp/notes.md', '/tmp/skip.py'],
        '2': ['/tmp/paper.pdf'],
    })
    assert names == ['paper.pdf']


def test_uploaded_document_names_only_current_turn():
    names = _uploaded_document_names(
        {
            '1': ['/tmp/old.pdf'],
            '2': ['/tmp/notes.md'],
        },
        current_turn_seq=2,
    )
    assert names == []


def test_parse_upload_frames_emit_tool_preview():
    translator = AgentEventFrameTranslator(query='第二篇论文讲了什么', run_id='parse-uploads')
    start = _parse_upload_event_frames(translator, ['paper.pdf'], phase='start')
    done = _parse_upload_event_frames(translator, ['paper.pdf'], phase='done')
    assert start and '<tp' in (start[0].get('text') or '')
    assert '正在解析上传文档' in (start[0].get('text') or '')
    assert done and '<trp' in (done[0].get('text') or '')
