"""Conservative diagnostics for failures serialized by the Skill tool boundary.

LazyLLM currently flattens sandbox failures into ToolExecutionError.value. Preserve
that original result and add machine-readable hints, without deciding permissions,
installing dependencies, or changing the retry policy.
"""
from __future__ import annotations

import re
from typing import Any


def _is_node_path(specifier: str) -> bool:
    return (specifier.startswith(('.', '/', '\\'))
            or re.match(r'^[A-Za-z]:[\\/]', specifier) is not None)


def classify_skill_failure(result: Any) -> Any:
    if (not isinstance(result, dict) or result.get('ok') is not False
            or result.get('needs_approval') or result.get('error_type')):
        return result
    message = result.get('value')
    if not isinstance(message, str):
        return result
    fields = {}
    exit_match = re.match(r'^Skill script execution failed with exit code (-?\d+): ', message)
    diagnostic = message[exit_match.end():] if exit_match else message
    if exit_match:
        fields['exit_code'] = int(exit_match.group(1))
    runtime = re.fullmatch(r'Required runtime not found: (node|bash|python[\d.]*)', diagnostic.strip())
    module = re.search(r"(?m)^ModuleNotFoundError: No module named '([\w.-]+)'\s*$", diagnostic)
    node_module = re.search(
        r"(?m)^Error(?: \[ERR_MODULE_NOT_FOUND\])?: Cannot find (?:module|package) '([^'\n]+)'", diagnostic,
    )
    if result.get('missing_env'):
        fields['error_type'] = 'missing_env'
    elif diagnostic.strip() == 'Unsupported Skill script extension.':
        fields['error_type'] = 'unsupported_runtime'
    elif runtime or module:
        fields.update(error_type='missing_dependency', dependency=(runtime or module).group(1))
    elif node_module and not _is_node_path(node_module.group(1)):
        # A missing relative/absolute script is not evidence of a missing package.
        fields.update(error_type='missing_dependency', dependency=node_module.group(1))
    else:
        fields['error_type'] = 'script_failed' if exit_match else 'unknown'
    return {**result, **fields}
