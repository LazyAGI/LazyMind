from __future__ import annotations

import logging
import random
from datetime import datetime, timezone
from typing import Optional

from sqlalchemy import insert, select

from lazymind.router.db.client import AsyncSessionLocal
from lazymind.router.db.models import (
    RouterAbStrategy,
    RouterAlgorithm,
    RouterSessionAssignment,
)

logger = logging.getLogger(__name__)


class ABRouter:
    """Decides which algorithm version handles a given request.

    Priority:
    1. Caller explicitly passes `algorithm_id` → use it directly.
    2. Session already has a binding in `router_session_assignments` → reuse.
    3. Active strategy exists → weighted random selection → persist binding.
    4. Fallback → use the 'default' algorithm.
    """

    async def select_algorithm(
        self,
        session_id: str,
        caller_algorithm_id: Optional[str] = None,
    ) -> str:
        # Priority 1: explicit override
        if caller_algorithm_id:
            return caller_algorithm_id

        # Priority 2: existing session binding
        async with AsyncSessionLocal() as session:
            row = await session.get(RouterSessionAssignment, session_id)
            if row is not None:
                # Touch last_seen_at
                await session.execute(
                    RouterSessionAssignment.__table__.update()
                    .where(RouterSessionAssignment.session_id == session_id)
                    .values(last_seen_at=datetime.now(timezone.utc))
                )
                await session.commit()
                return row.algorithm_id

        # Priority 3: active strategy weighted random
        algo_id = await self._weighted_random_from_active_strategy()

        # Persist the assignment
        if algo_id:
            async with AsyncSessionLocal() as session:
                now = datetime.now(timezone.utc)
                stmt = insert(RouterSessionAssignment).values(
                    session_id=session_id,
                    algorithm_id=algo_id,
                    assigned_at=now,
                    last_seen_at=now,
                )
                stmt = stmt.on_conflict_do_update(
                    index_elements=['session_id'],
                    set_={'last_seen_at': now},
                )
                await session.execute(stmt)
                await session.commit()
            return algo_id

        # Priority 4: fallback to default
        return 'default'

    async def _weighted_random_from_active_strategy(self) -> Optional[str]:
        async with AsyncSessionLocal() as session:
            row = await session.execute(
                select(RouterAbStrategy)
                .where(RouterAbStrategy.is_active.is_(True))
                .order_by(RouterAbStrategy.id.desc())
                .limit(1)
            )
            strategy = row.scalar_one_or_none()

        if strategy is None:
            return None

        weights: dict[str, int] = strategy.weights or {}
        if not weights:
            return None

        # Validate all referenced algorithms are active
        async with AsyncSessionLocal() as session:
            active_ids = {
                r.id
                for r in (
                    await session.execute(
                        select(RouterAlgorithm.id).where(
                            RouterAlgorithm.status == 'active',
                            RouterAlgorithm.id.in_(list(weights.keys())),
                        )
                    )
                ).scalars()
            }

        valid_weights = {k: v for k, v in weights.items() if k in active_ids}
        if not valid_weights:
            return None

        return await self._weighted_random(valid_weights)

    @staticmethod
    async def _weighted_random(weights: dict[str, int]) -> str:
        population = list(weights.keys())
        w = [weights[k] for k in population]
        return random.choices(population, weights=w, k=1)[0]


# Module-level singleton
_ab_router: Optional[ABRouter] = None


def get_ab_router() -> ABRouter:
    global _ab_router
    if _ab_router is None:
        _ab_router = ABRouter()
    return _ab_router
