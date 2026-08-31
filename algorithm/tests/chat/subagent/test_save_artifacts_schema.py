from unittest.mock import MagicMock, patch

import lazyllm
import pytest
from lazyllm.tools.agent import ToolExecutionError
from pydantic import ValidationError
from lazyllm.tools.agent.toolsManager import ToolManager
from lazymind.chat.engine.subagent.tools import (
    _resolve_list_index_from_sort_order,
    _save_artifact,
    save_artifacts,
)


def _save_tool():
    return ToolManager([save_artifacts]).all_tools[0]


def test_save_artifacts_schema_exposes_required_item_fields():
    schema = _save_tool().params_schema.model_json_schema()
    item_ref = schema['properties']['artifacts']['items']['$ref']
    item_schema = schema['$defs'][item_ref.rsplit('/', 1)[-1]]

    assert item_schema['required'] == ['key', 'value']
    assert 'content' not in item_schema['properties']
    assert item_schema['properties']['content_type']['enum'] == [
        'text', 'json', 'image', 'file', 'file_list',
    ]


def test_save_artifacts_schema_rejects_content_instead_of_value():
    tool = _save_tool()

    assert tool._validate_input({
        'artifacts': [{'key': 'preview_html', 'value': '<html></html>'}],
    }) == {
        'artifacts': [{'key': 'preview_html', 'value': '<html></html>'}],
    }
    with pytest.raises(ValidationError, match='artifacts.0.value'):
        tool._validate_input({
            'artifacts': [{'key': 'preview_html', 'content': '<html></html>'}],
        })


def test_save_artifacts_runtime_error_shows_copyable_value_example():
    with pytest.raises(ToolExecutionError) as captured:
        save_artifacts([{
            'key': 'preview_html',
            'content': '<html></html>',
        }])  # type: ignore[typeddict-item]

    assert 'uses content' in str(captured.value)
    assert '"value":"<actual content>"' in str(captured.value)


def test_sort_order_uses_durable_slot_order_during_workflow_rewind():
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {'workflow_session_id': 'session-1'}
    client = MagicMock()
    client.get_slot_order.return_value.result = {
        'order_list': [7, 3, 11],
        'order_version': 4,
    }
    try:
        with patch(
            'lazymind.chat.engine.subagent.tools._workflow_client',
            return_value=client,
        ):
            assert _resolve_list_index_from_sort_order('preview_html', 2) == (3, None)
    finally:
        if previous is None:
            lazyllm.globals.pop('agentic_config', None)
        else:
            lazyllm.globals['agentic_config'] = previous


def test_ppt_preview_slots_reject_direct_model_saves():
    ctx = MagicMock()
    ctx.params = {'workflow_runtime': {'publisher_owned_slots': ['preview_html']}}
    ctx.output_slots = ['preview_html']

    with patch(
        'lazymind.chat.engine.subagent.tools.require_context',
        return_value=ctx,
    ):
        with pytest.raises(ToolExecutionError, match='publisher-owned'):
            _save_artifact(
                'preview_html', '<html></html>', content_type='text',
            )


def test_package_publisher_can_emit_pre_resolved_list_index():
    ctx = MagicMock()
    ctx.params = {'workflow_runtime': {'publisher_owned_slots': ['preview_html']}}
    ctx.output_slots = ['preview_html']
    ctx.next_artifact_seq.return_value = 1

    with patch(
        'lazymind.chat.engine.subagent.tools.require_context',
        return_value=ctx,
    ):
        result = _save_artifact(
            'preview_html',
            '<html></html>',
            content_type='text',
            internal_publish=True,
            publisher_list_index=3,
        )

    assert result['status'] == 'ok'
    emitted = ctx.emit.call_args.args[0]
    assert emitted['value']['list_index'] == 3


def test_model_facing_save_cannot_set_publisher_list_index():
    ctx = MagicMock()
    ctx.params = {'workflow_runtime': {}}
    ctx.output_slots = ['result']

    with patch(
        'lazymind.chat.engine.subagent.tools.require_context',
        return_value=ctx,
    ):
        with pytest.raises(ToolExecutionError, match='reserved for package publisher'):
            _save_artifact(
                'result',
                'text',
                internal_publish=False,
                publisher_list_index=0,
            )
