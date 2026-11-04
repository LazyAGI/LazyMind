"""Offline paper publication: immutable SVGs, shared figure numbers and publication."""
import copy

import pytest

from lazymind.document_tools.artifacts import render_document
from lazymind.document_tools.paper import publication, contracts, workflow
from lazymind.document_tools.paper.contracts import normalize_papers


def chart(identifier='chart-001', svg='<svg/>'):
    return dict(id=identifier, title='比较', columns=['score'], svg=svg, finding_ids=[], caption='单次记录。')


def body(identifier='chart-001'):
    return (f'见[比较](#block-{identifier})。\n\n<a id="block-{identifier}"></a>\n'
            f'![比较](media-placeholder://{identifier})')


def test_publish_immutable_resources_and_reorder_shared_numbering(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    charts = [chart(), chart('chart-002', '<svg><title>second</title></svg>')]
    source = '# T\n\n## 结果\n' + body() + '\n\n' + body('chart-002')
    published = publication.publish_charts(source, charts)
    assert 'media-placeholder://' not in published
    assert publication.publish_charts(published, charts) == published
    assert len(list(tmp_path.rglob('*.svg'))) == 2
    for item in charts:
        assert (tmp_path / publication.chart_uri(item).removeprefix('/static-files/')).read_text() == item['svg']
    rendered = render_document(published)
    assert rendered['numbering']['entries']['chart-001']['label'] == '图1'
    assert '![图1 比较]' in rendered['export_document']
    assert '![比较]' in published and '图1' not in published
    reordered = publication.publish_charts(body('chart-002') + '\n\n' + body(), charts)
    assert render_document(reordered)['numbering']['entries']['chart-001']['label'] == '图2'
    changed = chart(svg='<svg><title>changed</title></svg>')
    assert publication.chart_uri(changed) != publication.chart_uri(charts[0])
    publication.publish_charts(body(), [changed])
    assert len(list(tmp_path.rglob('*.svg'))) == 3
    assert '?' not in published


def test_preserve_unowned_images_and_fenced_examples(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    extra = '\n\n![普通图](https://example.org/a.png)\n\n```md\n' + body() + '\n```'
    assert publication.publish_charts(body() + extra, [chart()]).endswith(extra)


@pytest.mark.parametrize('defect', ['unsafe', 'wrong_uri', 'unanchored', 'unknown', 'duplicate', 'dangling'])
def test_invalid_media_never_writes(tmp_path, monkeypatch, defect):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    source, charts = body(), [chart()]
    if defect == 'unsafe':
        charts[0]['svg'] = '<svg onload="alert(1)"/>'
    elif defect == 'wrong_uri':
        source = source.replace('media-placeholder://chart-001', '/static-files/paper-charts/wrong.svg')
    elif defect == 'unanchored':
        source += '\n![extra](' + publication.chart_uri(charts[0]) + ')'
    elif defect == 'unknown':
        source = source.replace('chart-001', 'chart-unknown')
    elif defect == 'duplicate':
        source += '\n' + body()
    else:
        source += '\n[unknown](#block-chart-002)'
    with pytest.raises(ValueError):
        publication.publish_charts(source, charts)
    assert not list(tmp_path.iterdir())


def test_existing_resource_tampering_fails_without_overwrite(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    target = tmp_path / publication.chart_uri(chart()).removeprefix('/static-files/')
    target.parent.mkdir()
    target.write_text('wrong bytes')
    with pytest.raises(ValueError, match='digest'):
        publication.publish_charts(body(), [chart()])
    assert target.read_text() == 'wrong bytes'


def test_validate_all_used_svgs_before_writing_and_ignore_unused(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    charts = [chart(), chart('chart-002', '<svg><script/></svg>')]
    with pytest.raises(ValueError):
        publication.publish_charts(body() + '\n\n' + body('chart-002'), charts)
    assert not list(tmp_path.iterdir())
    publication.publish_charts(body(), charts)
    files = list(tmp_path.rglob('*.svg'))
    assert len(files) == 1
    assert files[0].stat().st_mode & 0o444 == 0o444


def test_legacy_slot_figure_numbers_restore_without_losing_literal_title():
    from lazymind.document_tools.artifacts import strip_paper_chart_numbering
    original = body().replace('![比较]', '![图1 原有标题]')
    numbered = original.replace('></a>', ' data-paper-figure-label="图1"></a>').replace('![', '![图1 ')
    assert '![图1 图1 原有标题]' in numbered
    assert strip_paper_chart_numbering(numbered) == original
    assert strip_paper_chart_numbering(original) == original
    assert '![图1 图1 原有标题]' in render_document(numbered)['export_document']


def test_legacy_numbered_slot_edit_roundtrip_and_fenced_examples():
    from lazymind.document_tools.artifacts import save_document, strip_paper_chart_numbering
    original = '# T\n\n' + body()
    numbered = original.replace('></a>', ' data-paper-figure-label="图1"></a>').replace('![', '![图1 ')
    sample = '\n\n```md\n' + numbered + '\n```'
    assert strip_paper_chart_numbering(numbered + sample) == original + sample
    saved = save_document(numbered.replace('见[', '参见['), numbered)['source_document']
    assert '![比较]' in saved
    assert '![图1 比较]' not in saved
    assert 'data-paper-figure-label' not in saved


PAPERS = [dict(id='ref-001', citation='甲. 第一篇. 2026.'),
          dict(id='ref-002', citation='乙. 第二篇. 2025.')]


def test_first_use_numbers_repeats_reorder_delete_and_idempotence():
    source = '# T\n\n## 结果\n依据[文献:ref-002][文献:ref-001]，再次[文献:ref-002]。'
    saved = publication.publish_citations(source, PAPERS)
    assert saved.count('[\\[1\\]](#paper-ref-ref-002)') == 2
    assert '[\\[2\\]](#paper-ref-ref-001)' in saved
    assert saved.index('[1] 乙.') < saved.index('[2] 甲.')
    assert publication.publish_citations(saved, PAPERS) == saved
    publication.validate_citations(saved, PAPERS, published=True)
    body, region = publication.split_references(saved)
    changed = body.replace('[\\[1\\]](#paper-ref-ref-002)', '') + '\n\n' + region
    regenerated = publication.publish_citations(changed, PAPERS)
    assert 'ref-002' not in regenerated
    assert '[\\[1\\]](#paper-ref-ref-001)' in regenerated
    empty = publication.publish_citations(regenerated.replace('[\\[1\\]](#paper-ref-ref-001)', ''), PAPERS)
    assert 'paper-references-' not in empty


def test_skip_code_and_escape_literal_entries():
    source = '`[文献:ref-unknown]`\n\n```md\n[文献:ref-unknown]\n```\n正文[文献:ref-001]'
    published = publication.publish_citations(source, [dict(id='ref-001', citation='A & B. <Study> [x].')])
    assert '`[文献:ref-unknown]`' in published
    assert '&lt;Study&gt; \\[x\\]' in published
    publication.validate_citations(published, [dict(id='ref-001', citation='A & B. <Study> [x].')], published=True)


@pytest.mark.parametrize('source', ['[文献:ref-missing]', '[文献:ref-001', '[文献：ref-001]',
    '[x](#paper-ref-ref-001)', '[文献:ref-001]\n旧引用[1]',
    '[文献:ref-001]\n## 参考文献\n人工列表', publication.START,
    publication.START + publication.END + '额外正文'])
def test_reject_unresolved_corrupt_or_mixed_references(source):
    with pytest.raises(ValueError):
        publication.publish_citations(source, PAPERS)


def test_keep_legacy_without_guessing_and_protect_generated_region():
    legacy = '# T\n\n旧引用[1] (Author, 2020)。\n\n## 参考文献\n人工列表'
    assert publication.publish_citations(legacy, PAPERS) == legacy
    saved = publication.publish_citations('正文[文献:ref-001]', PAPERS)
    with pytest.raises(ValueError, match='不能直接修改'):
        publication.protect_reference_region(saved, saved.replace('第一篇', '另一篇'))
    with pytest.raises(ValueError, match='未同步'):
        publication.validate_citations(saved.replace('[\\[1\\]]', '[\\[2\\]]'), PAPERS, published=True)
    with pytest.raises(ValueError, match='ref-001'):
        publication.publish_citations(saved, [dict(id='ref-001', citation=None)])


def test_new_numeric_citations_cannot_bypass_id_contract():
    with pytest.raises(ValueError, match='新增文献引用'):
        publication.validate_unmanaged_additions(None, '正文[1]')
    publication.validate_unmanaged_additions('旧稿[1]', '修改后的旧稿[1]')
    with pytest.raises(ValueError, match='混编'):
        publication.publish_citations('原稿 (Author, 2020)，新增[文献:ref-001]', PAPERS)
    with pytest.raises(ValueError, match='无序号'):
        publication.publish_citations('[文献:ref-001]', [dict(id='ref-001', citation='[1] Already numbered')])


def test_chart_id_does_not_claim_writer_text_or_unowned_images(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    ordinary = ('<a id="block-paragraph1"></a>\n正文见[段落](#block-paragraph1)。\n\n'
                '<a id="block-uploaded_photo"></a>\n![原稿照片](https://example.org/photo.png)')
    source = ordinary + '\n\n' + body('Fig_score_A')
    published = publication.publish_charts(source, [chart('Fig_score_A')])
    assert published.startswith(ordinary)
    assert '![比较]' in published
    assert 'data-paper-figure-label' not in published
    exported = render_document(published)['export_document']
    assert '![图1 原稿照片]' in exported
    assert '![图2 比较]' in exported



@pytest.mark.parametrize('payload', [
    '<style>@import "https://example.org/a.css";</style>',
    '<style>path {fill:url(https://example.org/a.svg)}</style>',
    '<path style="fill:expression(alert(1))"/>',
    '<path onclick="alert(1)"/>', '<script>alert(1)</script>',
])
def test_svg_policy_rejects_active_or_remote_content(tmp_path, monkeypatch, payload):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    with pytest.raises(ValueError):
        publication.publish_charts(body('means'), [chart('means', '<svg>' + payload + '</svg>')])
    assert not list(tmp_path.iterdir())


def test_static_styles_and_local_references_publish_without_rewriting(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    svg = ('<svg xmlns="http://www.w3.org/2000/svg"><defs>'
           '<style>* {stroke-linejoin: round; stroke-linecap: butt}</style>'
           '<path id="p1" d="M 0 0 L 1 1"/></defs>'
           '<use href="#p1" style="fill: #333333; stroke: #000000"/></svg>')
    publication.publish_charts(body('means'), [chart('means', svg)])
    assert next(tmp_path.rglob('*.svg')).read_text() == svg


def paper(identifier=None):
    value = {'title': 'A study', 'text': 'Evidence from an abstract.', 'citation': 'A study[Z].',
             'metadata': {'year': 2025}, 'reading': [{'content_type': 'abstract', 'truncated': False}],
             'kind': 'paper', 'user_annotations': {'focus': 'method'}}
    if identifier is not None:
        value['id'] = identifier
    return value


def test_missing_ids_stable_across_order_retry_and_metadata_changes():
    items = [paper('SRC-001'), paper('KB-001'), paper(), {**paper(), 'title': 'Other'}]
    before = copy.deepcopy(items)
    normalized = normalize_papers(items)
    assert items == before
    assert normalized[:2] == items[:2]
    assert normalize_papers(normalized) == normalized
    assert normalize_papers(list(reversed(items))) == list(reversed(normalized))
    changed = {**items[2], 'citation': 'Another format'}
    assert normalize_papers([changed])[0]['id'] == normalized[2]['id']
    inputs = contracts.validate_input('write_document', {'topic': 'T', 'papers': items})
    assert inputs['papers'] == normalized
    inputs['papers'][0]['metadata']['year'] = 1900
    assert items[0]['metadata']['year'] == 2025


@pytest.mark.parametrize('items', [[paper('SRC-001'), paper('SRC-001')], [paper(), paper()],
                                    [paper('bad id')], [paper(123)]])
def test_reject_ambiguous_or_invalid_identity(items):
    with pytest.raises(ValueError):
        normalize_papers(items)


def test_pool_persists_identity_without_mutating_input():
    source = {'topic': 'T', 'papers': [paper()]}
    pool = workflow.pool_from_business(source)
    assigned = workflow.material(pool, 'papers')['content'][0]['id']
    assert assigned.startswith('ref-')
    assert 'id' not in source['papers'][0]
    inputs = contracts.validate_input('write_document', workflow.business_input('write_document', pool))
    assert inputs['papers'][0]['id'] == assigned
    assert workflow.material(pool, 'papers')['source_refs'] == [assigned]


def test_old_pool_missing_identity_creates_papers_version(monkeypatch):
    pool = workflow.pool_from_business({'topic': 'T', 'papers': [paper()]})
    workflow.material(pool, 'papers')['content'][0].pop('id')
    workflow.material(pool, 'papers')['content'][0]['abstract'] = 'Already prepared'
    before = copy.deepcopy(pool)
    def run(step, inputs):
        assert inputs['papers'][0]['id'].startswith('ref-')
        return {'outline': '# T\n\n## 方法', 'manuscript': '# T\n\n## 方法\n正文。', 'is_demo': False}
    monkeypatch.setattr(workflow, 'run_algorithm', run)
    result = workflow.execute_step('write_document', pool)['pool']
    assert pool == before
    assert result['current']['papers']['version'] == 2
    assert workflow.material(result, 'papers')['derived_from'] == [before['current']['papers']]


def test_source_evidence_preserved_and_manuscript_imported():
    papers = [paper('SRC-001'), {**paper('KB-001'), 'citation': 'KB source[Z].'}]
    original = '# T\n\n## Findings\nResult[SRC-001], context[KB-001], again[SRC-001].\n\n## 参考文献\n[SRC-001] A study[Z].\n[KB-001] KB source[Z].'
    normalized = contracts.validate_input('revise_document', {
        'topic': 'T', 'papers': papers, 'evidence': original, 'manuscript': original})
    assert normalized['evidence'] == original
    body = normalized['manuscript']
    assert '## 参考文献' not in body
    assert body.count('[文献:SRC-001]') == 2
    saved = publication.publish_citations(body, papers)
    assert saved.count('[\\[1\\]](#paper-ref-SRC-001)') == 2
    assert '[\\[2\\]](#paper-ref-KB-001)' in saved
    assert publication.normalize_source_citations(saved, papers) == saved
    assert publication.publish_citations(saved, papers) == saved
    publication.validate_citations(saved, papers, published=True)


def test_source_import_skips_code_links_images_and_numeric_ids():
    source = '`[SRC-001]`\n```md\n[SRC-001]\n```\n![SRC-001](image.svg) [SRC-001](https://example.com) [1] [SRC-001]'
    normalized = publication.normalize_source_citations(source, [paper('SRC-001'), paper('1')])
    assert normalized.endswith('[1] [文献:SRC-001]')
    assert normalized.count('[文献:') == 1
    with pytest.raises(ValueError, match='混编'):
        publication.publish_citations(normalized, [paper('SRC-001')])


@pytest.mark.parametrize('body', ['Unknown[SRC-999]',
    'Known[SRC-001]\n\n## 参考文献\n[SRC-001] Different entry'])
def test_source_import_rejects_unknown_or_conflicting_entries(body):
    with pytest.raises(ValueError):
        publication.normalize_source_citations(body, [paper('SRC-001')])


@pytest.mark.parametrize('entry', [None, '', 123])
def test_pr_citation_is_optional_but_nonempty_when_present(entry):
    with pytest.raises(ValueError, match='paper.citation'):
        contracts.validate_input('write_document', {'topic': 'T', 'papers': [{**paper(), 'citation': entry}]})


@pytest.mark.parametrize('heading', ['## References', '**参考文献**', '### Bibliography'])
def test_pr_source_list_heading_and_bullets(heading):
    body = f'Result[SRC-001].\n\n{heading}\n- [SRC-001] A study[Z].'
    assert publication.normalize_source_citations(body, [paper('SRC-001')]) == 'Result[文献:SRC-001].'


def test_generated_body_cannot_leak_pr_source_markers():
    from lazymind.document_tools.paper.writing.common import _validate_reference_markers
    with pytest.raises(ValueError, match='成稿来源引用'):
        _validate_reference_markers('Result[SRC-001]', [paper('SRC-001')])
    _validate_reference_markers('Result[文献:SRC-001]', [paper('SRC-001')])


def test_adjacent_source_citations_and_reference_style_links():
    papers = [paper('SRC-001'), paper('KB-001')]
    body = '[SRC-001][KB-001] and [SRC-001][label]'
    assert publication.normalize_source_citations(body, papers) == (
        '[文献:SRC-001][文献:KB-001] and [SRC-001][label]')


def test_chart_publication_validates_each_representation_once(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    calls = []
    validate = publication.validate_chart_references
    def record(source, charts):
        calls.append(source)
        return validate(source, charts)
    monkeypatch.setattr(publication, 'validate_chart_references', record)
    source = '# T\n\n## 结果\n' + body()
    result = publication.publish_charts(source, [chart()])
    assert calls == [source, result]
    calls.clear()
    assert publication.resolve_chart_sources(source, [chart()]) == result
    assert calls == [source, result]


def test_chart_single_object_allows_multiple_references():
    from lazymind.document_tools.paper.publication import validate_chart_references
    chart = {'id': 'chart-001', 'svg': '<svg/>'}
    obj = '<a id="block-chart-001"></a>\n![图](media-placeholder://chart-001)'
    body = '# 论文\n## 结果\n[结果](#block-chart-001)\n' + obj + '\n## 讨论\n[再次引用](#block-chart-001)'
    assert len(validate_chart_references(body, [chart])) == 1


def test_chart_reference_validation_does_not_backtrack_on_escaped_labels():
    import subprocess
    import sys
    # The old overlapping alternatives take exponential time on this valid
    # escaped label when its destination is not a chart reference.
    code = r"""
from lazymind.document_tools.paper.publication import validate_chart_references
body = '[' + (r'\[' * 100) + '](https://example.com)'
assert validate_chart_references(body, []) == []
"""
    subprocess.run([sys.executable, '-c', code], check=True, timeout=10)
