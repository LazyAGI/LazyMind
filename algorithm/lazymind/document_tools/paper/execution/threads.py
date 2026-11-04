"""Preserve both Python request context and LazyLLM session state across workers."""
from contextvars import copy_context

from lazyllm import ThreadPoolExecutor as LazyThreadPoolExecutor


class ThreadPoolExecutor(LazyThreadPoolExecutor):
    """Also support LazyLLM versions whose executor only propagates the session ID."""

    def submit(self, fn, /, *args, **kwargs):
        context = copy_context()
        return super().submit(context.run, fn, *args, **kwargs)
