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


def test_bid_outline_repairs_relative_headings_and_missing_trace_mappings():
    tools = _load_pipeline_tools()
    candidate = tools._bid_outline_from_markdown(
        '# 测试项目投标技术方案\n\n### 总体架构与建设思路\n\n##### 安全审计设计\n',
        '备用标题',
    )

    result = tools.validate_and_allocate_outline(
        json.dumps(json.dumps({'data': candidate}, ensure_ascii=False), ensure_ascii=False),
        '### BG-001\n背景要求\n### SEC-001\n安全要求',
        '### D-001\n不得缺少审计能力',
        '1000',
    )

    assert result['valid'] is True
    assert result['normalized_outline']['total_word_target'] == 1000
    leaves = tools._leaves(result['normalized_outline']['chapters'])
    assert sum(int(item['target_words']) for item in leaves) == 1000
    assert {ref for item in leaves for ref in item['bid_requirements_refs']} == {
        'BG-001', 'SEC-001',
    }
    assert {ref for item in leaves for ref in item['disqualification_refs']} == {'D-001'}
    assert any('自动分配' in warning for warning in result['warnings'])


def test_requirement_extractor_keeps_markdown_table_rows_separate():
    tools = _load_pipeline_tools()
    raw_text = '''# 技术要求

| 需求编号 | 需求描述 |
|---|---|
| REQ-01 | 平台提供设备监控管理和告警处置功能。 |
| PERF-01 | 提供数据备份方案并支持不少于 200 名并发用户。 |
| SEC-01 | 关键管理操作必须进行身份鉴别与权限校验。 |
'''

    result = tools.extract_technical_requirements(raw_text)

    assert result['total'] == 3
    assert result['counts'] == {'FUNC': 1, 'PERF': 1, 'SEC': 1}
    assert 'REQ-01 | 平台提供设备监控管理和告警处置功能。' in result['markdown']
    assert 'PERF-01 | 提供数据备份方案并支持不少于 200 名并发用户。' in result['markdown']
    assert 'SEC-01 | 关键管理操作必须进行身份鉴别与权限校验。' in result['markdown']


def test_chinese_numbered_section_heading_is_recognized():
    tools = _load_pipeline_tools()

    assert tools._heading_level('三、性能与可靠性要求') == 1
    assert tools._heading_level('第八章 验收要求') == 1


def test_requirement_extractor_splits_wrapped_source_ids_and_list_items():
    tools = _load_pipeline_tools()
    raw_text = '''REQ-01
平台提供用户管理功能，
支持统一查询。
REQ-02
系统年度可用性不低于 99.9%。
- 项目必须在 30 天内完成实施交付。
'''

    paragraphs = tools._paragraphs(raw_text)
    result = tools.extract_technical_requirements(raw_text)

    assert paragraphs == [
        (1, 'REQ-01 平台提供用户管理功能， 支持统一查询。'),
        (4, 'REQ-02 系统年度可用性不低于 99.9%。'),
        (6, '- 项目必须在 30 天内完成实施交付。'),
    ]
    assert result['counts'] == {'FUNC': 1, 'PERF': 1, 'IMPL': 1}


def test_disqualification_extractor_keeps_table_clauses_separate():
    tools = _load_pipeline_tools()
    raw_text = '''| 条款编号 | 条款内容 |
|---|---|
| T-01 | 必须逐项响应，否则视为无效投标。 |
| T-02 | 系统应达到等保三级。 |
'''

    result = tools.extract_disqualification_items(raw_text)

    assert result['total'] == 2
    assert result['explicit_count'] == 1
    assert result['risk_count'] == 1
    assert 'T-01 | 必须逐项响应，否则视为无效投标。' in result['markdown']
    assert 'T-02 | 系统应达到等保三级。' in result['markdown']


def test_pdf_bullet_heading_and_wrapped_disqualification_clause_are_not_requirements():
    tools = _load_pipeline_tools()
    raw_text = '''# 七、废标条款

DISQ-01（实质性要求）：投标文件未逐项响应 REQ-01 至 REQ-05、
SEC-01 至 SEC-03 中任一项，按无效投标处理。
'''

    paragraphs = tools._paragraphs(raw_text)
    requirements = tools.extract_technical_requirements(raw_text)
    disqualification = tools.extract_disqualification_items(raw_text)

    assert paragraphs == [(
        3,
        'DISQ-01（实质性要求）：投标文件未逐项响应 REQ-01 至 REQ-05、 '
        'SEC-01 至 SEC-03 中任一项，按无效投标处理。',
    )]
    assert requirements['total'] == 0
    assert disqualification['total'] == 1
    assert disqualification['explicit_count'] == 1
    assert disqualification['risk_count'] == 0


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


def test_architecture_with_plural_source_chapters_is_placed_at_common_parent():
    builder = _load_document_builder()
    markdown = '''# 测试技术方案

## 总体技术方案

### 响应承诺

承诺正文。

### 功能需求响应

功能正文。

### 性能与可靠性保障

性能正文。

### 安全设计

安全正文。

## 系统架构与功能效果

[[WORKFLOW_IMAGES]]
'''
    images = [
        {
            'path': '/tmp/architecture.png', 'title': '总体架构图',
            'type': 'architecture',
            'source_chapters': ['1.2 功能需求响应', '1.3 性能与可靠性保障', '1.4 安全设计'],
        },
        {
            'path': '/tmp/dashboard.png', 'title': '运行态势看板',
            'type': 'effect', 'source_chapter': '1.2 功能需求响应',
        },
        {
            'path': '/tmp/analytics.png', 'title': '统计分析与审计',
            'type': 'effect', 'source_chapter': '1.2 功能需求响应 · 1.4 安全设计',
        },
    ]

    placed = builder._place_workflow_images(markdown, images)

    assert placed.index('## 总体技术方案') < placed.index('IMAGE-1') < placed.index('### 响应承诺')
    assert placed.index('功能正文。') < placed.index('IMAGE-2') < placed.index('### 性能与可靠性保障')
    assert placed.index('安全正文。') < placed.index('IMAGE-3')
    assert '## 系统架构与功能效果' not in placed


def test_delivered_markdown_embeds_images_for_standalone_preview(tmp_path):
    builder = _load_document_builder()
    image = tmp_path / 'preview.png'
    image.write_bytes(b'workflow-image')

    markdown = '![效果图](media-placeholder://IMAGE-1)'
    embedded = builder._embed_workflow_images(markdown, [{'path': str(image)}])

    expected = base64.b64encode(b'workflow-image').decode('ascii')
    assert embedded == f'![效果图](data:image/png;base64,{expected})'
