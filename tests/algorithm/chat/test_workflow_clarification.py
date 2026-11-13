import json
import time
from pathlib import Path

import pytest
import yaml
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.ask_user import question_card_payload
from lazymind.chat.workflow.clarification import resolve_fields, startup_card, startup_metadata


ROOT = Path(__file__).resolve().parents[3] / 'workflows'


def policy(name):
    return yaml.safe_load((ROOT / name / 'workflow.yaml').read_text(encoding='utf-8'))['runtime']


def test_startup_card_stops_unmodified_agent_without_displaying_receipt(monkeypatch, tmp_path):
    import uuid
    from unittest.mock import MagicMock

    import lazyllm
    from lazyllm.tools import ReactAgent
    from lazymind.chat.service.component import AgentEventFrameTranslator
    from lazymind.chat.workflow import workflow_manager as manager

    runtime = policy('ppt-workflow')
    item = {'workflow_id': 'ppt-workflow', 'workflow_ref': 'builtin:ppt-workflow',
            'revision_id': 'r1', 'runtime': runtime, 'tool_name': 'trigger_ppt_workflow'}
    client = MagicMock()
    client.get_workflow.return_value.result = item
    monkeypatch.setattr(manager, '_client', lambda: client)
    toolkit = MagicMock()
    monkeypatch.setattr(manager, 'HostWorkflowToolkit', lambda *a, **kw: toolkit)
    events = []
    monkeypatch.setattr('lazyllm.tools.agent.base._write_agent_data',
                        lambda tag, **data: events.append({'tag': tag, **data}))
    contribution = manager.resolve_workflow_injection(
        None, current_query='做一个PPT', workflow_catalog=[item],
        allowed_workflow_refs=['builtin:ppt-workflow'], workflow_activations=[item],
    )
    pending, triggers = list(contribution.tools), []
    while pending:
        tool = pending.pop()
        if isinstance(tool, dict):
            pending.extend(tool.get('tools') or [])
        elif tool.__name__ == 'trigger_ppt_workflow':
            triggers.append(tool)
    assert len(triggers) == 1

    class Model:
        def __init__(self):
            self._module_id = uuid.uuid4().hex
            self.calls = 0

        def share(self, **kwargs):
            return self

        def used_by(self, module_id):
            return self

        def __call__(self, *args, **kwargs):
            self.calls += 1
            assert self.calls == 1, 'A startup question must wait for the user'
            return {'content': '', 'tool_calls': [{
                'id': 'startup', 'type': 'function',
                'function': {'name': 'trigger_ppt_workflow', 'arguments': '{}'},
            }]}

    with lazyllm.locals._scope():
        model = Model()
        agent = ReactAgent(model, triggers, workspace=str(tmp_path), force_summarize=True,
                           enable_builtin_tools=False, sandbox=None)
        receipt = agent('做一个PPT')
        assert isinstance(receipt, str) and receipt.strip()
        assert model.calls == 1
        assert 'workspace' not in lazyllm.locals['_lazyllm_agent']
    toolkit.prepare_workflow.assert_not_called()
    cards = [event for event in events if event['tag'] == 'ask_pending']
    assert len(cards) == 1 and cards[0]['questions']
    translator = AgentEventFrameTranslator(query='做一个PPT')
    assert translator.feed(cards[0])[0]['ask_pending']['questions']
    assert translator.finish(receipt) == []


def test_multiple_one_and_other_are_preserved():
    card = question_card_payload([{'id': 'style', 'text': 'Style?', 'type': 'multiple',
                                  'reason': 'Changes appearance.', 'choices': ['A', 'B']}])
    assert card['questions'][0]['id'] == 'style'
    assert card['questions'][0]['choices'] == ['A', 'B', '__ask_user_other__']


def test_suggestion_limit_does_not_truncate_fixed_choices():
    question = {'text': 'Which?', 'reason': 'Changes output.', 'type': 'single', 'choices': list('ABCDEFG')}
    with pytest.raises(ToolExecutionError):
        question_card_payload([question])
    card = question_card_payload([{**question, 'choice_policy': 'fixed'}])
    assert card['questions'][0]['choices'] == list('ABCDEFG')
    assert card['questions'][0]['allow_other'] is False


def count_extraction(value, evidence):
    return {'slide_count': [{'value': value, 'evidence': evidence}]}


@pytest.mark.parametrize('brief,expected,evidence', [
    ('制作份3页PPT，第1页是封面，第2页讲三种回血方式，第3页是行动清单', '3', '制作份3页PPT'),
    ('制作三页PPT，第一页是封面，第二页讲三种回血方式，第三页是行动清单', '3', '制作三页PPT'),
    ('制作12页PPT，第 1 页是封面，第 12 页是总结', '12', '制作12页PPT'),
    ('第3页是行动清单；总共3页', '3', '总共3页'),
    ('Create a 3-page PPT. Page 1 is the cover, page 2 is content, page 3 is the summary.', '3', 'a 3-page PPT'),
    ('Create a presentation with 3 slides. Slide 1 is the cover.', '3', 'a presentation with 3 slides'),
    ('Create a three-slide presentation. Use one slide for the cover.', '3', 'a three-slide presentation'),
    ('Create a PPT with 3 pages.', '3', 'a PPT with 3 pages'),
    ('Fais une présentation de trois diapositives.', '3', 'une présentation de trois diapositives'),
    ('Keep all template pages.', 'preserve_all', 'Keep all template pages'),
])
def test_ppt_accepts_grounded_semantic_totals_without_rescanning_ordinals(brief, expected, evidence):
    values, missing = resolve_fields(policy('ppt-workflow'), brief, extractions=count_extraction(expected, evidence))
    assert values['slide_count'] == expected
    assert 'slide_count' not in [field['id'] for field in missing]


@pytest.mark.parametrize('brief', [
    '第1页是封面，第2页介绍背景，第3页讲方案',
    '第 3 页讲方案',
    '第 13 页讲方案',
    '第十三页讲方案',
    '做3页或5页PPT，第1页是封面',
    'Page 1 is the cover, slide 2 is content.',
    'Use 3 slides for the introduction; decide the deck length yourself.',
])
def test_ppt_missing_or_conflicting_total_still_requires_confirmation(brief):
    values, missing = resolve_fields(policy('ppt-workflow'), brief, {'slide_count': '3'})
    assert 'slide_count' not in values
    assert 'slide_count' in [field['id'] for field in missing]


@pytest.mark.parametrize('candidates', [
    [{'value': '0', 'evidence': '3页'}],
    [{'value': '-3', 'evidence': '3页'}],
    [{'value': '3.5', 'evidence': '3页'}],
    [{'value': 'three', 'evidence': '3页'}],
    [{'value': True, 'evidence': '3页'}],
    [{'value': '3', 'evidence': '不存在的原文'}],
    [{'value': '3', 'evidence': ''}],
    [{'value': '3'}],
    [{'value': '3', 'evidence': '3页'}, {'value': '5', 'evidence': '5页'}],
    [{'value': '3', 'evidence': '3页'}, {'value': '5', 'evidence': '伪造'}],
])
def test_grounded_totals_reject_invalid_evidence_values_and_conflicts(candidates):
    values, missing = resolve_fields(policy('ppt-workflow'), '制作3页或5页PPT',
                                     extractions={'slide_count': candidates})
    assert 'slide_count' not in values
    assert 'slide_count' in [field['id'] for field in missing]


def test_equivalent_totals_and_confirmed_answers_remain_resolved():
    runtime = policy('ppt-workflow')
    candidates = {'slide_count': [{'value': '3', 'evidence': '三页'}, {'value': '3', 'evidence': '3页'}]}
    values, missing = resolve_fields(runtime, '制作三页PPT（共3页）', extractions=candidates)
    assert values['slide_count'] == '3'
    assert 'slide_count' not in [field['id'] for field in missing]
    values, missing = resolve_fields(runtime, '制作3页或5页PPT', previous={'slide_count': '5 页'},
                                     extractions=count_extraction('3', '3页'))
    assert values['slide_count'] == '5 页'
    assert 'slide_count' not in [field['id'] for field in missing]


def test_grounded_total_cannot_contradict_a_duplicate_scalar_binding():
    values, missing = resolve_fields(policy('ppt-workflow'), '制作3页PPT', {'slide_count': '5'},
                                     extractions=count_extraction('3', '3页'))
    assert 'slide_count' not in values
    assert 'slide_count' in [field['id'] for field in missing]


def test_count_extractions_cannot_supply_model_resolved_backgrounds():
    values, missing = resolve_fields(
        policy('ppt-workflow'), '制作3页PPT，主题：周末回血',
        {'topic': '周末回血'},
        extractions={
            **count_extraction('3', '制作3页PPT'),
            'generate_background_images': [{'value': 'enabled', 'evidence': '周末回血'}],
        },
    )
    assert values['slide_count'] == '3'
    assert 'generate_background_images' not in values
    assert [field['id'] for field in missing] == ['generate_background_images']


@pytest.mark.parametrize('background', ['enabled', 'disabled'])
def test_ppt_background_uses_model_binding_and_prior_answer_takes_precedence(background):
    runtime = policy('ppt-workflow')
    request = '做一个关于气候变化的PPT，8页'
    bindings = {'topic': '气候变化', 'generate_background_images': background}
    values, missing = resolve_fields(runtime, request, bindings,
                                     extractions=count_extraction('8', '8页'))
    assert not missing
    assert values['slide_count'] == '8'
    assert values['generate_background_images'] == background
    values, missing = resolve_fields(runtime, request, bindings,
                                     {'generate_background_images': '__ask_user_no__'},
                                     extractions=count_extraction('8', '8页'))
    assert not missing
    assert values['generate_background_images'] == 'disabled'


@pytest.mark.parametrize('brief', [
    '做8页关于垃圾分类的PPT，启用｜为每页生成独立 AI 底图',
    '做8页关于垃圾分类的PPT，生成AI底图',
])
def test_ppt_fields_are_not_extracted_from_request_text_by_host(brief):
    values, missing = resolve_fields(policy('ppt-workflow'), brief)
    assert values == {'template_mode': 'none'}
    assert [field['id'] for field in missing] == ['topic', 'slide_count', 'generate_background_images']


@pytest.mark.parametrize('instruction,expected', [
    ('不要使用默认投标样式', 'false'),
    ('不使用默认投标样式', 'false'),
    ('使用基础样式', 'false'),
    ('使用默认投标样式', 'true'),
    ('不要使用基础样式', 'true'),
])
def test_bid_style_aliases_respect_negation(instruction, expected):
    values, missing = resolve_fields(policy('bid_tech_proposal_writer'), f'写8000字技术标，{instruction}')
    assert not missing
    assert values['use_default_docx_template'] == expected


def test_conflicting_opt_ins_are_asked_instead_of_using_a_default():
    values, missing = resolve_fields(policy('bid_tech_proposal_writer'),
                                     '写8000字技术标，使用默认投标样式，使用基础样式')
    assert 'use_default_docx_template' not in values
    assert [field['id'] for field in missing] == ['use_default_docx_template']
    card = startup_card(missing, startup_metadata('builtin:bid_tech_proposal_writer', 'r1', '写技术标', values))
    assert card['questions'][0]['id'] == 'use_default_docx_template'
    assert card['questions'][0]['required'] is True


@pytest.mark.parametrize('language', ['不要英文', '不要使用英文', '英文，不要英文'])
def test_negating_a_non_boolean_enum_does_not_select_or_invent_an_alternative(language):
    values, missing = resolve_fields(policy('academic_research_pipeline'), f'写8000字文献综述，{language}，引用IEEE',
                                     {'research_topic': 'RAG'})
    assert 'paper_language' not in values
    assert [field['id'] for field in missing] == ['paper_language']


def test_explicit_non_boolean_alternative_is_kept_and_confirmed_answers_resolve_conflicts():
    runtime = policy('academic_research_pipeline')
    values, missing = resolve_fields(runtime, '写8000字中文文献综述，不要英文，引用IEEE', {'research_topic': 'RAG'})
    assert values['paper_language'] == 'zh-CN'
    assert not missing
    values, missing = resolve_fields(runtime, '写8000字或10000字的中文文献综述，引用IEEE',
                                     {'research_topic': 'RAG'}, {'word_target': '10000'})
    assert values['word_target'] == '10000'
    assert not missing


@pytest.mark.parametrize('workflow', [
    'academic_research_pipeline', 'bid_tech_proposal_writer', 'writer-workflow', 'product_solution_delivery',
])
@pytest.mark.parametrize('brief', [
    '写一篇8000字正文，摘要200字',
    '摘要不超过200字，正文8000字',
    '全文8000字；abstract: 200 words',
])
def test_main_word_target_is_not_replaced_by_abstract_length(workflow, brief):
    values, _ = resolve_fields(policy(workflow), brief, {'word_target': '8000'})
    assert values['word_target'] == '8000'


@pytest.mark.parametrize('workflow', ['academic_research_pipeline', 'bid_tech_proposal_writer'])
def test_abstract_length_alone_does_not_satisfy_required_main_length(workflow):
    values, missing = resolve_fields(policy(workflow), '写一份文档，摘要200字')
    assert 'word_target' not in values
    assert 'word_target' in [field['id'] for field in missing]


def test_ambiguous_lengths_require_confirmation_and_equal_normalized_lengths_do_not():
    runtime = policy('academic_research_pipeline')
    values, missing = resolve_fields(runtime, '写8000字或10000字的中文文献综述，引用IEEE',
                                     {'research_topic': 'RAG', 'word_target': '8000'})
    assert 'word_target' not in values
    assert [field['id'] for field in missing] == ['word_target']
    values, missing = resolve_fields(runtime, '写八千字的中文文献综述，总篇幅8000字，引用IEEE',
                                     {'research_topic': 'RAG'})
    assert values['word_target'] == '8000'
    assert not missing


def test_typed_template_binding_skips_style_and_background_questions():
    values, missing = resolve_fields(policy('ppt-workflow'), '用上传模板做8页关于气候变化的PPT', {
        'topic': '气候变化', 'template_pptx': 'template.pptx',
    }, extractions=count_extraction('8', '8页'))
    assert [field['id'] for field in missing] == ['presentation_output_format']
    assert values['template_mode'] == 'upload'
    assert 'generate_background_images' not in values
    assert 'visual_style' not in values


def test_academic_card_includes_all_missing_fields_and_reuses_answers():
    runtime = policy('academic_research_pipeline')
    values, missing = resolve_fields(runtime, '写论文')
    assert values == {'output_format': 'docx'}
    assert len(missing) == 5
    card = startup_card(missing, startup_metadata('builtin:academic_research_pipeline', 'r1', '写论文', values))
    assert [q['id'] for q in card['questions']] == [field['id'] for field in missing]
    values, missing = resolve_fields(runtime, '写论文', previous={
        'research_topic': 'RAG可靠性', 'paper_type': '文献综述', 'word_target': '8000',
    })
    assert [item['id'] for item in missing] == ['paper_language', 'citation_style']
    assert values['paper_type'] == 'literature_review'


def test_markdown_bid_does_not_ask_word_styles():
    values, missing = resolve_fields(policy('bid_tech_proposal_writer'), '写技术标，10000字，Markdown')
    assert not missing
    assert values['output_format'] == 'md'
    assert 'use_default_docx_template' not in values


def test_source_filename_does_not_choose_the_delivery_format():
    values, _ = resolve_fields(policy('academic_research_pipeline'), '参考README.md写一篇论文')
    assert values['output_format'] == 'docx'


def test_zero_and_false_do_not_become_missing():
    runtime = {'clarification_fields': [
        {'id': 'count', 'question': 'Count?', 'required': True, 'type': 'text'},
        {'id': 'enabled', 'question': 'Enable?', 'required': True, 'type': 'boolean'},
    ]}
    assert resolve_fields(runtime, '', previous={'count': 0, 'enabled': False}) == (
        {'count': 0, 'enabled': False}, [],
    )


def test_local_resolver_has_no_model_or_io_dependency():
    runtime = policy('academic_research_pipeline')
    start = time.perf_counter()
    for _ in range(100):
        resolve_fields(runtime, '写一篇8000字的中文文献综述，引用APA 7，导出Word')
    # A generous regression bound for local work, not an end-to-end SLA.
    assert time.perf_counter() - start < 1


@pytest.mark.parametrize('count,expected', [('八千', '8000'), ('1万', '10000'), ('1.5万', '15000'), ('8,000', '8000')])
def test_explicit_lengths_are_normalized_without_asking_again(count, expected):
    values, missing = resolve_fields(policy('academic_research_pipeline'),
                                     f'写一篇{count}字的中文文献综述，引用ieee')
    assert values['word_target'] == expected
    assert values['citation_style'] == 'IEEE'
    assert [item['id'] for item in missing] == ['research_topic']


def test_model_cannot_fill_unstated_academic_decisions():
    values, missing = resolve_fields(policy('academic_research_pipeline'), '写篇论文', {
        'research_topic': 'RAG', 'paper_type': 'research', 'word_target': '8000',
        'paper_language': 'zh-CN', 'citation_style': 'APA 7',
    })
    assert [item['id'] for item in missing] == ['paper_type', 'word_target', 'paper_language', 'citation_style']
    assert values == {'research_topic': 'RAG', 'output_format': 'docx'}


@pytest.mark.parametrize('explicit_activation', [False, True])
def test_owned_continuation_returns_only_remaining_questions_without_prepare(monkeypatch, explicit_activation):
    from unittest.mock import MagicMock
    from lazymind.chat.workflow import workflow_manager as manager
    runtime = policy('academic_research_pipeline')
    client = MagicMock()
    client.get_workflow.return_value.result = {'revision_id': 'r1', 'runtime': runtime}
    monkeypatch.setattr(manager, '_client', lambda: client)
    toolkit = MagicMock()
    monkeypatch.setattr(manager, 'HostWorkflowToolkit', lambda *a, **kw: toolkit)
    contribution = manager.resolve_workflow_injection(
        None, current_query='回答已提交', workflow_catalog=[{
            'workflow_id': 'academic', 'workflow_ref': 'builtin:academic', 'revision_id': 'r2', 'runtime': runtime,
        }], workflow_activations=[{
            'workflow_id': 'academic', 'workflow_ref': 'builtin:academic', 'revision_id': 'r2',
            'tool_name': 'trigger_academic_workflow', 'runtime': runtime,
        }] if explicit_activation else [], clarification={
            'workflow_ref': 'builtin:academic', 'revision_id': 'r1', 'startup_request_id': 'launch',
            'original_request': '写论文', 'values': {
                'research_topic': 'RAG', 'paper_type': '文献综述', 'word_target': '8000',
            },
        },
    )
    card = contribution.pending_ask
    assert [question['id'] for question in card['questions']] == ['paper_language', 'citation_style']
    assert card['workflow']['startup_request_id'] == 'launch'
    assert card['workflow']['revision_id'] == 'r1'
    client.get_workflow.assert_called_once_with('academic', 'r1')
    toolkit.prepare_workflow.assert_not_called()


@pytest.mark.parametrize('authorized', [False, True])
def test_discovered_startup_answer_restores_only_authorized_trigger(monkeypatch, authorized):
    from unittest.mock import MagicMock
    from lazymind.chat.workflow import workflow_manager as manager
    runtime = policy('ppt-workflow')
    values, _ = resolve_fields(runtime, '制作个人答辩PPT，12页，15分钟，不生成AI底图', {
        'topic': '个人答辩', 'slide_count': '12', 'template_pptx': 'template.pptx',
        'presentation_output_format': 'html',
    }, extractions=count_extraction('12', '12页'))
    values['presentation_output_format'] = 'html'
    client = MagicMock()
    client.get_workflow.return_value.result = {'revision_id': 'r1', 'runtime': runtime}
    client.get_state.return_value = {'session_id': 'session-1', 'state_version': 1,
                                     'projection': {'reachable': ['analysis'], 'ready': ['analysis']}}
    monkeypatch.setattr(manager, '_client', lambda: client)
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {'session_id': 'session-1', 'ready_steps': ['analysis']}
    monkeypatch.setattr(manager, 'HostWorkflowToolkit', lambda *a, **kw: toolkit)
    contribution = manager.resolve_workflow_injection(
        None, current_query='HTML', workflow_catalog=[{
            'workflow_id': 'ppt-workflow', 'workflow_ref': 'builtin:ppt-workflow',
            'revision_id': 'r2', 'runtime': runtime,
        }] if authorized else [], workflow_activations=[], clarification={
            'workflow_ref': 'builtin:ppt-workflow', 'revision_id': 'r1',
            'original_request': '制作个人答辩PPT，12页，15分钟，不生成AI底图', 'values': values,
        },
    )
    pending, triggers = list(contribution.tools), []
    while pending:
        tool = pending.pop()
        if isinstance(tool, dict):
            pending.extend(tool.get('tools') or [])
        elif tool.__name__ == 'trigger_ppt_workflow':
            triggers.append(tool)
    if not authorized:
        assert not triggers
        client.get_workflow.assert_not_called()
        toolkit.prepare_workflow.assert_not_called()
        return
    assert len(triggers) == 1
    assert 'trigger_ppt_workflow' in contribution.required_tool_names
    result = triggers[0]()
    assert result['session_id'] == 'session-1'
    submitted = toolkit.prepare_workflow.call_args.kwargs
    assert submitted['revision_id'] == 'r1'
    assert submitted['clarification_values']['presentation_output_format'] == 'html'
    assert submitted['clarification_values']['slide_count'] == '12'
    assert '15分钟' in submitted['request_context']


@pytest.fixture
def startup_tools(monkeypatch):
    from unittest.mock import MagicMock
    from lazymind.chat.workflow import workflow_manager as manager

    client, toolkit = MagicMock(), MagicMock()
    client.get_state.return_value = {'projection': {'ready': ['prepare']}}
    toolkit.prepare_workflow.return_value = {'session_id': 'session-1', 'ready_steps': ['prepare']}
    monkeypatch.setattr(manager, '_client', lambda: client)
    monkeypatch.setattr(manager, 'HostWorkflowToolkit', lambda *args, **kwargs: toolkit)
    events = []
    monkeypatch.setattr('lazyllm.tools.agent.base._write_agent_data',
                        lambda tag, **payload: events.append((tag, payload)))
    monkeypatch.setattr('lazymind.chat.engine.tools.ask_user._write_agent_data',
                        lambda tag, **payload: events.append((tag, payload)))

    def build(runtime, request, history=None, *, workflow_id='writer-workflow', session_holder=None):
        client.get_workflow.return_value.result = {
            'revision_id': 'revision-1', 'runtime': runtime,
            'compiled_graph': {'material_types': {}, 'material_producers': {}},
        }
        ref = f'builtin:{workflow_id}'
        activation = manager.workflow_activation_from_catalog_item({
            'workflow_id': workflow_id, 'workflow_ref': ref, 'revision_id': 'revision-1', 'runtime': runtime,
        })
        return manager._workflow_trigger_tools(
            [activation], {ref}, current_query=request, conversation_history=history,
            session_holder=session_holder if session_holder is not None else {},
        )[0]

    return build, client, toolkit, events


@pytest.mark.parametrize('brief,model_choice,count_evidence', [
    ('给我生成 12 页关于2026年英雄联盟 世界赛 比赛进程的ppt ai 底图 15分钟介绍 面向游戏观众 赛博朋克风格',
     'enabled', '12 页'),
    ('做12页关于垃圾分类的PPT，幻灯片用纯色，别另外画背景', 'disabled', '12页'),
    ('做12页关于AI底图设计的PPT', None, '12页'),
    ('做12页关于垃圾分类的PPT ai 底图，但不要生成 AI 底图', None, '12页'),
])
def test_ppt_trigger_accepts_all_model_choices_together(startup_tools, brief, model_choice, count_evidence):
    build, _, toolkit, events = startup_tools
    holder = {}
    trigger = build(policy('ppt-workflow'), brief, workflow_id='ppt-workflow', session_holder=holder)
    assert 'pending_ask' not in holder
    assert 'send all resolved fields in one trigger call' in trigger.__doc__
    inputs = {'topic': '垃圾分类', 'slide_count': '12'}
    if '英雄联盟' in brief:
        inputs.update(topic='2026年英雄联盟世界赛比赛进程', presentation_duration='15分钟',
                      target_customer='游戏观众', visual_style='赛博朋克风格')
    if model_choice:
        inputs['generate_background_images'] = model_choice
    result = trigger(input_bindings=inputs, clarification_extractions=count_extraction('12', count_evidence))
    if model_choice is None:
        assert result['status'] == 'needs_input'
        assert result['missing_clarification_fields'] == ['generate_background_images']
        assert [tag for tag, _ in events] == ['ask_pending']
        saved = json.loads(events[0][1]['workflow']['values_json'])
        assert all(saved[key] == value for key, value in inputs.items())
        toolkit.prepare_workflow.assert_not_called()
    else:
        assert result['outcome'] == 'ready'
        submitted = toolkit.prepare_workflow.call_args.kwargs
        assert submitted['clarification_values'] == {**inputs, 'template_mode': 'none'}
        assert brief in submitted['request_context']
        assert not events


@pytest.fixture
def run_startup_agent(tmp_path):
    import lazyllm
    from lazymind.chat.engine.agent_runtime import (
        AgentExecutor, AgentRunPlan, AgentRole, AgentExecutionOptions, PromptBuilder,
    )
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext

    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {}

    class Model:
        def __init__(self, call):
            self._module_id = f'startup-test-{id(self)}'
            self.requests = 0
            self.call = call

        def share(self, **kwargs):
            return self

        def used_by(self, _):
            return self

        def __call__(self, *args, **kwargs):
            self.requests += 1
            return ({'content': '', 'tool_calls': [self.call]} if self.requests == 1
                    else {'content': 'Unexpected continuation after asking the user.'})

    def run(tools, name, arguments, stop_tools=()):
        model = Model({'id': 'startup-call', 'type': 'function',
                       'function': {'name': name, 'arguments': arguments}})
        with lazyllm.locals._scope():
            plan = AgentRunPlan(
                role=AgentRole.CHAT,
                prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Start the workflow', source='user').build(),
                tools=tools, stop_tools=list(stop_tools), execution_options=AgentExecutionOptions(
                    workspace=str(tmp_path), max_retries=2, enable_builtin_tools=False,
                    workspace_permission=WorkspaceContext(local_runtime=False)),
            )
            result = AgentExecutor().create_agent(model, plan)('Start the workflow')
        return result, model.requests

    yield run
    if previous is None:
        lazyllm.globals.pop('agentic_config', None)
    else:
        lazyllm.globals['agentic_config'] = previous


@pytest.mark.parametrize('brief,evidence', [
    ('制作3页PPT，第1页是封面，第2页讲方法，第3页总结', '制作3页PPT'),
    ('Create a three-slide presentation. Slide 1 is the cover.', 'a three-slide presentation'),
])
def test_trigger_transports_grounded_count_without_an_extra_model_call(
    startup_tools, run_startup_agent, brief, evidence,
):
    import json
    build, _client, toolkit, events = startup_tools
    trigger = build(policy('ppt-workflow'), brief + '，主题：周末回血，生成AI底图')
    _, calls = run_startup_agent([trigger], trigger.__name__, json.dumps({
        'input_bindings': {'topic': '周末回血', 'generate_background_images': 'enabled'},
        'clarification_extractions': count_extraction('3', evidence),
    }), stop_tools=[trigger.__name__])
    assert calls == 1
    assert events == []
    toolkit.prepare_workflow.assert_called_once()
    assert toolkit.prepare_workflow.call_args.kwargs['clarification_values']['slide_count'] == '3'


def test_trigger_keeps_conflicting_totals_in_one_question_card(startup_tools):
    build, _client, toolkit, events = startup_tools
    trigger = build(policy('ppt-workflow'), '制作3页或5页PPT，主题：周末回血，生成AI底图')
    result = trigger(input_bindings={'topic': '周末回血', 'generate_background_images': 'enabled'},
                     clarification_extractions={'slide_count': [
                         {'value': '3', 'evidence': '3页'}, {'value': '5', 'evidence': '5页'},
                     ]})
    assert result['missing_clarification_fields'] == ['slide_count']
    assert result['_agent_control']['stop'] is True
    toolkit.prepare_workflow.assert_not_called()
    assert len(events) == 1
    assert [q['id'] for q in events[0][1]['questions']] == ['slide_count']
    assert events[0][1]['questions'][0]['choices'] == ['3', '5', '__ask_user_other__']


def test_ppt_conflict_card_preserves_user_candidates_and_clean_topic(startup_tools):
    import json
    build, _client, toolkit, events = startup_tools
    request = '请使用AIPPT规划与生成工作流、制作份3页PPT,主题为《打工人的周末回血指南》 要不还是4页吧 我也不确定'
    runtime = policy('ppt-workflow')
    trigger = build(runtime, request)
    result = trigger(input_bindings={'topic': '打工人的周末回血指南'}, clarification_extractions={'slide_count': [
        {'value': '4', 'evidence': '要不还是4页吧 我也不确定'},
        {'value': '3', 'evidence': '制作份3页PPT'},
        {'value': '3', 'evidence': '制作份3页PPT'},
    ]})
    assert result['missing_clarification_fields'] == ['slide_count', 'generate_background_images']
    toolkit.prepare_workflow.assert_not_called()
    assert len(events) == 1
    card = events[0][1]
    assert card['questions'][0]['choices'] == ['3', '4', '__ask_user_other__']
    assert card['questions'][1]['choices'] == [
        '启用｜为每页生成独立 AI 底图', '不启用｜使用纯色、渐变和矢量装饰',
    ]
    saved = json.loads(card['workflow']['values_json'])
    assert saved['topic'] == '打工人的周末回血指南'
    assert 'slide_count' not in saved
    values, missing = resolve_fields(runtime, request, previous={
        **saved, 'slide_count': '4', 'generate_background_images': '不启用｜使用纯色、渐变和矢量装饰',
    })
    assert values['topic'] == '打工人的周末回血指南'
    assert values['slide_count'] == '4'
    assert not missing
    assert next(f for f in runtime['clarification_fields'] if f['id'] == 'slide_count')['choices'][:3] == [
        '3 页', '5 页', '8 页',
    ]


@pytest.mark.parametrize('brief,topic', [
    ('主题为《打工人的周末回血指南》 要不还是4页吧 我也不确定', '打工人的周末回血指南'),
    ('主题为打工人的周末回血指南 做3页还是4页呢', '打工人的周末回血指南'),
    ('Title: Weekend Recovery. Maybe 3 or 4 slides?', 'Weekend Recovery'),
    ('主题为《2026 年三种低成本回血方式》 做3页PPT', '2026 年三种低成本回血方式'),
])
def test_ppt_topic_keeps_model_semantics_without_regex_overwrite(brief, topic):
    values, _ = resolve_fields(policy('ppt-workflow'), brief, {'topic': topic})
    assert values['topic'] == topic
    values, _ = resolve_fields(policy('ppt-workflow'), brief, {'topic': '另一个主题'}, previous={'topic': topic})
    assert values['topic'] == topic


def test_unknown_ppt_topic_still_requires_confirmation():
    values, missing = resolve_fields(policy('ppt-workflow'), '做3页或4页PPT')
    assert 'topic' not in values
    assert 'topic' in [field['id'] for field in missing]


def test_invalid_evidence_does_not_become_a_suggested_count():
    _, missing = resolve_fields(policy('ppt-workflow'), '制作3页PPT', {'topic': '周末回血'}, extractions={
        'slide_count': [{'value': '3', 'evidence': '3页'}, {'value': '999', 'evidence': '伪造原文'}],
    })
    card = startup_card(missing, {})
    assert card['questions'][0]['choices'] == ['3 页', '5 页', '8 页', '__ask_user_other__']


@pytest.mark.parametrize('answered', [False, True])
def test_writer_starts_without_requiring_duplicate_goal_binding(startup_tools, answered):
    build, _client, toolkit, events = startup_tools
    runtime = policy('writer-workflow')
    original = '用中文写一篇克苏鲁小说，不超过3章、3000字以内，第一章生成一张图片'
    history = [
        {'role': 'user', 'content': original},
        {'role': 'assistant', 'tool_calls': [{'id': 'ask-1', 'function': {
            'name': 'ask_user', 'arguments': {'questions': [{
                'id': 'writing_goal', 'text': '想写什么内容，或修改哪份文档？', 'type': 'text',
            }]}},
        }]},
        {'role': 'tool', 'tool_call_id': 'ask-1', 'name': 'ask_user',
         'content': 'The user submitted the form.'},
    ] if answered else None
    trigger = build(runtime, '主题是海边小镇的怪谈。' if answered else original, history)
    result = trigger()
    assert result['outcome'] == 'ready'
    context = toolkit.prepare_workflow.call_args.kwargs['request_context']
    assert original in context
    if answered:
        assert '主题是海边小镇的怪谈。' in context
    assert events == []


def test_writer_can_still_ask_for_an_unknown_topic(startup_tools, run_startup_agent):
    from lazymind.chat.service.chat_service import (
        _build_ask_user_tool, _workflow_startup_clarification_available,
    )

    build, _client, toolkit, events = startup_tools
    runtime = policy('writer-workflow')
    trigger = build(runtime, '帮我写篇文章')
    assert _workflow_startup_clarification_available(runtime, None)
    ask = _build_ask_user_tool(runtime)[0]
    _result, requests = run_startup_agent([trigger, ask], 'ask_user', {'questions': [{
        'id': 'writing_goal', 'text': '这篇文章想写什么主题？', 'type': 'text',
        'reason': '需要确定文章主题才能开始写作。', 'required': True,
    }]}, stop_tools=['ask_user'])
    assert requests == 1
    assert len(events) == 1
    assert events[0][1]['questions'][0]['id'] == 'writing_goal'
    toolkit.prepare_workflow.assert_not_called()


@pytest.mark.parametrize('source', ['host', 'core'])
@pytest.mark.parametrize('language', ['zh', 'en'])
@pytest.mark.parametrize('wrapped', [False, True])
def test_startup_question_receipt_renders_without_unresolved_placeholders(startup_tools, source, language, wrapped):
    import json
    import re
    from lazymind.chat.service.component.tool_rendering import _tool_result_frame_text

    build, _client, toolkit, events = startup_tools
    question = {'id': 'topic', 'question': '主题是什么？', 'type': 'text',
                'reason': '主题决定本次交付内容。', 'required': True}
    runtime = {'clarification_fields': [question]} if source == 'host' else {}
    if source == 'core':
        toolkit.prepare_workflow.return_value = {'status': 'needs_input', 'questions': [{
            **question, 'text': question['question'],
        }]}
    trigger = build(runtime, '开始工作流')
    result = trigger()
    assert result['status'] == 'needs_input'
    assert result['outcome'] == 'waiting_for_input'
    assert result['reason']
    assert result['_agent_control']['stop'] is True
    assert events[0][0] == 'ask_pending'
    frame = _tool_result_frame_text({
        'id': 'startup-question', 'name': trigger.__name__,
        'result': {'ok': True, 'value': json.dumps(result)} if wrapped else result,
    }, language)
    preview = re.search(r'<trp id="startup-question">(.*?)</trp>', frame, re.S).group(1)
    assert preview.strip() == ('请补充必要信息后继续。' if language == 'zh'
                               else 'Please provide the required information to continue.')
    assert result['reason'] not in preview
    assert result['outcome'] not in preview
    assert not re.search(r'\{[^{}]+\}', preview)


@pytest.mark.parametrize('source', ['host', 'core'])
def test_startup_question_stops_the_agent_before_another_model_round(startup_tools, run_startup_agent, source):
    build, client, toolkit, events = startup_tools
    question = {'id': 'topic', 'question': '主题是什么？', 'type': 'text',
                'reason': '主题决定本次交付内容。', 'required': True}
    runtime = {'clarification_fields': [question]} if source == 'host' else {}
    if source == 'core':
        toolkit.prepare_workflow.return_value = {'status': 'needs_input', 'questions': [{
            **question, 'text': question['question'],
        }]}
    trigger = build(runtime, '开始工作流')
    _result, requests = run_startup_agent([trigger], 'trigger_writer_workflow', {})
    assert requests == 1
    assert len(events) == 1
    assert events[0][0] == 'ask_pending'
    assert events[0][1]['questions'][0]['id'] == 'topic'
    assert 'workflow' in events[0][1]
    assert toolkit.prepare_workflow.call_count == (source == 'core')
    client.get_state.assert_not_called()
