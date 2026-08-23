import base64

import pytest

from lazymind.workflow_toolkit import load_workflow_package_tools, workflow_package_input_types


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

    tools = load_workflow_package_tools(
        package, ['build_report', 'undeclared_helper'], 'workflow-1', 'revision-1',
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

    assert load_workflow_package_tools(
        package, ['unsafe'], 'workflow-1', 'revision-1',
    ) == {}


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
        load_workflow_package_tools(package, ['run'], 'workflow-1', 'revision-1')


def test_reads_external_input_types_from_compiled_contract():
    package = {'compiled_graph': {
        'material_types': {
            'source': 'file', 'word_target': 'text', 'result': 'text',
        },
        'material_producers': {
            'source': {'kind': 'external'},
            'word_target': {'kind': 'external'},
            'result': {'kind': 'step', 'step_id': 'write'},
        },
    }}

    assert workflow_package_input_types(package) == {
        'source': 'file', 'word_target': 'text',
    }


def test_reads_external_input_types_from_legacy_package_source():
    package = {'files': {'workflow.yaml': _encoded('''
slots:
  - {id: source, type: file, external: true}
  - {id: word_target, type: text, external: true}
  - {id: result, type: text}
''')}}

    assert workflow_package_input_types(package) == {
        'source': 'file', 'word_target': 'text',
    }
