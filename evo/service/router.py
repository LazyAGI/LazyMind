from __future__ import annotations

import asyncio
import os
from collections.abc import Callable, Mapping
from pathlib import Path
from typing import Any, TypeVar

from evo.artifact_flow import ArtifactFlow
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

from .contracts import (
    AbStrategyBody,
    AlgorithmActionBody,
    RegisterAlgorithmBody,
    ServiceError,
)


T = TypeVar('T')
_EVO_PREFIX = 'evo_'


class RouterService:
    def __init__(self, root: str | Path, flow: ArtifactFlow) -> None:
        self.root = Path(root)
        self.flow = flow
        self.ledger = RouterAlgorithmLedger(self.root / 'router-store')
        self.manager = _ledger_manager(self.ledger)
        self._lock = asyncio.Lock()

    async def status(self) -> dict[str, Any]:
        return await self._call(self._status)

    async def algorithms(self, *, thread_id: str = '', algorithm_id: str = '',
                         status: str = ''
                         ) -> dict[str, Any]:
        return await self._call(
            self._algorithms,
            thread_id,
            algorithm_id,
            status,
        )

    async def register(self, request: RegisterAlgorithmBody | Mapping[str, Any]
                       ) -> dict[str, Any]:
        request = (
            request
            if isinstance(request, RegisterAlgorithmBody)
            else RegisterAlgorithmBody.model_validate(request)
        )
        if not request.algorithm_id.startswith(_EVO_PREFIX):
            raise ServiceError(422, _error(
                'algorithm_not_owned',
                'algorithm_id must start with evo_',
            ))
        if not await self.flow.has_run(request.owner.thread_id):
            raise ServiceError(404, f'thread not found: {request.owner.thread_id}')
        return await self._call(self._register, request)

    async def action(self, algorithm_id: str,
                     request: AlgorithmActionBody | Mapping[str, Any]
                     ) -> dict[str, Any]:
        request = (
            request
            if isinstance(request, AlgorithmActionBody)
            else AlgorithmActionBody.model_validate(request)
        )
        return await self._call(self._action, algorithm_id, request)

    async def delete(self, algorithm_id: str) -> dict[str, Any]:
        return await self._call(self._delete, algorithm_id)

    async def get_ab_strategy(self) -> dict[str, Any]:
        return await self._call(self._get_ab_strategy)

    async def put_ab_strategy(self, request: AbStrategyBody | Mapping[str, Any]
                              ) -> dict[str, Any]:
        request = (
            request
            if isinstance(request, AbStrategyBody)
            else AbStrategyBody.model_validate(request)
        )
        owner = request.owner
        if owner is not None and not await self.flow.has_run(owner.thread_id):
            raise ServiceError(404, f'thread not found: {owner.thread_id}')
        return await self._call(self._put_ab_strategy, request)

    async def delete_thread(self, thread_id: str) -> None:
        await self._call(self._delete_thread, thread_id)

    async def _call(self, function: Callable[..., T], *args: Any,
                    **kwargs: Any
                    ) -> T:
        async with self._lock:
            return await _run_sync(function, *args, **kwargs)

    def _delete_thread(self, thread_id: str) -> None:
        rows = self.ledger.list_algorithms(thread_id=thread_id)
        manual = [row['algorithm_id'] for row in rows if row['cleanup_policy'] == 'manual']
        if manual:
            raise ServiceError(
                409,
                _error('algorithm_conflict', 'manually managed algorithms must be deleted first'),
            )
        for row in rows:
            self._delete(str(row['algorithm_id']))

    def _status(self) -> dict[str, Any]:
        rows = self.ledger.list_algorithms()
        try:
            self.manager.status()
            live = [_owned_live_item(self.manager, self.ledger, row) for row in rows]
            active = [item for item in live if item['status'] == 'active']
            healthy = [item for item in active if item['healthy_instances'] > 0]
            return {
                'status': 'ok',
                'router_admin_url': self.manager.router_admin_url,
                'algorithms': {
                    'evo_owned': len(rows),
                    'active': len(active),
                    'healthy': len(healthy),
                },
                'ab_strategy': _strategy_view(self.manager.get_ab_strategy()),
            }
        except RouterManagerError as exc:
            raise _router_error(exc) from exc

    def _algorithms(self, thread_id: str, algorithm_id: str,
                    status: str
                    ) -> dict[str, Any]:
        rows = self.ledger.list_algorithms(
            thread_id=thread_id,
            algorithm_id=algorithm_id,
        )
        try:
            items = [_owned_live_item(self.manager, self.ledger, row) for row in rows]
        except RouterManagerError as exc:
            raise _router_error(exc) from exc
        if status:
            items = [item for item in items if item['status'] == status]
        return {'items': items}

    def _register(self, request: RegisterAlgorithmBody) -> dict[str, Any]:
        spec = RouterAlgorithmSpec(
            id=request.algorithm_id,
            name=request.name or request.algorithm_id,
            code_path=request.code_path,
            instance_count=request.instance_count,
            config=dict(request.config),
        )
        owner = request.owner
        try:
            response, detail = ensure_owned_algorithm(
                self.manager,
                self.ledger,
                spec,
                {
                    'thread_id': owner.thread_id,
                    'run_id': owner.thread_id,
                    'candidate_ref': owner.candidate_ref,
                    'cleanup_policy': request.cleanup_policy,
                },
                timeout_s=request.wait_ready_seconds,
            )
            return {
                'status': 'ready',
                'algorithm_id': spec.id,
                'router_chat_url': self.manager.router_chat_url,
                'router_admin_url': self.manager.router_admin_url,
                'register_response': {
                    key: value for key, value in response.items() if key != 'ports'
                },
                'healthcheck': self.manager.healthcheck_from_detail(detail),
            }
        except RouterManagerError as exc:
            raise _router_error(exc) from exc
        except RouterLedgerError as exc:
            raise ServiceError(409, _error('algorithm_conflict', str(exc))) from exc

    def _action(self, algorithm_id: str,
                request: AlgorithmActionBody
                ) -> dict[str, Any]:
        _owned_row(self.ledger, algorithm_id)
        try:
            health = manage_owned_algorithm(
                self.manager,
                self.ledger,
                algorithm_id,
                request.action,
                timeout_s=request.wait_ready_seconds,
            )
            return {
                'status': health.get('status'),
                'algorithm_id': algorithm_id,
                'action': request.action,
                'healthcheck': dict(health),
            }
        except RouterManagerError as exc:
            raise _router_error(exc) from exc
        except RouterLedgerError as exc:
            raise ServiceError(409, _error('algorithm_conflict', str(exc))) from exc

    def _delete(self, algorithm_id: str) -> dict[str, Any]:
        _owned_row(self.ledger, algorithm_id)
        try:
            return delete_owned_algorithm(
                self.manager,
                self.ledger,
                algorithm_id,
                self.root / 'work' / 'repair',
            )
        except RouterManagerError as exc:
            raise _router_error(exc) from exc
        except RouterLedgerError as exc:
            raise ServiceError(409, _error('algorithm_conflict', str(exc))) from exc

    def _get_ab_strategy(self) -> dict[str, Any]:
        try:
            return _strategy_response(self.manager.get_ab_strategy(), self.ledger)
        except RouterManagerError as exc:
            raise _router_error(exc) from exc

    def _put_ab_strategy(self, request: AbStrategyBody) -> dict[str, Any]:
        try:
            with self.ledger.router_mutation():
                previous = self.manager.get_ab_strategy()
                if request.weights is None:
                    router_response = self.manager.clear_ab_strategy()
                    next_strategy: dict[str, Any] = {'strategy': None}
                else:
                    _validate_weights(self.manager, self.ledger, request.weights)
                    router_response = self.manager.update_ab_strategy(request.weights)
                    next_strategy = {'strategy': router_response}
                owner = request.owner
                try:
                    self.ledger.record_ab_strategy(
                        thread_id='' if owner is None else owner.thread_id,
                        candidate_ref='' if owner is None else owner.candidate_ref,
                        previous_strategy=previous,
                        next_strategy=next_strategy,
                        reason=request.reason,
                    )
                except Exception:
                    weights = _strategy_weights(previous)
                    if weights:
                        self.manager.update_ab_strategy(weights)
                    else:
                        self.manager.clear_ab_strategy()
                    raise
            return _strategy_response(self.manager.get_ab_strategy(), self.ledger) | {
                'router_response': router_response,
            }
        except RouterManagerError as exc:
            raise _router_error(exc) from exc
        except RouterLedgerError as exc:
            raise ServiceError(409, _error('algorithm_conflict', str(exc))) from exc


async def _run_sync(function: Callable[..., T], *args: Any,
                    **kwargs: Any
                    ) -> T:
    worker = asyncio.create_task(asyncio.to_thread(function, *args, **kwargs))
    try:
        return await asyncio.shield(worker)
    except asyncio.CancelledError:
        await worker
        raise


def _ledger_manager(ledger: RouterAlgorithmLedger) -> RouterManager:
    chat_url = os.getenv('LAZYMIND_EVO_ROUTER_CHAT_URL') or DEFAULT_ROUTER_CHAT_URL
    admin_url = os.getenv('LAZYMIND_EVO_ROUTER_ADMIN_URL') or admin_url_from_chat_url(chat_url)
    return RouterManager(admin_url, chat_url)


def _owned_row(ledger: RouterAlgorithmLedger, algorithm_id: str) -> dict[str, Any]:
    row = ledger.get_algorithm(algorithm_id)
    if row is None:
        raise ServiceError(404, _error(
            'algorithm_not_owned',
            f'algorithm is not evo-owned: {algorithm_id}',
        ))
    return row


def _owned_live_item(manager: RouterManager, ledger: RouterAlgorithmLedger,
                     row: Mapping[str, Any]
                     ) -> dict[str, Any]:
    detail = manager.get_algorithm(str(row['algorithm_id']))
    health = manager.healthcheck_from_detail(detail)
    return {
        'algorithm_id': row['algorithm_id'],
        'status': str((detail or {}).get('status') or 'missing'),
        'expected_state': row['expected_state'],
        'healthy_instances': health['healthy_instances'],
        'instance_count': len(health['instances']),
        'owner': {
            'thread_id': row['thread_id'],
            'candidate_ref': row['candidate_ref'],
        },
        'router_chat_url': row['service_url'],
        'router_admin_url': row['router_admin_url'],
    }


def _validate_weights(manager: RouterManager, ledger: RouterAlgorithmLedger,
                      weights: Mapping[str, int]
                      ) -> None:
    if not weights or any(weight <= 0 for weight in weights.values()):
        raise ServiceError(422, _error(
            'ab_strategy_invalid',
            'weights must contain positive integers',
        ))
    for algorithm_id in weights:
        if algorithm_id != 'default':
            row = _owned_row(ledger, algorithm_id)
            if row['expected_state'] != 'active':
                raise ServiceError(409, _error(
                    'ab_strategy_invalid',
                    f'{algorithm_id} is not expected active',
                ))
        if manager.healthcheck(algorithm_id)['status'] != 'passed':
            raise ServiceError(409, _error(
                'algorithm_unhealthy',
                f'{algorithm_id} has no healthy instance',
            ))


def _strategy_response(strategy: Mapping[str, Any],
                       ledger: RouterAlgorithmLedger
                       ) -> dict[str, Any]:
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
    return dict(raw.get('weights') or {}) if raw is not None else {}


def _router_error(error: RouterManagerError) -> ServiceError:
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
    }.get(error.kind, 502)
    status = error.status_code if 400 <= error.status_code <= 599 else fallback
    return ServiceError(status, _error(error.kind, str(error)))


def _error(kind: str, message: str) -> dict[str, str]:
    return {'type': kind, 'message': message}


__all__ = ['RouterService']
