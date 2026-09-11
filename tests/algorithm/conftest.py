"""
Pytest fixtures for algorithm tests.
Add algorithm and backend paths for imports.
"""
import os
import sys

_root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
_algo = os.path.join(_root, 'algorithm')
if _algo not in sys.path:
    sys.path.insert(0, _algo)

# Worker subprocesses need the same source root as the pytest process.
_pythonpath = os.environ.get('PYTHONPATH', '').split(os.pathsep)
if _algo not in _pythonpath:
    os.environ['PYTHONPATH'] = os.pathsep.join([_algo, *filter(None, _pythonpath)])
