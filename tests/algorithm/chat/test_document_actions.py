from __future__ import annotations

import json
from pathlib import Path

import pytest
from lazyllm.tools.writer.provider import (
    WriterProviderCapabilityError,
    WriterProviderWriteOutcomeError,
)

from lazymind.document_tools import (
    DocumentActionError,
    document_action_specs,
    invoke_document_action,
    resolve_document_action,
)
from lazymind.document_tools import actions as document_actions


def test_v1_registry_has_exact_actions_phases_and_immutable_specs():
    specs = document_action_specs()

    assert {
        reference: set(phases)
        for reference, phases in specs.items()
    } == {
        "builtin:document.rewrite_selection.v1": {"preview", "execute"},
        "builtin:document.list_cross_reference_targets.v1": {"preview"},
        "builtin:document.update_cross_reference.v1": {"preview", "execute"},
        "builtin:document.render_document.v1": {"preview", "execute"},
        "builtin:document.save_document.v1": {"execute"},
        "builtin:document.sync_document.v1": {"execute"},
        "builtin:document.convert_document.v1": {"preview", "execute"},
        "builtin:document.write_document.v1": {"execute"},
        "builtin:document.wechat_draft_url.v1": {"preview"},
    }
    assert specs["builtin:document.sync_document.v1"]["execute"].external_side_effects
    assert all(
        not spec.durable_side_effects and not spec.external_side_effects
        for phases in specs.values()
        for phase, spec in phases.items()
        if phase == "preview"
    )
    with pytest.raises(TypeError):
        specs["builtin:document.render_document.v1"] = {}  # type: ignore[index]


def test_builtin_resolution_rejects_unknown_version_phase_and_wrong_action():
    with pytest.raises(DocumentActionError) as unknown:
        resolve_document_action("builtin:document.render_document.v2", "preview")
    assert unknown.value.error_code == "DOCUMENT_ACTION_UNAVAILABLE"
    assert unknown.value.status_code == 422

    with pytest.raises(DocumentActionError) as phase:
        resolve_document_action("builtin:document.save_document.v1", "preview")
    assert phase.value.error_code == "DOCUMENT_ACTION_UNAVAILABLE"

    with pytest.raises(DocumentActionError) as action:
        invoke_document_action(
            "builtin:document.render_document.v1",
            "preview",
            {},
            artifact="# title",
            action="save_document",
        )
    assert action.value.error_code == "DOCUMENT_ACTION_REFERENCE_INVALID"


def test_action_arguments_are_strict_and_runtime_fields_are_not_arguments():
    with pytest.raises(DocumentActionError) as unknown:
        invoke_document_action(
            "builtin:document.render_document.v1",
            "preview",
            {"unexpected": True},
            artifact="# title",
        )
    assert unknown.value.status_code == 422
    assert unknown.value.error_code == "WORKFLOW_ACTION_INVALID"

    with pytest.raises(DocumentActionError) as invalid:
        invoke_document_action(
            "builtin:document.rewrite_selection.v1",
            "preview",
            {
                "instruction": "rewrite",
                "selection": {"type": "markdown", "selected_text": 3},
            },
            artifact="# title\n\ntext",
            slot="draft_document",
        )
    assert invalid.value.status_code == 422


def test_render_and_save_actions_validate_their_fixed_results():
    markdown = "# Title\n\n## Section\n\nBody.\n"
    rendered = invoke_document_action(
        "builtin:document.render_document.v1",
        "preview",
        {},
        artifact={"data": markdown},
        slot="draft_document",
    )
    assert rendered["representation"] == "markdown"
    assert rendered["title"] == "Title"
    assert isinstance(rendered["numbering"], dict)

    saved = invoke_document_action(
        "builtin:document.save_document.v1",
        "execute",
        {"base_artifact": {"data": markdown}},
        artifact={"data": rendered["export_document"]},
        slot="draft_document",
    )
    assert saved["source_document"].rstrip() == rendered["document"].rstrip()
    assert saved["representation"] == "markdown"


def test_rewrite_execute_uses_exact_preview_without_second_model_call(
    monkeypatch, tmp_path,
):
    calls = []

    def fake_preview(document, instruction, selection, context, *, artifact_store):
        calls.append((document, instruction, selection, context))
        candidate = Path(artifact_store) / "candidate.md"
        candidate.write_text("# Title\n\nRewritten.\n", encoding="utf-8")
        return {
            "representation": "markdown",
            "results": [{"target": {"type": "block", "block_type": "paragraph"},
            "preview": {"old_text": "Original.", "new_text": "Rewritten."},
            "patch": {
                "type": "string_replace_set",
                "payload": {"replacements": [{"old_string": "Original.", "new_string": "Rewritten."}]},
            },
            }], "revised_document_md": str(candidate),
        }

    monkeypatch.setattr(
        "lazymind.document_tools.revision.preview_selection_rewrite", fake_preview
    )
    source = "# Title\n\nOriginal.\n"
    preview = invoke_document_action(
        "builtin:document.rewrite_selection.v1",
        "preview",
        {
            "instruction": "Improve it",
            "type": "markdown",
            "selection_ranges": [{"selected_text": "Original."}],
        },
        artifact={"data": source},
        artifact_store=str(tmp_path),
        slot="prd_document",
    )
    executed = invoke_document_action(
        "builtin:document.rewrite_selection.v1",
        "execute",
        {"commit_token": preview["commit"]["token"]},
        artifact={"data": source},
        artifact_store=str(tmp_path),
        slot="draft_document",
    )

    assert len(calls) == 1
    assert calls[0][1] == "Improve it"
    assert executed == {
        "representation": "markdown",
        "artifact": {
            "content_type": "text",
            "value": "# Title\n\nRewritten.\n",
        },
    }
    assert "/" not in json.dumps(executed)

    with pytest.raises(DocumentActionError) as stale:
        invoke_document_action(
            "builtin:document.rewrite_selection.v1",
            "execute",
            {"commit_token": preview["commit"]["token"]},
            artifact={"data": source + "changed"},
            artifact_store=str(tmp_path),
            slot="draft_document",
        )
    assert stale.value.error_code == "SELECTION_STALE"
    assert stale.value.status_code == 409


def test_markdown_cross_reference_actions_add_retarget_and_remove(tmp_path):
    source = (
        '# Document\n\n'
        '<a id="block-first"></a>\n## First\n\n'
        '<a id="block-second"></a>\n## Second\n\n'
        '<a id="block-diagram"></a>\n![Diagram](diagram.png)\n\n'
        'See this section and [old text](#block-deleted).\n'
    )
    listed = invoke_document_action(
        'builtin:document.list_cross_reference_targets.v1',
        'preview', {}, artifact=source,
    )
    assert [target['target_id'] for target in listed['targets']] == [
        'first', 'second', 'diagram',
    ]
    assert [target['type'] for target in listed['targets']] == [
        'heading', 'heading', 'image',
    ]
    assert listed['invalid_references'] == [{'target_id': 'deleted'}]

    added = invoke_document_action(
        'builtin:document.update_cross_reference.v1',
        'preview',
        {
            'operation': 'add',
            'selection': {'type': 'markdown', 'selected_text': 'this section'},
            'target_id': 'first',
        },
        artifact=source,
        artifact_store=str(tmp_path),
    )
    added_document = added['artifact']['value']
    assert '[this section](#block-first)' in added_document
    executed = invoke_document_action(
        'builtin:document.update_cross_reference.v1',
        'execute',
        {'commit_token': added['commit']['token']},
        artifact=source,
        artifact_store=str(tmp_path),
    )
    assert executed['artifact']['value'] == added_document

    retargeted = invoke_document_action(
        'builtin:document.update_cross_reference.v1',
        'preview',
        {
            'operation': 'retarget',
            'selection': {'type': 'markdown', 'selected_text': 'this section'},
            'target_id': 'second',
        },
        artifact=executed['artifact']['value'],
        artifact_store=str(tmp_path),
    )
    retargeted_document = retargeted['artifact']['value']
    assert '[this section](#block-second)' in retargeted_document

    removed = invoke_document_action(
        'builtin:document.update_cross_reference.v1',
        'preview',
        {
            'operation': 'remove',
            'selection': {'type': 'markdown', 'selected_text': 'this section'},
        },
        artifact=retargeted_document,
        artifact_store=str(tmp_path),
    )
    assert 'See this section and' in removed['artifact']['value']
    assert '#block-second' not in removed['artifact']['value']


def test_ir_cross_reference_action_uses_existing_internal_ref_spans(tmp_path):
    source = {
        'document_id': 'document-1',
        'blocks': [
            {'node_id': 'target', 'type': 'heading', 'content': 'Target'},
            {'node_id': 'body', 'type': 'paragraph', 'content': 'See target'},
        ],
    }
    preview = invoke_document_action(
        'builtin:document.update_cross_reference.v1',
        'preview',
        {
            'operation': 'add',
            'selection': {
                'type': 'ir', 'node_id': 'body', 'selected_text': 'target',
            },
            'target_id': 'target',
        },
        artifact=source,
        artifact_store=str(tmp_path),
    )

    spans = preview['artifact']['value']['blocks'][1]['spans']
    assert spans[1] == {
        'text': 'target',
        'style': {
            'link': {'type': 'internal_ref', 'target_node_id': 'target'},
        },
    }


def test_invalid_handler_result_is_an_upstream_failure(monkeypatch):
    spec = resolve_document_action(
        "builtin:document.render_document.v1", "preview"
    )
    invalid = type(spec)(
        reference=spec.reference,
        action=spec.action,
        version=spec.version,
        phase=spec.phase,
        arguments_model=spec.arguments_model,
        result_model=spec.result_model,
        handler=lambda **_kwargs: {"representation": "markdown"},
    )
    monkeypatch.setitem(
        document_actions._BUILTIN_ACTIONS[spec.reference], "preview", invalid
    )

    with pytest.raises(DocumentActionError) as error:
        invoke_document_action(
            spec.reference, "preview", {}, artifact="# Title"
        )
    assert error.value.error_code == "WORKFLOW_ACTION_RESULT_INVALID"
    assert error.value.status_code == 502


def test_sync_action_forwards_provider_neutral_arguments(monkeypatch, tmp_path):
    captured = {}

    def fake_sync_document(**kwargs):
        captured.update(kwargs)
        return {
            "success": True,
            "changed": True,
            "provider_synced": True,
            "patch_result": {"success": True},
            "persisted_document": {"document_id": "document-1"},
            "representation": "ir",
            "provider": "notion",
            "write_result": {"revision": "remote-2"},
            "target_document": {"doc_id": "document-1", "adapter": "notion"},
        }

    monkeypatch.setattr(
        "lazymind.document_tools.resources.sync_document", fake_sync_document
    )
    result = invoke_document_action(
        "builtin:document.sync_document.v1",
        "execute",
        {
            "source_document": {"document_id": "document-1"},
            "revised_document": {"document_id": "document-1"},
        },
        artifact_store=str(tmp_path),
        slot="draft_document",
    )

    assert captured == {
        "source_document": {"document_id": "document-1"},
        "revised_document": {"document_id": "document-1"},
        "media_assets": None,
        "artifact_store": str(tmp_path),
    }
    assert result["provider"] == "notion"


def test_conversion_and_write_actions_are_explicitly_composable(monkeypatch):
    calls = []
    converted = {
        "provider": "notion",
        "format": "notion_blocks",
        "content": [{"type": "paragraph"}],
        "source_document": {"document_id": "local-1"},
        "media_references": {},
    }

    def fake_convert(content, **arguments):
        calls.append(("convert", content, arguments))
        return converted

    def fake_write(**arguments):
        calls.append(("write", arguments))
        return {
            "success": True,
            "changed": True,
            "provider_synced": True,
            "patch_result": {"success": True},
            "persisted_document": {"document_id": "remote-1"},
            "representation": "ir",
            "provider": "notion",
            "write_result": {"doc_id": "remote-1"},
            "target_document": {"adapter": "notion", "doc_id": "remote-1"},
        }

    monkeypatch.setattr("lazymind.document_tools.resources.convert_document", fake_convert)
    monkeypatch.setattr("lazymind.document_tools.resources.write_document", fake_write)
    conversion = invoke_document_action(
        "builtin:document.convert_document.v1",
        "preview",
        {"provider": "notion"},
        artifact={"data": "# Draft"},
    )
    result = invoke_document_action(
        "builtin:document.write_document.v1",
        "execute",
        {"converted_document": conversion},
    )

    assert calls[0] == (
        "convert",
        "# Draft",
        {
            "provider": "notion",
            "output_format": "native",
            "target_document": None,
            "media_assets": None,
            "template": "",
        },
    )
    assert calls[1][0] == "write"
    assert calls[1][1]["converted_document"] == converted
    assert result["target_document"]["doc_id"] == "remote-1"


@pytest.mark.parametrize(
    ("code", "status"),
    [
        ("REVISION_CONFLICT", 409),
        ("FEISHU_ACCOUNT_REQUIRED", 401),
        ("PROVIDER_PERMISSION_DENIED", 403),
        ("PROVIDER_CAPABILITY_UNSUPPORTED", 422),
        ("PROVIDER_WRITE_OUTCOME_AMBIGUOUS", 502),
    ],
)
def test_structured_action_error_policy(code, status):
    error = ValueError("failure")
    error.error_code = code
    spec = resolve_document_action(
        "builtin:document.render_document.v1", "preview"
    )
    original = document_actions._BUILTIN_ACTIONS[spec.reference]["preview"]
    failing = type(spec)(
        reference=spec.reference,
        action=spec.action,
        version=spec.version,
        phase=spec.phase,
        arguments_model=spec.arguments_model,
        result_model=spec.result_model,
        handler=lambda **_kwargs: (_ for _ in ()).throw(error),
    )
    document_actions._BUILTIN_ACTIONS[spec.reference]["preview"] = failing
    try:
        with pytest.raises(DocumentActionError) as raised:
            invoke_document_action(spec.reference, "preview", {}, artifact="# Title")
        assert raised.value.error_code == code
        assert raised.value.status_code == status
        assert raised.value.retryable is False
    finally:
        document_actions._BUILTIN_ACTIONS[spec.reference]["preview"] = original


def test_provider_error_fields_are_preserved_as_structured_action_error():
    class ProviderError(RuntimeError):
        code = "GITHUB_PERMISSION_DENIED"
        status_code = 403
        retryable = False

    spec = resolve_document_action(
        "builtin:document.render_document.v1", "preview"
    )
    original = document_actions._BUILTIN_ACTIONS[spec.reference]["preview"]
    failing = type(spec)(
        reference=spec.reference,
        action=spec.action,
        version=spec.version,
        phase=spec.phase,
        arguments_model=spec.arguments_model,
        result_model=spec.result_model,
        handler=lambda **_kwargs: (_ for _ in ()).throw(ProviderError("denied")),
    )
    document_actions._BUILTIN_ACTIONS[spec.reference]["preview"] = failing
    try:
        with pytest.raises(DocumentActionError) as raised:
            invoke_document_action(spec.reference, "preview", {}, artifact="# Title")
        assert raised.value.error_code == "GITHUB_PERMISSION_DENIED"
        assert raised.value.status_code == 403
        assert raised.value.retryable is False
    finally:
        document_actions._BUILTIN_ACTIONS[spec.reference]["preview"] = original


@pytest.mark.parametrize(
    ("provider_error", "expected_code", "expected_status", "expected_details"),
    [
        (
            WriterProviderCapabilityError("wechat", "append"),
            "PROVIDER_CAPABILITY_UNSUPPORTED",
            422,
            {"provider": "wechat", "capability": "append"},
        ),
        (
            WriterProviderWriteOutcomeError("notion", "replace"),
            "PROVIDER_WRITE_OUTCOME_AMBIGUOUS",
            502,
            {"provider": "notion", "operation": "replace"},
        ),
    ],
)
def test_official_provider_errors_preserve_non_retryable_action_contract(
    provider_error, expected_code, expected_status, expected_details
):
    spec = resolve_document_action(
        "builtin:document.render_document.v1", "preview"
    )
    original = document_actions._BUILTIN_ACTIONS[spec.reference]["preview"]
    failing = type(spec)(
        reference=spec.reference,
        action=spec.action,
        version=spec.version,
        phase=spec.phase,
        arguments_model=spec.arguments_model,
        result_model=spec.result_model,
        handler=lambda **_kwargs: (_ for _ in ()).throw(provider_error),
    )
    document_actions._BUILTIN_ACTIONS[spec.reference]["preview"] = failing
    try:
        with pytest.raises(DocumentActionError) as raised:
            invoke_document_action(spec.reference, "preview", {}, artifact="# Title")
        assert raised.value.error_code == expected_code
        assert raised.value.status_code == expected_status
        assert raised.value.retryable is False
        assert raised.value.details == expected_details
    finally:
        document_actions._BUILTIN_ACTIONS[spec.reference]["preview"] = original


@pytest.mark.parametrize('output_format', ['markdown', 'latex', 'text'])
def test_portable_conversion_uses_unsaved_snapshot_without_platform_io(monkeypatch, output_format):
    def unexpected(*args, **kwargs):
        pytest.fail('Portable conversion must not resolve a platform')

    monkeypatch.setattr('lazymind.document_tools.resources.get_writer_provider', unexpected)
    result = invoke_document_action(
        'builtin:document.convert_document.v1', 'preview',
        {'output_format': output_format, 'document': '# Latest\n\nUnsaved $x_1$'},
        artifact={'data': '# Old content'},
    )
    assert result['provider'] == ''
    assert result['format'] == output_format
    assert 'Latest' in result['content'] and 'Old content' not in result['content']
    assert 'provider_synced' not in result


def test_conversion_snapshot_is_content_not_a_file_locator(tmp_path):
    secret = tmp_path / 'private.md'
    secret.write_text('DO NOT READ', encoding='utf-8')
    result = invoke_document_action(
        'builtin:document.convert_document.v1', 'preview',
        {'output_format': 'markdown', 'document': str(secret)},
        artifact={'data': '# Original'},
    )
    assert 'DO NOT READ' not in result['content']


def test_native_conversion_rejects_copy_snapshot():
    with pytest.raises(DocumentActionError):
        invoke_document_action(
            'builtin:document.convert_document.v1', 'preview',
            {'provider': 'notion', 'document': '# Unsaved'},
            artifact={'data': '# Original'},
        )
