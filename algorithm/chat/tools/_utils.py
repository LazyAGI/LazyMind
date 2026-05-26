from __future__ import annotations

import json
from typing import Any, Dict

import requests

from config import config as _cfg


def truncate_text(text: Any, max_len: int) -> str:
    if text is None:
        return ''
    raw = text if isinstance(text, str) else str(text)
    return raw if len(raw) <= max_len else f'{raw[:max_len]}...'


def parse_json_dict(value: Any) -> Dict[str, Any]:
    if isinstance(value, dict):
        return value
    if isinstance(value, (str, bytes, bytearray)) and value:
        try:
            parsed = json.loads(value)
            return parsed if isinstance(parsed, dict) else {}
        except (TypeError, ValueError):
            return {}
    return {}


def absolute_url(url: str) -> str:
    normalized = str(url or '').strip()
    if not normalized:
        return ''
    if normalized.startswith(('http://', 'https://')):
        return normalized
    return f'https://{normalized}'


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
        response = session.post(url, json=payload, timeout=timeout)

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
