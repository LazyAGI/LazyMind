import importlib.util
import sys
from pathlib import Path


_PATH = Path(__file__).parents[1] / 'lazymind/workflow_policy.py'
_SPEC = importlib.util.spec_from_file_location('workflow_policy', _PATH)
assert _SPEC and _SPEC.loader
policy = importlib.util.module_from_spec(_SPEC)
sys.modules[_SPEC.name] = policy
_SPEC.loader.exec_module(policy)


def test_lazymind_can_handoff_but_codex_is_synchronous():
    projection = {'status': 'running', 'ready_steps': ['draft']}
    lazy = policy.decide(projection, {'advance_tools': ['advance_step'], 'handoff': True})
    codex = policy.decide(projection, {'advance_tools': ['advance_step'], 'handoff': False})
    assert lazy.tool == 'advance_step_and_hand_off'
    assert codex.tool == 'advance_step'


def test_shadow_trace_records_equivalence():
    decision = policy.Decision('observe', 'get_workflow_state', None)
    assert policy.shadow_trace(decision, decision)['equivalent']


def test_missing_input_and_resume_precede_advancement():
    assert policy.decide({'missing_inputs': ['topic']}, {}).action == 'request_input'
    assert policy.decide({'status': 'stopped'}, {}, 'resume').tool == 'resume_workflow'
