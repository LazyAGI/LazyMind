"""
Pytest fixtures for algorithm tests.
Add algorithm and backend paths for imports.
"""
import os
import sys

import pytest

_root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
_algo = os.path.join(_root, 'algorithm')
if _algo not in sys.path:
    sys.path.insert(0, _algo)

# Worker subprocesses need the same source root as the pytest process.
_pythonpath = os.environ.get('PYTHONPATH', '').split(os.pathsep)
if _algo not in _pythonpath:
    os.environ['PYTHONPATH'] = os.pathsep.join([_algo, *filter(None, _pythonpath)])


@pytest.fixture(autouse=True)
def isolate_ppt_resource_prefetch(request, monkeypatch):
    # Workflow tests exercise preparation without starting real cloud downloads.
    # The resource suite tests the actual transport and background executor.
    if request.node.path.name != 'test_presentation_resources.py':
        monkeypatch.setattr('lazymind.presentation_resources.prefetch', lambda: None)
