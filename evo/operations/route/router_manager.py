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
STOP_TIMEOUT_SECONDS = 30.0


@dataclass(frozen=True)
class RouterAlgorithmSpec:
    id: str
    name: str
    code_path: str
    instance_count: int
    config: Mapping[str, Any]

    def payload(self) -> dict[str, Any]:
        return {
            'id': self.id,
            'name': self.name,
            'code_path': self.code_path,
            'instance_count': self.instance_count,
            'config': dict(self.config),
        }

    def matches(self, detail: Mapping[str, Any]) -> bool:
        return (
            detail.get('name') == self.name
            and detail.get('code_path') == self.code_path
            and dict(detail.get('config') or {}) == dict(self.config)
        )


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
        return self._request(
            'POST',
            f'{self.router_admin_url}/inner/algorithm/register',
            body=spec.payload(),
            timeout_s=timeout_s,
        )

    def ensure_algorithm(
        self,
        spec: RouterAlgorithmSpec,
        *,
        timeout_s: float,
        restart_unhealthy: bool = True,
        allow_existing: bool = True,
    ) -> tuple[dict[str, Any], dict[str, Any]]:
        detail = self.get_algorithm(spec.id)
        if detail is None:
            registered = self.register_algorithm(spec, timeout_s=timeout_s)
            return dict(registered) | {'created': True}, self.wait_ready(
                spec.id,
                timeout_s=timeout_s,
                instance_count=spec.instance_count,
            )
        if not allow_existing:
            raise RouterManagerError(
                'algorithm_conflict',
                f'algorithm {spec.id} exists in Router without Evo ownership',
                409,
            )
        if not spec.matches(detail):
            raise RouterManagerError(
                'algorithm_conflict',
                f'algorithm {spec.id} already has different name/code/config',
                409,
            )
        health = self.healthcheck_from_detail(detail)
        if health['status'] == 'passed' and health['healthy_instances'] == spec.instance_count:
            return {'reused': True}, detail
        if not restart_unhealthy:
            raise RouterManagerError('algorithm_unhealthy', f'algorithm {spec.id} exists but is not healthy', 409)
        if detail.get('status') == 'active':
            self.restart_algorithm(spec.id, timeout_s=timeout_s, instance_count=spec.instance_count)
            return {'reused': True, 'restarted': True}, self.get_algorithm(spec.id) or {}
        try:
            registered = self.register_algorithm(spec, timeout_s=timeout_s)
            detail = self.wait_ready(
                spec.id,
                timeout_s=timeout_s,
                instance_count=spec.instance_count,
            )
            return dict(registered) | {'reused': True, 'reactivated': True}, detail
        except RouterManagerError as exc:
            raise RouterManagerError('algorithm_reactivation_failed', str(exc), exc.status_code) from exc

    def wait_ready(
        self,
        algorithm_id: str,
        *,
        timeout_s: float,
        instance_count: int = 0,
    ) -> dict[str, Any]:
        deadline = time.monotonic() + timeout_s
        last: dict[str, Any] | None = None
        while time.monotonic() <= deadline:
            detail = self.get_algorithm(algorithm_id)
            last = detail
            health = self.healthcheck_from_detail(detail)
            if health['status'] == 'passed' and (
                not instance_count or health['healthy_instances'] == instance_count
            ):
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

    def restart_algorithm(
        self,
        algorithm_id: str,
        *,
        timeout_s: float,
        instance_count: int,
    ) -> dict[str, Any]:
        detail = self.get_algorithm(algorithm_id)
        if detail is None:
            raise RouterManagerError('algorithm_not_found', f'algorithm not found: {algorithm_id}', 404)
        if detail.get('status') != 'active':
            raise RouterManagerError(
                'algorithm_restart_conflict',
                f'algorithm {algorithm_id} is not active in Router',
                409,
            )
        if self.in_ab_strategy(algorithm_id):
            raise RouterManagerError(
                'algorithm_in_ab_strategy',
                f'algorithm {algorithm_id} is referenced by active AB strategy',
                409,
            )
        status = self.status()
        children = [
            item for item in status.get('local_child_processes') or []
            if isinstance(item, Mapping) and item.get('algorithm_id') == algorithm_id
        ]
        if len(children) != instance_count:
            raise RouterManagerError(
                'algorithm_restart_conflict',
                f'algorithm {algorithm_id} has {len(children)} local Router records; expected {instance_count}',
                409,
            )
        previous_health = {
            str(item.get('port')): str(item.get('last_health_at') or '')
            for item in children
        }
        try:
            result = self._request(
                'POST',
                f'{self._algorithm_url(algorithm_id)}/restart',
                timeout_s=ADMIN_TIMEOUT_SECONDS,
            )
            deadline = time.monotonic() + timeout_s
            while time.monotonic() <= deadline:
                current = [
                    item for item in self.status().get('local_child_processes') or []
                    if isinstance(item, Mapping) and item.get('algorithm_id') == algorithm_id
                ]
                health = self.healthcheck(algorithm_id)
                fresh = len(current) == instance_count and all(
                    item.get('status') == 'healthy'
                    and item.get('last_health_at')
                    and str(item.get('last_health_at')) != previous_health.get(str(item.get('port')))
                    for item in current
                )
                if fresh and health['status'] == 'passed' and health['healthy_instances'] == instance_count:
                    return result
                time.sleep(1.0)
            raise RouterManagerError(
                'router_timeout',
                f'algorithm {algorithm_id} did not report fresh health after restart',
            )
        except RouterManagerError as exc:
            raise RouterManagerError('algorithm_restart_failed', str(exc), exc.status_code) from exc

    def stop_algorithm(self, algorithm_id: str) -> dict[str, Any]:
        if self.in_ab_strategy(algorithm_id):
            raise RouterManagerError(
                'algorithm_in_ab_strategy',
                f'algorithm {algorithm_id} is referenced by active AB strategy',
                409,
            )
        try:
            try:
                result = self._request('DELETE', self._algorithm_url(algorithm_id), timeout_s=ADMIN_TIMEOUT_SECONDS)
            except RouterManagerError as exc:
                if exc.status_code == 404:
                    return {'algorithm_id': algorithm_id, 'status': 'missing'}
                raise
            deadline = time.monotonic() + STOP_TIMEOUT_SECONDS
            while time.monotonic() <= deadline:
                detail = self.get_algorithm(algorithm_id)
                if detail is None:
                    return result
                status = self.status()
                instances = list(detail.get('instances') or [])
                global_algorithms = status.get('global_algorithms')
                global_algorithm = (
                    global_algorithms.get(algorithm_id)
                    if isinstance(global_algorithms, Mapping)
                    else None
                )
                global_healthy = (
                    int(global_algorithm.get('healthy') or 0)
                    if isinstance(global_algorithm, Mapping)
                    else 0
                )
                if (
                    detail.get('status') == 'disabled'
                    and global_healthy == 0
                    and not any(item.get('status') == 'healthy' for item in instances if isinstance(item, Mapping))
                ):
                    return result
                time.sleep(1.0)
            raise RouterManagerError(
                'algorithm_stop_failed',
                f'algorithm {algorithm_id} still has healthy Router instances after stop',
                503,
            )
        except RouterManagerError as exc:
            if exc.kind == 'algorithm_stop_failed':
                raise
            raise RouterManagerError('algorithm_stop_failed', str(exc), exc.status_code) from exc

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

    def in_ab_strategy(self, algorithm_id: str) -> bool:
        strategy = self.get_ab_strategy().get('strategy')
        weights = strategy.get('weights') if isinstance(strategy, Mapping) else {}
        return algorithm_id in (weights or {})

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
