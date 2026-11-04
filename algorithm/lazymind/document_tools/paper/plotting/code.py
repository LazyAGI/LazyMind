"""Early code diagnostics; OS/container isolation, not AST checks, is the security boundary."""
import ast

ALLOWED_IMPORTS = {'matplotlib', 'numpy', 'scipy', 'pandas', 'seaborn', 'math', 'statistics'}
FORBIDDEN = {'open', 'exec', 'eval', 'compile', '__import__', 'getattr', 'setattr', 'delattr',
             'globals', 'locals', 'vars', 'input', 'breakpoint', 'help', 'exit', 'quit'}
FORBIDDEN_ATTRIBUTES = {'savefig', 'show', 'save', 'savez', 'load', 'loads', 'dump', 'dumps',
                        'read_csv', 'read_json', 'read_pickle', 'read_excel', 'read_table',
                        'to_csv', 'to_json', 'to_pickle', 'to_excel', 'fromfile', 'tofile',
                        'memmap', 'system', 'popen', 'random', 'default_rng'}


def validate_code(code: str) -> None:
    if not isinstance(code, str) or not code.strip() or len(code.encode()) > 32 * 1024:
        raise ValueError('绘图代码必须是非空且不超过 32 KiB 的 Python')
    tree = ast.parse(code, filename='<generated-plot>')
    functions = [node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'draw']
    if len(functions) != 1 or [a.arg for a in functions[0].args.args] != ['data', 'statistics', 'spec']:
        raise ValueError('代码必须定义 draw(data, statistics, spec)')
    for node in tree.body:
        if isinstance(node, (ast.Import, ast.ImportFrom, ast.FunctionDef)):
            continue
        if isinstance(node, ast.Assign):
            try:
                ast.literal_eval(node.value)
                continue
            except (ValueError, TypeError):
                pass
        if isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str):
            continue
        raise ValueError('顶层仅允许导入、函数定义和常量赋值')
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            invalid = [alias.name for alias in node.names if alias.name.split('.')[0] not in ALLOWED_IMPORTS]
            if invalid:
                raise ValueError(f'第 {node.lineno} 行绘图代码导入了未允许的模块: {", ".join(invalid)}；'
                                 'skill 函数由后端直接注入，无需导入 skill 模块；'
                                 f'允许的模块: {", ".join(sorted(ALLOWED_IMPORTS))}')
        elif isinstance(node, ast.ImportFrom):
            if node.level or (node.module or '').split('.')[0] not in ALLOWED_IMPORTS:
                module = '.' * node.level + (node.module or '')
                raise ValueError(f'第 {node.lineno} 行绘图代码导入了未允许的模块: {module}；'
                                 '删除该导入，skill 函数使用后端已注入的原名直接调用；'
                                 f'允许的模块: {", ".join(sorted(ALLOWED_IMPORTS))}')
        elif isinstance(node, ast.Name) and (node.id in FORBIDDEN or node.id.startswith('__')):
            raise ValueError(f'绘图代码不允许访问 {node.id}')
        elif isinstance(node, ast.Attribute) and (node.attr.startswith('_') or node.attr in FORBIDDEN_ATTRIBUTES):
            raise ValueError(f'绘图代码不允许访问属性 {node.attr}')
        elif isinstance(node, ast.FunctionDef) and node.decorator_list:
            raise ValueError('绘图函数不允许装饰器')
