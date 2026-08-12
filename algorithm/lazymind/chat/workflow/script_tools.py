from __future__ import annotations

import base64
import hashlib
import types
from typing import Any, Callable, Dict, Iterable

import yaml
from lazyllm import LOG


def _decode_file(files: Dict[str, Any], path: str) -> str:
    raw = files.get(path)
    if raw is None:
        raise ValueError(f'Workflow package file is missing: {path}')
    content = base64.b64decode(raw) if isinstance(raw, str) else bytes(raw)
    return content.decode('utf-8')


def _declared_tool_paths(files: Dict[str, Any]) -> Dict[str, str]:
    document = yaml.safe_load(_decode_file(files, 'workflow.yaml')) or {}
    declarations = document.get('tool_scripts') or []
    result: Dict[str, str] = {}
    for declaration in declarations:
        if not isinstance(declaration, dict):
            continue
        path = str(declaration.get('path') or '').strip()
        if (
            not path.startswith('scripts/')
            or not path.endswith('.py')
            or '..' in path.split('/')
        ):
            continue
        for raw_name in declaration.get('functions') or []:
            name = str(raw_name or '').strip()
            if name and name.isidentifier():
                if name in result and result[name] != path:
                    raise ValueError(f'Workflow tool is declared by multiple scripts: {name}')
                result[name] = path
    return result


def resolve_declared_script_tools(
    package: Dict[str, Any], names: Iterable[str],
) -> Dict[str, Callable[..., Any]]:
    """Resolve only functions explicitly declared by one immutable Workflow package."""
    files = package.get('files') if isinstance(package.get('files'), dict) else {}
    declared = _declared_tool_paths(files)
    requested = {str(name).strip() for name in names if str(name).strip()}
    paths = sorted({declared[name] for name in requested if name in declared})
    modules: Dict[str, types.ModuleType] = {}
    revision = str(package.get('revision_id') or 'unknown')
    for path in paths:
        source = _decode_file(files, path)
        digest = hashlib.sha256(f'{revision}:{path}'.encode()).hexdigest()[:16]
        module = types.ModuleType(f'_lazymind_workflow_{digest}')
        module.__file__ = f'{revision}/{path}'
        exec(compile(source, module.__file__, 'exec'), module.__dict__)
        modules[path] = module

    resolved: Dict[str, Callable[..., Any]] = {}
    for name in requested:
        path = declared.get(name)
        module = modules.get(path or '')
        candidate = module.__dict__.get(name) if module else None
        if not callable(candidate):
            continue
        if not str(getattr(candidate, '__doc__', '') or '').strip():
            candidate.__doc__ = f'Execute the published Workflow tool {name}.'
        resolved[name] = candidate
    return resolved


def load_pinned_workflow_tools(
    params: Dict[str, Any], names: Iterable[str],
) -> Dict[str, Callable[..., Any]]:
    """Fetch and load script tools from the exact Workflow revision in Attempt context."""
    workflow_id = str(params.get('workflow_id') or '').strip()
    revision_id = str(params.get('revision_id') or '').strip()
    requested = list(names)
    if not workflow_id or not revision_id or not requested:
        return {}
    try:
        import httpx
        from lazymind.config import config
        from lazymind.workflow_sdk import WorkflowClient

        package = WorkflowClient(
            str(config['core_api_url']).rstrip('/'),
            str(params.get('user_id') or ''),
            host='lazymind',
            transport=httpx,
        ).get_workflow(workflow_id, revision_id).result
        if str(package.get('revision_id') or '') != revision_id:
            raise RuntimeError('Core returned a different Workflow revision')
        expected_hash = str(params.get('tree_hash') or '').strip()
        if expected_hash and str(package.get('tree_hash') or '') != expected_hash:
            raise RuntimeError('Core returned a Workflow package with a different tree hash')
        resolved = resolve_declared_script_tools(package, requested)
        missing = sorted(set(requested) - set(resolved))
        if missing:
            LOG.warning(
                '[Workflow] revision %s does not provide declared tools %s',
                revision_id,
                missing,
            )
        return resolved
    except Exception as exc:
        LOG.warning('[Workflow] failed to load pinned script tools: %s', exc)
        return {}
