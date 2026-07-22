from __future__ import annotations

import importlib.util
import json
import sys
import types
from pathlib import Path

import pytest

from lazyllm.tools.writer.data_models import WriterBlock, WriterDocument
from lazyllm.tools.writer.data_models.revision import PatchResult, PatchSet
from lazyllm.tools.writer.utils import save_artifact_json


ROOT = Path(__file__).resolve().parents[3]
TOOLS_PATH = ROOT / 'plugins' / 'revise-plugin' / 'scripts' / 'tools.py'
WRITER_IR_SCHEMA = 'lazyllm.tools.writer.data_models.writer_ir.WriterDocument'


@pytest.fixture
def revise_tools(monkeypatch):
    packages = [
        'lazymind',
        'lazymind.chat',
        'lazymind.chat.engine',
        'lazymind.chat.engine.subagent',
        'lazymind.chat.engine.tools',
    ]
    for name in packages:
        module = types.ModuleType(name)
        module.__path__ = []
        monkeypatch.setitem(sys.modules, name, module)

    context_module = types.ModuleType('lazymind.chat.engine.subagent.context')
    context_module.require_context = lambda: None
    monkeypatch.setitem(sys.modules, context_module.__name__, context_module)

    writer_module = types.ModuleType('lazymind.chat.engine.tools.writer')

    class ToolkitBase:
        WRITER_IR_SCHEMA = WRITER_IR_SCHEMA

    writer_module.WriterToolkitBase = ToolkitBase
    writer_module.WriterRevisionToolkit = object
    writer_module.writer_schema = lambda name: f'lazyllm.tools.writer.data_models.{name}'
    monkeypatch.setitem(sys.modules, writer_module.__name__, writer_module)

    spec = importlib.util.spec_from_file_location('revise_plugin_tools_test', TOOLS_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def _save(path: Path, data, schema: str) -> str:
    return save_artifact_json(data, str(path), schema_name=schema, created_by='test')


def _source_document() -> WriterDocument:
    return WriterDocument(
        document_id='writer-doc-1',
        stage='final',
        blocks=[WriterBlock(
            node_id='writer-node-1',
            type='paragraph',
            content='Original',
            stage='final',
            provider_binding={'provider': 'feishu', 'block_id': 'block-1'},
        )],
        provider_binding={
            'provider': 'feishu',
            'document_id': 'feishu-doc-1',
            'uri': 'https://example.feishu.cn/docx/feishu-doc-1',
        },
    )


def test_load_document_saves_writer_document(revise_tools, monkeypatch, tmp_path):
    source_path = _save(tmp_path / 'loaded.json', _source_document().model_dump(), WRITER_IR_SCHEMA)

    class ResourceTools:
        def __init__(self, **kwargs):
            pass

        def document_to_docir(self, target):
            assert target.adapter == 'feishu'
            return {'artifact_path': source_path}

    monkeypatch.setattr(revise_tools, 'WriterResourceTools', ResourceTools)
    monkeypatch.setattr(revise_tools, '_run_root', lambda name: tmp_path / name)

    result = revise_tools.revise_load_document(
        'Polish this: https://example.feishu.cn/docx/feishu-doc-1',
    )

    assert set(result) == {'source_ir'}
    artifact = json.loads(Path(result['source_ir']).read_text(encoding='utf-8'))
    assert artifact['schema'] == WRITER_IR_SCHEMA
    assert artifact['data']['document_id'] == 'writer-doc-1'


def test_build_context_uses_writer_document_argument(revise_tools, monkeypatch, tmp_path):
    source_path = _save(tmp_path / 'source.json', _source_document().model_dump(), WRITER_IR_SCHEMA)
    calls = {}

    class RevisionToolkit:
        def build_revise_task(self, **kwargs):
            calls['task'] = kwargs
            return json.dumps({'task_id': 'task-1', 'query': kwargs['query'], 'task_type': 'revise'})

        def create_writing_context(self, **kwargs):
            calls['context'] = kwargs
            return json.dumps({'context_id': 'context-1', 'doc_id': 'writer-doc-1', 'query': 'revise'})

    monkeypatch.setattr(revise_tools, 'WriterRevisionToolkit', RevisionToolkit)
    monkeypatch.setattr(revise_tools, '_run_root', lambda name: tmp_path / name)

    result = revise_tools.revise_build_context(
        'Polish https://example.feishu.cn/docx/feishu-doc-1', source_path,
    )

    assert 'writer_document_json' in calls['context']
    assert 'doc_ir_json' not in calls['context']
    assert json.loads(calls['context']['writer_document_json'])['document_id'] == 'writer-doc-1'
    assert Path(result['revision_context']).exists()


def test_generate_revision_reads_revised_document(revise_tools, monkeypatch, tmp_path):
    source_path = _save(tmp_path / 'source.json', _source_document().model_dump(), WRITER_IR_SCHEMA)
    context_path = _save(
        tmp_path / 'context.json',
        {'context_id': 'context-1', 'doc_id': 'writer-doc-1', 'query': 'revise'},
        'lazyllm.tools.writer.data_models.context.WritingContext',
    )
    revised = _source_document().model_copy(deep=True)
    revised.blocks[0].content = 'Revised'

    class RevisionToolkit:
        def build_revise_task(self, **kwargs):
            return json.dumps({'task_id': 'task-1', 'query': kwargs['query'], 'task_type': 'revise'})

        def locate_revision_target(self, **kwargs):
            return json.dumps({'target_node_ids': ['writer-node-1']})

        def generate_modify_plan(self, **kwargs):
            return json.dumps({'scope': 'block', 'instructions': []})

        def generate_patch_set(self, **kwargs):
            return PatchSet(target_doc_id='writer-doc-1').model_dump_json()

        def apply_patch(self, **kwargs):
            return json.dumps({
                'patch_result': PatchResult(success=True).model_dump(),
                'revised_document': revised.model_dump(),
            })

    monkeypatch.setattr(revise_tools, 'WriterRevisionToolkit', RevisionToolkit)
    monkeypatch.setattr(revise_tools, '_run_root', lambda name: tmp_path / name)

    result = revise_tools.revise_generate_revision(source_path, context_path, 'Polish it')
    candidate = revise_tools._read_json_file(result['candidate_ir'])

    assert candidate['blocks'][0]['content'] == 'Revised'
    assert json.loads(Path(result['candidate_ir']).read_text(encoding='utf-8'))['schema'] == WRITER_IR_SCHEMA


def test_write_back_uses_source_and_patch_and_saves_persisted_document(
    revise_tools, monkeypatch, tmp_path,
):
    source = _source_document()
    source_path = _save(tmp_path / 'source.json', source.model_dump(), WRITER_IR_SCHEMA)
    patch = PatchSet(target_doc_id=source.document_id)
    patch_path = _save(
        tmp_path / 'patch.json', patch.model_dump(),
        'lazyllm.tools.writer.data_models.revision.PatchSet',
    )
    persisted = source.model_copy(deep=True)
    persisted.blocks[0].content = 'Persisted'
    patch_result_path = _save(
        tmp_path / 'remote_result.json', PatchResult(success=True).model_dump(),
        'lazyllm.tools.writer.data_models.revision.PatchResult',
    )
    persisted_path = _save(tmp_path / 'persisted.json', persisted.model_dump(), WRITER_IR_SCHEMA)
    calls = {}

    class ResourceTools:
        def __init__(self, **kwargs):
            pass

        def apply_patch_to_document(self, **kwargs):
            calls.update(kwargs)
            return {
                'artifact_path': patch_result_path,
                'metadata': {'artifact_paths': {'persisted_document': persisted_path}},
            }

    monkeypatch.setattr(revise_tools, 'WriterResourceTools', ResourceTools)
    monkeypatch.setattr(revise_tools, '_run_root', lambda name: tmp_path / name)

    result = revise_tools.revise_write_back(source_path, patch_path)

    assert calls['source_document']['document_id'] == source.document_id
    assert calls['patch_set']['target_doc_id'] == source.document_id
    assert revise_tools._read_json_file(result['synced_snapshot'])['blocks'][0]['content'] == 'Persisted'
