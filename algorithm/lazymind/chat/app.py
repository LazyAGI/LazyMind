from __future__ import annotations

from fastapi import FastAPI

from lazymind.chat.api import chat_routes, health_routes, model_check_routes


def create_app() -> FastAPI:
    app = FastAPI(
        title='LazyLLM Chat API',
        description='Knowledge-base-backed conversational API service',
        version='1.0.0',
    )
    from lazymind.chat.api import (
        model_features_routes,
    )
    from lazymind.review.api import memory_generate_routes
    from lazymind.review.api import vocab_routes

    app.include_router(health_routes.router)
    app.include_router(chat_routes.router)
    app.include_router(memory_generate_routes.router)
    app.include_router(model_features_routes.router)
    app.include_router(model_check_routes.router)
    app.include_router(vocab_routes.router)
    return app


app = create_app()

if __name__ == '__main__':
    import argparse
    import uvicorn

    parser = argparse.ArgumentParser()
    parser.add_argument('--host', type=str, default='0.0.0.0', help='listen host')
    parser.add_argument('--port', type=int, default=8046, help='listen port')
    args = parser.parse_args()

    uvicorn.run(app, host=args.host, port=args.port)
