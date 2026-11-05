"""Attachment/media regressions; these do not replace browser acceptance."""
from unittest.mock import Mock
import pytest
from lazymind.document_tools.paper import contracts, workflow
from lazymind.document_tools.paper.publication import figure_catalog, publish_charts, validate_chart_references
from lazymind.document_tools.paper.writing import write_outline
from lazymind.document_tools.paper.writing.attachments import select_files


@pytest.mark.parametrize('payload', ['["paper", "materials"]', '["paper"]', '["unknown", "materials"]'])
def test_attachment_roles_use_writer_parser_and_keep_role_validation(monkeypatch, payload):
    from lazymind.document_tools.paper.writing import attachments
    monkeypatch.setattr(attachments, 'AutoModel', lambda **kw: lambda prompt:
                        '<think>classification</think>\n```json\n' + payload + '\n```')
    documents = [{'filename': name, 'text': 'Source text'} for name in ('paper.md', 'notes.md')]
    if payload == '["paper", "materials"]':
        assert attachments.classify_markdown(documents, 'Use the sources') == ['paper', 'materials']
    else:
        with pytest.raises(ValueError, match='附件用途识别失败'):
            attachments.classify_markdown(documents, 'Use the sources')


def test_latest_attachment_batch_does_not_mix_old_outline():
    assert select_files({'history_files_per_turn': {'1': ['old.md'], '2': ['new.md', 'chart.svg']}}) == ['new.md', 'chart.svg']
    assert select_files({'history_files_per_turn': {'1': ['old.md'], '2': ['unsupported.zip']}}) == []


def test_media_pool_roundtrip_and_publication():
    media = {'library_id': 'uploads', 'assets': {'image-a': {
        'media_asset_id': 'image-a', 'asset_type': 'image', 'source_type': 'input_resource',
        'uri': '/static-files/paper-media/assets/a.svg', 'caption': 'User image',
    }}}
    pool = workflow.pool_from_business({'topic': 'Test', 'writing_materials': 'Observed score: 80%', 'media_assets': media})
    inputs = workflow.business_input('write_outline', pool, 'Write from these materials')
    normalized = contracts.validate_input('write_outline', inputs)
    assert normalized['writing_materials'] == 'Observed score: 80%'
    figures = figure_catalog([], normalized['media_assets'])
    body = '# Test\n\n## Results\nSee [image](#block-image-a).\n\n<a id="block-image-a"></a>\n![User image](media-placeholder://image-a)'
    published = publish_charts(body, figures)
    assert 'media-placeholder://' not in published
    assert '/static-files/paper-media/assets/a.svg' in published
    validate_chart_references(published, figures)
    assert publish_charts(published, figures) == published


def test_media_requires_registered_persistent_resource():
    with pytest.raises(ValueError, match='saved paper media'):
        contracts.validate_value('media_assets', {'library_id': 'x', 'assets': {'image-a': {
            'media_asset_id': 'image-a', 'asset_type': 'image', 'source_type': 'input_resource',
            'uri': '/etc/passwd'}}})


def test_partial_ownership_is_completed_without_moving_existing():
    outline = '# Test\n\n## Method\n<!-- paper-charts: ["image-a"] -->\n\n## Results\n'
    complete = outline + '<!-- paper-charts: ["image-b"] -->\n'
    prepared = {'writing_task': {'query': 'write'}, 'writing_context': {}, 'outline': outline,
                'charts': [{'id': 'image-a'}, {'id': 'image-b'}], 'chart_directory': []}
    toolkit = Mock()
    toolkit.generate_outline.return_value = complete
    assert write_outline.prepare_outline(prepared, toolkit) == complete
    toolkit.generate_outline.assert_called_once()
    toolkit.generate_outline.return_value = complete.replace('image-a', 'TEMP').replace('image-b', 'image-a').replace('TEMP', 'image-b')
    with pytest.raises(ValueError, match='moved an existing'):
        write_outline.prepare_outline(prepared, toolkit)


def test_unknown_existing_ownership_is_not_silently_replanned():
    prepared = {'writing_task': {}, 'writing_context': {},
                'outline': '# Test\n\n## Results\n<!-- paper-charts: ["unknown"] -->',
                'charts': [{'id': 'image-a'}]}
    toolkit = Mock()
    with pytest.raises(ValueError, match='unknown chart'):
        write_outline.prepare_outline(prepared, toolkit)
    toolkit.generate_outline.assert_not_called()


def test_public_writer_collects_real_svg_png_jpg(tmp_path):
    import json
    from PIL import Image
    from lazymind.document_tools.writing import WriterWritingCapabilities
    svg = tmp_path / 'diagram.svg'
    svg.write_text('<svg xmlns="http://www.w3.org/2000/svg" width="40" height="30"><rect width="40" height="30" fill="blue"/></svg>')
    png, jpg = tmp_path / 'diagram.png', tmp_path / 'photo.jpg'
    Image.new('RGB', (40, 30), 'blue').save(png)
    Image.new('RGB', (40, 30), 'green').save(jpg)
    result = json.loads(WriterWritingCapabilities().collect_available_media(
        writing_task_json=json.dumps({'task_id': 'upload-test', 'task_type': 'write', 'query': 'Use supplied images'}),
        input_resources_json=json.dumps([{'resource_id': p.name, 'resource_type': 'image', 'uri': str(p)} for p in (svg, png, jpg)]),
        media_store=str(tmp_path / 'saved'), use_vision_model=False,
    ))
    assert not result['warnings']
    assert len(result['media_assets']['assets']) == 3
    from pathlib import Path
    assert all(Path(asset['local_path']).is_file() for asset in result['media_assets']['assets'].values())


def test_svg_import_preserves_bytes_and_uses_source_labels(tmp_path, monkeypatch):
    from pathlib import Path
    from lazymind.document_tools.paper.writing.attachments import import_files
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    source = tmp_path / 'accuracy.svg'
    source.write_text('<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40">'
                      '<text x="1" y="20">Baseline 80%</text></svg>')
    result = import_files([str(source)], 'Use the supplied figure')
    asset, = result['media_assets']['assets'].values()
    assert Path(asset['local_path']).read_bytes() == source.read_bytes()
    assert 'Baseline 80%' in asset['summary']
    assert 'not visual interpretation' in asset['summary']
    assert asset['meta']['summary_source'] == 'svg_text'
    assert asset['meta']['semantic_status'] == 'ready'


def test_writing_upload_binding_preserves_material_sources(tmp_path, monkeypatch):
    import lazyllm
    from lazymind.document_tools.paper.writing import attachments

    config = {'history_files_per_turn': {'1': ['/uploads/old.json'],
                                       '2': ['/uploads/notes.md', '/uploads/chart.svg']}}
    monkeypatch.setattr(lazyllm, 'globals', {'agentic_config': config})
    importer = Mock(return_value={'topic': 'Notes', 'writing_materials': 'Complete Markdown source'})
    monkeypatch.setattr(attachments, 'import_files', importer)
    topic = tmp_path / 'topic.txt'
    topic.write_text('Explicit topic')
    bound = attachments.bind_attachment({'paper_topic': str(topic)}, str(tmp_path), 'write')
    importer.assert_called_once_with(['/uploads/notes.md', '/uploads/chart.svg'], 'write')
    pool = workflow.initialize_pool(bound, 'write')
    assert pool['topic'] == 'Explicit topic'
    assert workflow.material(pool, 'writing_materials')['content'] == 'Complete Markdown source'

    importer.reset_mock()
    for explicit in ({'write_outline_pool': 'confirmed.json'}, {'manuscript_file': 'draft.md'},
                     {'manuscript_text': 'draft.txt'}, bound):
        assert attachments.bind_attachment(explicit, str(tmp_path), 'write') == explicit
    config['history_files_per_turn'] = {'1': ['/uploads/materials.json']}
    assert attachments.bind_attachment({}, str(tmp_path), 'write') == {}
    importer.assert_not_called()
