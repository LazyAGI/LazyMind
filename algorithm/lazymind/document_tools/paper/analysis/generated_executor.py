"""Validate and execute generated analysis inside the Chat container."""
from __future__ import annotations

import ast
import json
import os
from pathlib import Path

from ..plotting.code import ALLOWED_IMPORTS, FORBIDDEN, FORBIDDEN_ATTRIBUTES
from ..execution import container

# These modules are bundled in the Chat image, plus the Python standard library.
# An import allowlist must not advertise optional packages absent from the execution image.
ANALYSIS_IMPORTS = ALLOWED_IMPORTS | {'copy', 'collections', 'itertools', 're', 'json'}
ANALYSIS_FORBIDDEN_ATTRIBUTES = FORBIDDEN_ATTRIBUTES - {'random', 'default_rng'}
JSON_MEMORY_ATTRIBUTES = {'loads', 'dumps'}


def generation_contract():
    """Expose the actual early validator rules before the first generation."""
    return dict(allowed_import_roots=sorted(ANALYSIS_IMPORTS), forbidden_names=sorted(FORBIDDEN),
                unbundled_optional_packages=['statsmodels', 'pymc', 'arviz'],
                ols_runtime='Use numpy.linalg.lstsq / pinv and scipy.stats; statsmodels is not bundled.',
                forbidden_attributes=sorted(ANALYSIS_FORBIDDEN_ATTRIBUTES),
                attribute_exceptions={'json': sorted(JSON_MEMORY_ATTRIBUTES)},
                private_attributes_allowed=False, decorators_allowed=False,
                required_functions=dict(analyze=['tables', 'plan', 'rng']),
                runtime_plan=dict(questions='host-assigned research questions', outline='short analysis outline',
                                  facts='complete supplied research context, identical to generation input'),
                top_level='imports, function definitions and literal constants only',
                randomness='use supplied rng; no filesystem/network/environment access',
                result_coordinates='Each emitted row owns a copied/frozen coordinate snapshot before later mutations.')


class AnalysisEnvironmentError(RuntimeError):
    pass


def validate_code(code):
    if not isinstance(code, str) or not code.strip() or len(code.encode()) > 64 * 1024:
        raise ValueError('统计代码须为不超过 64 KiB 的非空文本')
    tree = ast.parse(code, filename='<generated-analysis>')
    json_modules = {alias.asname or 'json' for node in ast.walk(tree) if isinstance(node, ast.Import)
                    for alias in node.names if alias.name == 'json'}
    for name, args in [('analyze', ['tables', 'plan', 'rng'])]:
        functions = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == name]
        if len(functions) != 1 or [a.arg for a in functions[0].args.args] != args:
            raise ValueError(f'统计程序必须定义 {name}({", ".join(args)})')
    for node in tree.body:
        if isinstance(node, (ast.Import, ast.ImportFrom, ast.FunctionDef)):
            continue
        if isinstance(node, ast.Assign):
            ast.literal_eval(node.value)
            continue
        if isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str):
            continue
        raise ValueError('统计程序顶层只允许导入、函数和常量')
    for node in ast.walk(tree):
        if isinstance(node, ast.Import) and any(a.name.split('.')[0] not in ANALYSIS_IMPORTS for a in node.names):
            names = [a.name for a in node.names if a.name.split('.')[0] not in ANALYSIS_IMPORTS]
            raise ValueError(f'统计代码第 {node.lineno} 行包含未允许的导入: {names}；可用根模块: {sorted(ANALYSIS_IMPORTS)}')
        if isinstance(node, ast.ImportFrom) and (
            node.level or (node.module or '').split('.')[0] not in ANALYSIS_IMPORTS
        ):
            raise ValueError(f'统计代码第 {node.lineno} 行包含未允许的导入: {"." * node.level}{node.module or ""}；'
                             f'可用根模块: {sorted(ANALYSIS_IMPORTS)}')
        if isinstance(node, ast.Name) and (node.id in FORBIDDEN or node.id.startswith('__')):
            raise ValueError('统计代码包含禁止的动态执行或 I/O: ' + node.id)
        if isinstance(node, ast.Attribute):
            json_memory = (isinstance(node.value, ast.Name) and node.value.id in json_modules
                           and node.attr in JSON_MEMORY_ATTRIBUTES)
            if node.attr.startswith('_') or (node.attr in ANALYSIS_FORBIDDEN_ATTRIBUTES and not json_memory):
                raise ValueError('统计代码包含禁止的 I/O 属性: ' + node.attr)
        if isinstance(node, ast.FunctionDef) and node.decorator_list:
            raise ValueError('统计代码不能使用装饰器')


def configuration():
    try:
        container.validate_environment()
    except container.ContainerEnvironmentError as exc:
        raise AnalysisEnvironmentError(str(exc)) from exc
    try:
        timeout = int(os.environ.get('LAZYMIND_PAPER_GENERATED_ANALYSIS_TIMEOUT', '180'))
        if not 1 <= timeout <= 3600:
            raise ValueError
    except ValueError as exc:
        raise AnalysisEnvironmentError('统计执行超时须为 1..3600 秒') from exc
    return timeout


def execute(payload):
    timeout = configuration()
    validate_code(payload['code'])
    raw = json.dumps(payload, ensure_ascii=False, allow_nan=False).encode()
    if len(raw) > 32 * 1024 * 1024:
        raise ValueError('统计执行输入超过 32 MiB')
    try:
        result = container.execute(payload, Path(__file__).with_name('generated_worker.py'),
                                   timeout, label='生成统计代码')
    except container.ContainerEnvironmentError as exc:
        raise AnalysisEnvironmentError(str(exc)) from exc
    if set(result) != {'results', 'versions', 'seed'} or result['seed'] != payload['seed']:
        raise RuntimeError('统计执行记录不完整')
    return result
