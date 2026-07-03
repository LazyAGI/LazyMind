from __future__ import annotations

import os
from collections.abc import Mapping
from typing import Annotated, Any, Literal

from fastapi import APIRouter, Body, HTTPException
from pydantic import BaseModel, ConfigDict, Field

from evo.operations.router_ledger import RouterAlgorithmLedger, json_hash
from evo.operations.router_manager import (
    DEFAULT_ROUTER_CHAT_URL,
    RouterAlgorithmSpec,
    RouterManager,
    RouterManagerError,
    admin_url_from_chat_url,
    normalize_chat_url,
)


EVO_ALGORITHM_PREFIX = 'evo_'


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid')


class AlgorithmOwner(StrictModel):
    thread_id: str = Field(min_length=1)
    run_id: str = ''
    candidate_ref: str = ''


class RegisterAlgorithmBody(StrictModel):
    algorithm_id: str = Field(min_length=1)
    name: str = ''
    code_path: str = Field(min_length=1)
    instance_count: int = Field(default=1, ge=1, le=4)
    config: dict[str, Any] = Field(default_factory=dict)
    owner: AlgorithmOwner
    wait_ready_seconds: float = Field(default=180.0, gt=0, le=900)
    cleanup_policy: Literal['thread_delete', 'manual'] = 'thread_delete'
    router_admin_url: str = ''
    router_chat_url: str = ''


class AlgorithmActionBody(StrictModel):
    action: Literal['healthcheck', 'restart', 'stop']
    wait_ready_seconds: float = Field(default=180.0, gt=0, le=900)


class AbStrategyBody(StrictModel):
    weights: dict[str, int] | None = None
    reason: str = ''
    owner: AlgorithmOwner | None = None
    router_admin_url: str = ''
    router_chat_url: str = ''


RegisterBody = Annotated[RegisterAlgorithmBody, Body()]
ActionBody = Annotated[AlgorithmActionBody, Body()]
StrategyBody = Annotated[AbStrategyBody, Body()]


def build_router_api(service: Any) -> APIRouter:
    api = APIRouter(prefix='/router', tags=['router-management'])

    @api.get('/status')
    def status(
        router_admin_url: str = '',
        router_chat_url: str = '',
    ) -> dict[str, Any]:
        manager = _manager(router_admin_url, router_chat_url)
        ledger = _ledger(service)
        try:
            manager.status()
            rows = ledger.list_algorithms()
            live = [_owned_live_item(manager, ledger, row) for row in rows]
            active = [item for item in live if item['status'] == 'active']
            healthy = [item for item in active if item['healthy_instances'] > 0]
            return {
                'status': 'ok',
                'router_admin_url': manager.router_admin_url,
                'algorithms': {
                    'evo_owned': len(rows),
                    'active': len(active),
                    'healthy': len(healthy),
                },
                'ab_strategy': _strategy_view(manager.get_ab_strategy()),
            }
        except RouterManagerError as exc:
            _raise_router_error(exc)

    @api.get('/algorithms')
    def algorithms(
        thread_id: str = '',
        algorithm_id: str = '',
        status: str = '',
        router_admin_url: str = '',
        router_chat_url: str = '',
    ) -> dict[str, Any]:
        manager = _manager(router_admin_url, router_chat_url)
        ledger = _ledger(service)
        rows = ledger.list_algorithms(thread_id=thread_id, algorithm_id=algorithm_id)
        try:
            items = [_owned_live_item(manager, ledger, row) for row in rows]
        except RouterManagerError as exc:
            _raise_router_error(exc)
        if status:
            items = [item for item in items if item['status'] == status]
        return {'items': items}

    @api.post('/algorithms')
    def register(payload: RegisterBody) -> dict[str, Any]:
        _require_evo_algorithm(payload.algorithm_id)
        manager = _manager(payload.router_admin_url, payload.router_chat_url)
        ledger = _ledger(service)
        spec = RouterAlgorithmSpec(
            id=payload.algorithm_id,
            name=payload.name or payload.algorithm_id,
            code_path=payload.code_path,
            instance_count=payload.instance_count,
            config=dict(payload.config),
        )
        register_request = _register_request(spec)
        config_hash = json_hash(spec.config)
        request_hash = json_hash(register_request)
        try:
            existing = manager.get_algorithm(spec.id)
            if existing is not None and not _same_registration(existing, register_request):
                raise HTTPException(409, _error('algorithm_conflict', 'algorithm_id already exists'))
            register_response = (
                {'reused': True}
                if existing is not None
                else manager.register_algorithm(spec, timeout_s=payload.wait_ready_seconds)
            )
            detail = manager.wait_ready(spec.id, timeout_s=payload.wait_ready_seconds)
            health = manager.healthcheck_from_detail(detail)
            owner = payload.owner
            run_id = owner.run_id or owner.thread_id
            try:
                ledger.upsert_algorithm(
                    algorithm_id=spec.id,
                    thread_id=owner.thread_id,
                    run_id=run_id,
                    candidate_ref=owner.candidate_ref,
                    router_admin_url=manager.router_admin_url,
                    service_url=manager.router_chat_url,
                    code_path=spec.code_path,
                    config_hash=config_hash,
                    register_request_hash=request_hash,
                    cleanup_policy=payload.cleanup_policy,
                )
                ledger.record_router_status(spec.id, health)
            except Exception as exc:
                raise HTTPException(
                    500,
                    _error('ledger_error', f'router registered {spec.id}, but ledger write failed: {exc}'),
                ) from exc
            return {
                'status': 'ready',
                'algorithm_id': spec.id,
                'router_chat_url': manager.router_chat_url,
                'router_admin_url': manager.router_admin_url,
                'register_response': _safe_register_response(register_response),
                'healthcheck': health,
            }
        except RouterManagerError as exc:
            _raise_router_error(exc)

    @api.post('/algorithms/{algorithm_id}:action')
    def action(
        algorithm_id: str,
        payload: ActionBody,
        router_admin_url: str = '',
        router_chat_url: str = '',
    ) -> dict[str, Any]:
        manager = _manager(router_admin_url, router_chat_url)
        ledger = _ledger(service)
        row = _owned_row(ledger, algorithm_id)
        try:
            if payload.action == 'healthcheck':
                health = manager.healthcheck(algorithm_id)
                ledger.record_router_status(algorithm_id, health)
                return _action_result(algorithm_id, payload.action, health)
            if payload.action == 'restart':
                manager.restart_algorithm(algorithm_id, timeout_s=payload.wait_ready_seconds)
                health = manager.healthcheck(algorithm_id)
                ledger.record_router_status(algorithm_id, health)
                return _action_result(algorithm_id, payload.action, health)
            _ensure_not_in_strategy(manager, algorithm_id)
            manager.stop_algorithm(algorithm_id)
            ledger.mark_state(row['algorithm_id'], 'stopped')
            health = {'status': 'stopped', 'healthy_instances': 0, 'instances': []}
            ledger.record_router_status(algorithm_id, health)
            return _action_result(algorithm_id, payload.action, health)
        except RouterManagerError as exc:
            _raise_router_error(exc)

    @api.get('/ab-strategy')
    def get_ab_strategy(
        router_admin_url: str = '',
        router_chat_url: str = '',
    ) -> dict[str, Any]:
        manager = _manager(router_admin_url, router_chat_url)
        try:
            return _strategy_response(manager.get_ab_strategy(), _ledger(service))
        except RouterManagerError as exc:
            _raise_router_error(exc)

    @api.put('/ab-strategy')
    def put_ab_strategy(payload: StrategyBody) -> dict[str, Any]:
        manager = _manager(payload.router_admin_url, payload.router_chat_url)
        ledger = _ledger(service)
        try:
            previous = manager.get_ab_strategy()
            if payload.weights is None:
                result = manager.clear_ab_strategy()
                next_strategy: dict[str, Any] = {'strategy': None}
            else:
                _validate_strategy_algorithms(manager, ledger, payload.weights)
                result = manager.update_ab_strategy(payload.weights)
                next_strategy = {'strategy': result}
            owner = payload.owner
            ledger.record_ab_strategy(
                thread_id='' if owner is None else owner.thread_id,
                candidate_ref='' if owner is None else owner.candidate_ref,
                previous_strategy=previous,
                next_strategy=next_strategy,
                reason=payload.reason,
            )
            return _strategy_response(manager.get_ab_strategy(), ledger) | {'router_response': result}
        except RouterManagerError as exc:
            _raise_router_error(exc)

    return api


def _manager(router_admin_url: str = '', router_chat_url: str = '') -> RouterManager:
    chat_url = router_chat_url or os.getenv('LAZYMIND_EVO_ROUTER_CHAT_URL') or DEFAULT_ROUTER_CHAT_URL
    admin_url = router_admin_url or os.getenv('LAZYMIND_EVO_ROUTER_ADMIN_URL') or admin_url_from_chat_url(chat_url)
    return RouterManager(admin_url, normalize_chat_url(chat_url))


def _ledger(service: Any) -> RouterAlgorithmLedger:
    return RouterAlgorithmLedger(service.threads.runtime.store_root)


def _owned_row(ledger: RouterAlgorithmLedger, algorithm_id: str) -> dict[str, Any]:
    row = ledger.get_algorithm(algorithm_id)
    if row is None:
        raise HTTPException(404, _error('algorithm_not_owned', f'algorithm is not evo-owned: {algorithm_id}'))
    return row


def _owned_live_item(
    manager: RouterManager,
    ledger: RouterAlgorithmLedger,
    row: Mapping[str, Any],
) -> dict[str, Any]:
    detail = manager.get_algorithm(str(row['algorithm_id']))
    health = manager.healthcheck_from_detail(detail)
    status = str((detail or {}).get('status') or 'missing')
    ledger.record_router_status(str(row['algorithm_id']), health)
    return {
        'algorithm_id': row['algorithm_id'],
        'status': status,
        'expected_state': row['expected_state'],
        'healthy_instances': health['healthy_instances'],
        'instance_count': len(health['instances']),
        'owner': {
            'thread_id': row['thread_id'],
            'run_id': row['run_id'],
            'candidate_ref': row['candidate_ref'],
        },
        'router_chat_url': row['service_url'],
        'router_admin_url': row['router_admin_url'],
    }


def _validate_strategy_algorithms(
    manager: RouterManager,
    ledger: RouterAlgorithmLedger,
    weights: Mapping[str, int],
) -> None:
    if not weights:
        raise HTTPException(422, _error('ab_strategy_invalid', 'weights must not be empty'))
    for algorithm_id, weight in weights.items():
        if weight <= 0:
            raise HTTPException(422, _error('ab_strategy_invalid', 'weights must be positive integers'))
        if algorithm_id != 'default':
            row = _owned_row(ledger, algorithm_id)
            if row.get('expected_state') != 'active':
                raise HTTPException(409, _error('ab_strategy_invalid', f'{algorithm_id} is not expected active'))
        health = manager.healthcheck(algorithm_id)
        if health['status'] != 'passed':
            raise HTTPException(409, _error('algorithm_unhealthy', f'{algorithm_id} has no healthy instance'))


def _ensure_not_in_strategy(manager: RouterManager, algorithm_id: str) -> None:
    weights = _strategy_weights(manager.get_ab_strategy())
    if algorithm_id in weights:
        raise HTTPException(409, _error('ab_strategy_invalid', f'{algorithm_id} is referenced by active strategy'))


def _strategy_response(strategy: Mapping[str, Any], ledger: RouterAlgorithmLedger) -> dict[str, Any]:
    return {
        **_strategy_view(strategy),
        'updated_by': _latest_audit_owner(ledger),
    }


def _strategy_view(strategy: Mapping[str, Any]) -> dict[str, Any]:
    raw = strategy.get('strategy') if isinstance(strategy.get('strategy'), Mapping) else None
    return {
        'active': raw is not None,
        'id': None if raw is None else raw.get('id'),
        'weights': {} if raw is None else dict(raw.get('weights') or {}),
    }


def _strategy_weights(strategy: Mapping[str, Any]) -> dict[str, int]:
    raw = strategy.get('strategy') if isinstance(strategy.get('strategy'), Mapping) else None
    weights = raw.get('weights') if isinstance(raw, Mapping) else {}
    return dict(weights or {})


def _latest_audit_owner(ledger: RouterAlgorithmLedger) -> dict[str, str]:
    audit = ledger.latest_ab_audit()
    if audit is None:
        return {}
    return {
        'thread_id': str(audit.get('thread_id') or ''),
        'candidate_ref': str(audit.get('candidate_ref') or ''),
        'reason': str(audit.get('reason') or ''),
    }


def _register_request(spec: RouterAlgorithmSpec) -> dict[str, Any]:
    return {
        'id': spec.id,
        'name': spec.name,
        'code_path': spec.code_path,
        'instance_count': spec.instance_count,
        'config': dict(spec.config),
    }


def _same_registration(existing: Mapping[str, Any], body: Mapping[str, Any]) -> bool:
    return existing.get('code_path') == body.get('code_path') and dict(existing.get('config') or {}) == body['config']


def _action_result(algorithm_id: str, action: str, health: Mapping[str, Any]) -> dict[str, Any]:
    return {
        'status': health.get('status'),
        'algorithm_id': algorithm_id,
        'action': action,
        'healthcheck': dict(health),
    }


def _safe_register_response(value: Mapping[str, Any]) -> dict[str, Any]:
    return {key: value[key] for key in value if key not in {'ports'}}


def _require_evo_algorithm(algorithm_id: str) -> None:
    if not algorithm_id.startswith(EVO_ALGORITHM_PREFIX):
        raise HTTPException(422, _error('algorithm_not_owned', 'algorithm_id must start with evo_'))


def _raise_router_error(exc: RouterManagerError) -> None:
    status = {
        'router_config_error': 400,
        'algorithm_not_found': 404,
        'router_timeout': 504,
        'router_transport_error': 503,
        'router_protocol_error': 502,
    }.get(exc.kind, 502)
    if exc.status_code == 404:
        status = 404
    raise HTTPException(status, _error(exc.kind, str(exc))) from exc


def _error(error_type: str, message: str) -> dict[str, str]:
    return {'type': error_type, 'message': message}
