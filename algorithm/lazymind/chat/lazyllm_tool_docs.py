from __future__ import annotations

import importlib.util
import inspect
import sys
import threading
import types
from pathlib import Path
from typing import Any


_DOC_MODULE_BY_TOOL_PREFIX = (
    ('lazyllm.tools.fs', 'tool_fs'),
    ('lazyllm.tools.tools.search', 'search'),
    ('lazyllm.tools.agent', 'tool_agent'),
    ('lazyllm.tools.sandbox', 'tool_sandbox'),
    ('lazyllm.tools.services', 'tool_services'),
    ('lazyllm.tools.infer_service', 'tool_infer_service'),
    ('lazyllm.tools.rag', 'tool_rag'),
    ('lazyllm.tools.writer', 'tool_writer'),
    ('lazyllm.tools.git', 'git'),
    ('lazyllm.tools.http_request', 'tool_http_request'),
    ('lazyllm.tools.mcp', 'tool_mcp'),
    ('lazyllm.tools.tools', 'tool_tools'),
)
_loaded_doc_modules: set[str] = set()
_load_lock = threading.Lock()


def ensure_lazyllm_tool_docs(tools: list[Any]) -> None:
    """Load a LazyLLM docs file only when a registered built-in tool lacks docs."""
    for tool in tools:
        _ensure_tool_docs(tool)


def _ensure_tool_docs(tool: Any) -> None:
    if isinstance(tool, tuple) and len(tool) == 2:
        tool = tool[0]
    if isinstance(tool, dict):
        for nested in tool.get('tools', []):
            _ensure_tool_docs(nested)
        return

    missing = [value for value in _public_callables(tool) if not inspect.getdoc(value)]
    for value in missing:
        doc_module = _doc_module_for(value)
        if doc_module:
            _load_doc_module(doc_module)


def _public_callables(tool: Any) -> list[Any]:
    public_apis = getattr(tool, '__public_apis__', None)
    if public_apis is not None:
        return [getattr(tool, name) for name in public_apis if callable(getattr(tool, name, None))]
    return [tool] if callable(tool) else []


def _doc_module_for(value: Any) -> str | None:
    module_name = getattr(value, '__module__', '')
    for prefix, doc_module in _DOC_MODULE_BY_TOOL_PREFIX:
        if module_name == prefix or module_name.startswith(prefix + '.'):
            return doc_module
    return None


def _load_doc_module(doc_module: str) -> None:
    with _load_lock:
        if doc_module in _loaded_doc_modules:
            return

        import lazyllm.docs

        docs_root = Path(lazyllm.docs.__file__).resolve().parent
        tools_root = docs_root / 'tools'
        package_name = 'lazyllm.docs.tools'
        if package_name not in sys.modules:
            package = types.ModuleType(package_name)
            package.__path__ = [str(tools_root)]
            package.__package__ = package_name
            sys.modules[package_name] = package

        module_name = f'{package_name}.{doc_module}'
        if module_name not in sys.modules:
            spec = importlib.util.spec_from_file_location(module_name, tools_root / f'{doc_module}.py')
            if spec is None or spec.loader is None:
                raise ImportError(f'cannot load LazyLLM tool docs: {doc_module}')
            module = importlib.util.module_from_spec(spec)
            sys.modules[module_name] = module
            try:
                spec.loader.exec_module(module)
            except BaseException:
                sys.modules.pop(module_name, None)
                raise
        _loaded_doc_modules.add(doc_module)
