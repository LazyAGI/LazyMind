from __future__ import annotations

import logging
import os

from fastapi import FastAPI

from lazymind.config import config
from lazymind.chat.api import (
    channel_intent_routes,
    agent_control_routes,
    chat_routes,
    health_routes,
    knowledge_search_routes,
    llm_task_routes,
    model_check_routes,
    model_features_routes,
    workflow_routes,
    subagent_routes,
)
from lazymind.chat.service.utils.trace_archive import start_local_trace_maintenance
from lazymind.chat.runtime_loader import start_background_chat_runtime_warmup
from lazymind.chat.workflow.remote_executor import start_remote_workflow_executor
from lazymind.rewrite.api import rewrite_routes
from lazymind.review.api import memory_review_routes, skill_organize_routes, skill_review_routes


# Internal workflow polling, heartbeats, and SubAgent event delivery are
# intentionally frequent.  Keep transport-level success/empty-queue messages
# out of the Chat service log while preserving warnings and failures.
logging.getLogger('httpx').setLevel(logging.WARNING)


def register_chat_routers(app: FastAPI) -> FastAPI:
    # health is always available for liveness probes.
    app.include_router(health_routes.router)
    # Agent control callbacks must remain available in both direct and router modes.
    app.include_router(agent_control_routes.router)
    # Writer sync and LazyMind task cancellation callbacks.
    app.include_router(workflow_routes.router)

    if not config['enable_router']:
        app.include_router(chat_routes.router)
        app.include_router(knowledge_search_routes.router)
        app.include_router(subagent_routes.router)

    if not config['router_child_proxied_only']:
        app.include_router(channel_intent_routes.router)
        app.include_router(rewrite_routes.router)
        app.include_router(memory_review_routes.router)
        app.include_router(skill_organize_routes.router)
        app.include_router(skill_review_routes.router)
        app.include_router(model_features_routes.router)
        app.include_router(model_check_routes.router)
        app.include_router(llm_task_routes.router)
    return app


def create_app() -> FastAPI:
    app = FastAPI(
        title='LazyMind API',
        description='Knowledge-base-backed conversational and routing API service',
        version='1.0.0',
    )
    return register_chat_routers(app)


app = create_app()
if os.getenv('LAZYMIND_RUNTIME_MODE', '').strip().lower() == 'local':
    start_background_chat_runtime_warmup()
if config['background_jobs_enabled']:
    start_local_trace_maintenance()
if not config['router_child_proxied_only']:
    # Router children only serve proxied model traffic. The parent Chat/Router
    # process owns the single remote Workflow Executor poller for this Host.
    start_remote_workflow_executor()

if __name__ == '__main__':
    import argparse
    import uvicorn

    parser = argparse.ArgumentParser()
    parser.add_argument('--host', type=str, default='0.0.0.0', help='listen host')
    parser.add_argument('--port', type=int, default=8046, help='listen port')
    args = parser.parse_args()

    uvicorn.run(app, host=args.host, port=args.port)
