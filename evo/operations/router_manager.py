from __future__ import annotations

import json
import time
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any
from urllib.parse import quote, urlparse

import httpx

from evo import normalize_chat_stream_url, normalize_http_origin


DEFAULT_ROUTER_CHAT_URL = 'http://chat:8046/api/chat/stream'
DEFAULT_ROUTER_ADMIN_URL = 'http://chat:8046'
ADMIN_TIMEOUT_SECONDS = 10.0


@dataclass(frozen=True)
class RouterAlgorithmSpec:
    id: str
    name: str
    code_path: str
    instance_count: int
    config: Mapping[str, Any]


class RouterManagerError(RuntimeError):
    def __init__(self, kind: str, message: str, status_code: int = 0) -> None:
        super().__init__(kind, message, status_code)
        self.kind = kind
        self.message = message
        self.status_code = status_code

    def __str__(self) -> str:
        return self.message


class RouterManager:
    def __init__(self, router_admin_url: str, router_chat_url: str = DEFAULT_ROUTER_CHAT_URL) -> None:
        self.router_admin_url = normalize_admin_url(router_admin_url)
        self.router_chat_url = normalize_chat_url(router_chat_url)

    def get_algorithm(self, algorithm_id: str) -> dict[str, Any] | None:
        try:
            return self._request('GET', self._algorithm_url(algorithm_id), timeout_s=ADMIN_TIMEOUT_SECONDS)
        except RouterManagerError as exc:
            if exc.status_code == 404:
                return None
            raise

    def register_algorithm(self, spec: RouterAlgorithmSpec, *, timeout_s: float) -> dict[str, Any]:
        body = {
            'id': spec.id,
            'name': spec.name,
            'code_path': spec.code_path,
            'instance_count': spec.instance_count,
            'config': dict(spec.config),
        }
        return self._request(
            'POST',
            f'{self.router_admin_url}/inner/algorithm/register',
            body=body,
            timeout_s=timeout_s,
        )

    def wait_ready(self, algorithm_id: str, *, timeout_s: float) -> dict[str, Any]:
        deadline = time.monotonic() + timeout_s
        last: dict[str, Any] | None = None
        while time.monotonic() <= deadline:
            detail = self.get_algorithm(algorithm_id)
            last = detail
            if self.healthcheck_from_detail(detail)['status'] == 'passed':
                return detail or {}
            time.sleep(1.0)
        raise RouterManagerError(
            'router_timeout',
            f'algorithm {algorithm_id} was not ready before {timeout_s:g}s; last={last}',
        )

    def healthcheck(self, algorithm_id: str) -> dict[str, Any]:
        detail = self.get_algorithm(algorithm_id)
        if detail is None:
            raise RouterManagerError('algorithm_not_found', f'algorithm not found: {algorithm_id}', 404)
        return self.healthcheck_from_detail(detail)

    def restart_algorithm(self, algorithm_id: str, *, timeout_s: float) -> dict[str, Any]:
        result = self._request(
            'POST',
            f'{self._algorithm_url(algorithm_id)}/restart',
            timeout_s=ADMIN_TIMEOUT_SECONDS,
        )
        self.wait_ready(algorithm_id, timeout_s=timeout_s)
        return result

    def stop_algorithm(self, algorithm_id: str) -> dict[str, Any]:
        return self._request('DELETE', self._algorithm_url(algorithm_id), timeout_s=ADMIN_TIMEOUT_SECONDS)

    def status(self) -> dict[str, Any]:
        return self._request('GET', f'{self.router_admin_url}/inner/status', timeout_s=ADMIN_TIMEOUT_SECONDS)

    def get_ab_strategy(self) -> dict[str, Any]:
        return self._request('GET', f'{self.router_admin_url}/inner/ab/strategy', timeout_s=ADMIN_TIMEOUT_SECONDS)

    def update_ab_strategy(self, weights: Mapping[str, int]) -> dict[str, Any]:
        return self._request(
            'PUT',
            f'{self.router_admin_url}/inner/ab/strategy',
            body={'weights': dict(weights)},
            timeout_s=ADMIN_TIMEOUT_SECONDS,
        )

    def clear_ab_strategy(self) -> dict[str, Any]:
        return self._request('DELETE', f'{self.router_admin_url}/inner/ab/strategy', timeout_s=ADMIN_TIMEOUT_SECONDS)

    def healthcheck_from_detail(self, detail: Mapping[str, Any] | None) -> dict[str, Any]:
        return algorithm_health(detail)

    def _algorithm_url(self, algorithm_id: str) -> str:
        value = str(algorithm_id or '').strip()
        if not value:
            raise RouterManagerError('router_config_error', 'algorithm_id is required')
        return f'{self.router_admin_url}/inner/algorithm/{quote(value, safe="")}'

    def _request(
        self,
        method: str,
        url: str,
        *,
        body: Mapping[str, Any] | None = None,
        timeout_s: float,
    ) -> dict[str, Any]:
        try:
            with httpx.Client(timeout=httpx.Timeout(timeout_s)) as client:
                response = client.request(method, url, json=body)
                response.raise_for_status()
                value = response.json()
        except httpx.HTTPStatusError as exc:
            message = _response_message(exc.response)
            raise RouterManagerError('router_http_error', message, exc.response.status_code) from exc
        except (httpx.TimeoutException, httpx.TransportError) as exc:
            raise RouterManagerError('router_transport_error', str(exc)) from exc
        except json.JSONDecodeError as exc:
            raise RouterManagerError('router_protocol_error', f'router response is not JSON: {url}') from exc
        if not isinstance(value, dict):
            raise RouterManagerError('router_protocol_error', f'router response is not an object: {url}')
        return value


def normalize_chat_url(value: object) -> str:
    try:
        return normalize_chat_stream_url(str(value or DEFAULT_ROUTER_CHAT_URL).strip(), 'router_chat_url')
    except ValueError as exc:
        raise RouterManagerError('router_config_error', str(exc)) from exc


def normalize_admin_url(value: object) -> str:
    try:
        return normalize_http_origin(str(value or DEFAULT_ROUTER_ADMIN_URL).strip(), 'router_admin_url')
    except ValueError as exc:
        raise RouterManagerError('router_config_error', str(exc)) from exc


def admin_url_from_chat_url(value: object) -> str:
    chat_url = normalize_chat_url(value)
    parsed = urlparse(chat_url)
    return f'{parsed.scheme}://{parsed.netloc}'


def algorithm_health(detail: Mapping[str, Any] | None) -> dict[str, Any]:
    instances = list((detail or {}).get('instances') or [])
    healthy = [item for item in instances if isinstance(item, Mapping) and item.get('status') == 'healthy']
    active = (detail or {}).get('status') == 'active'
    ok = active and bool(healthy)
    return {
        'status': 'passed' if ok else 'failed',
        'algorithm_status': (detail or {}).get('status'),
        'healthy_instances': len(healthy) if active else 0,
        'instances': instances,
    }


def _response_message(response: httpx.Response) -> str:
    text = response.text.strip()
    suffix = f': {text[:300]}' if text else ''
    return f'HTTP {response.status_code}{suffix}'
