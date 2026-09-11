from __future__ import annotations

from unittest.mock import MagicMock

from lazymind.chat.engine.agent_runtime import model_availability


def _terminal(code: str = 'balance_exhausted') -> dict:
    return {
        'model_call_id': 'call-1',
        'attempt_count': 1,
        'kind': 'failure',
        'has_semantic_output': False,
        'failure': {'origin': 'http', 'code': code},
    }


def _config(model: str = 'fake') -> dict:
    return {
        'llm': {
            'source': 'deepseek',
            'model': model,
            'base_url': 'https://api.deepseek.com/',
            'api_key': 'secret',
        },
    }


def _mock_catalog(monkeypatch, model_ids, *, status_code: int = 200) -> MagicMock:
    module = MagicMock()
    module._base_url = 'https://api.deepseek.com/'
    module._header = {'Authorization': 'Bearer secret'}
    monkeypatch.setattr(model_availability.lazyllm, 'OnlineModule', MagicMock(return_value=module))
    response = MagicMock()
    response.status_code = status_code
    response.json.return_value = {'object': 'list', 'data': [{'id': item} for item in model_ids]}
    get = MagicMock(return_value=response)
    monkeypatch.setattr(model_availability.requests, 'get', get)
    return get


def test_refines_ambiguous_failure_when_selected_model_is_not_listed(monkeypatch) -> None:
    get = _mock_catalog(monkeypatch, ['deepseek-v4-flash', 'deepseek-v4-pro'])

    refined = model_availability.refine_unavailable_model_terminal(_terminal(), _config())

    assert refined['failure']['code'] == 'not_found'
    assert get.call_args.args[0] == 'https://api.deepseek.com/models'
    assert get.call_args.kwargs['timeout'] == 5


def test_preserves_failure_when_selected_model_is_listed(monkeypatch) -> None:
    _mock_catalog(monkeypatch, ['fake'])
    terminal = _terminal()

    refined = model_availability.refine_unavailable_model_terminal(terminal, _config())

    assert refined is terminal
    assert refined['failure']['code'] == 'balance_exhausted'


def test_preserves_failure_when_provider_model_list_is_unavailable(monkeypatch) -> None:
    _mock_catalog(monkeypatch, [], status_code=404)
    terminal = _terminal()

    refined = model_availability.refine_unavailable_model_terminal(terminal, _config())

    assert refined is terminal


def test_preserves_failure_after_partial_model_output(monkeypatch) -> None:
    get = _mock_catalog(monkeypatch, [])
    terminal = _terminal()
    terminal['has_semantic_output'] = True

    refined = model_availability.refine_unavailable_model_terminal(terminal, _config())

    assert refined is terminal
    get.assert_not_called()


def test_refines_streamed_model_finished_event(monkeypatch) -> None:
    _mock_catalog(monkeypatch, ['deepseek-v4-flash'])
    item = {
        'tag': 'runtime_event',
        'runtime_event': {
            'type': 'model_call_finished',
            'data': _terminal(),
        },
    }

    refined = model_availability.refine_unavailable_model_event(item, _config())

    assert refined['runtime_event']['data']['failure']['code'] == 'not_found'
    assert item['runtime_event']['data']['failure']['code'] == 'balance_exhausted'
