import base64

import pytest

from lazymind.chat.workflow.script_tools import resolve_declared_script_tools


def _encoded(value: str) -> str:
    return base64.b64encode(value.encode()).decode()


def test_resolves_only_function_declared_for_exact_script_path():
    package = {
        'revision_id': 'revision-1',
        'files': {
            'workflow.yaml': _encoded('''
tool_scripts:
  - path: scripts/report_tools.py
    functions: [build_report]
'''),
            'scripts/report_tools.py': _encoded('''
def build_report(title: str) -> str:
    return "report:" + title

def undeclared_helper() -> str:
    return "private"
'''),
            'scripts/other.py': _encoded('''
def build_report(title: str) -> str:
    return "wrong:" + title
'''),
        },
    }

    tools = resolve_declared_script_tools(
        package,
        ['build_report', 'undeclared_helper'],
    )

    assert set(tools) == {'build_report'}
    assert tools['build_report']('demo') == 'report:demo'


def test_rejects_script_path_outside_workflow_scripts_directory():
    package = {
        'revision_id': 'revision-1',
        'files': {
            'workflow.yaml': _encoded('''
tool_scripts:
  - path: ../unsafe.py
    functions: [unsafe]
'''),
            '../unsafe.py': _encoded('def unsafe(): return True'),
        },
    }

    assert resolve_declared_script_tools(package, ['unsafe']) == {}


def test_rejects_duplicate_function_declarations():
    package = {
        'revision_id': 'revision-1',
        'files': {
            'workflow.yaml': _encoded('''
tool_scripts:
  - path: scripts/one.py
    functions: [run]
  - path: scripts/two.py
    functions: [run]
'''),
            'scripts/one.py': _encoded('def run(): return 1'),
            'scripts/two.py': _encoded('def run(): return 2'),
        },
    }

    with pytest.raises(ValueError, match='multiple scripts'):
        resolve_declared_script_tools(package, ['run'])
