import json
from pathlib import Path

import fitz
import lazyllm
import pytest
from PIL import Image
from lazyllm.tools.agent import ToolExecutionError, ToolManager

from lazymind.chat.engine.tools import chat_artifact, conversation_workspace
from lazymind.chat.engine.tools.resume import render_resume
from lazymind.chat.engine.tools.workspace_context import ToolResolutionContext, tool_resolution_scope


@pytest.fixture
def resume_delivery(tmp_path, monkeypatch):
    monkeypatch.setitem(conversation_workspace._cfg._impl, 'agentic_workspace', str(tmp_path / 'work'))
    old_config = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {
        'user_id': 'resume-user', 'conversation_id': 'resume-conversation',
    }
    monkeypatch.setenv('LAZYMIND_SUBAGENT_WORKSPACE', str(tmp_path / 'published'))
    events = []
    monkeypatch.setattr(chat_artifact, '_write_agent_data',
                        lambda tag, **payload: events.append({'tag': tag, **payload}))
    workspace = Path(conversation_workspace.chat_agent_workspace('resume-user', 'resume-conversation'))
    workspace.mkdir(parents=True)
    content = {
        'basics': {'name': '陈样例', 'title': '软件测试工程师', 'email': 'example@example.invalid'},
        'sections': [{'title': '项目经历', 'items': [{
            'title': '订单接口自动化', 'date': '2026.01–2026.02',
            'bullets': ['设计86条接口用例，保留课程项目归属。'],
        }]}],
    }
    yield content, events, workspace
    lazyllm.globals['agentic_config'] = old_config


def test_resume_tool_publishes_actual_finished_files_and_reuses_content(resume_delivery):
    content, events, workspace = resume_delivery
    first = render_resume(content, template='business')
    assert {Path(item['filename']).suffix for item in first['files']} == {'.pdf', '.docx', '.html'}
    assert len(events) == 3
    for item, event in zip(first['files'], events):
        assert item['file_markdown'] == f"[{item['filename']}](file_id:{item['artifact_id']})"
        assert event['content_type'] == 'file'
        assert Path(event['value']['path']).is_file()
    with fitz.open(events[0]['value']['path']) as pdf:
        assert '陈样例' in ''.join(page.get_text() for page in pdf)
    saved = workspace / first['resume_data_path']
    assert json.loads(saved.read_text())['resume'] == content
    second = render_resume(template='editorial', source_path=first['resume_data_path'])
    assert second['template'] == 'editorial'
    assert json.loads((workspace / second['resume_data_path']).read_text())['resume'] == content


def test_resume_photo_is_embedded_reused_and_explicitly_removable(resume_delivery):
    content, events, workspace = resume_delivery
    Image.new('RGB', (800, 400), '#245d66').save(workspace / 'uploaded-photo.png')
    first = render_resume(content, template='sidebar', photo_path='uploaded-photo.png', photo_fit='cover')
    assert first['photo_included']
    pdf_event = next(event for event in events if event['filename'].endswith('.pdf'))
    with fitz.open(pdf_event['value']['path']) as pdf:
        assert any(page.get_images() for page in pdf)
    reused = render_resume(template='modern', source_path=first['resume_data_path'])
    assert reused['photo_included']
    assert reused['photo_fit'] == 'cover'
    fit_changed = render_resume(template='campus', source_path=first['resume_data_path'], photo_fit='contain')
    assert fit_changed['photo_fit'] == 'contain'
    removed = render_resume(template='minimal', source_path=first['resume_data_path'], include_photo=False)
    assert not removed['photo_included']
    html_event = next(event for event in reversed(events) if event['filename'].endswith('.html'))
    assert '<img' not in Path(html_event['value']['path']).read_text()


def test_resume_source_cannot_escape_conversation_workspace(resume_delivery, tmp_path):
    content, events, workspace = resume_delivery
    outside = tmp_path / 'someone-else.json'
    outside.write_text(json.dumps({'schema_version': 1, 'resume': content}))
    with pytest.raises(ToolExecutionError, match='inside the current'):
        render_resume(source_path=str(outside))
    assert not events


def test_managed_uploaded_photo_works_without_a_host_access_guard(resume_delivery, tmp_path):
    content, events, workspace = resume_delivery
    uploads = tmp_path / 'managed-uploads'
    uploads.mkdir()
    photo = uploads / 'portrait.jpg'
    Image.new('RGB', (160, 240), '#245d66').save(photo)
    with tool_resolution_scope(ToolResolutionContext(managed_roots=(str(uploads.resolve()),))):
        result = render_resume(content, template='modern', photo_path=str(photo))
    assert result['photo_included']
    pdf_event = next(event for event in events if event['filename'].endswith('.pdf'))
    with fitz.open(pdf_event['value']['path']) as pdf:
        assert any(page.get_images() for page in pdf)
    # The same external path is not implicitly trusted in an unrelated context.
    with pytest.raises(ToolExecutionError, match='inside the current'):
        render_resume(content, photo_path=str(photo))


def test_resume_tool_is_available_in_native_chat():
    from lazymind.chat.service.chat_service import _build_chat_artifact_tools
    manager = ToolManager(_build_chat_artifact_tools())
    assert 'render_resume' in manager.tools_info
