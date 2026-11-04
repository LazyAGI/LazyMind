"""Choose and execute small experiments using the existing paper process sandbox."""
import ast
import json
import time
from pathlib import Path

from ..execution import container
from ..plotting.code import FORBIDDEN, FORBIDDEN_ATTRIBUTES
from .results import records_to_table
from .schema import code_column_types

TIMEOUT = 30
IMPORTS = {'numpy', 'scipy', 'pandas', 'math', 'statistics', 'random', 'collections', 'itertools', 'copy'}
ATTRIBUTES = FORBIDDEN_ATTRIBUTES - {'random', 'default_rng'}


def validate_code(source):
    columns = code_column_types(source)
    tree = ast.parse(source)
    for node in tree.body:
        if isinstance(node, (ast.Import, ast.ImportFrom, ast.FunctionDef)):
            continue
        if isinstance(node, ast.Assign):
            ast.literal_eval(node.value)
            continue
        if isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str):
            continue
        raise ValueError('实验代码顶层仅支持导入、函数和常量')
    for node in ast.walk(tree):
        if (isinstance(node, ast.ImportFrom) and node.module == '__future__' and not node.level
                and all(alias.name == 'annotations' for alias in node.names)):
            continue
        if isinstance(node, ast.Import) and any(a.name.split('.')[0] not in IMPORTS for a in node.names):
            raise ValueError('实验依赖超出当前轻量执行范围')
        if isinstance(node, ast.ImportFrom) and (node.level or (node.module or '').split('.')[0] not in IMPORTS):
            raise ValueError('实验依赖超出当前轻量执行范围')
        if isinstance(node, ast.Name) and (node.id in FORBIDDEN or node.id.startswith('__')):
            raise ValueError('实验包含当前不支持的动态执行或文件操作')
        if isinstance(node, ast.Attribute) and (
            (node.attr.startswith('_') and node.attr != '__name__') or node.attr in ATTRIBUTES
        ):
            raise ValueError('实验包含当前不支持的属性访问：' + node.attr)
        if isinstance(node, ast.FunctionDef) and node.decorator_list:
            raise ValueError('实验代码不支持装饰器')
    return columns


def decide(draft, inputs, allowed):
    if not allowed:
        return '未获本次轻量实验运行许可，使用模拟示例。'
    needs = draft.execution_requirements
    if needs.device != 'cpu' or needs.requires_network or needs.estimated_seconds > TIMEOUT:
        return '实验需要 GPU、外网或超过 30 秒的计算，使用模拟示例。' + needs.reason
    if needs.requires_external_data and inputs.get('data') is None:
        return '实验所需数据尚未提供，使用模拟示例。'
    files = {f.path: f.content for f in draft.code_files}
    try:
        validate_code(files['experiment.py'])
        # Requirements are advisory for downloads; never install anything in the service.
        for line in files['requirements.txt'].splitlines():
            name = line.strip().split('#', 1)[0].strip()
            if name and name.split('=')[0].split('>')[0].split('<')[0].split('[')[0].strip() not in IMPORTS:
                return '实验需要当前环境未支持的依赖，使用模拟示例。'
        container.validate_environment()
    except (ValueError, SyntaxError, container.ContainerEnvironmentError) as exc:
        return str(exc) + '；使用模拟示例。'
    return None


def execute(draft, inputs, allowed=False, on_event=None):
    reason = decide(draft, inputs, allowed)
    if reason:
        if inputs.get('data') is not None:
            reason = reason.replace('使用模拟示例', '保留用户数据，本次不生成替代结果')
        return {'status': 'not_run', 'reason': reason, 'logs': '', 'elapsed_seconds': 0.0, 'results': None}
    files = {f.path: f.content for f in draft.code_files}
    if on_event:
        on_event({'tag': 'phase', 'phase': '正在实际运行轻量实验（最多 30 秒）'})
    started = time.monotonic()
    try:
        result = container.execute(
            {'code': files['experiment.py'], 'config': json.loads(files['config.json']), 'data': inputs.get('data')},
            Path(__file__).with_name('worker.py'), TIMEOUT, label='实验代码')
        table = records_to_table(result['records'], code_column_types(files['experiment.py']))
        if len(table['rows']) > 100000 or len(table['rows']) * len(table['columns']) > 1000000:
            raise ValueError('实验结果超过 F4 支持的 10 万行或 100 万单元格')
        return {'status': 'succeeded', 'reason': None, 'logs': result['logs'],
                'elapsed_seconds': round(time.monotonic() - started, 3), 'results': table}
    except (RuntimeError, ValueError, KeyError) as exc:
        reason = str(exc)
        if on_event:
            phase = ('实验未完成，保留原因，不生成替代结果' if inputs.get('data') is not None
                     else '实验未完成，保留原因并使用模拟示例')
            on_event({'tag': 'phase', 'phase': phase})
        return {'status': 'timeout' if '超时' in reason else 'failed', 'reason': reason,
                'logs': reason, 'elapsed_seconds': round(time.monotonic() - started, 3), 'results': None}
