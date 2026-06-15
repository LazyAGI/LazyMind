from __future__ import annotations

import asyncio
import logging

from contextlib import asynccontextmanager
from fastapi import FastAPI

from lazymind.config import config
import lazymind.router.config  # noqa: F401 — registers router config keys

logger = logging.getLogger(__name__)


def create_app() -> FastAPI:
    if not config['enable_router']:
        # Fallback mode: behave exactly like the original chat service
        from lazymind.chat.app import create_app as create_chat_app
        return create_chat_app()

    # Router mode
    return _create_router_app()


def _create_router_app() -> FastAPI:
    from lazymind.router.db.client import get_engine
    from lazymind.router.db.models import Base
    from lazymind.router.core.process_manager import get_process_manager
    from lazymind.router.core.registry import get_global_registry
    from lazymind.router.core.health_checker import HealthChecker

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        await _startup(get_engine, Base, get_process_manager, get_global_registry, HealthChecker)
        yield
        await _shutdown(get_process_manager)

    app = FastAPI(
        title='LazyMind API',
        description='Knowledge-base-backed conversational and routing API service',
        version='1.0.0',
        lifespan=lifespan,
    )

    # Mount all chat-side routers (health, rewrite, skill_review, model_features,
    # model_check) so they stay available in router mode.
    from lazymind.chat.app import register_chat_routers
    register_chat_routers(app)

    from lazymind.router.api import (
        proxy_routes,
        algorithm_routes,
        strategy_routes,
        diagnostics_routes,
    )
    app.include_router(proxy_routes.router)
    app.include_router(algorithm_routes.router)
    app.include_router(strategy_routes.router)
    app.include_router(diagnostics_routes.router)

    return app


async def _startup(get_engine, Base, get_process_manager, get_global_registry, HealthChecker):
    from sqlalchemy.ext.asyncio import AsyncEngine

    engine: AsyncEngine = get_engine()
    async with engine.begin() as conn:
        await conn.run_sync(Base.metadata.create_all)
    logger.info('router_* tables ensured')

    pm = get_process_manager()
    await pm.claim_port_range()
    await pm.ensure_default_algorithm()

    registry = get_global_registry()
    await registry.refresh()

    # Start default algorithm instances
    ports = await pm.start_algorithm(
        algo_id='default',
        code_path=config['router_default_algo_path'],
        count=config['router_default_instance_count'],
    )
    logger.info('Started default algorithm instances on ports: %s', ports)
    await pm.wait_all_healthy(ports)

    # Do an initial registry refresh after instances come up
    await registry.refresh()

    # Start background tasks (health checker, heartbeat, registry refresh, dead-instance cleanup)
    hc = HealthChecker(pm, registry)
    hc_task = asyncio.create_task(hc.run_forever(), name='health-checker')

    def _on_hc_done(t: asyncio.Task) -> None:
        if not t.cancelled() and t.exception():
            logger.critical('health-checker task exited with error: %s', t.exception())

    hc_task.add_done_callback(_on_hc_done)
    logger.info('Router startup complete — instance_id=%s', pm.instance_id)


async def _shutdown(get_process_manager):
    pm = get_process_manager()
    await pm.shutdown()
    from lazymind.router.core.stream_proxy import get_stream_proxy
    try:
        await get_stream_proxy().close()
    except Exception as exc:
        logger.warning('Failed to close stream proxy: %s', exc)
    logger.info('Router shutdown complete')


app = create_app()


if __name__ == '__main__':
    import argparse
    import uvicorn

    parser = argparse.ArgumentParser()
    parser.add_argument('--host', type=str, default='0.0.0.0', help='listen host')
    parser.add_argument('--port', type=int, default=8046, help='listen port')
    args = parser.parse_args()

    uvicorn.run(app, host=args.host, port=args.port)
