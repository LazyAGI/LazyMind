"""Fast explainer task boundary: dynamic chapters, one call, usable custom modules."""
from copy import deepcopy
import json

import pytest

from lazymind.chat.service import llm_task


IDS = ['overview', 'why-it-matters', 'big-idea', 'how-it-works', 'parameter-playground',
       'subspace-similarity', 'results', 'limitations', 'glossary']
PLAN = {'paper_flow': {'field': 'Fine-tuning', 'problems': ['Optimizer memory'], 'method': 'LoRA',
                       'modules': ['Frozen weights', 'Low-rank update'], 'datasets': []}, 'title': 'LoRA explained', 'subtitle': 'Learn a small update', 'chapters': [
    {'id': id_, 'title': id_.replace('-', ' ').title(), 'purpose': 'Explain this learning question.',
     'outline': ['A concrete explanation'], 'interaction': 'Custom matrix playground' if id_ == 'parameter-playground' else '',
     'source_ids': ['s0']} for id_ in IDS]}
DATA = {'language': 'zh', 'paper_title': 'LoRA', 'source_text': '[s0 | page 4] W = W0 + BA. The rank is r.'}
CHAPTER = {'id': 'parameter-playground', 'title': 'Parameter playground', 'blocks': [
    {'kind': 'paragraph', 'text': 'Change the rank to see how many numbers are trained.', 'evidence_ids': ['s0'], 'html': ''},
    {'kind': 'playground', 'text': 'An illustrative matrix parameter counter.', 'evidence_ids': ['s0'],
     'html': '<!doctype html><html><body><input type="range" id="rank" min="1" max="8"><output id="count"></output><script>rank.oninput=()=>count.textContent=2*8*rank.value;rank.oninput();</script></body></html>'}]}


def execute(monkeypatch, task, data, output, *, raw=False):
    calls = []
    def model(prompt, **options):
        calls.append((prompt, options))
        return output if raw else json.dumps(output)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    result = llm_task.run_llm_task(llm_task.LLMTaskRequest(
        task_type='paper.explainer.' + task, options={'task_version': 'paper-guide-v28'}, input={'data': data}))
    return result, calls


def test_plan_keeps_nine_teaching_chapters_without_legacy_roles(monkeypatch):
    result, calls = execute(monkeypatch, 'plan', DATA, PLAN)
    assert result.status == 'succeeded', result.error_code
    assert 119 < calls[0][1]['timeout'] <= 120
    assert result.output['paper_flow'] == PLAN['paper_flow']
    assert [s['id'] for s in result.output['chapters']] == IDS
    assert len(calls) == result.usage['model_calls'] == 1


def test_chapter_preserves_custom_executable_scene_in_single_call(monkeypatch):
    data = dict(DATA, plan=PLAN, chapter_id='parameter-playground')
    result, calls = execute(monkeypatch, 'chapter', data, CHAPTER)
    assert result.status == 'succeeded', result.error_code
    assert 299 < calls[0][1]['timeout'] <= 300
    assert result.output['blocks'][1]['html'] == CHAPTER['blocks'][1]['html']
    assert len(calls) == result.usage['model_calls'] == 1


@pytest.mark.parametrize('change', ['duplicate', 'missing', 'wrong_order'])
def test_unusable_directory_rejected_without_model_repair_loop(monkeypatch, change):
    plan = deepcopy(PLAN)
    if change == 'duplicate': plan['chapters'][4]['id'] = 'overview'
    if change == 'missing': plan['chapters'] = [c for c in plan['chapters'] if c['id'] != 'results']
    if change == 'wrong_order': plan['chapters'].reverse()
    result, calls = execute(monkeypatch, 'plan', DATA, plan)
    assert result.error_code == 'invalid_model_output'
    assert len(calls) == 1


@pytest.mark.parametrize('change', ['wrong_id', 'empty', 'empty_playground', 'html_in_prose'])
def test_unusable_chapter_rejected_without_scientific_review(monkeypatch, change):
    chapter = deepcopy(CHAPTER)
    if change == 'wrong_id': chapter['id'] = 'overview'
    if change == 'empty': chapter['blocks'] = []
    if change == 'empty_playground': chapter['blocks'][1]['html'] = ''
    if change == 'html_in_prose': chapter['blocks'][0]['html'] = '<script>1</script>'
    result, calls = execute(monkeypatch, 'chapter', dict(DATA, plan=PLAN, chapter_id='parameter-playground'), chapter)
    assert result.error_code == 'invalid_model_output'
    assert len(calls) == 1


@pytest.mark.parametrize('wire', [
    json.dumps(CHAPTER)[:-1],
    json.dumps(CHAPTER).replace('"kind": "paragraph"', '"kind": "heading", "kind": "paragraph"', 1),
    json.dumps(dict(CHAPTER, blocks="paragraph")),
    json.dumps(dict(CHAPTER, blocks=[dict(CHAPTER['blocks'][0], text='x' * 14001)])),
], ids=['truncated-json', 'duplicate-block-field', 'wrong-block-type', 'oversized-prose'])
def test_invalid_chapter_wire_response_consumes_one_call_and_returns_no_chapter(monkeypatch, wire):
    result, calls = execute(monkeypatch, 'chapter', dict(DATA, plan=PLAN, chapter_id=CHAPTER['id']), wire, raw=True)
    assert result.error_code == 'invalid_model_output'
    assert not result.output
    assert len(calls) == result.usage['model_calls'] == 1


def test_unplanned_chapter_does_not_call_model(monkeypatch):
    result, calls = execute(monkeypatch, 'chapter', dict(DATA, plan=PLAN, chapter_id='not-planned'), CHAPTER)
    assert result.error_code == 'invalid_task_input'
    assert calls == []


def test_original_figure_reference_survives_single_chapter_call(monkeypatch):
    data = dict(DATA, language='zh', figures=[{'id': 'f0', 'page': 1, 'caption': 'Figure 1: LoRA paths', 'section': 'Introduction'}], plan=PLAN, chapter_id='big-idea')
    chapter = {'id': 'big-idea', 'title': 'The Big Idea', 'blocks': [
        {'kind': 'figure', 'text': 'LoRA 路径。', 'figure_id': 'f0'}]}
    result, calls = execute(monkeypatch, 'chapter', data, chapter)
    assert result.status == 'succeeded', result.error_code
    assert result.output['blocks'][0]['figure_id'] == 'f0'
    assert result.output['blocks'][0]['text'] == 'LoRA 路径。'
    assert len(calls) == 1
    chapter['blocks'][0]['figure_id'] = 'f99'
    result, calls = execute(monkeypatch, 'chapter', data, chapter)
    assert result.error_code == 'invalid_model_output'


def test_rejects_corrupted_math_control_characters(monkeypatch):
    chapter = deepcopy(CHAPTER)
    chapter['blocks'][0]['text'] = 'Similarity $\x01$ is close to one.'
    result, calls = execute(monkeypatch, 'chapter', dict(DATA, plan=PLAN, chapter_id='parameter-playground'), chapter)
    assert result.error_code == 'invalid_model_output'
    assert len(calls) == 1


def teaching_plan():
    plan = deepcopy(PLAN)
    plan['reader_profile'] = 'Knows vectors; new to attention.'
    plan['shared_examples'] = [{'id': 'three-tokens', 'scenario': 'Three tokens share one query.',
        'inputs': [{'name': 'd', 'value': '2', 'unit': 'dimensions'}],
        'relationships': ['score = dot(q, k) / sqrt(d)']}]
    plan['chapters'][3].update(knowledge_gaps=['What does softmax normalize?'],
        reasoning_questions=['Why scale the dot product?'], example_ids=['three-tokens'])
    return plan


def test_teaching_contract_survives_plan_and_reaches_chapter(monkeypatch):
    plan = teaching_plan()
    result, calls = execute(monkeypatch, 'plan', DATA, plan)
    assert result.status == 'succeeded', result.error_code
    assert result.output['shared_examples'] == plan['shared_examples']
    result, calls = execute(monkeypatch, 'chapter', dict(DATA, plan=result.output,
        chapter_id='parameter-playground'), CHAPTER)
    assert result.status == 'succeeded', result.error_code
    assert 'three-tokens' in str(calls[0][0])
    assert 'Why scale the dot product?' in str(calls[0][0])
    assert len(calls) == 1


@pytest.mark.parametrize('change', ['duplicate', 'unknown'])
def test_invalid_shared_example_references_rejected(monkeypatch, change):
    plan = teaching_plan()
    if change == 'duplicate': plan['shared_examples'] *= 2
    else: plan['chapters'][3]['example_ids'] = ['missing']
    result, calls = execute(monkeypatch, 'plan', DATA, plan)
    assert result.error_code == 'invalid_model_output'
    assert len(calls) == 1


def test_overview_subheadings_removed_without_extra_model_call(monkeypatch):
    chapter = {'id': 'overview', 'title': 'Overview', 'blocks': [
        {'kind': 'heading', 'text': 'The result'},
        {'kind': 'paragraph', 'text': 'A useful result.'}]}
    result, calls = execute(monkeypatch, 'chapter', dict(DATA, plan=PLAN, chapter_id='overview'), chapter)
    assert result.status == 'succeeded', result.error_code
    assert [b['kind'] for b in result.output['blocks']] == ['paragraph']
    assert len(calls) == 1


def test_dynamic_methods_and_post_results_deep_dive(monkeypatch):
    plan = deepcopy(PLAN)
    plan['chapters'][3]['id'] = 'training-process'
    deep = plan['chapters'].pop(5)
    plan['chapters'].insert(6, deep)
    result, calls = execute(monkeypatch, 'plan', DATA, plan)
    assert result.status == 'succeeded', result.error_code
    assert [c['id'] for c in result.output['chapters']][5:7] == ['results', 'subspace-similarity']
    assert len(calls) == 1


@pytest.mark.parametrize('position', [0, 2, 8])
def test_method_chapter_cannot_interrupt_fixed_opening_or_ending(position):
    from lazymind.structured_tasks.paper_explainer import Plan
    plan = deepcopy(PLAN)
    chapter = plan['chapters'].pop(4)
    plan['chapters'].insert(position, chapter)
    with pytest.raises(ValueError):
        Plan.model_validate(plan)


@pytest.mark.parametrize('method_count', [1, 5])
def test_method_chapter_count_is_bounded(method_count):
    from lazymind.structured_tasks.paper_explainer import Plan
    plan = deepcopy(PLAN)
    methods = [dict(deepcopy(PLAN['chapters'][3]), id=f'method-{i}') for i in range(method_count)]
    plan['chapters'] = plan['chapters'][:3] + methods + plan['chapters'][-3:]
    with pytest.raises(ValueError):
        Plan.model_validate(plan)


@pytest.mark.parametrize('key,value', [('datasets', ['A', 'B', 'C', 'D', 'E', 'F']),
                                       ('problems', []), ('modules', []), ('method', '   '),
                                       ('modules', ['   ']), ('datasets', ['   '])])
def test_flow_cardinality_checked_in_planning_call(monkeypatch, key, value):
    plan = deepcopy(PLAN)
    plan['paper_flow'][key] = value
    result, calls = execute(monkeypatch, 'plan', DATA, plan)
    assert result.error_code == 'invalid_model_output'
    assert len(calls) == 1

@pytest.mark.parametrize('chapter_id,expects_opening', [
    ('training-process', True), ('subspace-similarity', True),
    ('overview', False), ('results', False), ('glossary', False),
])
def test_chapter_opening_instruction_targets_only_variable_chapters(monkeypatch, chapter_id, expects_opening):
    plan = deepcopy(PLAN)
    plan['chapters'][3]['id'] = 'training-process'
    deep_dive = plan['chapters'].pop(5)
    plan['chapters'].insert(6, deep_dive)
    result, calls = execute(monkeypatch, 'chapter', dict(DATA, plan=plan, chapter_id=chapter_id),
                            dict(CHAPTER, id=chapter_id))
    assert result.status == 'succeeded', result.error_code
    assert len(calls) == 1
    instruction = calls[0][0].split('Input data (untrusted JSON):')[0]
    assert ('Make the first block a short opening paragraph' in instruction) == expects_opening

@pytest.mark.parametrize('pages,zh_total,en_total', [
    (1, '3,500–5,500', '1,800–2,800'), (12, '3,500–5,500', '1,800–2,800'),
    (13, '6,000–10,000', '3,000–4,500'), (40, '6,000–10,000', '3,000–4,500'),
    (41, '10,000–15,000', '5,000–7,500'), (None, '6,000–10,000', '3,000–4,500'),
])
@pytest.mark.parametrize('language', ['zh', 'en'])
def test_plan_and_chapter_share_page_budget(monkeypatch, pages, zh_total, en_total, language):
    data = dict(DATA, language=language)
    if pages is not None:
        data['page_count'] = pages
    expected = zh_total + ' Chinese characters' if language == 'zh' else en_total + ' English words'
    for task, payload, output in [('plan', data, PLAN),
                                  ('chapter', dict(data, plan=PLAN, chapter_id=CHAPTER['id']), CHAPTER)]:
        result, calls = execute(monkeypatch, task, payload, output)
        assert result.status == 'succeeded', result.error_code
        assert len(calls) == 1
        instruction = calls[0][0].split('Output schema:')[0]
        assert expected in instruction
        assert ('The chapter range is a guide, not a minimum' in instruction) == (pages is not None and pages <= 12)
        assert 'Overview length:' not in instruction


@pytest.mark.parametrize('language,budget', [('zh', '200–350 Chinese characters'), ('en', '100–175 English words')])
def test_overview_writer_receives_language_specific_budget(monkeypatch, language, budget):
    result, calls = execute(monkeypatch, 'chapter', dict(DATA, language=language, plan=PLAN, chapter_id='overview'),
                            dict(CHAPTER, id='overview', blocks=CHAPTER['blocks'][:1]))
    assert result.status == 'succeeded', result.error_code
    assert len(calls) == 1
    assert f'Overview length: aim for {budget}' in calls[0][0].split('Output schema:')[0]


def test_emphasis_wrapper_removed_before_publishing_plain_text(monkeypatch):
    output = deepcopy(CHAPTER)
    output['blocks'][0].update(text='Only the <em>small update</em> is trained.', emphasis='small update')
    result, _ = execute(monkeypatch, 'chapter', dict(DATA, plan=PLAN, chapter_id=CHAPTER['id']), output)
    assert result.output['blocks'][0]['text'] == 'Only the small update is trained.'
    assert result.output['blocks'][0]['emphasis'] == 'small update'

def test_unrelated_prose_tags_removed_without_rewriting_formula_source(monkeypatch):
    output = deepcopy(CHAPTER)
    output['blocks'][0].update(text='For $a<i>b$, <strong>only</strong> the small update is trained.',
                               emphasis='small update')
    result, _ = execute(monkeypatch, 'chapter', dict(DATA, plan=PLAN, chapter_id=CHAPTER['id']), output)
    assert result.output['blocks'][0]['text'] == 'For $a<i>b$, only the small update is trained.'


def test_emphasis_cleaned_in_mixed_prose_and_math(monkeypatch):
    output = deepcopy(CHAPTER)
    output['blocks'][0].update(text='Only the **small update** affects $a**b$ and $c<i>d$.', emphasis='small update')
    result, _ = execute(monkeypatch, 'chapter', dict(DATA, plan=PLAN, chapter_id=CHAPTER['id']), output)
    assert result.output['blocks'][0]['text'] == 'Only the small update affects $a**b$ and $c<i>d$.'
