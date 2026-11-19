"""Request-local verified package data for audited, model-free tool adapters."""
from contextlib import contextmanager
from contextvars import ContextVar
from types import MappingProxyType

_package_files = ContextVar('external_workflow_package_files', default=None)


@contextmanager
def external_package_scope(files):
    token = _package_files.set(MappingProxyType(files))
    try:
        yield
    finally:
        _package_files.reset(token)


def get_external_package_files():
    files = _package_files.get()
    if files is None:
        raise RuntimeError('A verified pinned workflow package is required')
    return files
