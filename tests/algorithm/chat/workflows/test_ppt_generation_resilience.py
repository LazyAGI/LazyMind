import importlib.util
import io
import json
from pathlib import Path
from types import SimpleNamespace

import lazyllm
import pytest
from PIL import Image

from lazyllm.module.llms.onlinemodule.base.model_outcome import (
    ModelCallError, ModelCallTerminal, ModelFailure, ModelFailureCode, ModelFailureOrigin,
)


HTML = '<!doctype html><html><head><title>Page</title></head><body>Generated page</body></html>'


@pytest.fixture
def tools():
    path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('ppt_resilience_tools', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def transport_failure():
    return ModelCallError('Model transport failed.', ModelCallTerminal(
        model_call_id='call-1', attempt_count=1, kind='failure',
        has_semantic_output=False, duration_ms=1200,
        failure=ModelFailure(origin=ModelFailureOrigin.TRANSPORT,
                             code=ModelFailureCode.REQUEST_TIMEOUT,
                             diagnostic_id='diag-1', response_started=True),
    ))


class AdapterModel:
    def __init__(self, respond):
        self.respond = respond
        self.calls = []

    def share(self, **_kwargs):
        return self

    def _get_dynamic_bucket(self):
        return {'source': 'test-provider', 'model': 'test-model', 'api_key': 'DO-NOT-LOG'}

    def __call__(self, prompt, **options):
        control = options['call_control']
        control.validate_request(SimpleNamespace(payload={'messages': [], **options}))
        self.calls.append(options)
        return self.respond(prompt, control)


def prepare_batch(monkeypatch, tmp_path, tools, pages, respond):
    deck = tmp_path / 'deck'
    (deck / 'pages').mkdir(parents=True)
    (deck / 'outline.json').write_text(json.dumps({'pages': [
        {'page_no': number, 'title': str(number)} for number in range(1, pages + 1)
    ]}))
    model = AdapterModel(respond)
    monkeypatch.setattr(lazyllm, 'AutoModel', lambda **_kwargs: model)

    def capture(_command, current_deck, number, *args, llm_call):
        html = llm_call('system', str(number), request_name=f'page-html:{number}')
        (current_deck / 'pages' / f'page_{number:03d}.html').write_text(html)
        return 0, {'status': 'ok'}

    runtime = SimpleNamespace(cmd_page_html=None, cmd_page_html_from_brief=None, _capture_cmd=capture)
    monkeypatch.setattr(tools, '_load_sn_ppt_modules', lambda: (None, runtime))
    monkeypatch.setattr(tools, '_load_slide_outline_briefs', lambda _numbers: {})
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda _slot: [])
    saved = {}

    def save(**kwargs):
        saved[kwargs['key'], kwargs['publisher_list_index']] = kwargs['value']
        return {'ok': True}

    monkeypatch.setattr(tools, '_save_artifact', save)
    monkeypatch.setenv('LAZYMIND_PPT_PAGE_RETRIES', '0')
    return deck, model, saved


def test_failed_pages_do_not_block_successful_pages_and_recovery_keeps_indices(monkeypatch, tmp_path, tools):
    failures = {2, 5, 9}

    def respond(prompt, _control):
        if int(prompt) in failures:
            raise transport_failure()
        return HTML.replace('Generated page', f'Generated page {prompt}')

    deck, model, saved = prepare_batch(monkeypatch, tmp_path, tools, 12, respond)
    result = tools._batch_page_html_publish_progressive(deck)
    assert result['status'] == 'partial'
    assert result['ok'] == result['published_count'] == 9
    assert result['unpublished_pages'] == [2, 5, 9]
    assert len(model.calls) == result['model_calls'] == 12
    for number in range(1, 13):
        html = saved['preview_html', number - 1]
        assert ('尚未生成' in html) == (number in failures)
        assert (deck / 'pages' / f'page_{number:03d}.html').exists() == (number not in failures)
    diagnostics = result['failed_detail'][0]['diagnostics']
    assert diagnostics['code'] == 'request_timeout'
    assert diagnostics['diagnostic_id'] == 'diag-1'
    assert diagnostics['model_call_id'] == 'call-1'
    assert diagnostics['source'] == 'test-provider'
    assert 'DO-NOT-LOG' not in json.dumps(result)
    failures.clear()
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda _slot: list(range(12)))
    recovered = tools._batch_page_html_publish_progressive(deck)
    assert recovered['published_count'] == 12
    assert len(model.calls) == 15  # unchanged successful pages are reused
    assert 'Generated page 2' in saved['preview_html', 1]
    assert 'Generated page 12' in saved['preview_html', 11]


def test_model_request_budget_covers_repairs_without_token_limits(monkeypatch, tmp_path, tools):
    monkeypatch.setenv('LAZYMIND_PPT_MAX_MODEL_CALLS', '1')
    deck, model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 8, lambda *_args: HTML)
    result = tools._batch_page_html_publish_progressive(deck)
    assert result['published_count'] == 1
    assert result['budget']['stop_reason'] == 'model_call_budget_exhausted'
    assert result['model_calls'] == len(model.calls) == 1
    assert model.calls[0]['max_retries'] == 1
    assert 'max_tokens' not in model.calls[0]
    assert 'max_completion_tokens' not in model.calls[0]
    # A repair request uses the same budget as its original page request.
    budget = tools._PPTGenerationBudget(1)
    tools._agent_llm_call('system', '1', _budget=budget)
    with pytest.raises(TimeoutError):
        tools._agent_llm_call('repair', '1', _budget=budget)
    assert budget.calls == 1


def test_http_error_details_survive_without_response_body(monkeypatch, tmp_path, tools):
    def respond(*_args):
        raise ModelCallError('Provider returned HTTP status 503.', ModelCallTerminal(
            model_call_id='call-503', attempt_count=1, kind='failure',
            has_semantic_output=False, duration_ms=500,
            failure=ModelFailure(origin=ModelFailureOrigin.HTTP, code=ModelFailureCode.SERVICE_UNAVAILABLE,
                                 provider_http_status=503, diagnostic_id='diag-503'),
        ))

    deck, _model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 1, respond)
    result = tools._batch_page_html_publish_progressive(deck)
    details = result['failed_detail'][0]['diagnostics']
    assert details['provider_http_status'] == 503
    assert details['code'] == 'service_unavailable'
    assert details['diagnostic_id'] == 'diag-503'
    assert details['model_call_id'] == 'call-503'
    assert details['duration_ms'] == 500
    assert details['source'] == 'test-provider'


def test_invalid_requests_are_not_automatically_retried(monkeypatch, tmp_path, tools):
    def respond(*_args):
        raise ModelCallError('Invalid request.', ModelCallTerminal(
            model_call_id='bad-request', attempt_count=1, kind='failure',
            has_semantic_output=False,
            failure=ModelFailure(origin=ModelFailureOrigin.HTTP, code=ModelFailureCode.INVALID_REQUEST,
                                 provider_http_status=400),
        ))

    deck, model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 1, respond)
    monkeypatch.setenv('LAZYMIND_PPT_PAGE_RETRIES', '3')
    result = tools._batch_page_html_publish_progressive(deck)
    assert result['retry_count'] == 0
    assert len(model.calls) == 1


def test_continuous_stream_is_stopped_at_page_deadline(monkeypatch, tools):
    clock = [0.0]
    monkeypatch.setattr(tools.time, 'monotonic', lambda: clock[0])
    monkeypatch.setenv('LAZYMIND_PPT_PAGE_TIMEOUT', '5')

    def respond(_prompt, control):
        for _chunk in range(100):
            clock[0] += 1
            control.check()
        return HTML

    model = AdapterModel(respond)
    monkeypatch.setattr(lazyllm, 'AutoModel', lambda **_kwargs: model)
    with pytest.raises(TimeoutError) as failure:
        tools._agent_llm_call('system', '1', _budget=tools._PPTGenerationBudget(1))
    assert clock[0] == 5
    assert tools._model_failure_details(failure.value)['code'] == 'page_deadline_exceeded'
    assert len(model.calls) == 1


def test_batch_deadline_prevents_next_request(monkeypatch, tmp_path, tools):
    clock = [0.0]
    monkeypatch.setattr(tools.time, 'monotonic', lambda: clock[0])
    monkeypatch.setenv('LAZYMIND_PPT_BATCH_TIMEOUT', '5')

    def respond(_prompt, _control):
        clock[0] = 6
        return HTML

    deck, model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 2, respond)
    result = tools._batch_page_html_publish_progressive(deck)
    assert len(model.calls) == 1
    assert result['budget']['stop_reason'] == 'batch_deadline_exceeded'


def test_consecutive_failures_trip_circuit_and_do_not_retry(monkeypatch, tmp_path, tools):
    def respond(*_args):
        raise transport_failure()

    deck, model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 8, respond)
    monkeypatch.setenv('LAZYMIND_PPT_PAGE_RETRIES', '3')
    result = tools._batch_page_html_publish_progressive(deck)
    assert result['model_calls'] == len(model.calls) == 3
    assert result['budget']['stop_reason'] == 'consecutive_failure_limit'
    assert result['retry_count'] == 0


def image_bytes(size=(256, 256), blank=False):
    image = Image.new('RGB', size, 'red')
    if not blank:
        image.paste('blue', (0, 0, size[0] // 2, size[1]))
    output = io.BytesIO()
    image.save(output, format='PNG')
    return output.getvalue()


@pytest.mark.parametrize('data', [b'not an image' * 20, image_bytes((1, 1)),
                                  image_bytes(blank=True), image_bytes()[:100]])
def test_bad_local_images_are_rejected_before_registration(monkeypatch, tmp_path, tools, data):
    source = tmp_path / 'invalid.png'
    source.write_bytes(data)
    destination = tmp_path / 'registered'
    destination.mkdir()
    monkeypatch.setattr(tools, '_material_root', lambda: destination)
    monkeypatch.setattr(tools, '_resolve_local_image_ref', lambda _value: source)
    with pytest.raises(ValueError):
        tools._stage_one_material_image({'local_path': str(source)}, 0)
    assert list(destination.iterdir()) == []


def test_http_200_is_not_enough_but_small_valid_images_are_accepted(monkeypatch, tools):
    data = [b'<html>access denied</html>' * 20]
    closed = []
    monkeypatch.setattr(tools.requests, 'get', lambda *_args, **_kwargs: SimpleNamespace(
        raise_for_status=lambda: None, iter_content=lambda **_kw: iter(data),
        close=lambda: closed.append(True),
    ))
    with pytest.raises(ValueError, match='HTML'):
        tools._download_image_bytes('https://example.test/image.jpg')
    data[0] = image_bytes()
    assert len(data[0]) < 1076  # small, compressed, valid images must not be rejected by byte size
    assert tools._download_image_bytes('https://example.test/image.png') == data[0]
    assert closed == [True, True]
