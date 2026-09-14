from __future__ import annotations

from typing import Any, Optional
from urllib.parse import urljoin

import lazyllm
import requests


_MODEL_LIST_TIMEOUT_SECONDS = 5


def _chat_model_config(llm_config: Any) -> Optional[dict[str, Any]]:
    if not isinstance(llm_config, dict):
        return None
    role_config = llm_config.get('llm')
    return role_config if isinstance(role_config, dict) else None


def _model_ids(payload: Any) -> Optional[set[str]]:
    collection: Any
    if isinstance(payload, list):
        collection = payload
    elif isinstance(payload, dict):
        collection = payload.get('data')
        if not isinstance(collection, list):
            collection = payload.get('models')
        if not isinstance(collection, list):
            return None
        # An incomplete page cannot prove that a model is absent.
        if payload.get('has_more') is True:
            return None
    else:
        return None

    result: set[str] = set()
    for item in collection:
        if isinstance(item, str) and item.strip():
            result.add(item.strip())
            continue
        if not isinstance(item, dict):
            continue
        for key in ('id', 'name', 'model', 'model_name'):
            value = item.get(key)
            if isinstance(value, str) and value.strip():
                result.add(value.strip())
                break
    if collection and not result:
        return None
    return result


def _listed_models(role_config: dict[str, Any]) -> Optional[set[str]]:
    model = str(role_config.get('model') or '').strip()
    source = str(role_config.get('source') or '').strip()
    base_url = str(role_config.get('base_url') or '').strip()
    if not model or not source or not base_url:
        return None

    try:
        module = lazyllm.OnlineModule(
            model=model,
            source=source,
            url=base_url,
            api_key=role_config.get('api_key'),
            skip_auth=bool(role_config.get('skip_auth', False)),
        )
        module_base_url = str(getattr(module, '_base_url', '') or base_url).strip()
        if not module_base_url:
            return None
        headers = getattr(module, '_header', None)
        if not isinstance(headers, dict):
            headers = {}
        response = requests.get(
            urljoin(module_base_url, 'models'),
            headers=headers,
            timeout=_MODEL_LIST_TIMEOUT_SECONDS,
        )
        try:
            if response.status_code != 200:
                return None
            return _model_ids(response.json())
        finally:
            response.close()
    except Exception:
        # Model discovery is supplementary. If the provider does not expose a
        # compatible list endpoint, preserve its original normalized failure.
        return None


def refine_unavailable_model_terminal(
    terminal: Any,
    llm_config: Any,
) -> Any:
    """Change an ambiguous provider failure to not_found only with list proof."""
    if not isinstance(terminal, dict) or terminal.get('kind') != 'failure':
        return terminal
    if terminal.get('has_semantic_output'):
        return terminal
    failure = terminal.get('failure')
    if not isinstance(failure, dict) or failure.get('code') == 'not_found':
        return terminal

    role_config = _chat_model_config(llm_config)
    if role_config is None:
        return terminal
    selected_model = str(role_config.get('model') or '').strip()
    listed_models = _listed_models(role_config)
    if listed_models is None:
        return terminal
    normalized_models = {item.casefold() for item in listed_models}
    if selected_model.casefold() in normalized_models:
        return terminal

    refined_failure = {**failure, 'code': 'not_found'}
    lazyllm.LOG.warning(
        '[AgentExecutor] selected model is absent from provider model list '
        f'source={role_config.get("source", "")} model={selected_model} '
        f'original_code={failure.get("code", "")}'
    )
    return {**terminal, 'failure': refined_failure}


def is_model_failure_event(item: Any) -> bool:
    if not isinstance(item, dict) or item.get('tag') != 'runtime_event':
        return False
    event = item.get('runtime_event')
    if not isinstance(event, dict) or event.get('type') != 'model_call_finished':
        return False
    data = event.get('data')
    return isinstance(data, dict) and data.get('kind') == 'failure'


def refine_unavailable_model_event(item: Any, llm_config: Any) -> Any:
    if not is_model_failure_event(item):
        return item
    event = item.get('runtime_event')
    refined = refine_unavailable_model_terminal(event.get('data'), llm_config)
    if refined is event.get('data'):
        return item
    return {
        **item,
        'runtime_event': {
            **event,
            'data': refined,
        },
    }
