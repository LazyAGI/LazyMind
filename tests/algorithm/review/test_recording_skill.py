import json
import pytest
from pydantic import ValidationError
from lazymind.review.api.recording_skill_routes import RecordingFrame, RecordingRequest, parse_recording_result


def test_incomplete_json_is_preserved_as_draft_content():
    raw = '{"name":"Incomplete"}'
    result = parse_recording_result(raw)
    assert result.content == raw and result.name and not result.error


def test_valid_json_fence_is_accepted():
    result = parse_recording_result('```json\n{"name":"Export","description":"Export a report",'
                                    '"content":"1. Select report\\n2. Export", "missing":[]}\n```')
    assert result.name == 'Export' and not result.missing


def test_markdown_fence_and_heading_are_supported():
    result = parse_recording_result('```markdown\n# 导出报表\n点击导出。\n```')
    assert result.name == '导出报表'
    assert result.content == '# 导出报表\n点击导出。'


@pytest.mark.parametrize('raw', ['', '   ', '```md\n\n```'])
def test_empty_skill_content_becomes_pending_draft(raw):
    result = parse_recording_result(raw)
    assert not result.error and not result.missing
    assert result.name and result.description
    assert '未识别到明确操作' in result.content


def test_source_validation_rejects_urls_and_malformed_images():
    for image in ['https://example.com/private.jpg', 'data:image/jpeg;base64,aGVsbG8=']:
        with pytest.raises(ValidationError):
            RecordingFrame(image=image, seconds=0)
    with pytest.raises(ValidationError):
        RecordingRequest(frames=[])


def recording_payload():
    return RecordingRequest(frames=[
        RecordingFrame(image='data:image/jpeg;base64,/9j/', seconds=0),
        RecordingFrame(image='data:image/jpeg;base64,/9j/', seconds=1),
    ])


def test_recording_missing_vision_returns_card_reason(monkeypatch):
    from lazymind import vision_model as vm
    from lazymind.review.api.recording_skill_routes import recording_skill

    def unavailable():
        raise vm.VisionModelUnavailable('主模型图片能力检测暂未成功，请重试或配置视觉模型。')
    monkeypatch.setattr(vm, 'select_vision_model_role', unavailable)
    result = recording_skill(recording_payload())
    assert '请重试或配置视觉模型' in result.error
    assert not result.content and not result.name


def test_recording_reuses_main_model_and_image_format(monkeypatch):
    import lazyllm
    from unittest.mock import Mock
    from lazymind import vision_model as vm
    from lazymind.review.api.recording_skill_routes import recording_skill
    monkeypatch.setattr(vm, 'select_vision_model_role', lambda: 'llm')
    model = Mock(side_effect=[
        '页面显示导出按钮。',
        json.dumps({'name': 'Export', 'description': 'Export report', 'content': '1. Export'}),
    ])
    factory = Mock(return_value=model)
    monkeypatch.setattr(lazyllm, 'AutoModel', factory)
    payload = recording_payload()
    payload.evidence.events = [
        {'kind': 'input', 'seconds': 0.5, 'value': '你好😀 token=test-token email@example.com'},
        {'kind': 'keydown', 'seconds': 0.6, 'key': 'a', 'text': 'a', 'keycode': 0},
    ]
    result = recording_skill(payload)
    from lazyllm.components.formatter.formatterbase import decode_query_with_filepaths
    image_request = decode_query_with_filepaths(model.call_args_list[0].args[0])
    assert len(image_request['files']) == 1
    request = model.call_args.args[0]
    assert 'data:image' not in request
    assert '页面显示导出按钮' in request
    assert '"seconds": 0.0' in request and '"seconds": 1.0' in request
    for event in payload.evidence.events:
        for field in ('value', 'key', 'text'):
            if field in event:
                assert event[field] in request
    assert factory.call_count == 2
    assert factory.call_args_list[0].kwargs == {'model': 'llm', 'type': 'vlm'}
    assert factory.call_args_list[1].kwargs == {'model': 'llm', 'type': 'llm'}
    assert result.name == 'Export' and not result.error


@pytest.mark.parametrize('has_llm', [False, True])
def test_recording_single_image_calls_then_text_only_synthesis(monkeypatch, has_llm):
    import base64
    import threading
    import time
    from pathlib import Path
    import lazyllm
    from lazyllm.components.formatter.formatterbase import decode_query_with_filepaths
    from lazymind import model_config, vision_model
    from lazymind.review.api.recording_skill_routes import recording_skill

    monkeypatch.setattr(vision_model, 'select_vision_model_role', lambda: 'vlm')
    monkeypatch.setattr(model_config, 'is_model_role_available', lambda role: has_llm and role == 'llm')
    paths = []
    active = 0
    peak = 0
    lock = threading.Lock()
    summaries = []

    def factory(*, model, type):
        def call(query, **kwargs):
            nonlocal active, peak
            assert kwargs['llm_chat_history'] == []
            assert kwargs['lazyllm_files'] is None
            assert 0 < kwargs['timeout'] <= 120
            decoded = decode_query_with_filepaths(query)
            if isinstance(decoded, dict):
                assert model == 'vlm' and type == 'vlm'
                assert len(decoded['files']) == 1
                path = Path(decoded['files'][0])
                index = int(path.stem)
                assert path.is_file()
                with lock:
                    paths.append(path)
                    active += 1
                    peak = max(peak, active)
                # Finish out of order to ensure chronological synthesis.
                time.sleep(0.03 if index == 0 else 0.001)
                with lock:
                    active -= 1
                return f'Visible frame {index}'
            assert model == ('llm' if has_llm else 'vlm')
            assert type == ('llm' if has_llm else 'vlm')
            summaries.append(query)
            return json.dumps({'name': 'Review', 'description': 'Review frames', 'content': '1. Review'})
        return call

    monkeypatch.setattr(lazyllm, 'AutoModel', factory)
    payload = RecordingRequest(frames=[
        RecordingFrame(image='data:image/jpeg;base64,' + base64.b64encode(b'\xff\xd8\xff' + bytes([i])).decode(),
                       seconds=i) for i in range(7)
    ])
    result = recording_skill(payload)
    assert result.name == 'Review' and not result.error
    assert len(paths) == 7 and 1 < peak <= 3
    assert all(not path.exists() for path in paths)
    assert len(summaries) == 1 and 'data:image' not in summaries[0]
    offsets = [summaries[0].index(f'Visible frame {i}') for i in range(7)]
    assert offsets == sorted(offsets)


@pytest.mark.parametrize('stage', ['frames', 'synthesis'])
def test_recording_failures_are_staged_without_exposing_input(monkeypatch, caplog, stage):
    import lazyllm
    from lazymind import vision_model
    from lazymind.review.api.recording_skill_routes import recording_skill
    from unittest.mock import Mock

    monkeypatch.setattr(vision_model, 'select_vision_model_role', lambda: 'llm')
    secret = 'private-recording-text-and-key'
    responses = {
        'frames': [RuntimeError(secret)],
        'synthesis': ['Visible page', RuntimeError(secret)],
    }
    model = Mock(side_effect=responses[stage])
    monkeypatch.setattr(lazyllm, 'AutoModel', Mock(return_value=model))
    result = recording_skill(recording_payload())
    assert result.error and not result.content
    assert f'stage={stage}' in caplog.text
    assert secret not in caplog.text and secret not in result.error
    assert model.call_count == (1 if stage == 'frames' else 2)


@pytest.mark.parametrize('length', [1500, 1501, 3000])
def test_recording_truncates_long_frame_description_before_synthesis(monkeypatch, length):
    import lazyllm
    from unittest.mock import Mock
    from lazymind import vision_model
    from lazymind.review.api.recording_skill_routes import recording_skill

    monkeypatch.setattr(vision_model, 'select_vision_model_role', lambda: 'llm')
    description = '画' * length
    model = Mock(side_effect=[
        description,
        json.dumps({'name': 'Export', 'description': 'Export report', 'content': '1. Export'}),
    ])
    monkeypatch.setattr(lazyllm, 'AutoModel', Mock(return_value=model))
    result = recording_skill(recording_payload())
    assert result.name == 'Export' and not result.error
    prompt = model.call_args.args[0]
    assert '画' * 1500 in prompt
    assert '画' * 1501 not in prompt


def test_recording_empty_frame_and_summary_still_produce_draft(monkeypatch):
    import lazyllm
    from unittest.mock import Mock
    from lazymind import vision_model
    from lazymind.review.api.recording_skill_routes import recording_skill

    monkeypatch.setattr(vision_model, 'select_vision_model_role', lambda: 'llm')
    model = Mock(side_effect=['   ', ''])
    monkeypatch.setattr(lazyllm, 'AutoModel', Mock(return_value=model))
    result = recording_skill(recording_payload())
    assert not result.error and not result.missing
    assert '未识别到明确操作' in result.content
    assert '未识别到明确操作' in model.call_args.args[0]
    assert model.call_count == 2


def test_recording_logs_reasoning_metadata_without_raw_content(monkeypatch, caplog):
    import logging
    import lazyllm
    from unittest.mock import Mock
    from lazymind import vision_model
    from lazymind.review.api.recording_skill_routes import recording_skill

    monkeypatch.setattr(vision_model, 'select_vision_model_role', lambda: 'llm')
    private = 'private-reasoning-text'
    model = Mock(side_effect=[
        f'<think>{private}</think>Visible page',
        json.dumps({'name': 'Export', 'description': 'Export report', 'content': '1. Export'}),
    ])
    monkeypatch.setattr(lazyllm, 'AutoModel', Mock(return_value=model))
    with caplog.at_level(logging.INFO):
        result = recording_skill(recording_payload())
    assert not result.error
    assert 'Recording frame output frame=0 output_type=str' in caplog.text
    assert 'think_tags=True' in caplog.text
    assert f'tagged_reasoning_chars={len(private)}' in caplog.text
    assert private not in caplog.text


def test_recording_stream_reports_progress_and_final_result(monkeypatch):
    import lazyllm
    from unittest.mock import Mock
    from fastapi import FastAPI
    from fastapi.testclient import TestClient
    from lazymind import vision_model
    from lazymind.review.api.recording_skill_routes import router

    monkeypatch.setattr(vision_model, 'select_vision_model_role', lambda: 'llm')
    model = Mock(side_effect=[
        'Visible page',
        '# Export\n1. Export',
    ])
    monkeypatch.setattr(lazyllm, 'AutoModel', Mock(return_value=model))
    app = FastAPI()
    app.include_router(router)
    response = TestClient(app).post('/api/chat/recording_skill_stream', json=recording_payload().model_dump())
    events = [json.loads(line) for line in response.text.splitlines()]
    assert response.status_code == 200
    assert events[:2] == [{'progress': 0}, {'progress': 90}]
    assert events[-1]['result']['name'] == 'Export'
    assert model.call_count == 2  # Duplicate frames share one description and one progress update.
