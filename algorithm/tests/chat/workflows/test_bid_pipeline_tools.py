import base64
import importlib.util
import json
from pathlib import Path


def _load_pipeline_tools():
    root = Path(__file__).resolve().parents[4]
    path = root / 'workflows' / 'bid_tech_proposal_writer' / 'scripts' / 'pipeline_tools.py'
    spec = importlib.util.spec_from_file_location('bid_pipeline_tools_for_test', path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _load_document_builder():
    root = Path(__file__).resolve().parents[4]
    path = root / 'workflows' / 'bid_tech_proposal_writer' / 'scripts' / 'document_builder.py'
    spec = importlib.util.spec_from_file_location('bid_document_builder_for_test', path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_small_target_allocates_positive_leaf_targets_with_exact_total():
    tools = _load_pipeline_tools()
    children = [
        {
            'title': f'功能{i}',
            'level': 2,
            'number': f'1.{i}',
            'target_words': 100,
            'bid_requirements_refs': [],
            'disqualification_refs': [],
            'children': [],
        }
        for i in range(1, 18)
    ]
    outline = {
        'project_name': '测试项目',
        'total_word_target': 1000,
        'chapters': [{
            'title': '功能设计',
            'level': 1,
            'number': '1',
            'target_words': 1700,
            'bid_requirements_refs': [],
            'disqualification_refs': [],
            'children': children,
        }],
    }

    result = tools.validate_and_allocate_outline(json.dumps(outline), '', '', '1000')

    assert result['valid'] is True
    allocated = result['normalized_outline']['chapters'][0]['children']
    targets = [item['target_words'] for item in allocated]
    assert min(targets) > 0
    assert sum(targets) == 1000
    assert result['normalized_outline']['chapters'][0]['target_words'] == 1000


def test_writer_image_placeholder_is_embedded_in_place_without_duplication(monkeypatch):
    builder = _load_document_builder()
    embedded_titles = []

    def record_images(_document, images):
        embedded_titles.extend(image['title'] for image in images)
        return len(images)

    monkeypatch.setattr(builder, '_add_images', record_images)
    images = [
        {'path': '/tmp/architecture.png', 'title': '架构图', 'type': 'architecture'},
        {'path': '/tmp/effect.png', 'title': '效果图', 'type': 'effect'},
    ]
    markdown = (
        '<a id="block-IMAGE-1"></a>\n'
        '![系统架构](media-placeholder://IMAGE-1)\n\n'
        '[[WORKFLOW_IMAGES]]\n'
    )

    _, _, embedded = builder._render_markdown(object(), markdown, images)

    assert embedded == 2
    assert embedded_titles == ['架构图', '效果图']


def test_effect_images_are_placed_in_source_chapters():
    builder = _load_document_builder()
    markdown = '''# 测试技术方案

## 总体架构设计

架构正文。

<a id="block-IMAGE-1"></a>
![架构图](media-placeholder://IMAGE-1)

## 门户权限与监控

门户正文。

## 工单看板功能

工单正文。

## 系统架构与功能效果

[[WORKFLOW_IMAGES]]
'''
    images = [
        {
            'path': '/tmp/architecture.png', 'title': '架构图',
            'type': 'architecture', 'source_chapter': '1.1 总体架构设计',
        },
        {
            'path': '/tmp/portal.png', 'title': '门户效果图',
            'type': 'effect', 'source_chapter': '1.2 门户权限与监控',
        },
        {
            'path': '/tmp/workorder.png', 'title': '工单效果图',
            'type': 'effect', 'source_chapter': '1.3 工单看板功能',
        },
    ]

    placed = builder._place_workflow_images(markdown, images)

    assert placed.count('media-placeholder://IMAGE-1') == 1
    assert placed.count('media-placeholder://IMAGE-2') == 1
    assert placed.count('media-placeholder://IMAGE-3') == 1
    assert placed.index('门户正文。') < placed.index('IMAGE-2') < placed.index('## 工单看板功能')
    assert placed.index('工单正文。') < placed.index('IMAGE-3')
    assert '[[WORKFLOW_IMAGES]]' not in placed
    assert '## 系统架构与功能效果' not in placed


def test_delivered_markdown_embeds_images_for_standalone_preview(tmp_path):
    builder = _load_document_builder()
    image = tmp_path / 'preview.png'
    image.write_bytes(b'workflow-image')

    markdown = '![效果图](media-placeholder://IMAGE-1)'
    embedded = builder._embed_workflow_images(markdown, [{'path': str(image)}])

    expected = base64.b64encode(b'workflow-image').decode('ascii')
    assert embedded == f'![效果图](data:image/png;base64,{expected})'
