"""Algorithm helpers for chat engine tools."""

from importlib import import_module

__all__ = ['DOCUMENT', 'retrieve_temp_nodes', 'search_kb']


def __getattr__(name):
    if name not in __all__:
        raise AttributeError(name)
    exports = ('retrieve_temp_nodes',) if name == 'retrieve_temp_nodes' else ('DOCUMENT', 'search_kb')
    module = import_module('.search_temp' if name == 'retrieve_temp_nodes' else '.search_kb', __name__)
    for attribute in exports:
        globals()[attribute] = getattr(module, attribute)
    return globals()[name]
