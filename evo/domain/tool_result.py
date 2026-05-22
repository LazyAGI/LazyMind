from __future__ import annotations
import json
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Generic, TypeVar
from evo.utils import jsonable

T = TypeVar('T')


class ErrorCode(str, Enum):
    DATA_NOT_LOADED = 'DATA_NOT_LOADED'
    CASE_NOT_FOUND = 'CASE_NOT_FOUND'
    TRACE_NOT_FOUND = 'TRACE_NOT_FOUND'
    INVALID_ARGUMENT = 'INVALID_ARGUMENT'
    IO_ERROR = 'IO_ERROR'
    INTERNAL_ERROR = 'INTERNAL_ERROR'


@dataclass
class ToolError:
    code: str
    message: str
    details: dict[str, Any] | None = None


class ToolFailure(RuntimeError):
    def __init__(self, tool: str, error: ToolError) -> None:  # noqa: B042
        super().__init__(f'[{tool}] {error.code}: {error.message}')
        self.tool = tool
        self.error = error


@dataclass
class ToolResult(Generic[T]):
    ok: bool
    tool: str = ''
    data: T | None = None
    error: ToolError | None = None
    latency_ms: float = 0.0
    meta: dict[str, Any] = field(default_factory=dict)
    handle: str | None = None

    @classmethod
    def success(
        cls, tool: str, data: T, latency_ms: float = 0.0, meta: dict[str, Any] | None = None
    ) -> 'ToolResult[T]':
        return cls(ok=True, tool=tool, data=data, latency_ms=latency_ms, meta=meta or {})

    @classmethod
    def failure(cls, tool: str, code: str | ErrorCode, message: str,
                details: dict[str, Any] | None = None, latency_ms: float = 0.0
                ) -> 'ToolResult[Any]':
        c = code.value if isinstance(code, ErrorCode) else code
        return cls(
            ok=False, tool=tool, error=ToolError(code=c, message=message, details=details), latency_ms=latency_ms
        )

    def unwrap(self) -> T:
        if not self.ok or self.error is not None:
            raise ToolFailure(self.tool, self.error or ToolError('UNKNOWN', 'unknown'))
        return self.data

    def as_dict(self) -> dict[str, Any]:
        if self.ok:
            out: dict[str, Any] = {'ok': True, 'data': jsonable(self.data)}
            if self.handle:
                out['handle'] = self.handle
        else:
            err = self.error or ToolError('UNKNOWN', 'unknown')
            out = {
                'ok': False,
                'error': {
                    'code': err.code,
                    'message': err.message,
                    **({'details': err.details} if err.details else {}),
                },
            }
        if self.latency_ms:
            out.setdefault('meta', {})['latency_ms'] = round(self.latency_ms, 3)
        if self.meta:
            out.setdefault('meta', {}).update(self.meta)
        return out

    def to_json(self, *, indent: int | None = 2) -> str:
        return json.dumps(self.as_dict(), ensure_ascii=False, indent=indent)
