import importlib.util
import sys
import types
from pathlib import Path
from unittest.mock import patch


ROOT = Path(__file__).parents[1] / 'lazymind'
saved_modules = {
    name: sys.modules.get(name)
    for name in ('lazymind', 'lazymind.workflow_policy', 'lazymind.chat.workflow_policy_shadow')
}
package = types.ModuleType('lazymind')
package.__path__ = [str(ROOT)]
sys.modules['lazymind'] = package
policy_spec = importlib.util.spec_from_file_location(
    'lazymind.workflow_policy', ROOT / 'workflow_policy.py',
)
assert policy_spec and policy_spec.loader
policy = importlib.util.module_from_spec(policy_spec)
sys.modules[policy_spec.name] = policy
policy_spec.loader.exec_module(policy)
shadow_spec = importlib.util.spec_from_file_location(
    'lazymind.chat.workflow_policy_shadow', ROOT / 'chat/workflow_policy_shadow.py',
)
assert shadow_spec and shadow_spec.loader
shadow = importlib.util.module_from_spec(shadow_spec)
sys.modules[shadow_spec.name] = shadow
shadow_spec.loader.exec_module(shadow)
for module_name, previous in saved_modules.items():
    if previous is None:
        sys.modules.pop(module_name, None)
    else:
        sys.modules[module_name] = previous


LAZYMIND_PROFILE = {
    'profile': 'lazymind',
    'advance_tools': ['advance_step', 'advance_step_and_hand_off'],
    'parallel_ready_steps': True,
    'handoff': True,
}


def test_decision_comparison_is_default_on():
    sink = {}
    with patch.dict('os.environ', {}, clear=True):
        trace = shadow.observe({'ready_steps': ['draft']}, LAZYMIND_PROFILE, sink,
                               source='test')
    assert trace is not None
    assert trace['authority'] == 'shared'


def test_shadow_records_structured_equivalent_trace_and_metrics():
    sink = {}
    projection = {
        'ready_steps': ['research', 'outline'],
        'approval_by_step': {'research': 'required', 'outline': 'required'},
    }
    with patch.dict('os.environ', {shadow.SHADOW_FLAG: 'true'}):
        trace = shadow.observe(projection, LAZYMIND_PROFILE, sink, source='golden')
    assert trace is not None
    assert trace['schema_version'] == 'workflow.shadow-trace.v1'
    assert trace['authority'] == 'shared'
    assert trace['equivalent'] is True
    assert trace['shared']['targets'] == ('research', 'outline')
    assert sink['workflow_policy_shadow_metrics'] == {
        'evaluated': 1, 'equivalent': 1, 'mismatch': 0,
    }
    assert shadow.equivalence_rate(sink['workflow_policy_shadow_metrics']) == 1.0


def test_golden_decisions_reach_pr3_equivalence_threshold():
    cases = [
        {'ready_steps': ['draft'], 'approval_by_step': {'draft': 'required'}},
        {'ready_steps': ['draft'], 'approval_by_step': {'draft': 'not_required'}},
        {'ready_steps': ['a', 'b'], 'approval_by_step': {'a': 'required', 'b': 'required'}},
        {'ready_steps': ['draft'], 'failed_steps': ['draft']},
        {'ready_steps': ['draft'], 'intent_tokens': ['continuous']},
        {'ready_steps': []},
    ]
    sink = {}
    with patch.dict('os.environ', {shadow.SHADOW_FLAG: '1'}):
        for case in cases:
            shadow.observe(case, LAZYMIND_PROFILE, sink, source='golden-suite')
    metrics = sink['workflow_policy_shadow_metrics']
    assert metrics['evaluated'] == len(cases)
    assert shadow.equivalence_rate(metrics) == 1.0


def test_codex_profile_never_selects_handoff():
    sink = {}
    profile = {
        'profile': 'codex', 'advance_tools': ['advance_step'],
        'parallel_ready_steps': True, 'handoff': False,
    }
    with patch.dict('os.environ', {shadow.SHADOW_FLAG: 'on'}):
        trace = shadow.observe({'ready_steps': ['draft']}, profile, sink, source='golden')
    assert trace is not None
    assert trace['shared']['tool'] == 'advance_step'
    assert trace['equivalent'] is True
