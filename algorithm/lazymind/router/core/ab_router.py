from __future__ import annotations

import logging
import random
from typing import Optional

from sqlalchemy import select

from lazymind.router.db.client import AsyncSessionLocal
from lazymind.router.db.models import (
    RouterAbStrategy,
    RouterAlgorithm,
)

logger = logging.getLogger(__name__)


class ABRouter:
    """Decides which algorithm version handles a given request.

    Priority:
    1. Caller explicitly passes `algorithm_id` → use it directly.
    2. Active strategy exists → weighted random selection.
    3. Fallback → use the 'default' algorithm.

    Session stickiness is intentionally NOT handled here. Callers that need
    multi-turn consistency should persist the returned `X-Algorithm-Id` response
    header and pass it back as `algorithm_id` in subsequent requests.
    """

    async def select_algorithm(
        self,
        caller_algorithm_id: Optional[str] = None,
    ) -> str:
        # Priority 1: explicit override from caller
        if caller_algorithm_id:
            return caller_algorithm_id

        # Priority 2: active strategy weighted random
        algo_id = await self._weighted_random_from_active_strategy()
        if algo_id:
            return algo_id

        # Priority 3: fallback to default
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

        # Filter to only active algorithms
        async with AsyncSessionLocal() as session:
            active_ids = set(
                (
                    await session.execute(
                        select(RouterAlgorithm.id).where(
                            RouterAlgorithm.status == 'active',
                            RouterAlgorithm.id.in_(list(weights.keys())),
                        )
                    )
                ).scalars()
            )

        valid_weights = {k: v for k, v in weights.items() if k in active_ids}
        if not valid_weights:
            return None

        # Further filter to only algorithms with at least one healthy instance
        from lazymind.router.core.registry import get_global_registry
        registry = get_global_registry()
        valid_weights = {
            k: v for k, v in valid_weights.items()
            if registry.get_healthy_instance(k) is not None
        }
        if not valid_weights:
            return None

        population = list(valid_weights.keys())
        w = [valid_weights[k] for k in population]
        return random.choices(population, weights=w, k=1)[0]


# Module-level singleton
_ab_router: Optional[ABRouter] = None


def get_ab_router() -> ABRouter:
    global _ab_router
    if _ab_router is None:
        _ab_router = ABRouter()
    return _ab_router
