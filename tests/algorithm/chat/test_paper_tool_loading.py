"""Paper adapters load only after pinned contract and binding validation."""
import base64
import json
import subprocess
import sys

import pytest
import yaml

from lazymind.chat.workflow import external_tools as gateway
from lazymind.chat.workflow import paper_tools
from lazymind.chat.workflow.external_ppt_tools import ExternalToolError


def request(*, validator='paper-agent-v1', name='paper_prepare_inputs', binding=None, step='write_outline'):
    config = {'runtime': {'execution': {'version': '1', 'steps': {
        step: {'output_validator': validator, 'tools': [{'name': name, 'source': 'package'}]}}}},
        'tool_scripts': [{'path': 'scripts/agent_tools.py', 'functions': [name]}]}
    files = {'workflow.yaml': yaml.safe_dump(config).encode(),
             'scripts/agent_tools.py': (paper_tools.PAPER_BINDING if binding is None else binding).encode()}
    tree = gateway.package_tree_hash(files)
    return dict(workflow_id='owned-resource', revision_id='revision', tree_hash=tree, step_id=step,
                package={'tree_hash': tree, 'files': {
                    key: base64.b64encode(value).decode() for key, value in files.items()}})


@pytest.mark.parametrize('changes', [
    {'validator': 'ppt-images-v1'}, {'binding': 'raise RuntimeError("untrusted package")'},
    {'step': 'unregistered-step'}, {'name': 'paper_unregistered'},
])
def test_invalid_paper_binding_or_contract_never_loads_engine(monkeypatch, changes):
    def unexpected(_name):
        pytest.fail('Loaded a paper engine before authenticating its pinned contract')
    monkeypatch.setattr(paper_tools, 'load', unexpected)
    payload = request(**changes)
    with pytest.raises(ExternalToolError):
        gateway.call_external_tool(gateway.ExternalToolCallRequest(
            **payload, tool_name=changes.get('name', 'paper_prepare_inputs'), arguments={}))


def test_context_must_match_the_pinned_revision_and_step(monkeypatch):
    monkeypatch.setattr(paper_tools, 'load', lambda _name: lambda: {'should_not_execute': True})
    for value in [None, {'revision': 'other', 'step_id': 'write_outline'},
                  {'revision': 'revision', 'step_id': 'experiment'}]:
        with pytest.raises(ExternalToolError, match='matching Core-owned'):
            gateway.call_external_tool(gateway.ExternalToolCallRequest(
                **request(), tool_name='paper_prepare_inputs', arguments={}, trusted_context=value))
    with pytest.raises(ExternalToolError, match='declared tool schema'):
        gateway.call_external_tool(gateway.ExternalToolCallRequest(
            **request(), tool_name='paper_prepare_inputs', arguments={'__paper_context': {'revision': 'forged'}}))


def test_nonpaper_registry_survives_missing_paper_engine_in_fresh_process():
    script = r'''import base64, importlib.abc, json, sys, yaml
class BlockPaper(importlib.abc.MetaPathFinder):
    def find_spec(self, fullname, path=None, target=None):
        if fullname == 'lazymind.document_tools.paper.agent_tools':
            raise ImportError('Paper dependency deliberately unavailable')
sys.meta_path.insert(0, BlockPaper())
from lazymind.chat.workflow import external_tools as g
assert 'lazymind.document_tools.paper.agent_tools' not in sys.modules
w = {'runtime': {'execution': {'version': '1', 'steps': {'inspect': {
     'tools': [{'name': 'ppt_inspect_pptx', 'source': 'package'}]}}}},
     'tool_scripts': [{'path': 'scripts/external_tools.py', 'functions': ['ppt_inspect_pptx']}]}
files = {'workflow.yaml': yaml.safe_dump(w).encode(), 'scripts/external_tools.py': g.PPT_BINDING.encode()}
tree = g.package_tree_hash(files)
req = dict(workflow_id='owned-ppt', revision_id='fixed', step_id='inspect', tree_hash=tree,
           package={'tree_hash': tree, 'files': {k: base64.b64encode(v).decode() for k, v in files.items()}})
assert g.list_external_tools(g.ExternalToolRequest(**req))['tools'][0]['available']
assert 'lazymind.document_tools.paper.agent_tools' not in sys.modules
from lazymind.chat.workflow import paper_tools
try:
    paper_tools.load('paper_prepare_inputs')
except g.ExternalToolError as exc:
    assert exc.code == 'TOOL_ADAPTER_UNAVAILABLE'
else:
    raise AssertionError('Missing paper engine was accepted')
assert g.list_external_tools(g.ExternalToolRequest(**req))['tools'][0]['available']
print(json.dumps({'ppt_available': True, 'paper_loaded': False}))
'''
    result = subprocess.run([sys.executable, '-c', script], text=True, capture_output=True, timeout=30)
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout.splitlines()[-1]) == {'ppt_available': True, 'paper_loaded': False}
