from __future__ import annotations

import logging
from typing import Dict

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field, model_validator
from sqlalchemy import func, select

from lazymind.router.db.client import AsyncSessionLocal
from lazymind.router.db.models import (
    RouterAbStrategy,
    RouterAlgorithm,
    RouterSessionAssignment,
)

logger = logging.getLogger(__name__)

router = APIRouter(prefix='/inner/ab', tags=['ab-strategy'])


class UpdateStrategyRequest(BaseModel):
    weights: Dict[str, int] = Field(
        description='Map of algorithm_id -> weight (integers, sum must equal 100)'
    )

    @model_validator(mode='after')
    def check_weights_sum(self) -> 'UpdateStrategyRequest':
        total = sum(self.weights.values())
        if total != 100:
            raise ValueError(f'Weights must sum to 100, got {total}')
        return self


@router.put('/strategy', summary='Update the active AB split strategy')
async def update_strategy(req: UpdateStrategyRequest):
    # Validate all referenced algorithms exist and are active
    async with AsyncSessionLocal() as session:
        rows = await session.execute(
            select(RouterAlgorithm.id).where(
                RouterAlgorithm.id.in_(list(req.weights.keys())),
                RouterAlgorithm.status == 'active',
            )
        )
        active_ids = {r.id for r in rows}

    missing = set(req.weights.keys()) - active_ids
    if missing:
        raise HTTPException(
            status_code=422,
            detail=f'Algorithm IDs not found or not active: {sorted(missing)}',
        )

    async with AsyncSessionLocal() as session:
        # Deactivate all existing active strategies
        await session.execute(
            RouterAbStrategy.__table__.update()
            .where(RouterAbStrategy.is_active.is_(True))
            .values(is_active=False)
        )
        new_strategy = RouterAbStrategy(weights=dict(req.weights), is_active=True)
        session.add(new_strategy)
        await session.commit()
        await session.refresh(new_strategy)

    return {'id': new_strategy.id, 'weights': new_strategy.weights, 'is_active': True}


@router.get('/strategy', summary='Get the current active AB strategy with session counts')
async def get_strategy():
    async with AsyncSessionLocal() as session:
        row = await session.execute(
            select(RouterAbStrategy)
            .where(RouterAbStrategy.is_active.is_(True))
            .order_by(RouterAbStrategy.id.desc())
            .limit(1)
        )
        strategy = row.scalar_one_or_none()

        if strategy is None:
            return {'strategy': None}

        # Count sessions per algorithm
        counts_rows = await session.execute(
            select(
                RouterSessionAssignment.algorithm_id,
                func.count(RouterSessionAssignment.session_id).label('count'),
            ).group_by(RouterSessionAssignment.algorithm_id)
        )
        session_counts = {r.algorithm_id: r.count for r in counts_rows}

    weights_with_counts = {
        algo_id: {'weight': w, 'session_count': session_counts.get(algo_id, 0)}
        for algo_id, w in (strategy.weights or {}).items()
    }

    return {
        'strategy': {
            'id': strategy.id,
            'weights': weights_with_counts,
            'is_active': strategy.is_active,
            'created_at': strategy.created_at.isoformat() if strategy.created_at else None,
        }
    }


@router.delete('/strategy', summary='Clear the active AB strategy (traffic falls back to default)')
async def delete_strategy():
    async with AsyncSessionLocal() as session:
        await session.execute(
            RouterAbStrategy.__table__.update()
            .where(RouterAbStrategy.is_active.is_(True))
            .values(is_active=False)
        )
        await session.commit()
    return {'status': 'cleared'}
