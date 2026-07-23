from math import isfinite

from .errors import DefinitionError


def _string(value: object, name: str) -> str:
    if not isinstance(value, str):
        raise TypeError(f'{name} must be str')
    return value


def _text(value: object, name: str) -> None:
    value = _string(value, name)
    if not value.strip():
        raise DefinitionError(f'{name} must be non-empty')


def _positive_int(value: object, name: str) -> None:
    if not isinstance(value, int) or isinstance(value, bool):
        raise TypeError(f'{name} must be int')
    if value < 1:
        raise DefinitionError(f'{name} must be >= 1')


def _positive_number(value: object, name: str) -> None:
    if not isinstance(value, (int, float)) or isinstance(value, bool):
        raise TypeError(f'{name} must be a number')
    if not isfinite(value) or value <= 0:
        raise DefinitionError(f'{name} must be finite and positive')


def _as_exception(error: BaseException) -> Exception:
    if isinstance(error, Exception):
        return error
    return RuntimeError(f'{type(error).__name__}: {error}')


__all__ = ['_as_exception', '_positive_int', '_positive_number', '_string', '_text']
