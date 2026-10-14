from unittest.mock import MagicMock, patch

import lazyllm
import pytest
from jsonschema import Draft202012Validator
from lazyllm.tools.agent import ToolExecutionError
from pydantic import ValidationError
from lazyllm.tools.agent.toolsManager import ToolManager, _build_tool_desc
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


def test_save_artifacts_export_omits_only_any_value_title_inside_reference():
    schema = _build_tool_desc(_save_tool())['function']['parameters']
    Draft202012Validator.check_schema(schema)
    item_ref = schema['properties']['artifacts']['items']['$ref']
    item_schema = schema['$defs'][item_ref.rsplit('/', 1)[-1]]
    value_schema = item_schema['properties']['value']

    # A title-only Any inside this $ref was rejected by Kimi as infinite recursion.
    assert value_schema == {}
    assert item_schema['properties']['key'] == {'title': 'Key', 'type': 'string'}
    assert item_schema['additionalProperties'] is False


@pytest.mark.parametrize('value', [
    '', '故宫 <html></html>', 0, 2**64, -1.5, True, False, None, {}, [],
    {'title': '保留字段', 'nested': [None, False, {'value': [1, '文物']}]},
    [1, 'text', None, {'additional': {'arbitrary': True}}],
])
def test_save_artifacts_json_values_survive_export_and_runtime_validation(value):
    tool = _save_tool()
    payload = {'artifacts': [{'key': 'report', 'value': value}]}
    schema = _build_tool_desc(tool)['function']['parameters']
    Draft202012Validator(schema).validate(payload)

    for validated in (
        tool._validate_input(payload),
        tool.params_schema.model_validate(payload, strict=True).model_dump(),
    ):
        actual = validated['artifacts'][0]['value']
        assert actual == value
        assert type(actual) is type(value)


def test_save_artifacts_schema_override_preserves_python_any_validation():
    value = object()
    tool = _save_tool()
    payload = {'artifacts': [{'key': 'report', 'value': value}]}

    assert tool._validate_input(payload)['artifacts'][0]['value'] is value
    assert tool.params_schema.model_validate(payload, strict=True).artifacts[0]['value'] is value


@pytest.mark.parametrize('item', [
    {'value': 'missing key'},
    {'key': 'report'},
    {'key': 'report', 'value': 'text', 'content_type': 'unsupported'},
    {'key': 'report', 'value': 'text', 'sort_order': []},
    {'key': 'report', 'value': 'text', 'unknown_field': 'unexpected'},
])
def test_save_artifacts_schema_override_preserves_other_field_validation(item):
    tool = _save_tool()
    payload = {'artifacts': [item]}
    assert not Draft202012Validator(
        _build_tool_desc(tool)['function']['parameters'],
    ).is_valid(payload)
    with pytest.raises(ValidationError):
        tool._validate_input(payload)
    with pytest.raises(ValidationError):
        tool.params_schema.model_validate(payload, strict=True)


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


def test_save_artifacts_runtime_rejects_missing_value():
    with pytest.raises(ToolExecutionError):
        save_artifacts([{
            'key': 'preview_html',
            'content': '<html></html>',
        }])  # type: ignore[typeddict-item]


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
