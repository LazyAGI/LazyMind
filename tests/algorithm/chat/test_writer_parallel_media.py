import importlib.util
import hashlib
import json
from pathlib import Path
from threading import Event, Lock
from types import SimpleNamespace

import pytest

from lazymind.document_tools import writing, execution
from lazyllm.tools.writer.utils import save_artifact_json
from lazyllm.tools.writer.tools.base import WriterToolBase
from lazyllm.tools.writer.data_models import WriterBlock


def _tools(tmp_path, monkeypatch):
    path = Path(__file__).resolve().parents[3] / 'workflows/writer-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('parallel_writer_tools', path)
    tools = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tools)
    context = SimpleNamespace(workspace_path=str(tmp_path), params={'step_id': 'write_document'}, emit=lambda e: None)
    monkeypatch.setattr(tools, 'require_context', lambda: context)
    monkeypatch.setattr(writing, 'AutoModel', lambda **kw: object())
    from lazymind import model_config
    monkeypatch.setattr(model_config, 'is_model_role_available', lambda role: True)
    return tools


def _image(tmp_path):
    path = tmp_path / 'image.png'
    from PIL import Image
    Image.new('RGB', (4, 4), 'red').save(path)
    return {'resource_type': 'image', 'uri': str(path), 'summary': 'Illustration',
            'meta': {'source_type': 'image_generation'}}


def _inputs(tmp_path, mode, representation):
    instruction = {'instruction_id': 'i1', 'section_title': 'Section', 'section_goal': 'Explain',
                   'content_ref': ({'node_id': 's1'} if representation == 'ir'
                                   else {'heading_path': ['Article', 'Section']}),
                   'meta': {'representation': representation,
                            'cross_references': [{'target': 'picture', 'kind': 'image',
                                                  'must_create': True, 'required': True, 'caption': 'Process'}],
                            'cross_reference_targets': ['picture']}}
    values = {
        'writing_task': {'task_id': 'task', 'query': 'Write', 'task_type': 'write',
                         'constraints': {'target_chars': 1000 if mode == 'whole' else 6000},
                         'output': {'representation': representation}},
        'writing_context': {'context_id': 'ctx'},
        'section_instructions': {'instruction_set_id': 'instructions', 'instructions': [instruction]},
        'visual_plan': {'instructions': [{'need_id': 'picture', 'content_ref': instruction['content_ref'],
                                         'visual_type': 'image', 'purpose': 'Explain the process',
                                         'preferred_strategy': 'image_generation', 'required': True}]},
        'media_assets': {'library_id': 'media', 'assets': {}},
    }
    paths = {'document_title': 'Article'}
    for key, value in values.items():
        path = tmp_path / f'{key}.json'
        schemas = {'writing_task': 'task.WritingTask', 'writing_context': 'context.WritingContext',
                   'section_instructions': 'planning.SectionInstructionList', 'visual_plan': 'multimodal.VisualPlan',
                   'media_assets': 'multimodal.MediaAssetLibrary'}
        save_artifact_json(value, str(path), schema_name=execution.writer_schema(schemas[key]))
        paths[f'{key}_path'] = str(path)
    if mode == 'flat':
        paths['section_instructions_path'] = ''
    return paths


@pytest.mark.parametrize('mode', ['whole', 'sections', 'flat'])
@pytest.mark.parametrize('representation', ['markdown', 'ir'])
def test_body_overlaps_media_and_survives_media_retry(monkeypatch, tmp_path, mode, representation):
    tools = _tools(tmp_path, monkeypatch)
    paths = _inputs(tmp_path, mode, representation)
    image = _image(tmp_path)
    acquiring, generated = Event(), Event()
    body_calls = []
    fail = True

    def acquire(request):
        acquiring.set()
        assert generated.wait(5), 'Media acquisition blocked body generation'
        return [] if fail else [image]

    def body(self, prompt, **kwargs):
        assert acquiring.wait(5), 'Body started without concurrent acquisition'
        assert 'picture' in prompt
        body_calls.append(prompt)
        generated.set()
        text = ('# Article\n\n' if mode != 'sections' else '')
        text += 'Body text.\n\n![Process](media-placeholder://picture)\n'
        kwargs['stream_output']['_stream_sink']({'tag': 'text', 'delta': text})
        return text

    def ir_body(self, prompt, model, **kwargs):
        assert acquiring.wait(5)
        assert 'picture' in prompt
        body_calls.append(prompt)
        generated.set()
        value = {'node_id': 's1', 'type': 'heading', 'content': 'Section', 'stage': 'draft',
                 'children': [{'node_id': 'p1', 'type': 'paragraph', 'content': 'Body text.', 'stage': 'draft',
                               'spans': [{'text': 'Body text.', 'style': {'link': {'type': 'internal_ref',
                                                                              'target_node_id': 'picture'}}}]},
                              {'node_id': 'picture', 'type': 'image', 'content': 'Process', 'stage': 'draft'}]}
        kwargs['stream_output']['_stream_sink']({'tag': 'text', 'delta': json.dumps(value)})
        return WriterBlock.model_validate(value)

    monkeypatch.setattr(writing, 'acquire_generated_image', acquire)
    monkeypatch.setattr(writing, 'acquire_web_search_resources', lambda request: [])
    monkeypatch.setattr(WriterToolBase, '_call_llm_text', body)
    monkeypatch.setattr(WriterToolBase, '_call_llm_structured', ir_body)
    with pytest.raises(RuntimeError, match='required visual media'):
        tools.writer_generate_draft_document(**paths)
    assert len(body_calls) == 1
    fail = False
    result = tools.writer_generate_draft_document(**paths)
    assert len(body_calls) == 1, 'Media retry regenerated completed prose'
    payload = Path(result['draft_document']).read_text()
    assert 'Body text.' in payload
    assert 'media-placeholder://' not in payload
    library = tools._read_json_file(result['resolved_media_assets'])
    assert library['visual_need_asset_ids']['picture']
    assert all(Path(asset['local_path']).is_file() for asset in library['assets'].values())
    if representation == 'ir':
        document = tools._read_json_file(result['draft_document'])
        assert '"type": "image"' in json.dumps(document)
        assert '.png' in json.dumps(document)
    else:
        assert '.png' in payload


def test_media_is_bounded_deduplicated_and_checkpointed(monkeypatch, tmp_path):
    _tools(tmp_path, monkeypatch)
    resource = _image(tmp_path)
    lock = Lock()
    three_started, release = Event(), Event()
    active, maximum = 0, 0
    calls = []
    fail = True
    plan = {'instructions': [
        {'need_id': f'n{i}', 'content_ref': {'node_id': 's'}, 'visual_type': 'image',
         'purpose': str(i if i < 4 else 0), 'preferred_strategy': 'image_generation', 'required': True}
        for i in range(5)
    ]}

    def acquire(request):
        nonlocal active, maximum
        with lock:
            calls.append(request['purpose'])
            active += 1
            maximum = max(maximum, active)
            if active == 3:
                three_started.set()
        assert release.wait(5)
        with lock:
            active -= 1
        return [] if fail and request['purpose'] == '3' else [resource]

    monkeypatch.setattr(writing, 'acquire_generated_image', acquire)
    monkeypatch.setattr(writing, 'acquire_web_search_resources', lambda request: [])
    from concurrent.futures import ThreadPoolExecutor
    asset_id = 'asset-' + hashlib.sha256(Path(resource['uri']).read_bytes()).hexdigest()
    stale_library = {'library_id': 'media', 'assets': {asset_id: {
        'media_asset_id': asset_id, 'asset_type': 'image', 'source_type': 'input_resource',
        'local_path': str(tmp_path / 'missing.png'),
    }}}
    kwargs = dict(media_store=str(tmp_path / 'media'), strict_required=True,
                  checkpoint_path=str(tmp_path / 'checkpoint.json'))
    with ThreadPoolExecutor(max_workers=1) as pool:
        result = pool.submit(writing.resolve_visual_media, plan, stale_library, **kwargs)
        try:
            assert three_started.wait(5), 'Independent images did not run concurrently'
            with lock:
                assert active == 3
        finally:
            release.set()
        with pytest.raises(RuntimeError, match='n3'):
            result.result(timeout=5)
    assert maximum == 3
    assert calls.count('0') == 1
    saved = json.loads((tmp_path / 'checkpoint.json').read_text())
    assert set(saved['visual_need_asset_ids']) == {'n0', 'n1', 'n2', 'n4'}
    assert Path(saved['assets'][asset_id]['local_path']).is_file()
    fail = False
    result = writing.resolve_visual_media(plan, {'library_id': 'media'}, **kwargs)
    assert sorted(calls) == ['0', '1', '2', '3', '3']
    assert set(result['media_assets']['visual_need_asset_ids']) == {f'n{i}' for i in range(5)}


def test_cancelled_media_does_not_start_fallback_or_save_late_results(monkeypatch, tmp_path):
    _tools(tmp_path, monkeypatch)
    from concurrent.futures import CancelledError, ThreadPoolExecutor
    resource = _image(tmp_path)
    entered, release, finished, stop = Event(), Event(), Event(), Event()
    fallback_calls = []

    def acquire(request):
        entered.set()
        try:
            assert release.wait(5)
            return [resource]
        finally:
            finished.set()

    monkeypatch.setattr(writing, 'acquire_generated_image', acquire)
    monkeypatch.setattr(writing, 'acquire_web_search_resources', lambda request: fallback_calls.append(request) or [])
    plan = {'instructions': [{'need_id': 'n', 'content_ref': {'document_root': True},
                              'visual_type': 'image', 'purpose': 'Picture',
                              'preferred_strategy': 'image_generation', 'required': True}]}
    checkpoint = tmp_path / 'cancelled.json'
    with ThreadPoolExecutor(max_workers=1) as pool:
        future = pool.submit(writing.resolve_visual_media, plan, {'library_id': 'media'},
                             media_store=str(tmp_path / 'media'), strict_required=True,
                             checkpoint_path=str(checkpoint), stop_event=stop)
        try:
            assert entered.wait(5)
            stop.set()
            with pytest.raises(CancelledError):
                future.result(timeout=2)
        finally:
            release.set()
        assert finished.wait(5)
    assert not checkpoint.exists()
    assert not fallback_calls


def test_body_failure_reuses_completed_media(monkeypatch, tmp_path):
    tools = _tools(tmp_path, monkeypatch)
    paths = _inputs(tmp_path, 'whole', 'markdown')
    resource = _image(tmp_path)
    media_saved = Event()
    acquisitions = []
    fail = True
    replace = Path.replace

    def observed_replace(self, target):
        result = replace(self, target)
        if Path(target).name == 'media-progress.json':
            media_saved.set()
        return result

    def body(self, prompt, **kwargs):
        assert media_saved.wait(5)
        if fail:
            raise ValueError('body unavailable')
        text = '# Article\n\nBody.\n\n![Process](media-placeholder://picture)\n'
        kwargs['stream_output']['_stream_sink']({'tag': 'text', 'delta': text})
        return text

    monkeypatch.setattr(Path, 'replace', observed_replace)
    monkeypatch.setattr(writing, 'acquire_generated_image', lambda request: acquisitions.append(request) or [resource])
    monkeypatch.setattr(WriterToolBase, '_call_llm_text', body)
    with pytest.raises(ValueError, match='body unavailable'):
        tools.writer_generate_draft_document(**paths)
    fail = False
    result = tools.writer_generate_draft_document(**paths)
    assert len(acquisitions) == 1
    assert '.png' in Path(result['draft_document']).read_text()


def test_deferred_ir_binding_preserves_rewrite_source_images(tmp_path):
    from lazyllm.tools.writer.tools import WriterDraftingTools
    resource = _image(tmp_path)
    original_image = {'node_id': 'original-image', 'type': 'image', 'content': 'Original caption',
                      'references': [{'type': 'media_asset', 'id': 'original-asset'}]}
    instruction = {'instruction_id': 'i', 'section_title': 'Section', 'section_goal': 'Rewrite',
                   'content_ref': {'node_id': 's'},
                   'meta': {'rewrite': True, 'source_images': [original_image]}}
    block = {'node_id': 's', 'type': 'heading', 'content': 'Section',
             'children': [{'node_id': 'p', 'type': 'paragraph', 'content': 'Updated body.'}]}
    library = {'library_id': 'media', 'assets': {'original-asset': {
        'media_asset_id': 'original-asset', 'asset_type': 'image', 'source_type': 'input_resource',
        'local_path': resource['uri'],
    }}}
    result = WriterDraftingTools().finalize_section_media(block, instruction, {'instructions': []}, library)
    assert result.children[0].content == 'Updated body.'
    image = next(item for item in result.iter_blocks() if item.node_id == 'original-image')
    assert image.content == 'Original caption'
    assert image.references == [{'type': 'media_asset', 'id': 'original-asset', 'path': resource['uri']}]
    assert 'path' not in original_image['references'][0]
    assert len(block['children']) == 1
