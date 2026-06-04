from __future__ import annotations

import logging
from typing import AsyncIterator

import httpx
from fastapi import Request
from fastapi.responses import StreamingResponse

logger = logging.getLogger(__name__)

# Shared async client with no timeout (streaming responses can be arbitrarily long)
_client = httpx.AsyncClient(timeout=None)


class StreamProxy:
    """Transparent HTTP/SSE proxy that forwards a FastAPI request to a target URL."""

    async def forward(
        self,
        request: Request,
        target_base_url: str,
        algorithm_id: str = '',
        instance_host: str = '',
    ) -> StreamingResponse:
        target_url = target_base_url.rstrip('/') + request.url.path
        if request.url.query:
            target_url += '?' + request.url.query

        # Forward all headers except hop-by-hop ones
        headers = {
            k: v
            for k, v in request.headers.items()
            if k.lower() not in ('host', 'content-length', 'transfer-encoding', 'connection')
        }

        body = await request.body()

        req = _client.build_request(
            method=request.method,
            url=target_url,
            headers=headers,
            content=body,
        )

        resp = await _client.send(req, stream=True)

        # Build response headers; inject tracing headers
        response_headers = dict(resp.headers)
        response_headers.pop('content-length', None)
        response_headers.pop('transfer-encoding', None)
        if algorithm_id:
            response_headers['X-Algorithm-Id'] = algorithm_id
        if instance_host:
            response_headers['X-Instance-Host'] = instance_host

        async def _stream_body() -> AsyncIterator[bytes]:
            try:
                async for chunk in resp.aiter_bytes():
                    yield chunk
            finally:
                await resp.aclose()

        return StreamingResponse(
            content=_stream_body(),
            status_code=resp.status_code,
            headers=response_headers,
            media_type=resp.headers.get('content-type', 'text/event-stream'),
        )

    async def close(self) -> None:
        await _client.aclose()


# Module-level singleton
_stream_proxy: StreamProxy | None = None


def get_stream_proxy() -> StreamProxy:
    global _stream_proxy
    if _stream_proxy is None:
        _stream_proxy = StreamProxy()
    return _stream_proxy
