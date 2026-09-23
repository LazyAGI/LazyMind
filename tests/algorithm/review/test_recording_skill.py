import json
import pytest
from pydantic import ValidationError
from lazymind.review.api.recording_skill_routes import RecordingFrame, RecordingRequest, parse_recording_result


def test_missing_evidence_discards_partial_generated_content():
    result = parse_recording_result(json.dumps({
        'name': 'Guess', 'description': 'Unverified', 'content': 'Invented steps',
        'missing': ['What was the output?'],
    }))
    assert result.missing == ['What was the output?']
    assert not result.name and not result.content


def test_incomplete_result_requests_details():
    result = parse_recording_result('{"name":"Incomplete"}')
    assert result.missing and not result.content


def test_valid_json_fence_is_accepted():
    result = parse_recording_result('```json\n{"name":"Export","description":"Export a report",'
                                    '"content":"1. Select report\\n2. Export", "missing":[]}\n```')
    assert result.name == 'Export' and not result.missing


def test_invalid_model_output_is_not_treated_as_a_skill():
    with pytest.raises((ValueError, ValidationError)):
        parse_recording_result('Sorry, I could not see any steps.')


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
    model = Mock(return_value=json.dumps({'name': 'Export', 'description': 'Export report', 'content': '1. Export'}))
    factory = Mock(return_value=model)
    monkeypatch.setattr(lazyllm, 'AutoModel', factory)
    payload = recording_payload()
    payload.evidence.events = [
        {'kind': 'input', 'seconds': 0.5, 'value': '你好😀 token=test-token email@example.com'},
        {'kind': 'keydown', 'seconds': 0.6, 'key': 'a', 'text': 'a', 'keycode': 0},
    ]
    result = recording_skill(payload)
    from lazyllm.components.formatter.formatterbase import decode_query_with_filepaths
    request = decode_query_with_filepaths(model.call_args.args[0])['query']
    for event in payload.evidence.events:
        for field in ('value', 'key', 'text'):
            if field in event:
                assert event[field] in request
    factory.assert_called_once_with(model='llm', type='vlm')
    assert result.name == 'Export' and not result.error
