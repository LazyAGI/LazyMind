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
        if action == 'restart':
            if previous_state != 'active':
                ledger.resolve_manage(algorithm_id, claimed_at, previous_state)
                raise RouterLedgerError(f'algorithm is not expected active: {algorithm_id}')
            if claim['instance_count'] is None:
                ledger.resolve_manage(algorithm_id, claimed_at, previous_state)
                raise RouterLedgerError(f'algorithm instance count is not registered: {algorithm_id}')
            restart_complete = False
            try:
                manager.restart_algorithm(
                    algorithm_id,
                    timeout_s=timeout_s,
                    instance_count=int(claim['instance_count']),
                )
                restart_complete = True
                health = manager.healthcheck(algorithm_id)
            except RouterManagerError as exc:
                state = 'orphaned' if restart_complete or exc.kind == 'algorithm_restart_failed' else previous_state
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
