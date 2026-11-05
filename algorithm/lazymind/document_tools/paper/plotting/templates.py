"""Load the supplied skill functions without enforcing one chart implementation."""
import ast
import copy
import re


NATIVE_FUNCTION_GUIDANCE = '''Skill helpers are injected as globals. Call their supplied names directly,
without importing skill files or copying example data. Use their real signatures when a helper fits.
Ordinary Matplotlib code may implement another required geometry with the shared appearance.
'''


def compose_native_program(code, reference, spec):
    blocks = re.findall(r'```python\s*\n([\s\S]*?)```', reference)
    return code + '\n\n' + blocks[-1] if blocks else code


def paper_template(script: str, common: str, source_name: str, target_name: str, extra_names: tuple = ()) -> str:
    """Extract only named drawing functions; no example globals, imports or top-level execution."""
    output = []
    defined = {n.name: n for n in ast.parse(script).body if isinstance(n, ast.FunctionDef)}
    pending, selected = [source_name, *extra_names], []
    while pending:
        name = pending.pop(0)
        if name in selected:
            continue
        if name not in defined:
            raise ValueError(f'论文 skill 缺少唯一绘图函数 {name}')
        selected.append(name)
        pending.extend(node.func.id for node in ast.walk(defined[name])
                       if isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
                       and node.func.id in defined)
    for source, names in [(common, ['paper_axes', 'paper_legend']), (script, selected)]:
        nodes = ast.parse(source).body
        for name in names:
            matches = [n for n in nodes if isinstance(n, ast.FunctionDef) and n.name == name]
            if len(matches) != 1:
                raise ValueError(f'论文 skill 缺少唯一绘图函数 {name}')
            function = copy.deepcopy(matches[0])
            if name == source_name:
                function.name = target_name
            output.append(ast.unparse(function))
    return ('import numpy as np\nimport matplotlib.pyplot as plt\nimport matplotlib.patches as mpatches\n'
            'import matplotlib.colors as mcolors\n\n'
            + '\n\n'.join(output))
