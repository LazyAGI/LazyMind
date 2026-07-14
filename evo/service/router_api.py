from __future__ import annotations

import os
from collections.abc import Mapping
from typing import Any, Literal

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, ConfigDict, Field

from evo.operations.route.router_algorithm import (
    delete_owned_algorithm,
    ensure_owned_algorithm,
    manage_owned_algorithm,
)
from evo.operations.route.router_ledger import RouterAlgorithmLedger, RouterLedgerError
from evo.operations.route.router_manager import (
    DEFAULT_ROUTER_CHAT_URL,
    RouterAlgorithmSpec,
    RouterManager,
    RouterManagerError,
    admin_url_from_chat_url,
)


EVO_ALGORITHM_PREFIX = 'evo_'


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid')


class AlgorithmOwner(StrictModel):
    thread_id: str = Field(pattern=r'^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$')
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


def build_router_api(service: Any) -> APIRouter:
    api = APIRouter(prefix='/router', tags=['router-management'])
    ledger = RouterAlgorithmLedger(service.threads.runtime.store_root)

    @api.get('/status')
    def status(
        router_admin_url: str = '',
        router_chat_url: str = '',
    ) -> dict[str, Any]:
        rows = ledger.list_algorithms()
        manager = _ledger_manager(ledger, router_admin_url, router_chat_url)
        try:
            manager.status()
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
        rows = ledger.list_algorithms(thread_id=thread_id, algorithm_id=algorithm_id)
        manager = _ledger_manager(ledger, router_admin_url, router_chat_url)
        try:
            items = [_owned_live_item(manager, ledger, row) for row in rows]
        except RouterManagerError as exc:
            _raise_router_error(exc)
        if status:
            items = [item for item in items if item['status'] == status]
        return {'items': items}

    @api.post('/algorithms')
    def register(payload: RegisterAlgorithmBody) -> dict[str, Any]:
        _require_evo_algorithm(payload.algorithm_id)
        manager = _ledger_manager(ledger, payload.router_admin_url, payload.router_chat_url)
        owner = payload.owner
        if owner.run_id and owner.run_id != owner.thread_id:
            raise HTTPException(422, _error('owner_invalid', 'owner.run_id must match owner.thread_id'))
        spec = RouterAlgorithmSpec(
            id=payload.algorithm_id,
            name=payload.name or payload.algorithm_id,
            code_path=payload.code_path,
            instance_count=payload.instance_count,
            config=dict(payload.config),
        )
        with service.threads.exclusive_operation(owner.thread_id):
            service.threads.public_thread(owner.thread_id, include_inputs=False)
            try:
                register_response, detail = ensure_owned_algorithm(
                    manager,
                    ledger,
                    spec,
                    {
                        'thread_id': owner.thread_id,
                        'run_id': owner.run_id,
                        'candidate_ref': owner.candidate_ref,
                        'cleanup_policy': payload.cleanup_policy,
                    },
                    timeout_s=payload.wait_ready_seconds,
                )
                health = manager.healthcheck_from_detail(detail)
                return {
                    'status': 'ready',
                    'algorithm_id': spec.id,
                    'router_chat_url': manager.router_chat_url,
                    'router_admin_url': manager.router_admin_url,
                    'register_response': {
                        key: value for key, value in register_response.items() if key != 'ports'
                    },
                    'healthcheck': health,
                }
            except RouterManagerError as exc:
                _raise_router_error(exc)
            except RouterLedgerError as exc:
                raise HTTPException(409, _error('algorithm_conflict', str(exc))) from exc
            except Exception as exc:
                raise HTTPException(500, _error('ledger_error', str(exc))) from exc

    @api.post('/algorithms/{algorithm_id}/action')
    def action(
        algorithm_id: str,
        payload: AlgorithmActionBody,
        router_admin_url: str = '',
        router_chat_url: str = '',
    ) -> dict[str, Any]:
        row = _owned_row(ledger, algorithm_id)
        manager = _manager_for_row(row, router_admin_url, router_chat_url)
        try:
            health = manage_owned_algorithm(
                manager,
                ledger,
                algorithm_id,
                payload.action,
                timeout_s=payload.wait_ready_seconds,
            )
            return {
                'status': health.get('status'),
                'algorithm_id': algorithm_id,
                'action': payload.action,
                'healthcheck': dict(health),
            }
        except RouterManagerError as exc:
            _raise_router_error(exc)
        except RouterLedgerError as exc:
            raise HTTPException(409, _error('algorithm_conflict', str(exc))) from exc

    @api.delete('/algorithms/{algorithm_id}')
    def delete_algorithm(
        algorithm_id: str,
        router_admin_url: str = '',
        router_chat_url: str = '',
    ) -> dict[str, Any]:
        row = _owned_row(ledger, algorithm_id)
        manager = _manager_for_row(row, router_admin_url, router_chat_url)
        with service.threads.exclusive_operation(str(row['thread_id'])):
            try:
                return delete_owned_algorithm(
                    manager,
                    ledger,
                    algorithm_id,
                    service.threads.repair_work_root,
                    service.threads.runtime.store_root,
                )
            except RouterManagerError as exc:
                _raise_router_error(exc)
            except RouterLedgerError as exc:
                raise HTTPException(409, _error('algorithm_conflict', str(exc))) from exc
            except Exception as exc:
                raise HTTPException(500, _error('algorithm_delete_error', str(exc))) from exc

    @api.get('/ab-strategy')
    def get_ab_strategy(
        router_admin_url: str = '',
        router_chat_url: str = '',
    ) -> dict[str, Any]:
        manager = _ledger_manager(ledger, router_admin_url, router_chat_url)
        try:
            return _strategy_response(manager.get_ab_strategy(), ledger)
        except RouterManagerError as exc:
            _raise_router_error(exc)

    @api.put('/ab-strategy')
    def put_ab_strategy(payload: AbStrategyBody) -> dict[str, Any]:
        manager = _ledger_manager(ledger, payload.router_admin_url, payload.router_chat_url)
        try:
            with ledger.router_mutation():
                previous = manager.get_ab_strategy()
                if payload.weights is None:
                    result = manager.clear_ab_strategy()
                    next_strategy: dict[str, Any] = {'strategy': None}
                else:
                    _validate_strategy_algorithms(manager, ledger, payload.weights)
                    result = manager.update_ab_strategy(payload.weights)
                    next_strategy = {'strategy': result}
                owner = payload.owner
                try:
                    ledger.record_ab_strategy(
                        thread_id='' if owner is None else owner.thread_id,
                        candidate_ref='' if owner is None else owner.candidate_ref,
                        previous_strategy=previous,
                        next_strategy=next_strategy,
                        reason=payload.reason,
                    )
                except Exception:
                    previous_weights = _strategy_weights(previous)
                    if previous_weights:
                        manager.update_ab_strategy(previous_weights)
                    else:
                        manager.clear_ab_strategy()
                    raise
                return _strategy_response(manager.get_ab_strategy(), ledger) | {'router_response': result}
        except RouterManagerError as exc:
            _raise_router_error(exc)
        except RouterLedgerError as exc:
            raise HTTPException(409, _error('algorithm_conflict', str(exc))) from exc

    return api


def _manager(router_admin_url: str = '', router_chat_url: str = '') -> RouterManager:
    chat_url = router_chat_url or os.getenv('LAZYMIND_EVO_ROUTER_CHAT_URL') or DEFAULT_ROUTER_CHAT_URL
    admin_url = router_admin_url or os.getenv('LAZYMIND_EVO_ROUTER_ADMIN_URL') or admin_url_from_chat_url(chat_url)
    return RouterManager(admin_url, chat_url)


def _manager_for_row(
    row: Mapping[str, Any],
    router_admin_url: str = '',
    router_chat_url: str = '',
) -> RouterManager:
    stored = RouterManager(str(row['router_admin_url']), str(row['service_url']))
    if router_admin_url or router_chat_url:
        requested = RouterManager(
            router_admin_url or stored.router_admin_url,
            router_chat_url or stored.router_chat_url,
        )
        if (requested.router_admin_url, requested.router_chat_url) != (stored.router_admin_url, stored.router_chat_url):
            raise HTTPException(409, _error('router_conflict', 'requested router does not own this algorithm'))
    return stored


def _ledger_manager(
    ledger: RouterAlgorithmLedger,
    router_admin_url: str = '',
    router_chat_url: str = '',
) -> RouterManager:
    rows = ledger.list_algorithms()
    endpoints = {(str(row['router_admin_url']), str(row['service_url'])) for row in rows}
    if len(endpoints) > 1:
        raise HTTPException(409, _error('router_conflict', 'evo algorithms belong to different routers'))
    if rows:
        return _manager_for_row(rows[0], router_admin_url, router_chat_url)
    return _manager(router_admin_url, router_chat_url)


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


def _strategy_response(strategy: Mapping[str, Any], ledger: RouterAlgorithmLedger) -> dict[str, Any]:
    audit = ledger.latest_ab_audit()
    return {
        **_strategy_view(strategy),
        'updated_by': {} if audit is None else {
            'thread_id': str(audit.get('thread_id') or ''),
            'candidate_ref': str(audit.get('candidate_ref') or ''),
            'reason': str(audit.get('reason') or ''),
        },
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


def _require_evo_algorithm(algorithm_id: str) -> None:
    if not algorithm_id.startswith(EVO_ALGORITHM_PREFIX):
        raise HTTPException(422, _error('algorithm_not_owned', 'algorithm_id must start with evo_'))


def _raise_router_error(exc: RouterManagerError) -> None:
    fallback = {
        'router_config_error': 400,
        'algorithm_conflict': 409,
        'algorithm_in_ab_strategy': 409,
        'algorithm_restart_conflict': 409,
        'algorithm_reactivation_failed': 503,
        'algorithm_unhealthy': 409,
        'algorithm_not_found': 404,
        'router_timeout': 504,
        'router_transport_error': 503,
        'router_protocol_error': 502,
    }.get(exc.kind, 502)
    status = exc.status_code if 400 <= exc.status_code <= 599 else fallback
    raise HTTPException(status, _error(exc.kind, str(exc))) from exc


def _error(error_type: str, message: str) -> dict[str, str]:
    return {'type': error_type, 'message': message}
