"""Operation materializers for the artifact-centric runtime."""

from importlib import import_module

__all__ = ['chat_router']


def __getattr__(name: str):
    if name != 'chat_router':
        raise AttributeError(f'module {__name__!r} has no attribute {name!r}')
    module = import_module('.route.chat_router', __name__)
    globals()[name] = module
    return module
