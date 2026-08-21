from unittest.mock import MagicMock, patch

import lazyllm
import pytest
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
    result = save_artifacts([{
        'key': 'preview_html',
        'content': '<html></html>',
    }])  # type: ignore[typeddict-item]

    assert result['success'] is False
    assert 'uses content' in result['error']['reason']
    assert '"value":"<actual content>"' in result['error']['reason']


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
        result = _save_artifact(
            'preview_html', '<html></html>', content_type='text',
        )

    assert result['success'] is False
    assert 'publisher-owned' in result['error']['reason']
