"""Paper review scope, material preparation, generation and publication regressions."""
import copy
import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import lazyllm
import pytest
from httpx import HTTPStatusError, ReadTimeout, Request, Response

from lazymind.common.token_estimation import estimate_tokens
from lazymind.document_tools.paper import api, contracts, literature_review, workflow
from lazymind.document_tools.paper.review import materials as review_materials
from lazymind.document_tools.paper.runtime_events import current_events


PAPER = {'id': 'P1', 'title': 'Test paper', 'text': 'Test source material.',
         'citation': 'Test paper[Z].', 'user_annotations': {'focus': 'composition'}}


@pytest.fixture
def paper():
    return copy.deepcopy(PAPER)


@pytest.fixture
def selection(monkeypatch):
    def use(*plans):
        values = iter(plans)
        calls = []

        def choose(prompt, schema):
            calls.append(json.loads(prompt.split('Input JSON:\n', 1)[1]))
            value = next(values)
            if isinstance(value, Exception):
                raise value
            return schema.model_validate(value)

        monkeypatch.setattr(review_materials, 'AutoModel', lambda **_: None)
        monkeypatch.setattr(literature_review, '_input_budget', lambda: 8192)
        monkeypatch.setattr(review_materials, 'WriterToolBase', lambda **_: SimpleNamespace(_call_llm_structured=choose))
        return calls
    return use


def test_scope_selects_requested_papers_and_keeps_background(paper, selection):
    papers = [paper, {'id': 'NEW', 'title': 'New paper', 'text': 'Paper body'},
              {'id': 'BRIEF', 'title': 'System brief', 'text': 'Experiment CPU constraints'}]
    before = copy.deepcopy(papers)
    calls = selection({'material_roles': {'M1': 'exclude', 'M2': 'paper', 'M3': 'background'}, 'queries': {}})
    result = review_materials.prepare({'papers': papers, 'request': 'Only the new paper; use the brief as context'}, [])
    assert papers == before
    assert [p['id'] for p in result] == ['NEW', 'BRIEF']
    assert result[1]['review_role'] == 'background'
    assert calls[0]['papers'][1]['id'] == 'M2'
    assert calls[0]['material_role_keys'] == ['M1', 'M2', 'M3']


def test_singleton_query_count_arrays_are_normalized_before_retrieval(paper, sciverse, selection):
    selection({'material_roles': {'M1': 'paper'}, 'queries': {'unused library query': ['12']},
               'external_queries': {'related': ['10']}},
              {'material_roles': {'M1': 'paper'}, 'queries': {}})
    result = review_materials.prepare({'papers': [paper]}, [])
    assert [p['title'] for p in result] == ['Test paper', 'Related research']
    sciverse.meta_search.assert_called_once_with(
        query='related', page_size=30, include_content=False, year_from=None, year_to=None)


def test_imported_study_background_keeps_its_role_during_material_reselection(paper, selection):
    background = dict(id='brief', title='Current study', text='Our methods and supplied measurements.',
                      review_role='background')
    selection({'material_roles': {'M1': 'paper', 'M2': 'paper'}, 'queries': {}})
    result = review_materials.prepare({'papers': [background, paper], 'request': 'Review prior work'}, [])
    assert result[0]['review_role'] == 'background'
    assert review_materials._paper_count(result) == 1


@pytest.mark.parametrize('count', [[], ['12', '15'], ['not a count'], ['0'], ['201'], [['12']]])
@pytest.mark.parametrize('field', ['queries', 'external_queries'])
def test_query_count_normalization_still_rejects_invalid_or_ambiguous_values(count, field):
    with pytest.raises(ValueError):
        review_materials.SearchPlan.model_validate({'queries': {}, field: {'related': count}})


def test_fixed_scope_preserves_exact_selection_without_model_or_kb(paper, monkeypatch):
    monkeypatch.setattr(review_materials, '_select', lambda *_: pytest.fail('frozen papers were reselected'))
    result = review_materials.prepare({'papers': [paper]}, ['other-library'], fixed_scope=True)
    assert result == [paper]
    result[0]['user_annotations']['focus'] = 'changed'
    assert paper['user_annotations']['focus'] == 'composition'


def test_scope_cannot_add_unavailable_material(paper, selection):
    selection({'material_roles': {'M1': 'paper', 'M2': 'paper'}, 'queries': {}})
    with pytest.raises(ValueError):
        review_materials.prepare({'papers': [paper]}, [])


def test_background_alone_is_not_a_review(paper, selection):
    selection({'material_roles': {'M1': 'background'}, 'queries': {}})
    with pytest.raises(contracts.MissingMaterials):
        review_materials.prepare({'papers': [paper]}, [])


def test_kb_selection_reads_only_chosen_documents_and_merges_hits(monkeypatch, selection):
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit
    calls = selection({'material_roles': {}, 'queries': {'timed learning': 4}},
                      {'material_roles': {'M1': 'paper', 'M2': 'exclude'}, 'queries': {}})
    search = Mock(return_value={'items': [
        {'kb_id': 'library', 'docid': doc, 'file_name': doc, 'text': text}
        for doc, text in [('one', 'first hit'), ('one', 'second hit'), ('two', 'unrelated hit')]
    ]})
    read = Mock(return_value={'content': {'text': 'Complete document body', 'truncated': False}})
    monkeypatch.setattr(KBToolkit, 'kb_search', search)
    monkeypatch.setattr(KBToolkit, 'read_document', read)
    result = review_materials.prepare({'papers': [], 'request': 'Only use the selected knowledge base'}, ['library'])
    search.assert_called_once_with('timed learning', k_max=12, kb_ids=['library'])
    read.assert_called_once_with('library', 'one')
    assert calls[1]['papers'][0]['snippet'] == 'first hit\n\nsecond hit'
    assert [p['id'] for p in result] == ['kb-library-one']
    assert result[0]['text'] == 'Complete document body'
    assert result[0]['reading']['content_type'] == 'knowledge_base_document'


def test_kb_candidates_are_selected_together_with_existing_papers(paper, monkeypatch, selection):
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit
    calls = selection({'material_roles': {'M1': 'paper'}, 'queries': {'related methods': 3}},
                      {'material_roles': {'M1': 'paper'}, 'queries': {}})
    monkeypatch.setattr(KBToolkit, 'kb_search', Mock(return_value={'items': [
        {'kb_id': 'library', 'docid': 'duplicate', 'file_name': paper['title'] + '.pdf', 'text': paper['text']},
        {'kb_id': 'library', 'docid': 'new', 'file_name': 'Related paper', 'text': 'Retrieved excerpt'},
    ]}))
    read = Mock(return_value={'content': {'text': 'Related paper body', 'truncated': False}})
    monkeypatch.setattr(KBToolkit, 'read_document', read)
    result = review_materials.prepare({'papers': [paper], 'request': 'Use attachments and the library'}, ['library'])
    assert [p['title'] for p in calls[1]['papers']] == ['Related paper']
    assert calls[1]['retained_papers'] == [{'title': paper['title'], 'review_role': 'paper'}]
    assert [p['id'] for p in result] == ['P1', 'kb-library-new']
    assert {key: value for key, value in result[0].items() if key != 'retrieval_warnings'} == paper
    read.assert_called_once_with('library', 'new')


def test_review_requires_readable_supplied_papers():
    with pytest.raises(contracts.MissingMaterials):
        api.run_algorithm('literature_review', {'topic': 'Learning', 'papers': [{'id': 'P1', 'title': 'Missing text'}]})


def test_review_without_readable_papers_cannot_use_model_knowledge(monkeypatch):
    generate = Mock(side_effect=AssertionError('Missing papers must not trigger synthesis'))
    monkeypatch.setattr(literature_review, 'AutoModel', generate)
    with pytest.raises(contracts.MissingMaterials, match='模型知识'):
        api.run_algorithm('literature_review', {'topic': 'Learning'})
    generate.assert_not_called()


@pytest.fixture
def sciverse(monkeypatch):
    provider = Mock()
    provider.meta_search.return_value = {'items': [
        {'title': 'Related research', 'url': 'https://doi.org/10.1/related', 'snippet': 'Retrieved abstract',
         'source': 'sciverse', 'extra': {'doc_id': 'doc-2', 'doi': '10.1/related', 'year': 2025}},
    ]}
    provider.get_content.return_value = {
        'content': 'Complete Sciverse paper',
        'extra': {'content_read': {'content_type': 'document', 'fallback': False, 'more': False}},
    }
    monkeypatch.setattr(review_materials, 'sciverse_provider', lambda: provider)
    return provider


def test_topic_only_searches_real_papers_with_explicit_years(sciverse, selection):
    calls = selection({'material_roles': {}, 'queries': {}, 'external_queries': {'learning methods': 5},
                       'year_from': 2024, 'year_to': 2026},
                      {'material_roles': {'M1': 'paper'}, 'queries': {}})
    result = review_materials.prepare({'topic': 'Learning', 'request': 'Find five papers from 2024 to 2026'}, [])
    sciverse.meta_search.assert_called_once_with(
        query='learning methods', page_size=15, include_content=False, year_from=2024, year_to=2026)
    assert result[0]['text'] == 'Complete Sciverse paper'
    assert result[0]['doi'] == '10.1/related' and result[0]['year'] == 2025
    assert result[0]['reading']['truncated'] is False
    assert calls[1]['plan_search'] is False


@pytest.mark.parametrize('bounds,requested,expected', [
    ((12, 20), None, 20), ((8, 12), None, 12), ((12, 20), 10, 10), ((12, 20), 30, 30),
])
def test_review_count_limits_retrieval_and_fulltext_reads(
        paper, sciverse, selection, bounds, requested, expected):
    hits = [
        {'title': f'Related paper {index}', 'snippet': 'Actual abstract', 'extra': {'doc_id': str(index)}}
        for index in range(45)
    ]
    calls = selection({'material_roles': {'M1': 'paper'}, 'queries': {}, 'external_queries': {'related': 50},
                       'requested_paper_count': requested},
                      {'material_roles': {f'M{index + 1}': 'paper' for index in range(len(hits))}, 'queries': {}})
    # A provider returning more than requested must not bypass the reading limit.
    sciverse.meta_search.return_value = {'items': hits}
    with review_materials.config.temp('paper_review_min_paper_count', bounds[0]), \
            review_materials.config.temp('paper_review_max_paper_count', bounds[1]):
        result = review_materials.prepare({'papers': [paper], 'request': 'Use related papers'}, [])
    assert len(result) == expected and result[0] == paper
    assert sciverse.meta_search.call_args.kwargs['page_size'] == (expected - 1) * 3
    assert sciverse.get_content.call_count == expected - 1
    assert calls[1]['paper_count_range'] == {'min': requested or bounds[0], 'max': expected}


@pytest.mark.parametrize('count', [12, 14, 17, 20])
def test_default_range_accepts_selected_coverage_without_filling_the_maximum(paper, sciverse, selection, count):
    hits = [{'title': f'Related paper {i}', 'snippet': 'Relevant evidence', 'extra': {'doc_id': str(i)}}
            for i in range(30)]
    calls = selection({'material_roles': {'M1': 'paper'}, 'queries': {}, 'external_queries': {'related': 50}},
                      {'material_roles': {f'M{i + 1}': 'paper' if i < count - 1 else 'exclude'
                                          for i in range(30)}, 'queries': {}})
    sciverse.meta_search.return_value = {'items': hits, 'total_pages': 3}
    result = review_materials.prepare({'papers': [paper]}, [])
    assert review_materials._paper_count(result) == count
    assert calls[0]['paper_count_range'] == calls[1]['paper_count_range'] == {'min': 12, 'max': 20}
    assert sciverse.meta_search.call_count == 1
    assert sciverse.get_content.call_count == count - 1


def test_existing_papers_within_range_do_not_trigger_filling_to_maximum(sciverse, selection):
    papers = [{'id': f'P{i}', 'title': f'Supplied {i}', 'text': 'Paper content'} for i in range(14)]
    selection({'material_roles': {f'M{i + 1}': 'paper' for i in range(14)},
               'queries': {}, 'external_queries': {'related': 20}})
    assert review_materials.prepare({'papers': papers}, []) == papers
    sciverse.meta_search.assert_not_called()


def test_invalid_default_range_is_rejected_before_model_calls(selection):
    calls = selection()
    with review_materials.config.temp('paper_review_min_paper_count', 21):
        with pytest.raises(ValueError, match='下限不能大于上限'):
            review_materials.prepare({'papers': []}, [])
    assert calls == []


def test_all_queries_and_sources_share_the_remaining_budget(paper, sciverse, selection, monkeypatch):
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit
    selection({'material_roles': {'M1': 'paper'}, 'queries': {'methods': 50, 'applications': 50},
               'external_queries': {'related': 50}},
              {'material_roles': {'M1': 'paper'}, 'queries': {}})
    search = Mock(return_value={'items': []})
    monkeypatch.setattr(KBToolkit, 'kb_search', search)
    result = review_materials.prepare({'papers': [paper]}, ['library'])
    counts = [call.kwargs['k_max'] for call in search.call_args_list]
    counts.append(sciverse.meta_search.call_args.kwargs['page_size'])
    assert all(count > 0 for count in counts) and sum(counts) == 57
    assert len(result) == 2


def test_supplied_papers_above_default_are_preserved_without_more_retrieval(sciverse, selection):
    papers = [{'id': f'P{index}', 'title': f'Supplied {index}', 'text': 'Source text'} for index in range(25)]
    selection({'material_roles': {f'M{index + 1}': 'paper' for index in range(len(papers))},
               'queries': {}, 'external_queries': {'related': 50}})
    assert review_materials.prepare({'papers': papers}, []) == papers
    sciverse.meta_search.assert_not_called()
    sciverse.get_content.assert_not_called()


def test_rerun_limits_old_retrieved_papers_but_keeps_upload(paper, sciverse, selection):
    papers = [paper, *[
        {'id': f'R{index}', 'title': f'Retrieved {index}', 'text': 'Source text',
         'source': 'sciverse' if index % 2 else 'knowledge_base'} for index in range(40)
    ]]
    selection({'material_roles': {f'M{index + 1}': 'paper' for index in range(len(papers))},
               'queries': {}, 'external_queries': {'related': 50}})
    result = review_materials.prepare({'papers': papers}, [])
    assert len(result) == 20 and result[0] == paper
    assert len(papers) == 41
    sciverse.meta_search.assert_not_called()


def test_candidate_screening_shortfall_fetches_next_page_within_range(paper, sciverse, selection):
    second = {'id': 'P2', 'title': 'Second uploaded paper', 'text': 'Full source'}
    brief = {'id': 'BRIEF', 'title': 'System brief', 'text': 'Background constraints'}

    def hits(start):
        return [{'title': f'Candidate {index}', 'snippet': 'Actual abstract', 'extra': {'doc_id': str(index)}}
                for index in range(start, start + 12)]
    calls = selection(
        {'material_roles': {'M1': 'background', 'M2': 'paper', 'M3': 'paper'},
         'queries': {}, 'external_queries': {'research agents': 50}},
        {'material_roles': {f'M{index + 1}': 'paper' if index < 4 else 'exclude' for index in range(12)},
         'queries': {}},
        {'material_roles': {f'M{index + 1}': 'paper' for index in range(12)}, 'queries': {}},
    )
    sciverse.meta_search.side_effect = [
        {'items': hits(0), 'total_pages': 2}, {'items': hits(12), 'total_pages': 2},
    ]
    result = review_materials.prepare({'papers': [brief, paper, second]}, [])
    assert review_materials._paper_count(result) == 18
    assert len(result) == 19 and result[0]['review_role'] == 'background'
    assert sciverse.get_content.call_count == 16
    assert [call.kwargs.get('page', 1) for call in sciverse.meta_search.call_args_list] == [1, 2]
    assert calls[2]['paper_count_range'] == {'min': 12, 'max': 20} and len(calls[2]['retained_papers']) == 7


def test_exhausted_results_disclose_shortfall_without_padding(paper, sciverse, selection):
    selection({'material_roles': {'M1': 'paper'}, 'queries': {}, 'external_queries': {'related': 5}},
              {'material_roles': {'M1': 'paper'}, 'queries': {}})
    result = review_materials.prepare({'papers': [paper]}, [])
    assert review_materials._paper_count(result) == 2
    assert any('实际仅取得 2 篇' in warning for warning in result[0]['retrieval_warnings'])
    assert sciverse.meta_search.call_count == 1


def test_repeated_pages_are_bounded_and_do_not_reread_papers(paper, sciverse, selection):
    selection({'material_roles': {'M1': 'paper'}, 'queries': {}, 'external_queries': {'related': 5}},
              {'material_roles': {'M1': 'paper'}, 'queries': {}})
    sciverse.meta_search.return_value['total_pages'] = 10
    result = review_materials.prepare({'papers': [paper]}, [])
    assert review_materials._paper_count(result) == 2
    assert sciverse.meta_search.call_count == 3
    sciverse.get_content.assert_called_once()


def test_only_supplied_scope_never_calls_retrieval(paper, sciverse, selection, monkeypatch):
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit
    search = Mock(side_effect=AssertionError('Excluded knowledge base was queried'))
    monkeypatch.setattr(KBToolkit, 'kb_search', search)
    selection({'material_roles': {'M1': 'paper'}, 'queries': {}, 'external_queries': {}})
    assert review_materials.prepare({'papers': [paper], 'request': 'Only use this attachment'}, ['library']) == [paper]
    search.assert_not_called()
    sciverse.meta_search.assert_not_called()


def test_explicit_expansion_can_extend_a_frozen_selection(paper, sciverse, selection):
    calls = selection({'material_roles': {'M1': 'paper'}, 'queries': {}, 'external_queries': {'related': 2}},
                      {'material_roles': {'M1': 'paper'}, 'queries': {}})
    result = review_materials.prepare(
        {'papers': [paper], 'request': 'Add related Sciverse papers'}, [], fixed_scope=True)
    assert calls[0]['fixed_scope'] is True
    assert result[0]['id'] == paper['id'] and len(result) == 2
    assert sciverse.meta_search.call_count == 1


def test_sciverse_duplicate_preserves_uploaded_identity_and_skips_read(paper, sciverse, selection):
    paper['doi'] = 'https://doi.org/10.1/RELATED'
    before = copy.deepcopy(paper)
    selection({'material_roles': {'M1': 'paper'}, 'queries': {}, 'external_queries': {'related': 2}})
    result = review_materials.prepare({'papers': [paper]}, [])
    assert [{key: value for key, value in item.items() if key != 'retrieval_warnings'} for item in result] == [before]
    assert paper == before
    sciverse.get_content.assert_not_called()


def test_kb_and_sciverse_expand_together_without_querying_other_libraries(paper, sciverse, selection, monkeypatch):
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit
    selection({'material_roles': {'M1': 'paper'}, 'queries': {'learning': 3},
               'external_queries': {'related': 4}},
              {'material_roles': {'M1': 'paper', 'M2': 'paper'}, 'queries': {}})
    search = Mock(return_value={'items': [
        {'kb_id': 'library', 'docid': 'kb-new', 'file_name': 'KB paper', 'text': 'KB excerpt'},
        {'kb_id': 'unselected', 'docid': 'private', 'file_name': 'Other paper', 'text': 'Other excerpt'},
    ]})
    monkeypatch.setattr(KBToolkit, 'kb_search', search)
    read = Mock(return_value={'content': {'text': 'Complete KB paper', 'truncated': False}})
    monkeypatch.setattr(KBToolkit, 'read_document', read)
    result = review_materials.prepare({'topic': 'Learning', 'papers': [paper]}, ['library'])
    assert [item['title'] for item in result] == ['Test paper', 'KB paper', 'Related research']
    assert [item['text'] for item in result] == [paper['text'], 'Complete KB paper', 'Complete Sciverse paper']
    search.assert_called_once_with('learning', k_max=9, kb_ids=['library'])
    read.assert_called_once_with('library', 'kb-new')


def test_search_failure_keeps_supplied_papers_and_discloses_it(paper, sciverse, selection):
    selection({'material_roles': {'M1': 'paper'}, 'queries': {}, 'external_queries': {'related': 3}})
    sciverse.meta_search.side_effect = ReadTimeout('service unavailable')
    result = review_materials.prepare({'papers': [paper]}, [])
    assert result[0]['id'] == paper['id'] and result[0]['text'] == paper['text']
    assert 'Sciverse' in result[0]['retrieval_warnings'][0]
    assert 'retrieval_warnings' not in paper


@pytest.mark.parametrize('role', ['paper', 'background'])
def test_kb_read_failure_keeps_actual_excerpt_and_other_sources(paper, selection, monkeypatch, role):
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit
    selection({'material_roles': {'M1': 'paper'}, 'queries': {'learning': 3}},
              {'material_roles': {'M1': role}, 'queries': {}})
    monkeypatch.setattr(KBToolkit, 'kb_search', Mock(return_value={'items': [
        {'kb_id': 'library', 'docid': 'new', 'file_name': 'Related paper', 'text': 'Actual retrieved excerpt'},
    ]}))
    monkeypatch.setattr(KBToolkit, 'read_document', Mock(side_effect=ReadTimeout('service unavailable')))
    result = review_materials.prepare({'papers': [paper]}, ['library'])
    assert result[0]['text'] == paper['text']
    assert result[1]['text'] == 'Actual retrieved excerpt'
    assert result[1].get('review_role', 'paper') == role
    assert result[1]['reading'] == {'content_type': 'knowledge_base_excerpt', 'truncated': True}
    assert '知识库全文读取失败' in result[0]['retrieval_warnings'][0]


def test_no_search_results_cannot_become_a_successful_review(sciverse, selection):
    selection({'material_roles': {}, 'queries': {}, 'external_queries': {'nothing': 3}})
    sciverse.meta_search.return_value = {'items': []}
    with pytest.raises(contracts.MissingMaterials, match='Sciverse.*未找到'):
        review_materials.prepare({'topic': 'Learning'}, [])


def test_partial_content_is_not_replaced_with_a_later_fallback(sciverse, selection):
    selection({'material_roles': {}, 'queries': {}, 'external_queries': {'related': 3}},
              {'material_roles': {'M1': 'paper'}, 'queries': {}})
    sciverse.get_content.side_effect = [
        {'content': 'Verified first page', 'extra': {'content_read': {
            'content_type': 'document', 'fallback': False, 'more': True, 'next_offset': 100}}},
        {'content': 'Repeated abstract', 'extra': {'content_read': {'fallback': True}}},
    ]
    result = review_materials.prepare({'topic': 'Learning'}, [])
    assert result[0]['text'] == 'Verified first page'
    assert result[0]['reading']['truncated'] is True
    assert '全文未完整取得' in result[0]['retrieval_warnings'][0]
    assert [call.kwargs['offset'] for call in sciverse.get_content.call_args_list] == [0, 100]


def test_retrieval_notices_stay_in_progress_and_out_of_review_body(monkeypatch):
    from lazymind.document_tools.paper import runtime_events
    monkeypatch.setattr(literature_review, '_input_budget', lambda: 8192)
    monkeypatch.setattr(literature_review, 'AutoModel', lambda **_: None)
    monkeypatch.setattr(runtime_events, 'generate_markdown', lambda *_args, **_kwargs: '# Review\n\nBody')
    progress = Mock()
    monkeypatch.setattr(literature_review, 'report_progress', progress)
    document = literature_review._render({'papers': [{'retrieval_warnings': ['Sciverse 检索失败。']}],
                                         'topic': 'Learning'}, [], [])
    assert document.startswith('# Review\n\nBody')
    assert 'Sciverse 检索失败' not in document
    progress.assert_any_call(84, '资料获取提示：Sciverse 检索失败。')


@pytest.mark.parametrize('heading', ['参考文献', 'References'])
def test_references_are_numbered_from_actual_papers_and_background_cannot_be_cited(monkeypatch, heading):
    from lazymind.document_tools.paper import runtime_events
    notes = [{'id': 'upload-academic', 'title': 'Uploaded paper', 'citation': 'Uploaded scholarly paper[J].'},
             {'id': 'sciverse-retrieved', 'title': 'Retrieved paper', 'citation': 'Retrieved paper[J].'}]
    background = [{'id': 'upload-data', 'title': '实验数据', 'citation': 'Incorrect paper citation[Z].'}]
    monkeypatch.setattr(literature_review, '_input_budget', lambda: 8192)
    monkeypatch.setattr(literature_review, 'AutoModel', lambda **_: None)
    monkeypatch.setattr(runtime_events, 'generate_markdown', lambda *_args, **_kwargs:
                        '# Review\n\nResult [upload-academic] [sciverse-retrieved]. Context [upload-data].\n\n'
                        f'## {heading}\n\n[upload-data] Wrong reference\n[upload-academic] Duplicate')
    document = literature_review._render({'papers': notes + background}, notes, background)
    assert 'Result [\\[1\\]](#paper-ref-upload-academic) [\\[2\\]](#paper-ref-sciverse-retrieved). Context .' in document
    assert document.endswith('<a id="paper-references-start"></a>\n\n'
                             f'<a id="block-paper-references"></a>\n\n## {heading}\n\n'
                             '<a id="paper-ref-upload-academic"></a>\n\n[1] Uploaded scholarly paper\\[J\\].\n\n'
                             '<a id="paper-ref-sciverse-retrieved"></a>\n\n[2] Retrieved paper\\[J\\].\n\n'
                             '<a id="paper-references-end"></a>')
    assert '[upload-' not in document and '[sciverse-' not in document and 'Wrong reference' not in document
    assert 'Incorrect paper citation' not in document


@pytest.mark.parametrize('language,heading', [('zh', '参考文献'), ('en', 'References')])
def test_review_uses_manuscript_reference_format_and_preserves_source_order(monkeypatch, language, heading):
    from lazymind.document_tools.paper import publication, runtime_events
    notes = [{'id': 'P1', 'citation': 'A & B. <Study> *title* [J].'},
             {'id': 'P2', 'citation': 'Second paper.'},
             {'id': 'P3', 'citation': 'Reviewed but not cited in prose.'}]
    monkeypatch.setattr(literature_review, '_input_budget', lambda: 8192)
    monkeypatch.setattr(literature_review, 'AutoModel', lambda **_: None)
    monkeypatch.setattr(runtime_events, 'generate_markdown', lambda *_args, **_kwargs:
                        f'# Review\n\nSecond [P2], first [P1], again [P2].\n\n## {heading}')
    document = literature_review._render({'papers': notes}, notes, [])
    body, references = publication.split_references(document)
    assert body == ('# Review\n\nSecond [\\[2\\]](#paper-ref-P2), first [\\[1\\]](#paper-ref-P1), '
                    'again [\\[2\\]](#paper-ref-P2).')
    manuscript = publication.publish_citations('[文献:P1] [文献:P2] [文献:P3]', notes, paper_language=language)
    assert references == publication.split_references(manuscript)[1]
    assert '[1] A &amp; B. &lt;Study&gt; \\*title\\* \\[J\\].' in references
    assert '[3] Reviewed but not cited in prose.' in references


def test_background_discovered_during_reading_cannot_produce_a_paper_review(monkeypatch):
    monkeypatch.setattr(literature_review, '_input_budget', lambda: 8192)
    monkeypatch.setattr(literature_review, 'AutoModel', lambda **_: None)
    monkeypatch.setattr(literature_review, 'WriterToolBase', lambda **_: SimpleNamespace(
        _call_llm_structured=lambda _prompt, schema: schema.model_validate({
            'note': 'User experiment data, not published research.', 'citation': '', 'review_role': 'background',
        })))
    with pytest.raises(contracts.MissingMaterials, match='背景或实验资料'):
        literature_review.run({'topic': 'Learning', 'papers': [{
            'id': 'upload-data', 'title': '实验数据', 'text': 'Internal experiment results',
            'citation': 'A previously invented paper reference[Z].',
        }]})


def test_review_output_allows_background_without_citation_but_still_requires_paper_citation():
    from lazymind.document_tools.paper.writing.common import _reference_candidates
    papers = [copy.deepcopy(PAPER), {'id': 'upload-data', 'title': '实验数据', 'text': 'User data',
                                     'review_role': 'background'}]
    output = {'papers': papers, 'evidence': '# Review', 'is_demo': False}
    assert contracts.validate_output('literature_review', output) == output
    assert _reference_candidates(papers) == [{'id': PAPER['id'], 'citation': PAPER['citation']}]
    papers[0].pop('citation')
    with pytest.raises(ValueError, match='paper.citation'):
        contracts.validate_output('literature_review', output)


@pytest.fixture
def handoff(monkeypatch):
    from lazymind.chat.service import academic_handoff
    value = {'kind': 'review', 'input': {
        'user_instruction': 'Compare the selected papers', 'candidate_directions': ['learning', 'vision'],
        'selected_directions': ['learning'], 'papers': [{
            'paper': {'paper_id': 'global-paper', 'title': 'External paper', 'abstract': None, 'source_version': 'v2'},
            'is_seed': False, 'is_important': True, 'direction_relation': 'Supports learning',
        }],
    }}
    monkeypatch.setattr(academic_handoff, 'academic_handoff_context', lambda: copy.deepcopy(value))
    first = {'status': 'ready', 'content_level': 'fulltext', 'content_hash': 'hash-v2', 'source_version': 'v2',
             'paper': value['input']['papers'][0]['paper'], 'pages': [{'page': 1, 'text': 'First page'}],
             'warnings': [], 'next_cursor': 'cursor-v2'}
    last = {**first, 'pages': [{'page': 0, 'text': 'Unknown page'}],
            'warnings': ['Page unknown'], 'next_cursor': ''}
    read = Mock(side_effect=[first, last])
    monkeypatch.setattr(academic_handoff, 'academic_read_paper', read)
    return value, read


def test_handoff_preserves_identity_scope_and_versioned_pages(handoff):
    original, read = handoff
    result = review_materials.handoff_input(review_materials.current_handoff())
    assert result['topic'] == 'learning'
    assert original['input']['user_instruction'] in result['request']
    assert 'selected_directions' in result['request']
    paper = result['papers'][0]
    assert paper['id'] == paper['paper_id'] == 'global-paper'
    assert paper['is_important'] is True and paper['direction_relation'] == 'Supports learning'
    assert paper['text'] == '<!-- page:1 -->\nFirst page\n\nUnknown page'
    assert paper['source_version'] == 'v2'
    assert paper['reading'] == {'content_type': 'fulltext', 'content_hash': 'hash-v2', 'warnings': ['Page unknown']}
    assert [call.kwargs for call in read.call_args_list] == [
        {'paper_id': 'global-paper'}, {'cursor': 'cursor-v2'}]


@pytest.mark.parametrize('status', [404, 403])
def test_handoff_lookup_distinguishes_ordinary_chat_from_denied_access(monkeypatch, status):
    from lazymind.chat.service import academic_handoff

    request = Request('GET', 'http://core/academic/chat-context')
    response = Response(status, request=request)
    monkeypatch.setattr(academic_handoff, 'academic_handoff_context',
                        response.raise_for_status)
    if status == 404:
        assert review_materials.current_handoff() is None
    else:
        with pytest.raises(HTTPStatusError):
            review_materials.current_handoff()


@pytest.mark.parametrize('resume', [False, True], ids=['first-entry', 'saved-pool'])
def test_handoff_enters_f2_and_publishes_reusable_artifacts(handoff, tmp_path, monkeypatch, resume, selection):
    from lazymind.chat.engine.subagent.context import SubAgentContext

    monkeypatch.setattr(workflow, '_conversation_papers', lambda *_: pytest.fail('handoff read other attachments'))
    plans = selection({'material_roles': {'M1': 'paper'}, 'queries': {}})
    received = []

    def review(inputs):
        received.append(copy.deepcopy(inputs))
        return {'evidence': '# Review result', 'is_demo': False,
                'papers': [{**paper, 'citation': 'External paper.'} for paper in inputs['papers']]}

    monkeypatch.setattr(literature_review, 'run', review)
    bound = {}
    if resume:
        previous = workflow.pool_from_business({
            'topic': 'Updated topic', 'question': 'Updated research question', 'evidence': '# Edited review',
            'papers': [{'id': 'global-paper', 'title': 'External paper', 'text': 'Saved full text',
                        'source_version': 'v2', 'is_important': True}],
        })
        path = tmp_path / 'saved-pool.json'
        path.write_text(json.dumps(previous))
        bound['recovery_literature_pool'] = str(path)
        handoff[1].side_effect = AssertionError('resumed review reread the original handoff')
    events = []
    slots = ['literature_review_pool', 'literature_review_document']
    lazyllm.globals['subagent_ctx'] = SubAgentContext(
        task_id='handoff-review', conversation_id='test', agent_type='workflow_step',
        objective='Review selected papers', workspace_path=str(tmp_path), input_slots=[],
        output_slots=slots, db=None, emit=events.append,
        params={'step_id': 'literature_review', 'user_input': 'Generate the review',
                'remote_inputs': bound, 'output_slot_types': dict.fromkeys(slots, 'file')},
    )
    assert workflow.run_paper_step()['status'] == 'completed'
    assert [paper['id'] for paper in received[0]['papers']] == ['global-paper']
    assert plans[0]['fixed_scope'] is True
    saved = next(e for e in events if e['type'] == 'artifact' and e['slot'] == slots[0])
    pool = workflow._read_bound(saved['value']['path'])
    if resume:
        assert received[0]['topic'] == 'Updated topic'
        assert received[0]['question'] == 'Updated research question'
        assert received[0]['evidence'] == '# Edited review'
        assert received[0]['papers'] == workflow.material(previous, 'papers')['content']
        assert pool['materials'][:len(previous['materials'])] == previous['materials']
        assert json.loads(path.read_text()) == previous
        handoff[1].assert_not_called()
    else:
        assert handoff[0]['input']['user_instruction'] in received[0]['request']
    downstream = workflow.business_input('refine_topic', pool)
    assert downstream['papers'][0]['source_version'] == 'v2'
    assert downstream['papers'][0]['is_important'] is True
    assert downstream['evidence'] == '# Review result'


def test_handoff_waits_for_pending_fulltext(handoff, monkeypatch):
    _, read = handoff
    pages = list(read.side_effect)
    read.side_effect = [{'status': 'processing'}, *pages]
    monkeypatch.setattr(review_materials.time, 'sleep', lambda _: None)
    result = review_materials.handoff_input(handoff[0])
    assert 'First page' in result['papers'][0]['text']
    assert [call.kwargs for call in read.call_args_list[:2]] == [{'paper_id': 'global-paper'}] * 2


@pytest.mark.parametrize('status', ['failed', 'unavailable'])
def test_unready_handoff_cannot_become_a_successful_review(handoff, status):
    _, read = handoff
    read.side_effect = None
    read.return_value = {'status': status, 'content_level': 'none', 'warnings': ['Not ready']}
    with pytest.raises(contracts.MissingMaterials, match='Not ready'):
        review_materials.handoff_input(handoff[0])


@pytest.mark.parametrize('instruction,group,field', [
    (review_materials.MATERIAL_SELECTION_PROMPT, 'papers', 'snippet'),
    (literature_review.SYNTHESIS_PROMPT, 'background', 'note'),
])
def test_prompts_share_text_budget_and_preserve_source_fields(paper, instruction, group, field):
    data = {group: [
        {'id': paper['id'], 'citation': paper['citation'], field: paper['text']},
        {'id': 'SRC-007', 'citation': 'Xu R, An J, Zhan B. arXiv:2208.00412, 2022.',
         field: "Active automata learning in the framework of Angluin's $L^{*}$ algorithm has been applied "
                'to learning many kinds of automata models.'},
    ]}
    before = copy.deepcopy(data)
    empty = copy.deepcopy(data)
    for item in empty[group]:
        item[field] = ''
    empty['truncated'] = True
    fixed = literature_review.INPUT_PROMPT.format(instruction=instruction, data=json.dumps(empty, ensure_ascii=False))
    limit = estimate_tokens(fixed) + min(estimate_tokens(item[field]) for item in data[group])
    settings = lazyllm.globals['config']
    previous = settings.get('dynamic_model_configs')
    settings['dynamic_model_configs'] = {'llm': {'chat': {'max_input_tokens': limit * 2}}}
    try:
        prompt = literature_review._prompt(instruction, data)
    finally:
        if previous is None:
            settings.pop('dynamic_model_configs', None)
        else:
            settings['dynamic_model_configs'] = previous
    bounded = json.loads(prompt.split('Input JSON:\n', 1)[1])
    assert data == before
    assert estimate_tokens(prompt) <= limit
    assert bounded['truncated'] is True
    for original, item in zip(data[group], bounded[group]):
        assert item['id'] == original['id']
        assert item['citation'] == original['citation']
        assert 0 < len(item[field]) < len(original[field])
        assert original[field].startswith(item[field])
    sizes = [estimate_tokens(item[field]) for item in bounded[group]]
    assert max(sizes) - min(sizes) <= 2


def test_supplied_papers_are_read_synthesized_and_published_for_downstream(tmp_path, monkeypatch, selection, sciverse):
    from lazymind.chat.engine.subagent.context import SubAgentContext

    events = []
    readings, syntheses = {}, []
    request = 'Use the supplied papers and system context, expand with Sciverse; write about 300 words.'
    inputs = {'topic': 'Learning', 'request': request, 'papers': [
        copy.deepcopy(PAPER),
        {'id': 'A2', 'title': 'Abstract paper', 'abstract': 'Abstract-only evidence.'},
        {'id': 'B1', 'title': 'System context', 'text': 'Local experiment constraints.', 'review_role': 'background'},
        {'id': 'upload-brief', 'title': '背景与创新点', 'text': 'Internal project description.',
         'citation': 'Incorrect brief[Z]. 2026.'},
        {'id': 'upload-data', 'title': '实验数据', 'text': 'Internal test report with wiki and PR links.'},
    ]}
    selection({'material_roles': {'M1': 'paper', 'M2': 'paper', 'M3': 'background', 'M4': 'paper', 'M5': 'paper'},
               'queries': {},
               'external_queries': {'related': 3}},
              {'material_roles': {'M1': 'paper'}, 'queries': {}})

    def share(*, stream):
        def generate(prompt):
            data = json.loads(prompt.split('Input JSON:\n', 1)[1])
            if 'source_id' in data:
                readings[data['source_id']] = data
                return json.dumps({'note': 'Read: ' + data['text'],
                                   'citation': 'Model reference for ' + data['title'] + '[Z].',
                                   'review_role': 'background' if data['title'] in ('背景与创新点', '实验数据') else 'paper'})
            syntheses.append(data)
            raw = '# Review\n\n' + '\n\n'.join(p['note'] + ' [' + p['id'] + ']' for p in data['sources'])
            raw += '\n\n## References\n\n' + '\n'.join(
                '[' + p['id'] + '] ' + p['citation'] for p in data['sources'])
            stream['_stream_sink']({'tag': 'think', 'delta': 'Internal reasoning'})
            for delta in raw:
                stream['_stream_sink']({'tag': 'text', 'delta': delta})
            return raw
        return generate

    monkeypatch.setattr(literature_review, 'AutoModel', lambda **_: SimpleNamespace(share=share))
    monkeypatch.setattr(literature_review, '_input_budget', lambda: 8192)
    entry = tmp_path / 'papers.json'
    entry.write_text(json.dumps(inputs))
    slots = ['literature_review_pool', 'literature_review_document']
    lazyllm.globals['subagent_ctx'] = SubAgentContext(
        task_id='literature-stream', conversation_id='test', agent_type='workflow_step',
        objective='Review papers', workspace_path=str(tmp_path), input_slots=['paper_input'],
        output_slots=slots, db=None, emit=events.append,
        params={'step_id': 'literature_review', 'remote_inputs': {'paper_input': str(entry)},
                'output_slot_types': dict.fromkeys(slots, 'file')},
    )
    assert workflow.run_paper_step()['status'] == 'completed'
    published = next(e for e in events if e['type'] == 'artifact' and e['slot'] == slots[1])
    body = Path(published['value']['path']).read_text()
    assert {value['title']: value['text'] for value in readings.values()} == {
        'Test paper': PAPER['text'], 'Abstract paper': 'Abstract-only evidence.',
        'System context': 'Local experiment constraints.', 'Related research': 'Complete Sciverse paper',
        '背景与创新点': 'Internal project description.', '实验数据': 'Internal test report with wiki and PR links.',
    }
    assert all(value['research']['request'] == request for value in readings.values())
    assert len(syntheses) == 1
    synthesis = syntheses[0]
    assert synthesis['research']['request'] == request
    assert [p['note'] for p in synthesis['sources']] == [
        'Read: Test source material.', 'Read: Abstract-only evidence.', 'Read: Complete Sciverse paper']
    assert [p['title'] for p in synthesis['background']] == ['System context', '背景与创新点', '实验数据']
    assert all(p['citation'] == '' for p in synthesis['background'])
    assert 'Test paper\\[Z\\].' in body and 'Model reference for Abstract paper\\[Z\\].' in body
    assert '[\\[1\\]](#paper-ref-P1)' in body and '[\\[2\\]](#paper-ref-A2)' in body and '[M1]' not in body
    assert '[P1]' not in body and '[B1]' not in body and 'upload-' not in body
    assert '<a id="paper-ref-P1"></a>\n\n[1] Test paper\\[Z\\].\n' in body
    assert '<a id="paper-ref-A2"></a>\n\n[2] Model reference for Abstract paper\\[Z\\].\n' in body
    assert body.endswith('Model reference for Related research\\[Z\\].\n\n<a id="paper-references-end"></a>')
    final_stream = next(e['stream_id'] for e in reversed(events) if e['type'] == 'artifact_stream_start')
    assert ''.join(e['delta'] for e in events
                   if e['type'] == 'artifact_stream' and e['stream_id'] == final_stream) == body
    assert '实际仅取得' not in body
    assert next(i for i, e in enumerate(events) if e['type'] == 'artifact_stream_end') < events.index(published)
    assert all(e['slot'] == slots[1] for e in events if e['type'].startswith('artifact_stream'))
    assert 'Internal reasoning' not in json.dumps(events)
    assert current_events() is None
    saved = next(e for e in events if e['type'] == 'artifact' and e['slot'] == slots[0])
    downstream = workflow.business_input('refine_topic', workflow._read_bound(saved['value']['path']))
    assert [p['id'] for p in downstream['papers'][:2]] == ['P1', 'A2']
    assert downstream['papers'][-1]['source'] == 'sciverse'
    assert downstream['papers'][-1]['text'] == 'Complete Sciverse paper'
    assert [p['title'] for p in downstream['papers'] if p.get('review_role') == 'background'] == [
        'System context', '背景与创新点', '实验数据']
    assert downstream['papers'][0]['user_annotations'] == PAPER['user_annotations']
    assert downstream['papers'][0]['citation'] == PAPER['citation']
    assert downstream['papers'][1]['reading']['content_type'] == 'abstract'
    assert downstream['evidence'] == body
    assert json.loads(entry.read_text()) == inputs
