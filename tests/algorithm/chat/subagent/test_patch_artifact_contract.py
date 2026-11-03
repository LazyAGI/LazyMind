import json

import pytest
from lazyllm.tools.agent import ToolExecutionError, ToolManager
from lazymind.chat.engine.subagent import tools
from lazymind.chat.engine.subagent.context import SubAgentContext


@pytest.fixture
def draft(tmp_path, monkeypatch):
    ctx = SubAgentContext('task', 'chat', 'ordinary', 'edit', {}, str(tmp_path), [], ['report'], None, lambda e: None)
    ctx.write_draft('report', 'json', '{"summary":"before","items":[1]}', pending_commit=False)
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    return ctx


@pytest.mark.parametrize('legacy', [False, True])
@pytest.mark.parametrize('kind,payload', [
    ('json_patch', [{'op': 'replace', 'path': '/summary', 'value': 'after'}]),
    ('json_merge', {'summary': 'after'}),
    ('str_replace', {'old_str': 'before', 'new_str': 'after'}),
])
def test_native_and_legacy_patches_edit_the_same_draft(draft, legacy, kind, payload):
    supplied = json.dumps(payload) if legacy else payload
    # Exercise the public tool schema as well as the implementation.
    tool = ToolManager([tools.patch_artifact]).all_tools[0]
    arguments = tool._validate_input({'key': 'report', 'patch_type': kind, 'patch': supplied})
    tools.patch_artifact(**arguments)
    assert json.loads(draft.read_draft('report')[0])['summary'] == 'after'


@pytest.mark.parametrize('payload,match', [
    ([{'op': 'add', 'path': '/missing/-', 'value': 2}], r'patch\[0\].*/missing/-.*parent/member'),
    ([{'op': 'replace', 'path': '/summary'}], r'patch\[0\].value'),
    ([{'op': 'unknown', 'path': '/summary'}], r'patch\[0\].op'),
    ([{'op': [], 'path': '/summary'}], r'patch\[0\].op.*got list'),
    ([{'op': 'copy', 'path': '/summary'}], r'patch\[0\].from'),
])
def test_invalid_operation_identifies_the_location_and_preserves_draft(draft, payload, match):
    before = draft.read_draft('report')
    with pytest.raises(ToolExecutionError, match=match):
        tools.patch_artifact('report', payload, 'json_patch')
    assert draft.read_draft('report') == before


def test_later_operation_failure_does_not_apply_the_first(draft):
    before = draft.read_draft('report')
    with pytest.raises(ToolExecutionError, match=r'patch\[1\]'):
        tools.patch_artifact('report', [
            {'op': 'replace', 'path': '/summary', 'value': 'changed'},
            {'op': 'remove', 'path': '/missing/item'},
        ], 'json_patch')
    assert draft.read_draft('report') == before
