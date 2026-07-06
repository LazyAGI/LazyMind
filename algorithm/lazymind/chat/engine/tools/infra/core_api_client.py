from __future__ import annotations

from typing import Any, Dict

import lazyllm
import requests

from lazymind.config import config as _cfg


def _current_user_headers() -> Dict[str, str]:
    agentic_config = lazyllm.globals.get('agentic_config') or {}
    user_id = str(agentic_config.get('user_id') or '').strip()
    headers: Dict[str, str] = {}
    if user_id:
        headers['X-User-Id'] = user_id
    internal_token = str(_cfg['core_internal_token'] or '').strip()
    if internal_token:
        headers['X-LazyMind-Internal-Token'] = internal_token
    return headers


def post_core_api(
    path: str,
    payload: Dict[str, Any],
) -> Dict[str, Any]:
    base_url = str(_cfg['core_api_url'] or '').strip().rstrip('/')
    if not base_url:
        raise RuntimeError("'core_api_url' is required in config.")

    url = f"{base_url}/{path.lstrip('/')}"
    timeout = int(_cfg['core_api_timeout'])
    with requests.sessions.Session() as session:
        session.trust_env = False
        response = session.post(url, json=payload, headers=_current_user_headers(), timeout=timeout)

    try:
        body = response.json()
    except ValueError:
        body = {'text': response.text}

    if not response.ok:
        msg = (
            body.get('msg') or body.get('message')
            if isinstance(body, dict)
            else response.text
        )
        raise RuntimeError(f'POST {url} failed with HTTP {response.status_code}: {msg}')

    if isinstance(body, dict) and body.get('code') not in (None, 0):
        msg = body.get('msg') or body.get('message') or body
        raise RuntimeError(f'POST {url} failed: {msg}')

    return {
        'persisted': 'core_api',
        'url': url,
        'response': body,
    }


def get_core_api(path: str, params: Dict[str, Any] | None = None) -> Dict[str, Any]:
    base_url = str(_cfg['core_api_url'] or '').strip().rstrip('/')
    if not base_url:
        raise RuntimeError("'core_api_url' is required in config.")

    url = f"{base_url}/{path.lstrip('/')}"
    timeout = int(_cfg['core_api_timeout'])
    with requests.sessions.Session() as session:
        session.trust_env = False
        response = session.get(url, params=params, headers=_current_user_headers(), timeout=timeout)

    try:
        body = response.json()
    except ValueError:
        body = {'text': response.text}

    if not response.ok:
        msg = (
            body.get('msg') or body.get('message')
            if isinstance(body, dict)
            else response.text
        )
        raise RuntimeError(f'GET {url} failed with HTTP {response.status_code}: {msg}')

    if isinstance(body, dict) and 'data' in body:
        return body.get('data') or {}
    return body if isinstance(body, dict) else {}
