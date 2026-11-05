"""Source-bound facts on top of the actual deterministic public Writer plan."""
import copy
import json
from unittest.mock import Mock

import pytest

from lazyllm.tools.writer.data_models.context import WritingContext
from lazyllm.tools.writer.data_models.task import WritingTask
from lazyllm.tools.writer.tools.planning_tools import WriterPlanningTools
from lazymind.document_tools.paper import contracts, shared_facts
from lazymind.document_tools.paper.shared_facts import bind_shared_facts, source_paragraphs
from lazymind.document_tools.paper.writing import write_document
from lazymind.document_tools.toolkits import WriterCreateToolkit


OUTLINE = '# Comparison\n\n## Methods\n\n## Results\n\n## Discussion'
FINDING = dict(id='F-difference', statement='Method B is higher by 3.3 percentage points.',
               calculation='100 * (4 / 21 - 3 / 19)', value=3.258145363408521,
               unit='percentage points', columns=[dict(table_id='runs', column='success')],
               sample_size=dict(n=40, unit='independent run', excluded_n=8,
                                source_rows=[dict(table_id='runs', count=48)]))
QUESTION = 'Can the mechanism improve success?\n\nThe contribution is a prototype only.'
MATERIALS = ('The inputs are validated before execution.\n\nEach condition is run once.\n\n'
             'Results are simulated.\n\nSuccess means a completed task.')
PAPER = 'Prior work uses a separate controller.\n\nUNRELATED_FULL_TEXT_MARKER'


def material(key, value):
    return dict(resource_id='write_document:' + key, inline_text=json.dumps(value),
                meta={'material_kind': key.split(':')[0]})


def prepared():
    return dict(
        writing_task={'query': 'Write the comparison.', 'constraints': {}}, outline=OUTLINE,
        charts=[], chart_directory=[], reference_candidates=[{'id': 'P1', 'citation': 'Source [Z].'}],
        resources=[material('question', QUESTION), material('writing_materials', MATERIALS),
                   material('paper:P1', {'text': PAPER}),
                   material('analysis', {'findings': [copy.deepcopy(FINDING)],
                                         'methods': ['Descriptive comparison of independent runs'],
                                         'limitations': ['No population-level causal inference.']})],
        writing_context={'context_id': 'shared-facts-test', 'facts': [
            {'fact_id': 'old', 'key': 'write_document:paper:P1', 'value': PAPER}]})


def real_plan():
    task = WritingTask(task_id='real-plan', query='Prepare facts.', task_type='write')
    context = WritingContext(context_id='real-plan')
    targets = [dict(content_ref={'heading_path': ['Comparison', title], 'occurrence': 1},
                    section_title=title, outline_body='Explain the supplied evidence.')
               for title in ['Methods', 'Results', 'Discussion']]
    return WriterPlanningTools._build_deterministic_section_instructions(targets, context, task).model_dump()


def source_category(text):
    if text.startswith('Can '):
        return 'research_question'
    if 'contribution' in text or 'not proven' in text:
        return 'contribution'
    if 'Old report' in text or 'New report' in text:
        return 'conflict'
    if 'simulated' in text:
        return 'status'
    if 'run once' in text:
        return 'limitation'
    if 'Success means' in text:
        return 'terminology'
    if 'inputs are validated' in text:
        return 'method'
    if 'Prior work' in text:
        return 'literature'
    return 'background'


def selected(spans, instructions, analysis_ids=()):
    sources = [dict(source_id=span['id'], categories=[source_category(span['content'])]) for span in spans]
    by_category = {}
    for item in sources:
        by_category.setdefault(item['categories'][0], []).append(item['source_id'])
    sections = []
    for item in instructions:
        title = item['section_title']
        ids = (by_category.get('method', []) if title == 'Methods' else
               by_category.get('literature', []) if title == 'Discussion' else [])
        ids += [key for key in analysis_ids if (title == 'Results') == key.startswith('analysis:finding:')]
        sections.append(dict(instruction_id=item['instruction_id'], argument_role='Explain evidence for ' + title,
                             fact_ids=ids))
    return dict(sources=sources, sections=sections)


def selection(data, instructions):
    return selected(source_paragraphs(data['resources']), instructions['instructions'],
                    shared_facts._analysis_facts(data['resources']))


def fact_model(monkeypatch, responses=None):
    calls = []

    def respond(prompt):
        payload = json.loads(prompt.rsplit('\n', 1)[1])
        calls.append(payload)
        if responses is not None:
            return json.dumps(responses.pop(0))
        if 'manuscript' in payload:
            return json.dumps(dict(issues=[], pending=[]))
        return json.dumps(selected(payload['source_paragraphs'], payload['sections'],
                                   [fact['fact_id'] for fact in payload['analysis_facts']]))
    monkeypatch.setattr(shared_facts, 'AutoModel', lambda **kwargs: respond)
    return calls


def test_actual_deterministic_writer_plan_is_enriched_without_invented_meta(monkeypatch):
    data, instructions = prepared(), real_plan()
    assert instructions['meta']['source'] == 'outline' and 'paper_facts' not in instructions['meta']
    assert all('argument_role' not in item['meta'] for item in instructions['instructions'])
    calls = fact_model(monkeypatch)
    bind_shared_facts(instructions, data)
    assert len(calls) == 1
    assert all(item['meta']['argument_role'] for item in instructions['instructions'])
    assert len(data['writing_context']['meta']['paper_source_coverage']) == len(source_paragraphs(data['resources']))


def test_exact_numbers_and_provenance_without_broadcasting_source_corpus():
    data, instructions = prepared(), real_plan()
    original = copy.deepcopy(data['resources'])
    bind_shared_facts(instructions, data, selection=selection(data, instructions))
    facts = {fact['fact_id']: fact for fact in data['writing_context']['facts']}
    numeric = json.loads(facts['analysis:finding:F-difference']['value'])
    assert numeric['content'] == FINDING
    assert numeric['source_id'] == 'write_document:analysis'
    assert 'Results are simulated.' in json.dumps(data['writing_context'])
    assert 'The contribution is a prototype only.' in json.dumps(data['writing_context'])
    assert 'UNRELATED_FULL_TEXT_MARKER' not in json.dumps(data['writing_context'])
    method, results, discussion = instructions['instructions']
    assert 'The inputs are validated' in json.dumps(method['fact_constraints'])
    assert 'The inputs are validated' not in json.dumps(results['fact_constraints'])
    assert 'Prior work uses a separate controller.' in json.dumps(discussion['fact_constraints'])
    assert any(fact['key'] == 'reference_candidates' for fact in facts.values())
    assert data['resources'] == original


@pytest.mark.parametrize('defect', ['empty', 'missing', 'source', 'duplicate', 'fact_id', 'role', 'section_id'])
def test_bad_or_incomplete_source_binding_is_rejected_before_drafting(defect):
    data, instructions = prepared(), real_plan()
    value = selection(data, instructions)
    if defect == 'empty':
        value['sources'] = []
    elif defect == 'missing':
        value['sources'].pop()
    elif defect == 'source':
        value['sources'][0]['source_id'] = 'invented-source'
    elif defect == 'duplicate':
        value['sources'][1]['source_id'] = value['sources'][0]['source_id']
    elif defect == 'fact_id':
        value['sections'][0]['fact_ids'] = ['unknown-fact']
    elif defect == 'role':
        value['sections'][0]['argument_role'] = ' '
    else:
        value['sections'][0]['instruction_id'] = 'invented-section'
    before = copy.deepcopy(data)
    with pytest.raises(ValueError):
        bind_shared_facts(instructions, data, selection=value)
    assert data == before


def test_complete_paragraph_keeps_negation_and_status_even_when_classified_as_contribution():
    data, instructions = prepared(), real_plan()
    text = 'The mechanism is not proven to improve quality. Results are simulated.'
    data['resources'] = [material('writing_materials', text)]
    bind_shared_facts(instructions, data, selection=selection(data, instructions))
    facts = data['writing_context']['facts']
    assert len(facts) == 2  # The original paragraph plus the reference candidate catalog.
    value = json.loads(facts[0]['value'])
    assert value['content'] == text
    assert value['content'] != 'proven to improve quality.'
    assert text[value['start']:value['end']] == text
    assert 'status' in data['writing_context']['meta']['paper_fact_gaps']


def test_conflicting_accounts_and_explicit_status_are_retained():
    data, instructions = prepared(), real_plan()
    data['resources'] = [material(
        'writing_materials', 'Old report: sample size is 24.\n\n'
        'New report: sample size is 19.\n\nResults are simulated.')]
    bind_shared_facts(instructions, data, selection=selection(data, instructions))
    facts = data['writing_context']['facts']
    assert len([fact for fact in facts if fact['key'] == 'conflict']) == 2
    assert any('simulated' in fact['value'] for fact in facts if fact['key'] == 'status')
    assert 'status' not in data['writing_context']['meta']['paper_fact_gaps']


def test_invalid_empty_model_result_is_repaired_without_losing_sources(monkeypatch):
    data, instructions = prepared(), real_plan()
    correct = selection(data, instructions)
    empty = copy.deepcopy(correct)
    empty['sources'] = []
    calls = fact_model(monkeypatch, [empty, correct])
    bind_shared_facts(instructions, data)
    assert len(calls) == 2 and 'every supplied source paragraph' in calls[1]['correction']
    assert len(data['writing_context']['meta']['paper_source_coverage']) == len(correct['sources'])


def test_failed_fact_preparation_does_not_publish_a_truncated_context(monkeypatch):
    data, instructions = prepared(), real_plan()
    value = selection(data, instructions)
    value['sources'] = []
    fact_model(monkeypatch, [value, value])
    before = copy.deepcopy(data)
    with pytest.raises(ValueError, match='Shared paper fact preparation failed'):
        bind_shared_facts(instructions, data)
    assert data == before


def test_real_public_writer_and_draft_entry_reach_streaming(monkeypatch):
    inputs = contracts.validate_input('write_document', dict(
        topic='Comparison', outline=OUTLINE, question=QUESTION, writing_materials=MATERIALS,
        papers=[dict(id='P1', title='Source', text=PAPER, citation='Source [Z].')]))
    calls = fact_model(monkeypatch)
    toolkit = WriterCreateToolkit()
    original = toolkit.generate_section_instructions
    plans = []

    def real_planning(**kwargs):
        result = original(**kwargs)
        plans.append(json.loads(result)['section_instructions'])
        return result
    monkeypatch.setattr(toolkit, 'generate_section_instructions', real_planning)
    streamed = []

    def stream(**kwargs):
        streamed.append(kwargs)
        return json.dumps([OUTLINE])
    monkeypatch.setattr(toolkit, 'stream_draft_blocks_markdown', stream)
    monkeypatch.setattr(toolkit, 'generate_draft_document_markdown', lambda **kwargs: json.dumps(
        {'draft_document': OUTLINE}))
    # The public Writer may construct an LLM handle; deterministic planning must never call it.
    from lazymind.document_tools import writing
    forbidden = Mock(side_effect=AssertionError('Deterministic Writer planning called a model'))
    monkeypatch.setattr(writing, 'AutoModel', lambda **kwargs: forbidden)
    assert write_document._write_draft(inputs, toolkit=toolkit)['manuscript'] == OUTLINE
    assert 'paper_facts' not in plans[0]['meta']
    assert len(calls) == 2 and len(streamed) == 1
    assert 'manuscript' in calls[-1] and calls[-1]['source_facts']
    context = json.loads(streamed[0]['writing_context_json'])
    assert 'UNRELATED_FULL_TEXT_MARKER' not in json.dumps(context)
    assert 'Results are simulated.' in json.dumps(context)
    forbidden.assert_not_called()


def test_citation_reading_coverage_survives_context_compression():
    data, instructions = prepared(), real_plan()
    data['resources'][2] = material('paper:P1', dict(
        id='P1', title='Source', text=PAPER, content_type='excerpt', truncated=True,
        reading=[{'content_type': 'abstract', 'fallback': True}]))
    bind_shared_facts(instructions, data, selection=selection(data, instructions))
    fact = next(item for item in data['writing_context']['facts'] if item['key'] == 'paper_reading_coverage')
    content = json.loads(fact['value'])['content']
    assert content['content_type'] == 'excerpt' and content['truncated'] is True
    assert content['reading'] == [{'content_type': 'abstract', 'fallback': True}]


@pytest.fixture
def relationship(monkeypatch):
    source = 'Batch RUN-12 used Model-Z2: lengths 512/639 changed to 481/527 characters. Quality was not controlled.'
    wrong = 'Batch RUN-18 used Model-Z2: lengths 512/639 changed to 481/527 characters.'
    corrected = wrong.replace('RUN-18', 'RUN-12')
    data = {'shared_facts': [shared_facts._fact('source:1', 'status', source, 'writing_materials')]}
    issue = dict(fact_id='source:1', source_quote=source, quote=wrong, replacement=corrected,
                 reason='The lengths belong to another batch.')
    body = '# Paper\n\n## Results\n' + wrong + '\n\n## Limits\nQuality remains unknown.'
    calls = []

    def install(responses):
        def model(prompt):
            calls.append(json.loads(prompt.rsplit('\n', 1)[1]))
            response = responses.pop(0)
            # Public model proposals name immutable facts; only the host copies source text.
            for issue in response['issues']:
                issue.pop('source_quote', None)
            return json.dumps(response)
        monkeypatch.setattr(shared_facts, 'AutoModel', lambda **kwargs: model)
    return data, issue, body, calls, install


def test_source_relation_mismatch_is_locally_corrected_then_rechecked(relationship):
    data, issue, body, calls, install = relationship
    install([dict(issues=[issue], pending=[]), dict(issues=[], pending=['Independent quality labels are missing.'])])
    result = shared_facts.check_fact_relations(body, data)
    assert result == body.replace(issue['quote'], issue['replacement'])
    assert len(calls) == 2
    assert calls[1]['previous_corrections'][0]['source_quote'] == json.loads(data['shared_facts'][0]['value'])['content']
    assert calls[1]['manuscript'] == result
    assert data['shared_facts'][0]['fact_id'] == 'source:1'


@pytest.mark.parametrize('defect', ['fact_id', 'quote', 'invented_number', 'whole_paper'])
def test_unverifiable_proposal_is_not_applied_or_claimed_verified(relationship, defect, monkeypatch):
    data, issue, body, calls, install = relationship
    if defect == 'fact_id':
        issue['fact_id'] = 'invented'
    elif defect == 'quote':
        issue['quote'] = 'Not present in the manuscript'
    elif defect == 'invented_number':
        issue['replacement'] += ' There were 999 trials.'
    else:
        issue['quote'] = body
        issue['replacement'] = body.replace('RUN-18', 'RUN-12')
    from lazymind.document_tools.paper.analysis import trace
    saved = {}
    monkeypatch.setattr(trace, 'save', lambda name, value: saved.update({name: value}))
    install([dict(issues=[issue], pending=[])])
    assert shared_facts.check_fact_relations(body, data) == body
    report = saved['shared-fact-relations-final.json']
    assert report['pending'] and not report['corrections'] and report['independently_verified'] is False
    assert len(calls) == 1


def test_remaining_relation_conflict_does_not_trigger_unbounded_rewriting(relationship):
    data, issue, body, calls, install = relationship
    remaining = dict(issue, quote=issue['replacement'],
                     replacement=issue['replacement'] + ' Quality was not controlled.')
    install([dict(issues=[issue], pending=[]), dict(issues=[remaining], pending=[])])
    with pytest.raises(ValueError, match='一次局部更正'):
        shared_facts.check_fact_relations(body, data)
    assert len(calls) == 2


def test_coverage_limitations_do_not_block_the_draft_or_claim_independent_verification(relationship, monkeypatch):
    from lazymind.document_tools.paper.analysis import trace
    data, _, body, calls, install = relationship
    saved = {}
    monkeypatch.setattr(trace, 'save', lambda name, value: saved.update({name: value}))
    install([dict(issues=[], pending=['The source records do not supply quality labels.'])])
    assert shared_facts.check_fact_relations(body, data) == body
    report = saved['shared-fact-relations-final.json']
    assert report['independently_verified'] is False and report['pending']
    assert report['kind'] == 'source_grounded_model_review' and len(calls) == 1


def test_unavailable_analysis_target_is_preserved_without_numeric_finding():
    data, instructions = prepared(), real_plan()
    target = dict(id='missing', question='What is the quality improvement?', status='unavailable',
                  reason='No independent quality labels.', columns=[])
    # One analysis resource represents the actual F4 output, rather than merging incompatible analyses.
    data['resources'] = [item for item in data['resources'] if item['meta']['material_kind'] != 'analysis'] + [
        material('analysis', {'targets': [target], 'findings': [],
                              'limitations': ['Quality cannot be estimated.']})]
    bind_shared_facts(instructions, data, selection=selection(data, instructions))
    facts = {item['fact_id']: json.loads(item['value'])['content'] for item in data['shared_facts']}
    assert facts['analysis:targets'] == [target]
    assert not any(key.startswith('analysis:finding:') for key in facts)


def test_host_binds_complete_source_negation_and_status(relationship, monkeypatch):
    from lazymind.document_tools.paper.analysis import trace
    data, issue, _, calls, install = relationship
    source = 'The study is not proven to improve quality. Results are simulated.'
    data['shared_facts'] = [shared_facts._fact('source:1', 'status', source, 'writing_materials')]
    wrong = 'The study is proven to improve quality.'
    issue.update(quote=wrong, replacement=source)
    body = '# Paper\n\n## Results\n' + wrong
    saved = {}
    monkeypatch.setattr(trace, 'save', lambda name, value: saved.update({name: value}))
    install([dict(issues=[issue], pending=[]), dict(issues=[], pending=[])])
    assert shared_facts.check_fact_relations(body, data) == body.replace(wrong, source)
    assert saved['shared-fact-relations-initial.json']['initial']['issues'][0]['source_quote'] == source
    assert 'source_quote' not in calls[0]['schema']['$defs']['RelationProposal']['properties']


def test_bound_source_cannot_be_replaced_with_a_negation_stripped_excerpt(relationship):
    data, issue, body, _, _ = relationship
    source = 'The study is not proven to improve quality. Results are simulated.'
    data['shared_facts'] = [shared_facts._fact('source:1', 'status', source, 'writing_materials')]
    issue['source_quote'] = 'proven to improve quality.'
    review = shared_facts.RelationReview(issues=[issue])
    with pytest.raises(ValueError, match='来源摘录'):
        shared_facts._relation_edits(body, review, shared_facts._relation_sources(data))


def test_relation_cannot_choose_one_conflicting_account_as_truth(relationship):
    data, issue, body, calls, install = relationship
    source = 'Old report: 24 samples. New report: 19 samples. The conflict is unresolved.'
    data['shared_facts'] = [shared_facts._fact('source:1', 'conflict', source, 'writing_materials')]
    issue.update(source_quote=source, replacement='The study used 24 samples.')
    # A selective correction cannot be applied; source conflict remains explicit and unresolved.
    grounded, rejected = shared_facts._ground_relation_review(
        body, shared_facts.RelationReview(issues=[dict(issue, source_quote=source)]),
        shared_facts._relation_sources(data))
    assert not grounded.issues and '冲突来源' in rejected[0]['reason'] and grounded.pending
    assert calls == []


def test_runtime_fact_relation_audit_is_durable_without_nested_capture(relationship, tmp_path, monkeypatch):
    import lazyllm
    from lazymind.chat.engine.subagent.context import SubAgentContext, set_context
    from lazymind.document_tools.paper.analysis import trace
    data, _, body, calls, install = relationship
    install([dict(issues=[], pending=[]), dict(issues=[], pending=[])])
    monkeypatch.setattr(write_document, '_write_draft', lambda inputs: {
        'manuscript': shared_facts.check_fact_relations(body, data), 'is_demo': False})
    previous = lazyllm.globals.get('subagent_ctx')
    set_context(SubAgentContext(
        task_id='f5-audit', conversation_id='f5-test', agent_type='workflow_step',
        objective='Offline audit', params={}, workspace_path=str(tmp_path), input_slots=[], output_slots=[],
        db=None, emit=lambda event: None))
    try:
        assert write_document.run({})['manuscript'] == body
        initial = list(tmp_path.rglob('shared-fact-relations-initial.json'))
        final = list(tmp_path.rglob('shared-fact-relations-final.json'))
        assert len(initial) == len(final) == 1
        with trace.capture(str(tmp_path), 'outer-audit') as recorder:
            root = recorder.root
            assert write_document.run({})['manuscript'] == body
            assert trace._current.get().root == root
        assert len(list(tmp_path.rglob('shared-fact-relations-final.json'))) == 2
    finally:
        if previous is None:
            lazyllm.globals.pop('subagent_ctx', None)
        else:
            lazyllm.globals['subagent_ctx'] = previous


def test_pending_and_rejected_quotes_use_existing_visible_text_channel(relationship, monkeypatch):
    from lazymind.document_tools.paper.runtime_events import bind_runtime_events
    data, issue, body, calls, install = relationship
    issue['quote'] = 'This sentence exists only in the source, not in the paper.'
    install([dict(issues=[issue], pending=['Independent labels are missing.'])])
    events = []
    with bind_runtime_events(events.append, 'write_document'):
        assert shared_facts.check_fact_relations(body, data) == body
    visible = '\n'.join(event['text'] for event in events if event.get('type') == 'text')
    assert '尚未核实' in visible and 'Independent labels are missing.' in visible
    assert '未应用核对建议' in visible and len(calls) == 1


def test_invalid_quote_does_not_block_other_grounded_correction(relationship):
    data, issue, body, calls, install = relationship
    invalid = dict(issue, quote='The source-only sentence is absent from the manuscript.')
    install([dict(issues=[invalid, issue], pending=[]), dict(issues=[], pending=[])])
    result = shared_facts.check_fact_relations(body, data)
    assert result == body.replace(issue['quote'], issue['replacement']) and len(calls) == 2
