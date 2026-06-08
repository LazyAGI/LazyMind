from __future__ import annotations

import logging

from fastapi import APIRouter
from sqlalchemy import select

from lazymind.router.core.process_manager import get_process_manager
from lazymind.router.core.registry import get_global_registry
from lazymind.router.db.client import AsyncSessionLocal
from lazymind.router.db.models import (
    RouterAbStrategy,
    RouterChildProcess,
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
