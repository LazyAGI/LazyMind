from __future__ import annotations
from typing import Any
from fastapi import FastAPI
from evo.runtime.config import EvoConfig
from evo.service.core.manager import JobManager
from evo.service.api import health


def create_app(
    config: EvoConfig | None = None, *, job_manager: JobManager | None = None, thread_hub: 'Any | None' = None
) -> FastAPI:
    from evo.runtime.config import load_config
    from evo.service.api.errors import register_handlers

    cfg = config or load_config()
    jm = (
        job_manager
        if job_manager is not None
        else __import__('evo.service.core.manager', fromlist=['build_manager']).build_manager(cfg)
    )
    app = FastAPI(title='evo service', version='poc-2')
    app.state.cfg = cfg
    app.state.jm = jm
    register_handlers(app)
    app.include_router(health.build_health_router())
    from evo.service.api.opencode import build_opencode_router

    app.include_router(build_opencode_router(cfg))
    from evo.service.threads import ThreadHub, mount as mount_hub
    from evo.service.core.intent_store import IntentStore
    from evo.service.core.ops_executor import OpsExecutor
    from evo.orchestrator.planner import Planner
    from evo.runtime.model_gateway import ModelGateway
    from lazyllm import AutoModel
    from evo.orchestrator.llm import make_evo_stream_llm

    client = AutoModel(model=cfg.model_config.llm_role)
    gateway: ModelGateway[str] = ModelGateway(cfg.llm, name='evo-planner-llm')
    planner = Planner(
        llm=lambda prompt: gateway.call(lambda: client(prompt), cache_key=prompt, agent='planner'),
        stream_llm=make_evo_stream_llm(cfg),
    )
    intent_store = IntentStore(cfg.storage.base_dir / 'state' / 'intents')
    ops = OpsExecutor(jm)
    hub = ThreadHub(jm=jm, planner=planner, intent_store=intent_store, ops=ops)
    mount_hub(app, hub)
    from evo.service.threads.results import build_results_router

    app.include_router(build_results_router(base_dir=cfg.storage.base_dir, store=jm.store))
    return app


def get_app() -> FastAPI:
    return create_app()
