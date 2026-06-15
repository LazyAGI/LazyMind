from __future__ import annotations

from fastapi import FastAPI

from lazymind.config import config
from lazymind.chat.api import chat_routes, health_routes, model_check_routes, model_features_routes
from lazymind.rewrite.api import rewrite_routes
from lazymind.review.api import skill_review_routes


def register_chat_routers(app: FastAPI) -> FastAPI:
    app.include_router(health_routes.router)
    # In router mode, chat requests are forwarded by the proxy layer instead of
    # being served directly, so chat_routes must not be mounted here.
    if not config['enable_router']:
        app.include_router(chat_routes.router)
    app.include_router(rewrite_routes.router)
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
