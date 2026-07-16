from __future__ import annotations

from collections.abc import Mapping
from pathlib import Path
from typing import Any

from .cleanup import delete_algorithm_artifacts, delete_managed_workspace
from .router_ledger import RouterAlgorithmLedger, RouterLedgerError, json_hash
from .router_manager import RouterAlgorithmSpec, RouterManager, RouterManagerError


def ensure_owned_algorithm(
    manager: RouterManager,
    ledger: RouterAlgorithmLedger,
    spec: RouterAlgorithmSpec,
    owner: Mapping[str, str],
    *,
    timeout_s: float,
    restart_unhealthy: bool = True,
) -> tuple[dict[str, Any], dict[str, Any]]:
    with ledger.router_mutation():
        request_hash = json_hash(spec.payload())
        thread_id = owner['thread_id']
        claim, previous_state = ledger.claim_algorithm(
            algorithm_id=spec.id,
            thread_id=thread_id,
            run_id=owner.get('run_id') or thread_id,
            candidate_ref=owner.get('candidate_ref', ''),
            router_admin_url=manager.router_admin_url,
            service_url=manager.router_chat_url,
            code_path=spec.code_path,
            instance_count=spec.instance_count,
            config_hash=json_hash(spec.config),
            register_request_hash=request_hash,
            cleanup_policy=owner.get('cleanup_policy', 'thread_delete'),
        )
        try:
            registration, detail = manager.ensure_algorithm(
                spec,
                timeout_s=timeout_s,
                restart_unhealthy=restart_unhealthy,
                allow_existing=previous_state is not None,
            )
        except Exception as exc:
            if isinstance(exc, RouterManagerError) and exc.kind in {
                'algorithm_reactivation_failed',
                'algorithm_restart_failed',
            }:
                next_state = 'orphaned'
            elif previous_state is not None or (
                isinstance(exc, RouterManagerError) and exc.kind == 'algorithm_conflict'
            ):
                next_state = previous_state
            else:
                next_state = 'orphaned'
            ledger.resolve_claim(
                spec.id,
                thread_id,
                request_hash,
                float(claim['updated_at']),
                next_state,
            )
            raise
        ledger.resolve_claim(
            spec.id,
            thread_id,
            request_hash,
            float(claim['updated_at']),
            'active',
        )
        return registration, detail


def delete_owned_algorithm(
    manager: RouterManager,
    ledger: RouterAlgorithmLedger,
    algorithm_id: str,
    managed_repair_root: str | Path,
    artifact_store_root: str | Path,
) -> dict[str, Any]:
    with ledger.router_mutation():
        claim, previous_state = ledger.begin_delete(algorithm_id)
        claimed_at = float(claim['updated_at'])
        try:
            detail = manager.get_algorithm(algorithm_id)
            manager.stop_algorithm(algorithm_id)
        except RouterManagerError as exc:
            state = 'orphaned' if exc.kind == 'algorithm_stop_failed' else previous_state
            ledger.resolve_delete(algorithm_id, claimed_at, state)
            raise

        try:
            artifacts_deleted = delete_algorithm_artifacts(
                Path(artifact_store_root),
                str(claim['run_id']),
                algorithm_id,
            )
            workspace = delete_managed_workspace(claim, ledger, Path(managed_repair_root))
        except Exception:
            ledger.resolve_delete(algorithm_id, claimed_at, 'stopped')
            raise
        ledger.resolve_delete(algorithm_id, claimed_at, None)
        return {
            'status': 'deleted',
            'algorithm_id': algorithm_id,
            'router_status': 'missing' if detail is None else 'disabled',
            'router_record_retained': detail is not None,
            'ledger_deleted': True,
            'artifacts_deleted': artifacts_deleted,
            'workspace': workspace,
            'retained_history': [
                'router_metadata',
                'router_ab_strategy_history',
                'evo_ab_audit',
                'repair_artifacts',
                'repair_trace',
            ],
        }


def discard_owned_algorithm(
    manager: RouterManager,
    ledger: RouterAlgorithmLedger,
    algorithm_id: str,
) -> None:
    with ledger.router_mutation():
        if ledger.get_algorithm(algorithm_id) is None:
            return
        claim, previous_state = ledger.begin_delete(algorithm_id)
        claimed_at = float(claim['updated_at'])
        try:
            manager.stop_algorithm(algorithm_id)
        except RouterManagerError:
            ledger.resolve_delete(algorithm_id, claimed_at, previous_state)
            raise
        ledger.resolve_delete(algorithm_id, claimed_at, None)


def discard_unpublished_algorithms(ledger: RouterAlgorithmLedger, thread_id: str) -> None:
    for row in ledger.list_algorithms(thread_id=thread_id, published=False):
        discard_owned_algorithm(
            RouterManager(str(row['router_admin_url']), str(row['service_url'])),
            ledger,
            str(row['algorithm_id']),
        )


def publish_owned_algorithm(
    manager: RouterManager,
    ledger: RouterAlgorithmLedger,
    algorithm_id: str,
) -> None:
    with ledger.router_mutation():
        row = ledger.get_algorithm(algorithm_id)
        if row is None:
            raise RouterLedgerError(f'algorithm is not evo-owned: {algorithm_id}')
        health = manager.healthcheck(algorithm_id)
        if health['status'] != 'passed':
            raise RouterLedgerError(f'algorithm is not healthy: {algorithm_id}')
        conflicts = [
            item for item in ledger.list_algorithms(thread_id=str(row['thread_id']))
            if item['algorithm_id'] != algorithm_id
        ]
        if conflicts:
            raise RouterLedgerError(f'thread already owns an algorithm: {row["thread_id"]}')
        ledger.publish_algorithm(algorithm_id)


def manage_owned_algorithm(
    manager: RouterManager,
    ledger: RouterAlgorithmLedger,
    algorithm_id: str,
    action: str,
    *,
    timeout_s: float,
) -> dict[str, Any]:
    if action == 'healthcheck':
        health = manager.healthcheck(algorithm_id)
        ledger.record_router_status(algorithm_id, health)
        return health

    with ledger.router_mutation():
        claim, previous_state = ledger.begin_manage(algorithm_id)
        claimed_at = float(claim['updated_at'])
        if action == 'start':
            if previous_state != 'stopped':
                ledger.resolve_manage(algorithm_id, claimed_at, previous_state)
                raise RouterLedgerError(f'algorithm is not expected stopped: {algorithm_id}')
            if claim['instance_count'] is None:
                ledger.resolve_manage(algorithm_id, claimed_at, previous_state)
                raise RouterLedgerError(f'algorithm instance count is not registered: {algorithm_id}')
            start_complete = False
            try:
                manager.start_algorithm(
                    algorithm_id,
                    timeout_s=timeout_s,
                    instance_count=int(claim['instance_count']),
                )
                start_complete = True
                health = manager.healthcheck(algorithm_id)
            except RouterManagerError as exc:
                state = 'orphaned' if start_complete or exc.kind == 'algorithm_start_failed' else previous_state
                ledger.resolve_manage(algorithm_id, claimed_at, state)
                raise
            next_state = 'active'
        elif action == 'restart':
            if previous_state not in {'active', 'stopped'}:
                ledger.resolve_manage(algorithm_id, claimed_at, previous_state)
                raise RouterLedgerError(f'algorithm cannot be restarted from {previous_state}: {algorithm_id}')
            if claim['instance_count'] is None:
                ledger.resolve_manage(algorithm_id, claimed_at, previous_state)
                raise RouterLedgerError(f'algorithm instance count is not registered: {algorithm_id}')
            restart_complete = False
            try:
                if previous_state == 'active':
                    manager.restart_algorithm(
                        algorithm_id,
                        timeout_s=timeout_s,
                        instance_count=int(claim['instance_count']),
                    )
                else:
                    detail = manager.get_algorithm(algorithm_id)
                    if detail is None:
                        raise RouterManagerError('algorithm_not_found', f'algorithm not found: {algorithm_id}', 404)
                    manager.ensure_algorithm(
                        RouterAlgorithmSpec(
                            id=algorithm_id,
                            name=str(detail.get('name') or algorithm_id),
                            code_path=str(detail.get('code_path') or ''),
                            instance_count=int(claim['instance_count']),
                            config=dict(detail.get('config') or {}),
                        ),
                        timeout_s=timeout_s,
                    )
                restart_complete = True
                health = manager.healthcheck(algorithm_id)
            except RouterManagerError as exc:
                state = 'orphaned' if restart_complete or exc.kind in {
                    'algorithm_reactivation_failed',
                    'algorithm_restart_failed',
                } else previous_state
                ledger.resolve_manage(algorithm_id, claimed_at, state)
                raise
            next_state = 'active'
        else:
            try:
                manager.stop_algorithm(algorithm_id)
            except RouterManagerError as exc:
                state = 'orphaned' if exc.kind == 'algorithm_stop_failed' else previous_state
                ledger.resolve_manage(algorithm_id, claimed_at, state)
                raise
            health = {'status': 'stopped', 'healthy_instances': 0, 'instances': []}
            next_state = 'stopped'
        ledger.resolve_manage(algorithm_id, claimed_at, next_state)
        ledger.record_router_status(algorithm_id, health)
        return health
