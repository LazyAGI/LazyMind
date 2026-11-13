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
        control = options.get('call_control')
        if control is not None:
            control.validate_request(SimpleNamespace(payload={'messages': [], **options}))
        self.calls.append(options)
        return self.respond(prompt, control)


def prepare_batch(monkeypatch, tmp_path, tools, pages, respond):
    deck = tmp_path / 'deck'
    (deck / 'pages').mkdir(parents=True)
    (deck / 'task_pack.json').write_text(json.dumps({'params': {'page_count': pages}}))
    (deck / 'info_pack.json').write_text('{}')
    (deck / 'outline.json').write_text(json.dumps({'pages': [
        {'page_no': number, 'title': str(number)} for number in range(1, pages + 1)
    ]}))
    model = AdapterModel(respond)
    monkeypatch.setattr(lazyllm, 'AutoModel', lambda **_kwargs: model)

    def capture(_command, current_deck, number=None, *args, llm_call, **_kwargs):
        if _command is runtime.cmd_ensure_style:
            return 0, {'status': 'ok'}
        html = llm_call('system', str(number), request_name=f'page-html:{number}')
        (current_deck / 'pages' / f'page_{number:03d}.html').write_text(html)
        return 0, {'status': 'ok'}

    runtime = SimpleNamespace(cmd_page_html=None, cmd_page_html_from_brief=None,
                              cmd_ensure_style=object(), _capture_cmd=capture)
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


@pytest.fixture
def cancel_queue(monkeypatch):
    from lazyllm.common import queue
    signals = {}

    class CancelQueue:
        def __init__(self, *, klass):
            assert klass == 'cancel'

        def enqueue(self, value):
            signals.setdefault(lazyllm.globals._sid, []).append(value)

        def dequeue(self):
            return signals.pop(lazyllm.globals._sid, [])

    monkeypatch.setattr(queue, 'FileSystemQueue', CancelQueue)


def test_pause_during_page_model_call_exits_without_retrying_or_starting_later_pages(
    monkeypatch, tmp_path, tools, cancel_queue,
):
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError, request_cancel

    def respond(prompt, control):
        if prompt == '2':
            request_cancel('ppt-task')
            # Provider processing may run in a different context than the task.
            with lazyllm.globals._bind_sid('provider-thread'):
                control.check()
            pytest.fail('the page model continued after pause')
        return HTML

    deck, model, saved = prepare_batch(monkeypatch, tmp_path, tools, 3, respond)
    monkeypatch.setenv('LAZYMIND_PPT_PAGE_RETRIES', '3')
    with lazyllm.globals._bind_sid('ppt-task'), pytest.raises(UserCancelledError):
        tools._batch_page_html_publish_progressive(deck)
    assert len(model.calls) == 2
    assert (deck / 'pages/page_001.html').exists()
    assert not (deck / 'pages/page_002.html').exists()
    assert not (deck / 'pages/page_003.html').exists()
    assert ('preview_html', 0) in saved
    assert ('preview_html', 1) not in saved


def test_pause_before_page_generation_does_not_start_model_requests(monkeypatch, tmp_path, tools, cancel_queue):
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError, request_cancel
    deck, model, saved = prepare_batch(monkeypatch, tmp_path, tools, 2, lambda *_: HTML)
    request_cancel('ppt-task')
    with lazyllm.globals._bind_sid('ppt-task'), pytest.raises(UserCancelledError):
        tools._batch_page_html_publish_progressive(deck)
    assert model.calls == []
    assert saved == {}


def test_ppt_model_calls_without_page_budget_also_honor_pause(monkeypatch, tools, cancel_queue):
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError, request_cancel

    def respond(_prompt, control):
        request_cancel('ppt-task')
        control.check()
        pytest.fail('the model continued after pause')

    model = AdapterModel(respond)
    monkeypatch.setattr(lazyllm, 'AutoModel', lambda **_: model)
    with lazyllm.globals._bind_sid('ppt-task'), pytest.raises(UserCancelledError):
        tools._agent_llm_call('system', 'outline')
    assert len(model.calls) == 1


def publication_store(monkeypatch, tools):
    """Fake the public artifact transport, retaining stable indices across Attempts."""
    orders = {'preview_html': [], 'preview_notes': []}
    selected = {}
    history = []
    attempt = [1]

    def save(**kwargs):
        slot, index = kwargs['key'], kwargs['publisher_list_index']
        if index not in orders[slot]:
            orders[slot].append(index)
        selected[slot, index] = {
            'slot': slot, 'list_index': index, 'selected': True, 'validity': 'effective',
            'attempt': attempt[0], 'value': {'text': kwargs['value']},
        }
        return {'ok': True}

    def artifacts(_session):
        rows = []
        for item in [*history, *selected.values()]:
            order = orders[item['slot']]
            position = order.index(item['list_index']) + 1 if item['list_index'] in order else None
            rows.append({**item, 'sort_order': position})
        return SimpleNamespace(result={'artifacts': rows})

    def invalidate():
        history.extend({**item, 'selected': False, 'validity': 'stale'} for item in selected.values())
        selected.clear()
        attempt[0] += 1

    monkeypatch.setattr(tools, '_save_artifact', save)
    monkeypatch.setattr(tools, '_workflow_session_id', lambda: 'retry-session')
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda slot: list(orders[slot]))
    monkeypatch.setattr(tools, '_workflow_client', lambda: SimpleNamespace(list_artifacts=artifacts))
    monkeypatch.setattr(tools, '_load_saved_notes', lambda: None)
    return SimpleNamespace(orders=orders, selected=selected, invalidate=invalidate)


def test_retry_republishes_cached_successes_in_new_attempt_before_model_calls(monkeypatch, tmp_path, tools):
    failures = {1, 2, 5, 6, 7}
    retry = [False]

    def respond(prompt, _control):
        if retry[0]:
            for number in (3, 4):
                page = store.selected['preview_html', number - 1]
                assert page['attempt'] == 2
                assert not tools._is_ppt_placeholder(page['value']['text'])
        if int(prompt) in failures:
            raise transport_failure()
        return HTML.replace('Generated page', f'Generated page {prompt}')

    deck, model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 7, respond)
    store = publication_store(monkeypatch, tools)
    first = tools._batch_page_html_publish_progressive(deck)
    assert first['published_count'] == 2
    assert first['unpublished_pages'] == [1, 2, 5, 6, 7]
    store.invalidate()
    failures.clear()
    retry[0] = True
    second = tools._batch_page_html_publish_progressive(deck)
    assert second['status'] == 'ok'
    assert second['published_count'] == 7
    assert second['unpublished_pages'] == []
    assert len(model.calls) == 12  # 7 initial requests + only 5 missing pages
    status = tools.ppt_list_pages(str(deck))
    assert status['fully_published'] is True
    assert status['missing_pages'] == status['placeholder_pages'] == []
    assert status['ui_published_count'] == status['ui_notes_published_count'] == 7


def test_retry_page_five_first_replaces_placeholders_for_cached_three_and_four(monkeypatch, tmp_path, tools):
    failures = {1, 2, 5, 6, 7}

    def respond(prompt, _control):
        if int(prompt) in failures:
            raise transport_failure()
        return HTML.replace('Generated page', f'Generated page {prompt}')

    deck, model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 7, respond)
    store = publication_store(monkeypatch, tools)
    tools._batch_page_html_publish_progressive(deck)
    store.invalidate()
    failures.clear()
    # The retry finishes page 5 first, reserving positions 1..4 as placeholders.
    for order in store.orders.values():
        order.clear()
    runtime = tools._load_sn_ppt_modules()[1]
    runtime._capture_cmd(runtime.cmd_page_html, deck, 5, llm_call=tools._agent_llm_call)
    assert tools._publish_one_page(deck, 5)['ok']
    for number in (3, 4):
        assert tools._is_ppt_placeholder(store.selected['preview_html', number - 1]['value']['text'])
    result = tools._run_stage_inprocess('page-html', deck, page=1)
    assert result.get('status') == 'ok', result
    assert not result.get('publish_error'), result
    for number in (3, 4):
        assert tools._page_checkpoint_matches(deck, number, None), result
        assert not tools._is_ppt_placeholder(store.selected['preview_html', number - 1]['value']['text'])
    for number in (2, 6, 7):
        tools._run_stage_inprocess('page-html', deck, page=number)
    assert len(model.calls) == 12
    assert store.orders == {'preview_html': list(range(7)), 'preview_notes': list(range(7))}
    status = tools.ppt_list_pages(str(deck))
    assert status['fully_published'] is True
    assert status['published_pages'] == list(range(1, 8))


@pytest.mark.parametrize('problem', ['placeholder', 'legacy_placeholder', 'notes_placeholder',
                                     'stale', 'wrong_notes', 'missing_file', 'changed_file'])
def test_seven_positions_do_not_prove_seven_real_pages(monkeypatch, tmp_path, tools, problem):
    deck, _model, _saved = prepare_batch(
        monkeypatch, tmp_path, tools, 7, lambda prompt, _control: HTML.replace('Page', f'Page {prompt}'))
    store = publication_store(monkeypatch, tools)
    tools._batch_page_html_publish_progressive(deck)
    html = store.selected['preview_html', 2]
    notes = store.selected['preview_notes', 2]
    if problem == 'placeholder':
        for index in (2, 3):
            store.selected['preview_html', index]['value']['text'] = '<!-- lazymind-ppt-placeholder -->' + HTML
    elif problem == 'legacy_placeholder':
        html['value']['text'] = '<html><body><p>第 3 页尚未生成或发布成功，请重试该页。</p></body></html>'
    elif problem == 'notes_placeholder':
        notes['value']['text'] = '<!-- lazymind-ppt-placeholder --> Missing speaker notes'
    elif problem == 'stale':
        html['validity'] = 'stale'
    elif problem == 'wrong_notes':
        notes['value'] = dict(store.selected['preview_notes', 1]['value'])
    elif problem == 'missing_file':
        (deck / 'pages/page_003.html').unlink()
    else:
        (deck / 'pages/page_003.html').write_text(HTML + '<!-- changed -->')
    status = tools.ppt_list_pages(str(deck))
    assert status['ui_slot_count'] == 7
    assert status['fully_published'] is False
    missing = [3, 4] if problem == 'placeholder' else [3]
    assert status['missing_pages'] == missing
    if problem == 'placeholder':
        assert status['ui_published_count'] == 5
    if problem in {'placeholder', 'legacy_placeholder', 'notes_placeholder'}:
        assert status['placeholder_pages'] == missing


def test_deferred_republish_rejects_files_from_changed_inputs(monkeypatch, tmp_path, tools):
    deck, _model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 2, lambda *_args: HTML)
    store = publication_store(monkeypatch, tools)
    tools._batch_page_html_publish_progressive(deck)
    store.invalidate()
    (deck / 'info_pack.json').write_text('{"requirement":"new evidence"}')
    assert tools._publish_ready_trailing_pages(deck, 1) == []
    assert store.selected == {}


def test_notes_injection_does_not_invalidate_success_checkpoint(monkeypatch, tmp_path, tools):
    deck, model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 1, lambda *_args: HTML)
    store = publication_store(monkeypatch, tools)
    monkeypatch.setattr(tools, '_load_saved_notes', lambda: {1: 'Approved English speaker script.'})
    tools._batch_page_html_publish_progressive(deck)
    store.invalidate()
    result = tools._batch_page_html_publish_progressive(deck)
    assert result['status'] == 'ok'
    assert len(model.calls) == 1
    assert store.selected['preview_notes', 0]['value']['text'] == 'Approved English speaker script.'


def test_single_page_success_is_reused_by_a_later_batch_attempt(monkeypatch, tmp_path, tools):
    deck, model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 2, lambda *_args: HTML)
    store = publication_store(monkeypatch, tools)
    assert tools._run_stage_inprocess('page-html', deck, page=2)['status'] == 'ok'
    store.invalidate()
    result = tools._batch_page_html_publish_progressive(deck)
    assert result['status'] == 'ok'
    assert len(model.calls) == 2  # Page 2 was not sent to the model again.
    assert tools.ppt_list_pages(str(deck))['fully_published'] is True


def test_completion_uses_display_positions_and_fails_closed_on_read_errors(monkeypatch, tmp_path, tools):
    deck, _model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 2, lambda *_args: HTML)
    store = publication_store(monkeypatch, tools)
    for order in store.orders.values():
        order.extend([42, 7])  # Stable list indices are not page numbers.
    tools._batch_page_html_publish_progressive(deck)
    assert tools.ppt_list_pages(str(deck))['fully_published'] is True

    def unavailable(_session):
        raise ConnectionError('Artifact service unavailable')

    monkeypatch.setattr(tools, '_workflow_client', lambda: SimpleNamespace(list_artifacts=unavailable))
    result = tools.ppt_list_pages(str(deck))
    assert result['fully_published'] is False
    assert result['missing_pages'] == [1, 2]
    assert result['publication_error'] == 'Artifact service unavailable'


@pytest.mark.parametrize('change', ['brief', 'html', 'checkpoint', 'evidence'])
def test_retry_regenerates_only_pages_without_a_matching_checkpoint(monkeypatch, tmp_path, tools, change):
    deck, model, _saved = prepare_batch(monkeypatch, tmp_path, tools, 2, lambda *_args: HTML)
    store = publication_store(monkeypatch, tools)
    tools._batch_page_html_publish_progressive(deck)
    store.invalidate()
    if change == 'brief':
        monkeypatch.setattr(tools, '_load_slide_outline_briefs', lambda _numbers: {1: 'Changed page request'})
    elif change == 'html':
        (deck / 'pages/page_001.html').write_text(HTML + '<!-- locally changed -->')
    elif change == 'checkpoint':
        (deck / '.page_retry_001.json').unlink()
    else:
        (deck / 'info_pack.json').write_text('{"evidence":"changed"}')
    result = tools._batch_page_html_publish_progressive(deck)
    assert result['status'] == 'ok'
    assert len(model.calls) == (4 if change == 'evidence' else 3)
    assert tools.ppt_list_pages(str(deck))['fully_published'] is True


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


def prepare_completed_retry(monkeypatch, tmp_path, tools):
    deck, model, _ = prepare_batch(monkeypatch, tmp_path, tools, 4, lambda *_: HTML)
    store = publication_store(monkeypatch, tools)
    ctx = SimpleNamespace(workspace_path=str(tmp_path), params={}, task_id='retry-task', emit=lambda event: None)
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    monkeypatch.setattr(tools, '_required_output_format', lambda: 'html')
    (deck / 'outline_draft.md').write_text('Approved outline')
    (deck / 'complete_page_prompts.json').write_text('{}')
    monkeypatch.setattr(tools, '_selected_slide_outline_items', lambda: [
        (number - 1, f'Page {number} design') for number in range(1, 5)
    ])
    monkeypatch.setattr(tools, '_load_slide_outline_briefs', lambda numbers: {
        number: brief for number, (_, brief) in enumerate(tools._selected_slide_outline_items(), 1)
        if number in numbers
    })
    assert tools._prepare_deferred_outline(deck)['status'] == 'direct'
    assert tools._batch_page_html_publish_progressive(deck)['published_count'] == 4
    store.invalidate()
    # Same confirmed request with the duplicate metadata seen in the failed run.
    current = 'Generate four slides\n\nConfirmed workflow parameters:\n{"slide_count":"4"}'
    ctx.params = {'current_user_input': current, 'launch_user_input': current + '\nConfirmed workflow parameters:\n{}',
                  'retry_existing_outputs': True}
    return deck, model, store, ctx


def test_generate_pages_retry_recovers_and_publishes_complete_deck(monkeypatch, tmp_path, tools):
    deck, model, store, _ = prepare_completed_retry(monkeypatch, tmp_path, tools)
    monkeypatch.setattr(tools, 'ppt_run_stage', lambda *a, **k: pytest.fail('complete retry must not regenerate'))
    result = tools.ppt_generate_pages(str(deck))
    assert result['status'] == 'ok'
    assert result['published_count'] == 4
    assert len(model.calls) == 4
    assert len(store.selected) == 8
    assert all(item['attempt'] == 2 for item in store.selected.values())
    artifacts = [{'slot': item['slot'], 'content_type': 'text', 'value': item['value']}
                 for item in store.selected.values()]
    assert tools.check_ppt_delivery(artifacts)['status'] == 'ok'


def test_completed_deck_edit_is_not_treated_as_failed_retry(monkeypatch, tmp_path, tools):
    deck, _, store, ctx = prepare_completed_retry(monkeypatch, tmp_path, tools)
    ctx.params.pop('retry_existing_outputs')
    ctx.params['current_user_input'] = 'Move the title to the bottom'
    with pytest.raises(tools.ToolExecutionError, match='existing deck is complete'):
        tools.ppt_generate_pages(str(deck))
    assert store.selected == {}


@pytest.mark.parametrize('change', ['brief', 'page_count', 'input', 'html', 'placeholder', 'missing'])
def test_retry_does_not_republish_changed_or_incomplete_deck(monkeypatch, tmp_path, tools, change):
    deck, _, store, _ = prepare_completed_retry(monkeypatch, tmp_path, tools)
    if change in {'brief', 'page_count'}:
        items = tools._selected_slide_outline_items()
        if change == 'brief':
            items[0] = (0, 'A new confirmed design')
        else:
            items.append((4, 'Page 5 design'))
        monkeypatch.setattr(tools, '_selected_slide_outline_items', lambda: items)
    elif change == 'input':
        (deck / 'info_pack.json').write_text('{"updated":true}')
    elif change == 'missing':
        (deck / 'pages/page_001.html').unlink()
    else:
        (deck / 'pages/page_001.html').write_text(
            '<html><!-- lazymind-ppt-placeholder --></html>' if change == 'placeholder' else HTML + 'changed')
    # Changed inputs or files must use normal generation before publishing.

    def generate(_deck, *, stage):
        assert stage == 'asset-plan'
        assert store.selected == {}
        raise RuntimeError('normal generation required')
    monkeypatch.setattr(tools, 'ppt_run_stage', generate)
    with pytest.raises(RuntimeError, match='normal generation required'):
        tools.ppt_generate_pages(str(deck))


def test_pause_during_cached_republication_stops_and_can_resume(monkeypatch, tmp_path, tools, cancel_queue):
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError, request_cancel

    deck, model, store, ctx = prepare_completed_retry(monkeypatch, tmp_path, tools)
    save = tools._save_artifact

    def pause_after_first_page(**kwargs):
        result = save(**kwargs)
        if kwargs['key'] == 'preview_notes':
            request_cancel('cached-retry')
        return result

    monkeypatch.setattr(tools, '_save_artifact', pause_after_first_page)
    with lazyllm.globals._bind_sid('cached-retry'), pytest.raises(UserCancelledError):
        tools.ppt_generate_pages(str(deck))
    assert len(store.selected) == 2
    assert len(model.calls) == 4
    monkeypatch.setattr(tools, '_save_artifact', save)
    ctx._ppt_page_failures = {}
    with lazyllm.globals._bind_sid('resumed-retry'):
        assert tools.ppt_generate_pages(str(deck))['status'] == 'ok'
    assert len(store.selected) == 8
    assert len(model.calls) == 4
