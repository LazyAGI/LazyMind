"""Uploaded literature remains citable without running the literature-review step."""
import base64
import copy
from dataclasses import replace
import json
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

from lazymind.document_tools.paper import agent_tools as tools, publication
from lazymind.document_tools.paper.source_preparation import is_citable, prepare_sources
from lazymind.document_tools.paper.workflow import material, pool_from_business
from lazymind.document_tools.paper.writing.common import _reference_candidates


PAPER = ('# Collaborative Document Revision\n\nAda Example, Bo Sample\n2024\n'
         'Journal of Document Systems\nDOI: 10.1234/revision\n\n'
         'Abstract: We evaluate an existing document revision method.\n')
NOTES = '# Our experiment records\n\n| method | score |\n|---|---|\n| A | 2 |\n'
OUTLINE = '# Document Revision\n\n## Introduction\nPrior research.\n\n## Methods\nApproach.\n'


@pytest.fixture
def context(monkeypatch, tmp_path):
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'source-preparation-test')
    monkeypatch.setenv('LAZYLLM_HOME', str(tmp_path))
    monkeypatch.setattr('lazyllm.AutoModel', lambda *a, **kw: pytest.fail('paper tool called a model'))
    return tools.PaperContext('run', 'attempt', 'revision', 'write_outline', {}, {
        'paper_topic': 'Document Revision',
        'research_materials': [{'filename': 'prior.pdf.md', 'text': PAPER},
                               {'filename': 'records.md', 'text': NOTES}],
    }, request='Use the uploaded prior study and our records; skip the literature review.')


def annotations(pool):
    papers = material(pool, 'papers')['content']
    return [dict(paper_id=papers[0]['id'], role='paper', evidence=[PAPER], bibliography={
        'title': 'Collaborative Document Revision', 'authors': ['Ada Example', 'Bo Sample'],
        'year': 2024, 'venue': 'Journal of Document Systems', 'doi': '10.1234/revision',
    }), dict(paper_id=papers[1]['id'], role='background', evidence=['Our experiment records'])]


def accepted_pool(bundle, step):
    artifact = next(item for item in bundle['artifacts'] if item['slot'] == step + '_pool')
    return json.loads(base64.b64decode(artifact['value']['content_base64']))


@pytest.mark.parametrize('step,field', [
    ('research_topic', 'question'), ('write_outline', 'outline'), ('write_document', 'manuscript'),
    ('literature_review', 'evidence'),
])
def test_all_writing_entries_prepare_and_publish_uploaded_literature(context, step, field):
    context = replace(context, step_id=step)
    if step == 'write_document':
        context = replace(context, values={**context.values, 'paper_input': {
            'topic': 'Document Revision', 'outline': OUTLINE}})
    pool = tools.base_pool(context)
    original = copy.deepcopy(pool)
    source_id = material(pool, 'papers')['content'][0]['id']
    body = OUTLINE + '\nPrior evidence [文献:' + source_id + '].\n'
    output = {field: body, 'source_preparation': annotations(pool)}
    if step == 'literature_review':
        output['selected_paper_ids'] = [paper['id'] for paper in material(pool, 'papers')['content']]
    with tools.paper_scope(context):
        prepared = tools.paper_prepare_inputs()
        bundle = tools.paper_prepare_candidate(output, [])
        assert tools.verify_publication(context, bundle['artifacts']) == {'valid': True}
    assert prepared['source_preparation']['pending'][0]['paper_id'] == source_id
    accepted = accepted_pool(bundle, step)
    papers = material(accepted, 'papers')['content']
    assert papers[0]['id'] == source_id
    assert papers[0]['text'] == PAPER
    assert papers[0]['source_version'] == material(original, 'papers')['content'][0]['source_version']
    assert papers[0]['review_role'] == 'paper'
    assert papers[0]['title'] == 'Collaborative Document Revision'
    assert papers[0]['authors'] == ['Ada Example', 'Bo Sample']
    assert papers[0]['citation'] == ('Ada Example, Bo Sample. Collaborative Document Revision. '
                                     'Journal of Document Systems. 2024. 10.1234/revision')
    assert papers[1]['review_role'] == 'background'
    assert _reference_candidates(papers) == [{'id': source_id, 'citation': papers[0]['citation']}]
    rendered = publication.publish_citations(body, papers)
    assert '#paper-ref-' + source_id in rendered
    assert '10.1234/revision' in rendered
    assert 'records.md' not in rendered
    assert material(accepted, 'source_data')['content'] == material(original, 'source_data')['content']
    assert pool == original
    assert output['source_preparation'] == annotations(pool)


def test_prepared_pool_is_reused_without_reimport_or_second_classification(context):
    first = tools.base_pool(context)
    with tools.paper_scope(context):
        bundle = tools.paper_prepare_candidate({'outline': OUTLINE, 'source_preparation': annotations(first)}, [])
    accepted = accepted_pool(bundle, 'write_outline')
    continued = replace(context, step_id='write_document', values={
        **context.values, 'write_outline_pool': accepted})
    assert tools.base_pool(continued) == accepted
    with tools.paper_scope(continued):
        prepared = tools.paper_prepare_inputs()
        source_id = material(accepted, 'papers')['content'][0]['id']
        bundle = tools.paper_prepare_candidate({'manuscript': OUTLINE + '[文献:' + source_id + ']'}, [])
    assert 'source_preparation' not in prepared
    final = accepted_pool(bundle, 'write_document')
    assert material(final, 'papers')['content'] == material(accepted, 'papers')['content']
    assert len(material(final, 'papers')['content']) == 2


def test_upload_with_supplied_citation_still_requires_classification(context):
    source = {'filename': 'records.md', 'text': NOTES, 'citation': 'Experiment records.'}
    context = replace(context, values={'paper_topic': 'Revision', 'research_materials': [source]})
    pool = tools.base_pool(context)
    paper = material(pool, 'papers')['content'][0]
    assert not is_citable(paper)
    assert _reference_candidates([paper]) == []
    with tools.paper_scope(context):
        with pytest.raises(ValueError, match='citation'):
            tools.paper_prepare_candidate({'outline': OUTLINE + '[文献:' + paper['id'] + ']'}, [])


@pytest.mark.parametrize('role', ['background', 'unknown'])
def test_non_literature_and_unknown_materials_never_become_references(context, role):
    pool = tools.base_pool(context)
    entries = annotations(pool)
    entries[0] = dict(paper_id=entries[0]['paper_id'], role=role, evidence=[PAPER])
    papers = prepare_sources(material(pool, 'papers')['content'], entries)
    assert not is_citable(papers[0])
    with pytest.raises(ValueError, match='citation'):
        publication.publish_citations('[文献:' + entries[0]['paper_id'] + ']', papers)


@pytest.mark.parametrize('change,error', [
    ({'paper_id': 'invented'}, '实际绑定'),
    ({'evidence': ['This quote does not exist.']}, '实际原文'),
    ({'bibliography': {'title': 'Invented title', 'authors': ['Ada Example']}}, 'title'),
    ({'bibliography': {'title': 'Collaborative Document Revision', 'authors': ['Invented Author']}}, 'authors'),
    ({'bibliography': {'title': 'Collaborative Document Revision', 'authors': ['Ada Example'], 'year': 2025}}, 'year'),
    ({'bibliography': {'title': 'Collaborative Document Revision'}}, '论文身份不足'),
    ({'role': 'background'}, '只有识别为论文'),
    ({'citation': 'Fake citation.'}, 'Extra inputs'),
])
def test_unbound_or_ungrounded_metadata_cannot_be_saved(context, change, error):
    pool = tools.base_pool(context)
    papers = material(pool, 'papers')['content']
    entries = annotations(pool)
    entries[0].update(change)
    before = copy.deepcopy(papers)
    with pytest.raises(ValueError, match=error):
        prepare_sources(papers, entries)
    assert papers == before


def test_existing_bibliography_and_source_identity_are_preserved(context):
    pool = tools.base_pool(context)
    papers = material(pool, 'papers')['content']
    papers[0].update(citation='Existing authoritative citation.', year=2024)
    prepared = prepare_sources(papers, annotations(pool))
    assert prepared[0]['citation'] == 'Existing authoritative citation.'
    assert prepared[0]['id'] == papers[0]['id']
    assert prepare_sources(prepared, annotations(pool)) == prepared
    papers[0]['review_role'] = 'background'
    with pytest.raises(ValueError, match='明确的背景'):
        prepare_sources(papers, annotations(pool))


def test_embedded_reference_list_cannot_supply_the_uploaded_papers_own_identity(context):
    pool = tools.base_pool(context)
    papers = material(pool, 'papers')['content']
    reference = 'Another Study. Other Author. 2020.'
    papers[0]['text'] += '\n## References\n' + reference
    entries = annotations(pool)
    entries[0].update(evidence=[reference], bibliography={
        'title': 'Another Study', 'authors': ['Other Author'], 'year': 2020})
    with pytest.raises(ValueError, match='title'):
        prepare_sources(papers, entries)


def test_bound_metadata_is_preserved_when_body_contains_only_an_abstract(context):
    pool = tools.base_pool(context)
    entries = annotations(pool)
    papers = material(pool, 'papers')['content']
    papers[0].update(entries[0]['bibliography'])
    papers[0]['text'] = 'Abstract: We evaluate document revision methods.'
    entries[0]['evidence'] = [papers[0]['text']]
    prepared = prepare_sources(papers, entries)
    assert all(prepared[0][key] == value for key, value in entries[0]['bibliography'].items())
    assert is_citable(prepared[0])
    entries[0]['bibliography']['year'] = 2025
    with pytest.raises(ValueError, match='不能覆盖已有书目事实 year'):
        prepare_sources(papers, entries)


@pytest.mark.parametrize('newline', ['\n', '\r\n'], ids=['lf', 'crlf'])
def test_native_and_external_file_adapters_share_preparation(context, monkeypatch, tmp_path, newline):
    raw = PAPER.replace('\n', newline).encode('utf-8')
    path = tmp_path / 'study.md'
    path.write_bytes(raw)
    identity = {key: getattr(context, key) for key in
                ('session_id', 'attempt_id', 'revision', 'step_id', 'bindings', 'request', 'launch_request')}
    runtime = SimpleNamespace(
        params={'_paper_execution': identity, 'remote_inputs': {'research_materials': [str(path)]}},
        workspace_path=str(tmp_path),
    )
    monkeypatch.setattr('lazymind.chat.engine.subagent.context.require_context', lambda: runtime)
    native = tools.native_context()
    external = tools.PaperContext.parse({**identity, 'values': {'research_materials': [{
        'filename': 'study.md', '__file_base64': base64.b64encode(raw).decode()}]}})
    native_pool = tools.base_pool(native)
    assert native_pool == tools.base_pool(external)
    entry = annotations(tools.base_pool(context))[0]
    entry['paper_id'] = material(native_pool, 'papers')['content'][0]['id']
    bundles = []
    for ctx in (native, external):
        with tools.paper_scope(ctx):
            bundles.append(tools.paper_prepare_candidate({'outline': OUTLINE, 'source_preparation': [entry]}, []))
    assert accepted_pool(bundles[0], 'write_outline') == accepted_pool(bundles[1], 'write_outline')


def test_legacy_citable_pools_keep_working_without_source_annotations(context):
    pool = pool_from_business({'topic': 'Revision', 'papers': [
        {'id': 'existing', 'title': 'Prior study', 'text': PAPER, 'citation': 'Existing citation.'}]})
    context = replace(context, values={'seed_pool': pool})
    with tools.paper_scope(context):
        assert 'source_preparation' not in tools.paper_prepare_inputs()
        result = tools.paper_prepare_candidate({'outline': OUTLINE + '[文献:existing]'}, [])
    assert material(accepted_pool(result, 'write_outline'), 'papers')['content'][0]['citation'] == 'Existing citation.'


def test_legacy_bound_upload_recognizes_literature_before_extracting_evidence(monkeypatch, tmp_path):
    from lazymind.document_tools.paper import source_materials

    source = tmp_path / 'paper.md'
    source.write_text(PAPER)
    selector = Mock(return_value=json.dumps({
        'blocks': [0, 1, 2], 'review_role': 'paper', 'source_evidence': [PAPER],
        'bibliography': {'title': 'Collaborative Document Revision', 'authors': ['Ada Example', 'Bo Sample'],
                         'year': 2024, 'venue': 'Journal of Document Systems', 'doi': '10.1234/revision'},
    }))
    selector.share.return_value = selector
    selector.prompt.return_value = selector
    monkeypatch.setattr(source_materials, 'AutoModel', lambda **_: selector)
    pool = pool_from_business({'topic': 'Revision'})
    assert source_materials.sync_bound_materials(pool, [str(source)], 'Use this prior study')
    paper = material(pool, 'papers')['content'][0]
    assert paper['review_role'] == 'paper'
    assert is_citable(paper)
    assert paper['title'] == 'Collaborative Document Revision'
    assert '10.1234/revision' in publication.publish_citations('[文献:' + paper['id'] + ']', [paper])


@pytest.mark.parametrize('provided', [
    {'title': 'Collaborative Document Revision'},
    {'title': 'Collaborative Document Revision', 'doi': '10.1234/revision'},
])
def test_partial_bibliography_reuses_bound_authors_and_other_fields(context, provided):
    pool = tools.base_pool(context)
    papers = material(pool, 'papers')['content']
    entries = annotations(pool)
    papers[0].update(entries[0]['bibliography'])
    entries[0]['bibliography'] = provided
    original = copy.deepcopy(papers)
    prepared = prepare_sources(papers, entries)
    assert prepared[0]['authors'] == ['Ada Example', 'Bo Sample']
    assert prepared[0]['year'] == 2024
    assert prepared[0]['venue'] == 'Journal of Document Systems'
    assert prepared[0]['doi'] == '10.1234/revision'
    expected = (
        'Ada Example, Bo Sample. Collaborative Document Revision. '
        'Journal of Document Systems. 2024. 10.1234/revision'
    )
    assert prepared[0]['citation'] == expected
    assert 'authors' not in prepared[0]['citation_preparation']['bibliography']
    assert papers == original


def test_explicit_author_removal_does_not_clear_bound_authors(context):
    pool = tools.base_pool(context)
    papers = material(pool, 'papers')['content']
    papers[0]['authors'] = ['Ada Example', 'Bo Sample']
    entries = annotations(pool)
    entries[0]['bibliography']['authors'] = []
    with pytest.raises(ValueError, match='不能覆盖已有书目事实 authors'):
        prepare_sources(papers, entries)
    assert papers[0]['authors'] == ['Ada Example', 'Bo Sample']


def test_omitted_unknown_authors_can_use_a_grounded_doi(context):
    pool = tools.base_pool(context)
    entries = annotations(pool)
    entries[0]['bibliography'] = {'title': 'Collaborative Document Revision', 'doi': '10.1234/revision'}
    prepared = prepare_sources(material(pool, 'papers')['content'], entries)
    assert prepared[0]['citation'] == 'Collaborative Document Revision. 10.1234/revision'
    assert is_citable(prepared[0])


def test_legacy_extraction_preserves_omitted_bound_authors(context, monkeypatch):
    from lazymind.document_tools.paper import source_materials

    source = {'id': 'known', 'title': 'Collaborative Document Revision', 'text': PAPER,
              'authors': ['Ada Example', 'Bo Sample'], 'year': 2024}
    original = copy.deepcopy(source)
    selector = Mock(return_value=json.dumps({
        'blocks': [0, 1, 2], 'review_role': 'paper', 'source_evidence': [PAPER],
        'bibliography': {'title': 'Collaborative Document Revision', 'doi': '10.1234/revision'},
    }))
    selector.share.return_value = selector
    selector.prompt.return_value = selector
    monkeypatch.setattr(source_materials, 'AutoModel', lambda **_: selector)
    result = source_materials.extract_paper(source, 'Use this prior study')
    assert result['authors'] == source['authors']
    assert 'Ada Example, Bo Sample' in result['citation']
    assert '2024' in result['citation']
    assert source == original


@pytest.mark.parametrize('grounded', [True, False])
def test_revision_preview_and_submission_share_uploaded_source_preparation(context, grounded):
    from lazyllm.tools.agent import ToolManager
    from lazymind.document_tools.paper.review.schema import text_digest

    manuscript = '# Paper\n\n## Methods\nLimited observations.\n'
    pool = pool_from_business({'topic': 'Study', 'manuscript': manuscript})
    context = replace(context, step_id='revise_document', values={
        'seed_pool': pool, 'research_materials': context.values['research_materials']},
        request='Cite the uploaded prior study in Methods, without a literature review.')
    entries = annotations(tools.base_pool(context))
    source_id = entries[0]['paper_id']
    if not grounded:
        entries[0]['bibliography']['authors'] = ['Invented Author']
    plan = {'scope': 'span', 'instructions': [{'modify_type': 'update', 'target_scope': 'fragment',
            'content_ref': {'heading_path': ['Paper', 'Methods']}, 'locator_text': 'Limited observations.',
            'instruction': 'Cite the uploaded study.'}]}
    replacements = {'replacements': [{'old_string': 'Limited observations.',
                    'new_string': 'Limited observations [文献:' + source_id + '].'}]}
    args = {'modify_plan': plan, 'replacements': replacements, 'source_preparation': entries}
    manager = ToolManager([tools.paper_preview_revision])
    assert manager.tools_info['paper_preview_revision'].validate_parameters(args)
    original = copy.deepcopy(context.values)
    output = {**args, 'review_result': {'summary': 'Source reference added.', 'issues': [], 'limitations': []}}
    with tools.paper_scope(context):
        if not grounded:
            with pytest.raises(ValueError, match='authors'):
                tools.paper_preview_revision(**args)
            with pytest.raises(ValueError, match='authors'):
                tools.paper_prepare_candidate(output, [])
            assert context.values == original
            return
        preview = tools.paper_preview_revision(**args)
        assert '#paper-ref-' + source_id in preview['manuscript']
        assert 'Ada Example, Bo Sample' in preview['manuscript']
        assert context.values == original
        bundle = tools.paper_prepare_candidate(output, [])
        assert tools.verify_publication(context, bundle['artifacts']) == {'valid': True}
    accepted = accepted_pool(bundle, 'revise_document')
    assert material(accepted, 'manuscript')['content'] == preview['manuscript']
    assert material(accepted, 'review_data')['content']['manuscript_sha256'] == text_digest(preview['manuscript'])
    assert material(accepted, 'papers')['content'][0]['review_role'] == 'paper'
    assert context.values == original
