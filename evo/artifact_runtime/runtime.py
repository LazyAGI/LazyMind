from __future__ import annotations

import time
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path

from .controller import AttemptExecutor, RunController
from .durability import ArtifactValueCodec, ControlPlaneCodec, SQLiteRuntimeStores, open_sqlite_runtime
from .external import ExternalCallGateway, ExternalCallPolicy
from .graph import DAGGraph
from .intent import (
    GraphPlanSubmitter,
    IntentCommandGateway,
    IntentCommandPolicy,
    IntentCommandRequest,
    IntentCommandResult,
)
from .intervention import FlowInterventionCoordinator
from .lease import ClaimLeaseReaper, LeasedPlanExecutionWorker, WorkerLeasePolicy
from .mutation import ArtifactMutationService
from .projection import InMemoryRunProjectionStore, ProjectionSynchronizer
from .reconciliation import ReconciliationScheduler
from .runtime_driver import DEFAULT_CHECKPOINT_ID, DurableRuntimeDriver, RuntimeDriverPolicy, RuntimeDriverResult
from .store import ArtifactCommitCoordinator, ArtifactStoreVersionResolver
from .supervisor import RuntimeSupervisor, RuntimeSupervisorPolicy
from .utils import validate_nonempty
from .worker import MaterializerExecutor


@dataclass(frozen=True)
class EvoRuntimeConfig:
    path: str | Path
    driver_id: str = 'evo-runtime-driver'
    worker_id: str = 'evo-runtime-worker'
    checkpoint_id: str = DEFAULT_CHECKPOINT_ID
    llm_config: dict[str, object] = field(default_factory=dict)
    worker_lease_policy: WorkerLeasePolicy = field(default_factory=lambda: WorkerLeasePolicy(300.0, 100))
    driver_policy: RuntimeDriverPolicy = field(default_factory=RuntimeDriverPolicy)
    supervisor_policy: RuntimeSupervisorPolicy = field(default_factory=RuntimeSupervisorPolicy)
    intent_command_policy: IntentCommandPolicy = field(default_factory=IntentCommandPolicy)

    def __post_init__(self) -> None:
        validate_nonempty(str(self.path), 'path')
        validate_nonempty(self.driver_id, 'driver_id')
        validate_nonempty(self.worker_id, 'worker_id')
        validate_nonempty(self.checkpoint_id, 'checkpoint_id')


class BootstrappedIntentGateway:
    def __init__(self, runtime: EvoRuntime) -> None:
        self._runtime = runtime

    def execute(self, request: IntentCommandRequest) -> IntentCommandResult:
        return self._runtime.execute_intent(request)


class EvoRuntime:
    def __init__(
        self,
        *,
        config: EvoRuntimeConfig,
        stores: SQLiteRuntimeStores,
        graph: DAGGraph,
        controller: RunController,
        intervention: FlowInterventionCoordinator,
        projection_store: InMemoryRunProjectionStore,
        supervisor: RuntimeSupervisor,
        driver: DurableRuntimeDriver,
        intent_gateway: IntentCommandGateway,
        worker: LeasedPlanExecutionWorker,
        reaper: ClaimLeaseReaper,
        external_gateway: ExternalCallGateway,
        executor: AttemptExecutor,
        owns_stores: bool,
    ) -> None:
        self.config = config
        self.stores = stores
        self.graph = graph
        self.controller = controller
        self.intervention = intervention
        self.projection_store = projection_store
        self.supervisor = supervisor
        self.driver = driver
        self.intent_gateway = intent_gateway
        self.gateway = BootstrappedIntentGateway(self)
        self.worker = worker
        self.reaper = reaper
        self.external_gateway = external_gateway
        self.executor = executor
        self._owns_stores = owns_stores
        self._projection_cursor = 0
        self._projection_bootstrapped = False
        self._projection_sync = ProjectionSynchronizer(controller, projection_store)
        self.set_llm_config(config.llm_config)
        self.validate()

    @property
    def projection_cursor(self) -> int:
        return self._projection_cursor

    def validate(self) -> None:
        self.graph.validate()
        if self.executor is None:
            raise ValueError('executor must be configured')
        if self.external_gateway is None:
            raise ValueError('external gateway must be configured')
        if not isinstance(self.gateway, BootstrappedIntentGateway) or self.gateway._runtime is not self:
            raise ValueError('gateway must be the runtime bootstrapped facade')
        if self.intent_gateway is self.gateway:
            raise ValueError('raw intent gateway must not be the bootstrapped facade')
        if self.reaper.controller is not self.controller:
            raise ValueError('reaper must share runtime controller')
        if self.worker.controller is not self.controller:
            raise ValueError('worker must share runtime controller')
        if self.supervisor.controller is not self.controller:
            raise ValueError('supervisor must share runtime controller')
        if self.supervisor.worker is not self.worker:
            raise ValueError('supervisor must use runtime worker')
        if self.supervisor.reaper is not self.reaper:
            raise ValueError('supervisor must use runtime reaper')
        if self.driver.supervisor is not self.supervisor:
            raise ValueError('driver must use runtime supervisor')
        if self.intent_gateway.controller is not self.controller:
            raise ValueError('intent gateway must use runtime controller')
        if self.intent_gateway.intervention is not self.intervention:
            raise ValueError('intent gateway must use runtime intervention')
        if self.intent_gateway.driver is not self.driver:
            raise ValueError('intent gateway must use runtime driver')
        if self.intent_gateway.log is not self.stores.intent_command_log:
            raise ValueError('intent gateway must use durable intent command log')
        if self.intent_gateway.plan_submitter is None:
            raise ValueError('intent gateway must use runtime plan submitter')

    def bootstrap_projection(self) -> int:
        """Fully catch up the in-memory projection read model to the event log."""
        cursor = 0
        while True:
            previous = cursor
            next_cursor = self._projection_sync.sync_since(
                cursor,
                limit=self.supervisor.policy.max_projection_events_per_sync,
            )
            max_seq = self.controller.event_log.max_seq()
            cursor = next_cursor
            if next_cursor == previous and max_seq <= cursor:
                self._projection_cursor = cursor
                self._projection_bootstrapped = True
                return cursor
            if next_cursor == previous:
                continue

    def sync_projection(self) -> int:
        """Convenience read-model sync; production driver entrances use checkpoint coverage."""
        if not self._projection_bootstrapped:
            return self.bootstrap_projection()
        return self._sync_projection_from_current()

    def _sync_projection_from_current(self) -> int:
        while True:
            previous = self._projection_cursor
            next_cursor = self._projection_sync.sync_since(
                self._projection_cursor,
                limit=self.supervisor.policy.max_projection_events_per_sync,
            )
            max_seq = self.controller.event_log.max_seq()
            if next_cursor > previous:
                self._projection_cursor = next_cursor
                continue
            if max_seq <= self._projection_cursor:
                return self._projection_cursor

    def warm_up(self) -> int:
        return self.bootstrap_projection()

    def set_llm_config(self, llm_config: dict[str, object] | None) -> None:
        object.__setattr__(self.config, 'llm_config', dict(llm_config or {}))
        if isinstance(self.executor, MaterializerExecutor):
            self.executor.set_llm_config(self.config.llm_config)

    def ensure_projection_bootstrapped(self) -> int:
        if not self._projection_bootstrapped:
            return self.bootstrap_projection()
        return self._sync_projection_from_current()

    def ensure_projection_covers_driver_checkpoint(self) -> int:
        """Ensure the read model covers the durable driver cursor without chasing newer events."""
        checkpoint = self.stores.runtime_driver_checkpoints.load(self.config.checkpoint_id)
        if self._projection_cursor >= checkpoint.cursor:
            self._projection_bootstrapped = True
            return self._projection_cursor
        cursor = self._projection_cursor
        while cursor < checkpoint.cursor:
            previous = cursor
            limit = min(
                self.supervisor.policy.max_projection_events_per_sync,
                max(1, checkpoint.cursor - cursor),
            )
            next_cursor = self._projection_sync.sync_since(
                cursor,
                limit=limit,
            )
            cursor = next_cursor
            if next_cursor == previous:
                break
        self._projection_cursor = cursor
        self._projection_bootstrapped = self._projection_cursor >= checkpoint.cursor
        return self._projection_cursor

    def run_until_idle(
        self,
        *,
        run_ids: tuple[str, ...] = (),
        stop_requested: Callable[[], bool] | None = None,
        sleep_fn: Callable[[float], None] | None = time.sleep,
    ) -> RuntimeDriverResult:
        self.ensure_projection_covers_driver_checkpoint()
        result = self.driver.run_until_idle(run_ids=run_ids, stop_requested=stop_requested, sleep_fn=sleep_fn)
        self._absorb_projection_cursor(result.cursor)
        return result

    def execute_intent(self, request: IntentCommandRequest) -> IntentCommandResult:
        self.ensure_projection_covers_driver_checkpoint()
        result = self.intent_gateway.execute(request)
        if result.advance_result is not None:
            self._absorb_projection_cursor(result.advance_result.cursor)
        return result

    def _absorb_projection_cursor(self, cursor: int) -> None:
        if cursor > self._projection_cursor:
            self._projection_cursor = cursor
            self._projection_bootstrapped = True

    def close(self) -> None:
        if self._owns_stores:
            self.stores.close()


def open_evo_runtime(
    path: str | Path | None = None,
    *,
    graph: DAGGraph,
    config: EvoRuntimeConfig | None = None,
    stores: SQLiteRuntimeStores | None = None,
    value_codec: ArtifactValueCodec | None = None,
    control_codec: ControlPlaneCodec | None = None,
    external_gateway: ExternalCallGateway | None = None,
    external_policy: ExternalCallPolicy | None = None,
    executor: AttemptExecutor | None = None,
    clock: Callable[[], float] = time.time,
) -> EvoRuntime:
    if graph is None:
        raise ValueError('graph is required')
    runtime_config = config or EvoRuntimeConfig(_resolve_path(path, stores))
    owns_stores = stores is None
    runtime_stores = stores or open_sqlite_runtime(
        runtime_config.path, value_codec=value_codec, control_codec=control_codec)
    committer = ArtifactCommitCoordinator(runtime_stores.artifact_store)
    controller = RunController(event_log=runtime_stores.event_log, committer=committer)
    resolver = ArtifactStoreVersionResolver(runtime_stores.artifact_store)
    mutation_service = ArtifactMutationService(runtime_stores.artifact_store, log=runtime_stores.mutation_log)
    scheduler = ReconciliationScheduler(graph, resolver, controller)
    intervention = FlowInterventionCoordinator(
        controller=controller,
        mutation_service=mutation_service,
        scheduler=scheduler,
        log=runtime_stores.intervention_log,
    )
    actual_external_gateway = external_gateway or ExternalCallGateway(
        runtime_stores.external_call_ledger,
        policy=external_policy,
        clock=clock,
    )
    if executor is None:
        actual_executor = MaterializerExecutor(
            graph,
            runtime_stores.artifact_store,
            external_gateway=actual_external_gateway,
            llm_config=runtime_config.llm_config,
        )
        actual_executor.bind_controller(controller)
    else:
        actual_executor = executor
    worker = LeasedPlanExecutionWorker(
        controller,
        actual_executor,
        worker_id=runtime_config.worker_id,
        policy=runtime_config.worker_lease_policy,
        clock=clock,
    )
    reaper = ClaimLeaseReaper(controller, policy=runtime_config.worker_lease_policy, clock=clock)
    projection_store = InMemoryRunProjectionStore()
    supervisor = RuntimeSupervisor(
        controller,
        projection_store,
        reaper,
        worker,
        policy=runtime_config.supervisor_policy,
    )
    driver = DurableRuntimeDriver(
        supervisor,
        runtime_stores.runtime_driver_checkpoints,
        driver_id=runtime_config.driver_id,
        checkpoint_id=runtime_config.checkpoint_id,
        policy=runtime_config.driver_policy,
        clock=clock,
    )
    intent_gateway = IntentCommandGateway(
        controller=controller,
        intervention=intervention,
        driver=driver,
        log=runtime_stores.intent_command_log,
        policy=runtime_config.intent_command_policy,
        plan_submitter=GraphPlanSubmitter(graph, resolver, controller),
        clock=clock,
    )
    return EvoRuntime(
        config=runtime_config,
        stores=runtime_stores,
        graph=graph,
        controller=controller,
        intervention=intervention,
        projection_store=projection_store,
        supervisor=supervisor,
        driver=driver,
        intent_gateway=intent_gateway,
        worker=worker,
        reaper=reaper,
        external_gateway=actual_external_gateway,
        executor=actual_executor,
        owns_stores=owns_stores,
    )


def _resolve_path(path: str | Path | None, stores: SQLiteRuntimeStores | None) -> str | Path:
    if path is not None:
        return path
    if stores is not None:
        return stores.event_log.path
    raise ValueError('path is required when stores are not provided')
