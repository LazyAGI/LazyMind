"""EVO business operations and Artifact Runtime declarations."""

from importlib import import_module

__all__ = ['chat_router', 'evo_flow_definition', 'evo_operations']


def __getattr__(name: str):
    if name == 'chat_router':
        value = import_module('.route.chat_router', __name__)
    elif name == 'evo_flow_definition':
        value = import_module('.flow', __name__).evo_flow_definition
    elif name == 'evo_operations':
        value = import_module('.operation', __name__).evo_operations
    else:
        raise AttributeError(f'module {__name__!r} has no attribute {name!r}')
    globals()[name] = value
    return value
