from __future__ import annotations
import asyncio
import logging
from typing import AsyncIterator, Callable, Iterator

from lazyllm import AutoModel
from evo.runtime.config import EvoConfig
from evo.runtime.model_gateway import ModelGateway

LLMFactory = Callable[[], Callable[[str], AsyncIterator[str]]]


def default_llm_provider(cfg: EvoConfig):
    client = None

    def provider():
        nonlocal client
        if client is None:
            client = AutoModel(model=cfg.model_config.llm_role)
        return client

    return provider


def default_embed_provider(cfg: EvoConfig):
    client = None

    def provider():
        nonlocal client
        if client is None:
            client = AutoModel(model=cfg.model_config.embed_role)
        return client

    return provider


def _chunked(text: str, size: int = 64) -> list[str]:
    return [text[i: i + size] for i in range(0, len(text), size)] or ['']


def make_evo_llm(cfg: EvoConfig, *, chunk_size: int = 64) -> LLMFactory:
    role = cfg.model_config.llm_role
    gateway: ModelGateway[str] = ModelGateway(
        cfg.llm, name='evo-orchestrator-llm', logger=logging.getLogger('evo.orchestrator.llm')
    )
    client = AutoModel(model=role)

    def factory() -> Callable[[str], AsyncIterator[str]]:
        async def call(prompt: str) -> AsyncIterator[str]:
            text = await asyncio.to_thread(gateway.call, lambda: client(prompt), cache_key=prompt, agent='orchestrator')
            for chunk in _chunked(text or '', chunk_size):
                await asyncio.sleep(0)
                yield chunk

        return call

    return factory


def make_evo_stream_llm(cfg: EvoConfig) -> Callable[[str, Callable[[], bool]], Iterator[str]]:
    role = cfg.model_config.llm_role
    gateway: ModelGateway[str] = ModelGateway(
        cfg.llm, name='evo-stream-llm', logger=logging.getLogger('evo.orchestrator.llm')
    )
    client = AutoModel(model=role)

    def stream(prompt: str, cancel_requested: Callable[[], bool]) -> Iterator[str]:
        if cancel_requested():
            raise RuntimeError('MESSAGE_CANCELLED')
        text = gateway.call(lambda: client(prompt), cache_key=prompt, agent='planner_stream') or ''
        for chunk in _chunked(text):
            if cancel_requested():
                raise RuntimeError('MESSAGE_CANCELLED')
            yield chunk

    return stream
