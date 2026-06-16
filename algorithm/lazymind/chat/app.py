from __future__ import annotations

from fastapi import FastAPI

from lazymind.config import config
from lazymind.chat.api import (
    chat_routes,
    health_routes,
    model_check_routes,
    model_features_routes,
    subagent_routes,
)
from lazymind.rewrite.api import rewrite_routes
from lazymind.review.api import memory_review_routes, skill_review_routes


def register_chat_routers(app: FastAPI) -> FastAPI:
    # health is always available for liveness probes.
    app.include_router(health_routes.router)

    # chat / subagent are the request types that the router dispatches to child
    # processes. In router mode the main process exposes proxy versions instead,
    # so it must not mount them directly; child processes (enable_router=false)
    # mount them to do the real work.
    if not config['enable_router']:
        app.include_router(chat_routes.router)
        app.include_router(subagent_routes.router)

    # Router-only child processes act purely as chat/subagent executors behind
    # the proxy. The stateless shared endpoints below are served by the main
    # process directly, so they are skipped on those children.
    if not config['router_child_proxied_only']:
        app.include_router(rewrite_routes.router)
        app.include_router(memory_review_routes.router)
        app.include_router(skill_review_routes.router)
        app.include_router(model_features_routes.router)
        app.include_router(model_check_routes.router)
    return app


def create_app() -> FastAPI:
    app = FastAPI(
        title='LazyMind API',
        description='Knowledge-base-backed conversational and routing API service',
        version='1.0.0',
    )
    return register_chat_routers(app)


app = create_app()

if __name__ == '__main__':
    import argparse
    import uvicorn

    parser = argparse.ArgumentParser()
    parser.add_argument('--host', type=str, default='0.0.0.0', help='listen host')
    parser.add_argument('--port', type=int, default=8046, help='listen port')
    args = parser.parse_args()

    uvicorn.run(app, host=args.host, port=args.port)
