from __future__ import annotations

import ast
import math
import operator
from functools import wraps
from typing import Any, Callable, Dict, Union

from lazyllm import fc_register

_MAX_EXPRESSION_LEN = 512

_SAFE_BINOPS: dict[type, Callable[[Any, Any], Any]] = {
    ast.Add: operator.add,
    ast.Sub: operator.sub,
    ast.Mult: operator.mul,
    ast.Div: operator.truediv,
    ast.FloorDiv: operator.floordiv,
    ast.Mod: operator.mod,
    ast.Pow: operator.pow,
}

_SAFE_UNARYOPS: dict[type, Callable[[Any], Any]] = {
    ast.UAdd: operator.pos,
    ast.USub: operator.neg,
}

_SAFE_CONSTANTS: dict[str, float] = {
    'pi': math.pi,
    'e': math.e,
    'tau': math.tau,
}

_SAFE_FUNCTIONS: dict[str, Callable[..., Any]] = {
    'abs': abs,
    'round': round,
    'min': min,
    'max': max,
    'sqrt': math.sqrt,
    'fabs': math.fabs,
    'sin': math.sin,
    'cos': math.cos,
    'tan': math.tan,
    'asin': math.asin,
    'acos': math.acos,
    'atan': math.atan,
    'atan2': math.atan2,
    'sinh': math.sinh,
    'cosh': math.cosh,
    'tanh': math.tanh,
    'exp': math.exp,
    'log': math.log,
    'log10': math.log10,
    'log2': math.log2,
    'pow': pow,
    'ceil': math.ceil,
    'floor': math.floor,
    'trunc': math.trunc,
    'degrees': math.degrees,
    'radians': math.radians,
    'hypot': math.hypot,
    'factorial': math.factorial,
}


def _tool_failure(tool_name: str, exc: Exception) -> Dict[str, Any]:
    return {
        'success': False,
        'reason': f'{tool_name} failed: {exc}',
        'error': str(exc),
        'error_type': type(exc).__name__,
    }


def _handle_tool_errors(func):
    @wraps(func)
    def wrapper(*args, **kwargs):
        try:
            return func(*args, **kwargs)
        except Exception as exc:
            return _tool_failure(func.__name__, exc)

    return wrapper


def _numeric_value(node: ast.Constant) -> Union[int, float]:
    if isinstance(node.value, bool):
        raise ValueError('boolean literals are not allowed in expressions')
    if isinstance(node.value, (int, float)):
        return node.value
    raise ValueError(f'unsupported literal type: {type(node.value).__name__}')


def _safe_eval_node(node: ast.AST) -> Union[int, float]:
    if isinstance(node, ast.Constant):
        return _numeric_value(node)

    if isinstance(node, ast.BinOp):
        op_type = type(node.op)
        if op_type not in _SAFE_BINOPS:
            raise ValueError(f'unsupported binary operator: {op_type.__name__}')
        left = _safe_eval_node(node.left)
        right = _safe_eval_node(node.right)
        return _SAFE_BINOPS[op_type](left, right)

    if isinstance(node, ast.UnaryOp):
        op_type = type(node.op)
        if op_type not in _SAFE_UNARYOPS:
            raise ValueError(f'unsupported unary operator: {op_type.__name__}')
        return _SAFE_UNARYOPS[op_type](_safe_eval_node(node.operand))

    if isinstance(node, ast.Call):
        if not isinstance(node.func, ast.Name):
            raise ValueError('only simple function calls are allowed')
        func_name = node.func.id
        func = _SAFE_FUNCTIONS.get(func_name)
        if func is None:
            raise ValueError(f'unsupported function: {func_name}')
        if node.keywords:
            raise ValueError('keyword arguments are not allowed in function calls')
        args = [_safe_eval_node(arg) for arg in node.args]
        return func(*args)

    if isinstance(node, ast.Name):
        if node.id not in _SAFE_CONSTANTS:
            raise ValueError(f'unsupported name: {node.id}')
        return _SAFE_CONSTANTS[node.id]

    raise ValueError(f'unsupported expression element: {type(node).__name__}')


def _safe_evaluate(expression: str) -> Union[int, float]:
    text = str(expression or '').strip()
    if not text:
        raise ValueError('expression is required')
    if len(text) > _MAX_EXPRESSION_LEN:
        raise ValueError(f'expression exceeds maximum length ({_MAX_EXPRESSION_LEN})')
    try:
        tree = ast.parse(text, mode='eval')
    except SyntaxError as exc:
        raise ValueError(f'invalid expression syntax: {exc.msg}') from exc
    if not isinstance(tree.body, ast.AST):
        raise ValueError('invalid expression')
    return _safe_eval_node(tree.body)


def _format_result(value: Union[int, float]) -> str:
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    text = format(value, '.12g')
    if 'e' in text or 'E' in text:
        return text
    if '.' in text:
        return text.rstrip('0').rstrip('.') or '0'
    return text


@fc_register('tool', execute_in_sandbox=False)
@_handle_tool_errors
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
        A dict with ``success``, ``status``, ``expression``, and ``result``.
    """
    normalized = str(expression or '').strip()
    value = _safe_evaluate(normalized)
    if isinstance(value, bool):
        raise ValueError('expression did not evaluate to a number')
    if not isinstance(value, (int, float)):
        raise ValueError('expression did not evaluate to a number')
    if isinstance(value, float) and (math.isnan(value) or math.isinf(value)):
        raise ValueError('expression result is not a finite number')

    formatted = _format_result(value)
    return {
        'success': True,
        'status': 'ok',
        'expression': normalized,
        'result': formatted,
        'value': value,
    }
