from __future__ import annotations

import math
from typing import Any, Dict

from lazymind.chat.engine.tools.infra import (
    handle_tool_errors,
    safe_evaluate_expression,
    tool_success,
)


def format_calculation_result(value: int | float) -> str:
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    text = format(value, '.12g')
    if 'e' in text or 'E' in text:
        return text
    if '.' in text:
        return text.rstrip('0').rstrip('.') or '0'
    return text


@handle_tool_errors
def calculator(expression: str) -> Dict[str, Any]:
    """Evaluate a mathematical expression safely.

    Use this tool for numeric calculations, unit conversions, percentages, and
    formula evaluation. Only arithmetic operators and a fixed set of ``math``
    functions are allowed; arbitrary Python code is rejected.

    Args:
        expression: A math expression such as ``(12 * 13) / 6``, ``sqrt(2)``,
            ``sin(pi / 4)``, or ``2 ** 10``. Supported operators are ``+``,
            ``-``, ``*``, ``/``, ``//``, ``%``, and ``**``. Supported
            functions include ``sqrt``, ``sin``, ``cos``, ``tan``, ``log``,
            ``log10``, ``exp``, ``pow``, ``ceil``, ``floor``, ``fabs``,
            ``factorial``, ``min``, ``max``, ``abs``, and ``round``. Constants
            ``pi``, ``e``, and ``tau`` are available.

    Returns:
        A unified tool payload whose ``result`` contains the evaluated value.
    """
    normalized = str(expression or '').strip()
    value = safe_evaluate_expression(normalized)
    if isinstance(value, bool):
        raise ValueError('expression did not evaluate to a number')
    if not isinstance(value, (int, float)):
        raise ValueError('expression did not evaluate to a number')
    if isinstance(value, float) and (math.isnan(value) or math.isinf(value)):
        raise ValueError('expression result is not a finite number')

    formatted = format_calculation_result(value)
    return tool_success('calculator', {
        'status': 'ok',
        'expression': normalized,
        'result': formatted,
        'value': value,
    })
