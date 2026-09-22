import json
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import pytest
from lazyllm.tools.writer.data_models import TargetDocument, WriterBlock, WriterDocument
from lazyllm.tools.writer.provider import WriterProviderDocument

from lazymind.chat.engine.tools import (
    WriterCreateToolkit as RegisteredWriterCreateToolkit,
)
from lazymind.chat.engine.tools import (
    WriterRevisionToolkit as RegisteredWriterRevisionToolkit,
)
from lazymind.document_tools import (
    DocumentResourceToolkit,
    DocumentRevisionToolkit,
    DocumentWritingToolkit,
    WriterCreateToolkit,
    WriterResourceToolkit,
    WriterRevisionToolkit,
    document_action_names,
    get_document_action,
    inspect_document,
    list_document_providers,
    register_document_action,
)
from lazymind.document_tools.artifacts import (
    WriterArtifactCapabilities,
    _normalize_streamed_markdown_section,
    lmd_to_markdown,
    markdown_to_lmd,
    markdown_to_writer_document,
)
from lazymind.document_tools.resources import WriterResourceCapabilities
from lazymind.document_tools import resources as document_resources
from lazymind.document_tools.references import bind_cross_reference_targets
from lazymind.document_tools.revision import WriterRevisionCapabilities
from lazymind.document_tools.writing import WriterWritingCapabilities


WRITING_CHAT_APIS = {
    "build_writing_task",
    "build_resources",
    "profile_resources",
    "create_writing_context",
    "prepare_outline",
    "generate_outline",
    "generate_rewrite_outline",
    "generate_rewrite_section_instructions",
    "generate_section_instructions",
    "generate_draft_section",
    "generate_draft_section_markdown",
    "generate_draft_blocks",
    "generate_draft_blocks_markdown",
    "generate_draft_document",
    "generate_draft_document_markdown",
    "update_writing_context",
    "check_consistency",
    "generate_final_document",
    "render_markdown",
}
REVISION_CHAT_APIS = {
    "build_revise_task",
    "build_revision_task",
    "locate_revision_target",
    "generate_modify_plan",
    "build_revision_visual_plan",
    "generate_patch_set",
    "generate_string_replace_set",
    "plan_revision",
    "validate_patch_set",
    "apply_patch",
    "apply_string_replace",
    "apply_revision",
}
RESOURCE_CHAT_APIS = {
    "load_document",
    "create_document",
    "publish_revision",
    "convert_document",
    "write_document",
}
WORKFLOW_ONLY_APIS = {
    "collect_available_media",
    "resolve_visual_needs",
    "materialize_acquired_media",
    "stream_outline",
    "execute_writing_subtasks",
    "stream_draft_blocks_ir",
    "stream_draft_blocks_markdown",
    "resolve_create_target",
    "prepare_markdown_for_editor",
}


def test_writing_subtask_progress_keeps_workflow_host_across_threads(monkeypatch):
    from lazymind.document_tools import execution

    progress = []

    class ThreadedWriterCreateToolkit:
        def execute_writing_subtasks(self, **kwargs):
            with ThreadPoolExecutor(max_workers=1) as executor:
                executor.submit(
                    kwargs['on_progress'],
                    [{'subtask_id': 'research', 'status': 'running'}],
                ).result()
            return '{}'

    monkeypatch.setattr(execution, '_read_json_string', lambda _path: '{}')
    monkeypatch.setattr(
        execution,
        '_save_writer_document',
        lambda *_args, **_kwargs: '/tmp/outline.lmd',
    )

    result = execution.invoke(
        {
            'WriterCreateToolkit': ThreadedWriterCreateToolkit,
            '_emit_writer_progress': lambda phase, **details: progress.append(
                (phase, details)
            ),
        },
        '_writer_execute_writing_subtasks',
        {
            'outline_path': '/tmp/outline.lmd',
            'writing_context_path': '/tmp/writing-context.json',
        },
    )

    assert result == '/tmp/outline.lmd'
    assert progress == [
        (
            '正在执行写作子任务',
            {'writing_subtasks': [{'subtask_id': 'research', 'status': 'running'}]},
        )
    ]


def test_chat_registration_uses_shared_document_toolkits():
    assert RegisteredWriterCreateToolkit is WriterCreateToolkit
    assert DocumentWritingToolkit is WriterCreateToolkit
    assert DocumentRevisionToolkit is WriterRevisionToolkit
    assert DocumentResourceToolkit is WriterResourceToolkit
    assert RegisteredWriterRevisionToolkit is WriterRevisionToolkit


def test_document_inspection_distinguishes_supported_representations():
    markdown = inspect_document('Short prose without headings.', 'text/markdown')
    writer_ir = inspect_document(WriterDocument(
        document_id='document-1',
        provider_binding={'provider': 'notion'},
    ).model_dump())

    assert markdown['representation'] == 'markdown'
    assert markdown['features']['provider_binding'] is False
    assert writer_ir['representation'] == 'ir'
    assert writer_ir['features']['provider_binding'] is True
    assert inspect_document('ordinary status text')['is_document'] is False
    assert inspect_document({'status': 'done'})['is_document'] is False
    with pytest.raises(ValueError, match='does not match the Writer IR schema'):
        inspect_document({'status': 'done'}, 'application/vnd.lazymind.writer+json')


def test_document_provider_projection_matches_registered_adapters():
    providers = {provider['id']: provider['capabilities']
                 for provider in list_document_providers()}

    assert set(providers) == {
        'feishu', 'github', 'googledrive', 'notion', 'obsidian', 'wechat',
    }
    assert providers['googledrive'] == ['load']
    assert 'append' in providers['github']
    assert 'append' not in providers['wechat']


def test_chat_tool_calls_work_without_a_workflow_context():
    from lazyllm.tools.agent import ToolManager

    manager = ToolManager([RegisteredWriterCreateToolkit(), RegisteredWriterRevisionToolkit()])

    def call(name, **arguments):
        result, = manager([{
            'id': name, 'type': 'function',
            'function': {'name': name, 'arguments': arguments},
        }])
        assert result['ok'], result
        return json.loads(result['value'])

    task = call('WriterCreateToolkit_build_writing_task', query='写一篇800字的报告')
    assert task['task_type'] == 'write'
    assert task['constraints']['target_chars'] == 800
    context = call(
        'WriterCreateToolkit_create_writing_context',
        writing_task_json=json.dumps(task),
    )
    assert context['context_id']
    revised = call(
        'WriterRevisionToolkit_build_revision_task',
        query='润色报告', writer_document_json='# 报告\n\n正文。',
    )
    assert revised['task_type'] == 'revise'


@pytest.mark.parametrize('response', [
    '# Title\n\nRevised.',
    '```markdown\n# Title\n\nRevised.\n```',
    'Here is the revision:\n# Title\n\nRevised.',
])
def test_complete_markdown_revision_uses_shared_tool_once(monkeypatch, tmp_path, response):
    from lazymind.document_tools import revision

    calls = []

    class Revision:
        def __init__(self, **kwargs):
            assert kwargs['artifact_store'] == str(tmp_path)

        def _call_llm_text(self, prompt):
            calls.append(prompt)
            return response

    monkeypatch.setattr(revision, 'WriterRevisionTools', Revision)
    monkeypatch.setattr(revision, 'AutoModel', lambda **kwargs: object())
    result = revision.revise_markdown_document(
        '# Title\n\nOriginal.', 'Polish',
        constraints='Only use SRC-001.', artifact_store=str(tmp_path),
    )
    assert result == '# Title\n\nRevised.'
    assert len(calls) == 1
    assert 'SRC-001' in calls[0]
    assert 'Original.' in calls[0]


def test_complete_markdown_revision_rejects_empty_result(monkeypatch, tmp_path):
    from types import SimpleNamespace
    from lazymind.document_tools import revision

    monkeypatch.setattr(revision, 'AutoModel', lambda **kwargs: object())
    monkeypatch.setattr(
        revision, 'WriterRevisionTools',
        lambda **kwargs: SimpleNamespace(_call_llm_text=lambda prompt: ''),
    )
    with pytest.raises(ValueError, match='no revised Markdown'):
        revision.revise_markdown_document('Original.', 'Polish', artifact_store=str(tmp_path))


def test_document_action_registry_is_explicit_and_deterministic():
    def preview():
        return "preview"

    register_document_action("test_document_action", "preview", preview)

    assert get_document_action("test_document_action", "preview") is preview
    assert "test_document_action" in document_action_names()

    with pytest.raises(ValueError, match="already registered"):
        register_document_action("test_document_action", "preview", preview)

    with pytest.raises(ValueError, match="unsupported document action phase"):
        register_document_action("test_document_action", "apply", preview)


def test_concrete_toolkits_own_disjoint_capability_sets():
    assert issubclass(WriterCreateToolkit, WriterWritingCapabilities)
    assert issubclass(WriterCreateToolkit, WriterArtifactCapabilities)
    assert not issubclass(WriterCreateToolkit, WriterRevisionCapabilities)
    assert not issubclass(WriterCreateToolkit, WriterResourceCapabilities)

    assert issubclass(WriterRevisionToolkit, WriterRevisionCapabilities)
    assert not issubclass(WriterRevisionToolkit, WriterWritingCapabilities)
    assert not issubclass(WriterRevisionToolkit, WriterResourceCapabilities)

    assert issubclass(WriterResourceToolkit, WriterResourceCapabilities)
    assert not issubclass(WriterResourceToolkit, WriterWritingCapabilities)
    assert not issubclass(WriterResourceToolkit, WriterRevisionCapabilities)


def test_all_45_capabilities_have_one_physical_owner():
    owners = (
        WriterWritingCapabilities,
        WriterArtifactCapabilities,
        WriterRevisionCapabilities,
        WriterResourceCapabilities,
    )
    owned = {
        owner: {
            name
            for name, value in vars(owner).items()
            if not name.startswith("_") and callable(value)
        }
        for owner in owners
    }

    resource_workflow_apis = {
        "resolve_create_target",
        "prepare_markdown_for_editor",
    }
    assert owned[WriterWritingCapabilities] == (
        WRITING_CHAT_APIS - {"render_markdown"}
    ) | (WORKFLOW_ONLY_APIS - resource_workflow_apis)
    assert owned[WriterArtifactCapabilities] == {"render_markdown"}
    assert owned[WriterRevisionCapabilities] == REVISION_CHAT_APIS
    assert owned[WriterResourceCapabilities] == (
        RESOURCE_CHAT_APIS | resource_workflow_apis
    )
    assert sum(len(names) for names in owned.values()) == 45


def test_chat_toolkit_exposure_remains_the_36_tool_snapshot():
    assert set(WriterCreateToolkit.__public_apis__) == WRITING_CHAT_APIS
    assert set(WriterRevisionToolkit.__public_apis__) == REVISION_CHAT_APIS
    assert set(WriterResourceToolkit.__public_apis__) == RESOURCE_CHAT_APIS
    assert (
        sum(
            map(
                len,
                (
                    WriterCreateToolkit.__public_apis__,
                    WriterRevisionToolkit.__public_apis__,
                    WriterResourceToolkit.__public_apis__,
                ),
            )
        )
        == 36
    )
    assert WORKFLOW_ONLY_APIS.isdisjoint(WriterCreateToolkit.__public_apis__)
    assert WORKFLOW_ONLY_APIS.isdisjoint(WriterResourceToolkit.__public_apis__)


def test_markdown_heading_normalization_accepts_tab_separator():
    instruction = type("Instruction", (), {"section_title": "标题"})()
    assert (
        _normalize_streamed_markdown_section("##\t标题\n\n正文", instruction)
        == "## 标题\n\n正文"
    )


def test_provider_locator_stops_at_ascii_whitespace(monkeypatch):
    captured = []

    class FakeProvider:
        def resolve(self, locator):
            captured.append(locator)
            return type("Target", (), {"uri": locator, "adapter": "fake", "meta": {}})()

    monkeypatch.setattr(
        document_resources, "match_writer_provider", lambda _locator: FakeProvider()
    )

    targets = document_resources.resolve_provider_targets(
        "读取 fake://document-id 然后继续写作"
    )

    assert captured == ["fake://document-id"]
    assert len(targets) == 1


def test_markdown_lmd_conversion_uses_existing_writer_rules():
    fixture_root = Path(__file__).parent / "fixtures" / "document_conversion"
    markdown = (fixture_root / "basic.md").read_text(encoding="utf-8")

    document = markdown_to_writer_document(
        markdown,
        document_id="document-1",
        stage="draft",
    )
    lmd = markdown_to_lmd(
        markdown,
        document_id="document-1",
        stage="draft",
    )
    envelope = json.loads(lmd)

    assert document.document_id == "document-1"
    assert document.stage == "draft"
    assert document.title == "标题"
    expected_lmd = json.loads(
        (fixture_root / "basic.lmd.json").read_text(encoding="utf-8")
    )
    assert {key: value for key, value in envelope.items() if key != "meta"} == expected_lmd
    assert envelope["meta"]["created_by"] == "lazyllm-writer-conversion"
    assert lmd_to_markdown(lmd) == (
        fixture_root / "basic.roundtrip.md"
    ).read_text(encoding="utf-8")


def test_cross_reference_binding_discovers_and_refreshes_all_targets():
    instructions = [
        {
            "meta": {
                "outline_node_id": "section-1",
                "cross_references": [{"target": "section-2"}],
                "cross_reference_targets": ["stale"],
            }
        },
        {"meta": {"outline_node_id": "section-2"}},
    ]

    bind_cross_reference_targets(instructions)

    assert instructions[0]["meta"]["cross_reference_targets"] == [
        "section-1",
        "section-2",
    ]
    assert instructions[1]["meta"]["cross_reference_targets"] == [
        "section-1",
        "section-2",
    ]


def _bound_document(provider="fake"):
    return WriterDocument(
        document_id="local-1",
        title="Draft",
        stage="final",
        revision="remote-1",
        provider_binding={
            "provider": provider,
            "document_id": "remote-1",
            "uri": f"{provider}:/remote-1",
        },
        metadata={
            "source": {"adapter": provider, "uri": f"{provider}:/remote-1"},
            "provider_metadata": {"remote": True},
            "block_count": 1,
            "source_block_count": 1,
            "semantic": "preserved",
        },
        blocks=[WriterBlock(
            node_id="body",
            type="paragraph",
            content="before",
            provider_binding={"provider": provider, "block_id": "block-1"},
            provider_payload={"remote": True},
        )],
    )


def test_conversion_is_pure_and_cross_provider_content_is_detached(monkeypatch):
    calls = []
    class FakeProvider:
        def convert_document(self, content, *, target=None, media_assets=None):
            return self.convert_document_with_template(
                content, target=target, media_assets=media_assets,
            )

        def convert_document_with_template(
            self, content, *, target=None, media_assets=None, template=None,
        ):
            calls.append((content, target, media_assets))
            return WriterProviderDocument(
                provider="destination",
                format="fake_blocks",
                content=[{"text": content.blocks[0].content}],
                source_document=content,
            )

    monkeypatch.setattr(
        document_resources, "get_writer_provider", lambda _provider: FakeProvider()
    )
    result = document_resources.convert_document(
        _bound_document("source").model_dump(), "destination"
    )

    detached = calls[0][0]
    assert detached.revision is None
    assert detached.provider_binding == {}
    assert detached.metadata.get("source") is None
    assert detached.metadata.get("provider_metadata") is None
    assert detached.metadata.get("block_count") is None
    assert detached.metadata.get("source_block_count") is None
    assert detached.metadata["semantic"] == "preserved"
    assert detached.blocks[0].provider_binding == {}
    assert detached.blocks[0].provider_payload == {}
    assert result["content"] == [{"text": "before"}]


def test_write_consumes_conversion_without_converting_again(monkeypatch):
    calls = []
    persisted = _bound_document("fake")
    converted = WriterProviderDocument(
        provider="fake",
        format="fake_blocks",
        content=[{"text": "before"}],
        source_document=persisted,
    )

    class FakeProvider:
        def require_capability(self, capability):
            calls.append(("require", capability))

        def write_document(self, document, target, *, media_assets=None, mode="replace"):
            calls.append(("write", document, target, media_assets, mode))
            return {
                "doc_id": "remote-1",
                "adapter": "fake",
                "locator": "https://example.test/remote-1",
                "persisted_document": persisted,
                "representation": "ir",
            }

    monkeypatch.setattr(
        document_resources, "get_writer_provider", lambda _provider: FakeProvider()
    )
    result = document_resources.write_document(
        converted.model_dump(),
        target_document={"adapter": "fake", "doc_id": "remote-1"},
    )

    assert [call[0] for call in calls] == ["require", "write"]
    assert calls[1][1].content == [{"text": "before"}]
    assert result["provider"] == "fake"
    assert result["persisted_document"]["provider_binding"]["provider"] == "fake"

    serialized = json.loads(WriterResourceCapabilities().write_document(
        converted_document_json=converted.model_dump_json(),
        target_document_json=json.dumps({"adapter": "fake", "doc_id": "remote-1"}),
    ))
    assert serialized["publish_result"]["persisted_document"]["document_id"] == "local-1"


def test_write_uses_provider_supplied_empty_published_link(monkeypatch):
    source = WriterDocument(
        document_id="local-1",
        title="Draft",
        stage="final",
        blocks=[WriterBlock(node_id="body", type="paragraph", content="content")],
    )
    converted = WriterProviderDocument(
        provider="obsidian",
        format="markdown",
        content="# Draft\n",
        source_document=source,
    )

    class FakeProvider:
        def require_capability(self, _capability):
            pass

        def write_document(self, _document, _target, **_kwargs):
            return {
                "doc_id": "vlt_test:Draft.md",
                "adapter": "obsidian",
                "locator": "obsidian://vlt_test/Draft.md",
                "persisted_document": "# Draft\n",
                "representation": "markdown",
                "published_link": "",
            }

    monkeypatch.setattr(
        document_resources, "get_writer_provider", lambda _provider: FakeProvider()
    )

    result = document_resources.write_document(
        converted.model_dump(),
        target_document={
            "adapter": "obsidian",
            "uri": "obsidian://vlt_test/Draft.md",
        },
    )

    assert "published_link" not in result
    serialized = json.loads(WriterResourceCapabilities().write_document(
        converted_document_json=converted.model_dump_json(),
        target_document_json=json.dumps({
            "adapter": "obsidian",
            "uri": "obsidian://vlt_test/Draft.md",
        }),
    ))
    assert serialized["published_link"] == ""


def test_write_strips_provider_supplied_published_link(monkeypatch):
    source = WriterDocument(
        document_id="local-1",
        title="Draft",
        stage="final",
        blocks=[WriterBlock(node_id="body", type="paragraph", content="content")],
    )
    converted = WriterProviderDocument(
        provider="obsidian",
        format="markdown",
        content="# Draft\n",
        source_document=source,
    )

    class FakeProvider:
        def require_capability(self, _capability):
            pass

        def write_document(self, _document, _target, **_kwargs):
            return {
                "doc_id": "vlt_test:Draft.md",
                "adapter": "obsidian",
                "locator": "obsidian://vlt_test/Draft.md",
                "persisted_document": "# Draft\n",
                "representation": "markdown",
                "published_link": "  https://example.test/doc  ",
            }

    monkeypatch.setattr(
        document_resources, "get_writer_provider", lambda _provider: FakeProvider()
    )

    serialized = json.loads(WriterResourceCapabilities().write_document(
        converted_document_json=converted.model_dump_json(),
        target_document_json=json.dumps({
            "adapter": "obsidian",
            "uri": "obsidian://vlt_test/Draft.md",
        }),
    ))

    assert serialized["published_link"] == "https://example.test/doc"


def test_write_keeps_local_image_reference_after_provider_readback(monkeypatch):
    source = WriterDocument(
        document_id="local-1",
        stage="final",
        blocks=[WriterBlock(
            node_id="IMAGE-1",
            type="image",
            references=[{
                "type": "media_asset",
                "id": "asset-1",
                "path": "/data/subagent/media/asset-1.jpg",
            }],
        )],
    )
    persisted = source.model_copy(deep=True)
    persisted.provider_binding = {
        "provider": "feishu",
        "document_id": "remote-1",
    }
    source.metadata = {
        "source": {"adapter": "feishu", "uri": "feishu:/old"},
        "provider_metadata": {"old": True},
        "semantic": "preserved",
    }
    persisted.metadata = {
        "source": {"adapter": "notion", "uri": "notion:/new"},
        "provider_metadata": {"new": True},
        "block_count": 1,
    }
    persisted.blocks[0].node_id = "provider-image-1"
    persisted.blocks[0].references = []
    persisted.blocks[0].provider_binding = {
        "provider": "feishu",
        "block_id": "remote-block-1",
    }
    persisted.blocks[0].provider_payload = {
        "raw_block": {"image": {"token": "image-token-1"}},
    }
    converted = WriterProviderDocument(
        provider="feishu",
        format="feishu_blocks",
        content=[{"block_type": 27}],
        source_document=source,
    )

    class FakeProvider:
        def require_capability(self, _capability):
            pass

        def write_document(self, _document, _target, **_kwargs):
            return {
                "doc_id": "remote-1",
                "adapter": "feishu",
                "persisted_document": persisted,
                "representation": "ir",
            }

    monkeypatch.setattr(
        document_resources, "get_writer_provider", lambda _provider: FakeProvider()
    )

    result = document_resources.write_document(
        converted.model_dump(),
        target_document={"adapter": "feishu", "doc_id": "remote-1"},
    )

    image = result["persisted_document"]["blocks"][0]
    assert image["node_id"] == "IMAGE-1"
    assert image["references"] == source.blocks[0].references
    assert image["provider_binding"] == persisted.blocks[0].provider_binding
    assert image["provider_payload"] == persisted.blocks[0].provider_payload
    assert result["persisted_document"]["metadata"] == {
        "source": {"adapter": "notion", "uri": "notion:/new"},
        "provider_metadata": {"new": True},
        "block_count": 1,
        "semantic": "preserved",
    }


@pytest.mark.parametrize("provider_name", ["feishu", "notion"])
def test_first_publication_creates_target_and_returns_provider_binding(
    monkeypatch, provider_name
):
    calls = []
    source = WriterDocument(
        document_id="local-1",
        title="Draft",
        stage="final",
        blocks=[WriterBlock(node_id="body", type="paragraph", content="content")],
    )
    converted = WriterProviderDocument(
        provider=provider_name,
        format=f"{provider_name}_blocks",
        content=[{"text": "content"}],
        source_document=source,
    )
    target = TargetDocument(
        adapter=provider_name,
        doc_id="remote-1",
        uri=f"https://example.test/{provider_name}/remote-1",
        title="Draft",
    )
    persisted = source.model_copy(deep=True)
    persisted.revision = "remote-revision-1"
    persisted.provider_binding = {
        "provider": provider_name,
        "document_id": "remote-1",
        "uri": target.uri,
    }
    persisted.blocks[0].provider_binding = {
        "provider": provider_name,
        "block_id": "remote-block-1",
    }

    class FakeProvider:
        def require_capability(self, capability):
            calls.append(("require", capability))

        def create_document(self, title, parent_uri=""):
            calls.append(("create", title, parent_uri))
            return target

        def write_document(self, document, write_target, **kwargs):
            calls.append(("write", document, write_target, kwargs))
            return {
                "doc_id": target.doc_id,
                "adapter": provider_name,
                "locator": target.uri,
            }

        def load_document(self, load_target):
            calls.append(("load", load_target))
            return {
                "source_document": persisted,
                "target_document": target,
                "representation": "ir",
            }

    monkeypatch.setattr(
        document_resources, "get_writer_provider", lambda _provider: FakeProvider()
    )

    result = document_resources.write_document(converted.model_dump())

    assert [call[0] for call in calls] == ["require", "create", "require", "write", "load"]
    assert result["provider"] == provider_name
    assert result["target_document"]["adapter"] == provider_name
    assert result["target_document"]["doc_id"] == "remote-1"
    assert result["persisted_document"]["provider_binding"] == persisted.provider_binding
    assert result["persisted_document"]["blocks"][0]["provider_binding"] == (
        persisted.blocks[0].provider_binding
    )


@pytest.mark.parametrize('output_format', ['markdown', 'latex', 'text'])
def test_workflow_portable_conversion_returns_content_without_artifact_store(tmp_path, output_format):
    from lazymind.document_tools.execution import invoke

    source = tmp_path / 'article.md'
    source.write_text('# Article\n\nCurrent content', encoding='utf-8')
    result = json.loads(invoke({}, '_writer_convert_document', {
        'content_path': str(source), 'output_format': output_format,
    }))
    assert result['format'] == output_format
    assert result['provider'] == ''
    assert 'Current content' in result['content']
    assert list(tmp_path.iterdir()) == [source]
