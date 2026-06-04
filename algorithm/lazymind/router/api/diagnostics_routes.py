from __future__ import annotations

import logging

from fastapi import APIRouter, HTTPException
from sqlalchemy import delete, select

from lazymind.router.core.process_manager import get_process_manager
from lazymind.router.core.registry import get_global_registry
from lazymind.router.db.client import AsyncSessionLocal
from lazymind.router.db.models import (
    RouterAbStrategy,
    RouterChildProcess,
    RouterSessionAssignment,
)

logger = logging.getLogger(__name__)

router = APIRouter(prefix='/inner', tags=['diagnostics'])


@router.get('/status', summary='Full status of this router instance')
async def get_status():
    pm = get_process_manager()
    registry = get_global_registry()

    # Local child processes
    async with AsyncSessionLocal() as session:
        rows = await session.execute(
            select(RouterChildProcess).where(
                RouterChildProcess.instance_id == pm.instance_id
            )
        )
        local_children = rows.scalars().all()

    # Active AB strategy
    async with AsyncSessionLocal() as session:
        row = await session.execute(
            select(RouterAbStrategy)
            .where(RouterAbStrategy.is_active.is_(True))
            .order_by(RouterAbStrategy.id.desc())
            .limit(1)
        )
        strategy = row.scalar_one_or_none()

    # Global snapshot summary
    global_snapshot = registry.snapshot()
    global_summary = {
        algo_id: {
            'total': len(instances),
            'healthy': sum(1 for i in instances if i.status == 'healthy'),
        }
        for algo_id, instances in global_snapshot.items()
    }

    return {
        'instance_id': pm.instance_id,
        'host': pm.host,
        'port_range': list(pm.port_range),
        'local_child_processes': [
            {
                'algorithm_id': c.algorithm_id,
                'port': c.port,
                'pid': c.pid,
                'status': c.status,
                'failures': c.failures,
                'last_health_at': c.last_health_at.isoformat() if c.last_health_at else None,
            }
            for c in local_children
        ],
        'global_algorithms': global_summary,
        'ab_strategy': {
            'id': strategy.id,
            'weights': strategy.weights,
            'is_active': strategy.is_active,
        } if strategy else None,
    }


@router.get('/session/{session_id}', summary='Get the algorithm binding for a session')
async def get_session_binding(session_id: str):
    async with AsyncSessionLocal() as session:
        row = await session.get(RouterSessionAssignment, session_id)
    if row is None:
        raise HTTPException(status_code=404, detail=f'No binding for session {session_id!r}')
    return {
        'session_id': row.session_id,
        'algorithm_id': row.algorithm_id,
        'assigned_at': row.assigned_at.isoformat() if row.assigned_at else None,
        'last_seen_at': row.last_seen_at.isoformat() if row.last_seen_at else None,
    }


@router.delete('/session/{session_id}', summary='Clear a session binding (re-routes via AB on next request)')
async def delete_session_binding(session_id: str):
    async with AsyncSessionLocal() as session:
        row = await session.get(RouterSessionAssignment, session_id)
        if row is None:
            raise HTTPException(status_code=404, detail=f'No binding for session {session_id!r}')
        await session.execute(
            delete(RouterSessionAssignment).where(
                RouterSessionAssignment.session_id == session_id
            )
        )
        await session.commit()
    return {'status': 'deleted', 'session_id': session_id}
