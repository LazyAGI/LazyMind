"""Fast explainer task boundary: dynamic chapters, one call, usable custom modules."""
from copy import deepcopy
import json

import pytest

from lazymind.chat.service import llm_task


IDS = ['overview', 'why-it-matters', 'big-idea', 'how-it-works', 'parameter-playground',
       'subspace-similarity', 'results', 'limitations', 'glossary']
PLAN = {'title': 'LoRA explained', 'subtitle': 'Learn a small update', 'chapters': [
    {'id': id_, 'title': id_.replace('-', ' ').title(), 'purpose': 'Explain this learning question.',
     'outline': ['A concrete explanation'], 'interaction': 'Custom matrix playground' if id_ == 'parameter-playground' else '',
     'source_ids': ['s0']} for id_ in IDS]}
DATA = {'language': 'zh', 'paper_title': 'LoRA', 'source_text': '[s0 | page 4] W = W0 + BA. The rank is r.'}
CHAPTER = {'id': 'parameter-playground', 'title': 'Parameter playground', 'blocks': [
    {'kind': 'paragraph', 'text': 'Change the rank to see how many numbers are trained.', 'evidence_ids': ['s0'], 'html': ''},
    {'kind': 'playground', 'text': 'An illustrative matrix parameter counter.', 'evidence_ids': ['s0'],
     'html': '<!doctype html><html><body><input type="range" id="rank" min="1" max="8"><output id="count"></output><script>rank.oninput=()=>count.textContent=2*8*rank.value;rank.oninput();</script></body></html>'}]}


def execute(monkeypatch, task, data, output):
    calls = []
    def model(prompt, **options):
        calls.append((prompt, options))
        return json.dumps(output)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    result = llm_task.run_llm_task(llm_task.LLMTaskRequest(
        task_type='paper.explainer.' + task, options={'task_version': 'paper-guide-v27'}, input={'data': data}))
    return result, calls


def test_plan_keeps_nine_teaching_chapters_without_legacy_roles(monkeypatch):
    result, calls = execute(monkeypatch, 'plan', DATA, PLAN)
    assert result.status == 'succeeded', result.error_code
    assert [s['id'] for s in result.output['chapters']] == IDS
    assert len(calls) == result.usage['model_calls'] == 1


def test_chapter_preserves_custom_executable_scene_in_single_call(monkeypatch):
    data = dict(DATA, plan=PLAN, chapter_id='parameter-playground')
    result, calls = execute(monkeypatch, 'chapter', data, CHAPTER)
    assert result.status == 'succeeded', result.error_code
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
