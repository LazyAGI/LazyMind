from __future__ import annotations
import importlib
import inspect
import json
import logging
import pkgutil
import threading
import time
import traceback
from dataclasses import dataclass, field
from functools import wraps
from typing import Any, Callable, Iterable
from evo.domain.tool_result import ErrorCode, ToolResult

_log = logging.getLogger('evo.tools.registry')
ToolFn = Callable[..., ToolResult[Any]]
LLMFn = Callable[..., str]
Summarizer = Callable[[ToolResult[Any]], str]


@dataclass
class ToolSpec:
    name: str
    fn: ToolFn
    doc: str
    signature: inspect.Signature
    tags: list[str] = field(default_factory=list)
    lazyllm_group: str = 'tool'
    summarizer: Summarizer | None = None

    def describe(self) -> str:
        params = [
            p + (f'={v.default!r}' if v.default is not inspect.Parameter.empty else '')
            for p, v in self.signature.parameters.items()
        ]
        return f"- **{self.name}**({', '.join(params)}): {(self.doc or '').strip().split(chr(10) + chr(10))[0]}"

    def summarize_result(self, result: ToolResult[Any], *, max_chars: int = 200) -> str:
        if not result.ok:
            return f"FAIL {((result.error.message if result.error else 'error')[:max_chars])}"
        try:
            text = (self.summarizer or _default_summarizer)(result)
        except Exception as exc:
            text = f'<summarizer error: {exc}>'
        return text[:max_chars]


class ToolRegistry:
    def __init__(self, *, discovery_package: str = 'evo.tools') -> None:
        self._specs: dict[str, ToolSpec] = {}
        self._discovery_package = discovery_package
        self._discovered = False
        self._discover_lock = threading.Lock()

    def register(self, spec: ToolSpec, *, replace: bool = False) -> None:
        if replace or spec.name not in self._specs:
            self._specs[spec.name] = spec

    def _ensure_discovered(self) -> None:
        if self._discovered:
            return
        with self._discover_lock:
            if self._discovered:
                return
            try:
                _discover_package(self._discovery_package)
            except Exception as exc:
                _log.warning('Tool auto-discovery failed: %s', exc)
            self._discovered = True

    def get(self, name: str) -> ToolSpec:
        self._ensure_discovered()
        if name not in self._specs:
            raise KeyError(f'Unknown tool: {name}. Known: {sorted(self._specs)}')
        return self._specs[name]

    def names(self) -> list[str]:
        self._ensure_discovered()
        return sorted(self._specs)

    def all(self) -> list[ToolSpec]:
        return [self._specs[n] for n in self.names()]

    def subset(self, names: Iterable[str]) -> list[ToolSpec]:
        return [self.get(n) for n in names]


_registry = ToolRegistry()


def get_registry() -> ToolRegistry:
    return _registry


def _default_summarizer(result: ToolResult[Any]) -> str:
    data = result.data
    if isinstance(data, dict):
        return f'keys={sorted(data)[:6]}'
    if isinstance(data, list):
        return f'list of {len(data)} items'
    try:
        return json.dumps(data, ensure_ascii=False)[:200]
    except Exception:
        return f'<{type(data).__name__}>'


def _wrap(fn: ToolFn, name: str) -> ToolFn:
    @wraps(fn)
    def _inner(**kwargs: Any) -> ToolResult[Any]:
        t0 = time.time()
        try:
            result = fn(**kwargs)
        except Exception as exc:
            _log.exception('Tool %s crashed', name)
            result = ToolResult.failure(
                name,
                ErrorCode.INTERNAL_ERROR,
                f'{type(exc).__name__}: {exc}',
                details={'traceback': traceback.format_exc(limit=5)},
            )
        if not isinstance(result, ToolResult):
            result = ToolResult.failure(
                name, ErrorCode.INTERNAL_ERROR, f'Tool {name} returned {type(result).__name__}, expected ToolResult'
            )
        result.tool = result.tool or name
        result.latency_ms = result.latency_ms or (time.time() - t0) * 1000
        if result.ok and result.handle is None:
            from evo.runtime.session import get_current_session

            sess = get_current_session()
            if sess is not None and sess.handle_store is not None:
                result.handle = sess.handle_store.append(name, kwargs, result.data)
        return result

    return _inner


def tool(*, name: str | None = None, tags: list[str] | None = None, lazyllm_group: str = 'tool',
         llm_exposed: bool = True, summarizer: Summarizer | None = None,
         ) -> Callable[[ToolFn], ToolFn]:
    def decorator(fn: ToolFn) -> ToolFn:
        tool_name = name or fn.__name__
        wrapped = _wrap(fn, tool_name)
        _registry.register(
            ToolSpec(
                tool_name,
                wrapped,
                inspect.getdoc(fn) or '',
                inspect.signature(fn),
                list(tags or []),
                lazyllm_group,
                summarizer,
            ),
            replace=True,
        )
        return wrapped

    return decorator


def get_llm_callable(name: str) -> LLMFn:
    spec = _registry.get(name)

    def _call(**kwargs: Any) -> str:
        return spec.fn(**kwargs).to_json()

    _call.__name__, _call.__doc__, _call.__signature__ = name, spec.doc, spec.signature
    return _call


def _discover_package(package: str) -> None:
    mod = importlib.import_module(package)
    if hasattr(mod, '__path__'):
        for info in pkgutil.walk_packages(mod.__path__, prefix=f'{package}.'):
            if not info.name.endswith('.registry'):
                try:
                    importlib.import_module(info.name)
                except Exception as exc:
                    _log.warning('Skipping %s during tool discovery: %s', info.name, exc)


def discover(package: str = 'evo.tools') -> list[str]:
    _discover_package(package)
    _registry._discovered = True
    return sorted(_registry._specs)
