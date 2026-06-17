from __future__ import annotations

import time
from collections.abc import Callable
from dataclasses import dataclass

from .controller import AttemptClaim, AttemptExecutor, ClaimHeartbeatResult, LeaseRecoveryResult, RunController
from .utils import validate_nonempty
from .worker import WorkerDispatchResult, dispatch_claims


@dataclass(frozen=True)
class WorkerLeasePolicy:
    lease_seconds: float
    max_recoveries: int

    def __post_init__(self) -> None:
        if self.lease_seconds <= 0:
            raise ValueError('lease_seconds must be > 0')
        if self.max_recoveries < 0:
            raise ValueError('max_recoveries must be >= 0')


class LeasedPlanExecutionWorker:
    def __init__(
        self,
        controller: RunController,
        executor: AttemptExecutor,
        *,
        worker_id: str,
        policy: WorkerLeasePolicy,
        clock: Callable[[], float] = time.time,
    ) -> None:
        validate_nonempty(worker_id, 'worker_id')
        self.controller = controller
        self.executor = executor
        self.worker_id = worker_id
        self.policy = policy
        self.clock = clock

    def dispatch_once(self, run_id: str, *, limit: int = 1) -> WorkerDispatchResult:
        if limit < 1:
            raise ValueError('limit must be >= 1')
        claims = self.controller.claim_ready(
            run_id,
            limit=limit,
            worker_id=self.worker_id,
            lease_expires_at=self.clock() + self.policy.lease_seconds,
        )
        return WorkerDispatchResult(
            run_id,
            len(claims),
            dispatch_claims(self.controller, self.executor, claims),
            'dispatched' if claims else 'idle',
            _run_status(self.controller, run_id),
        )

    def heartbeat(self, claim: AttemptClaim) -> ClaimHeartbeatResult:
        return self.controller.heartbeat_claim(
            claim,
            lease_expires_at=self.clock() + self.policy.lease_seconds,
        )


class ClaimLeaseReaper:
    def __init__(
        self,
        controller: RunController,
        *,
        policy: WorkerLeasePolicy,
        clock: Callable[[], float] = time.time,
    ) -> None:
        self.controller = controller
        self.policy = policy
        self.clock = clock

    def recover_run(self, run_id: str, *, now: float | None = None, command_id: str) -> LeaseRecoveryResult:
        return self.controller.recover_expired_claims(
            run_id,
            now=self.clock() if now is None else now,
            max_recoveries=self.policy.max_recoveries,
            command_id=command_id,
        )


def _run_status(controller: RunController, run_id: str):
    state = controller.state(run_id)
    return state.run.status if state.run_exists else None
