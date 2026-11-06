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


def test_ppt_explicit_background_cannot_be_invented_by_model():
    runtime = policy('ppt-workflow')
    values, missing = resolve_fields(runtime, '做一个关于气候变化的PPT，8页',
                                     {'topic': '气候变化', 'generate_background_images': 'enabled'})
    assert values['slide_count'] == '8'
    assert [item['id'] for item in missing] == ['generate_background_images']
    values, missing = resolve_fields(runtime, '做一个关于气候变化的PPT，8页',
                                     {'topic': '气候变化'}, {'generate_background_images': '__ask_user_no__'})
    assert not missing
    assert values['generate_background_images'] == 'disabled'


@pytest.mark.parametrize('instruction,expected', [
    ('不要启用AI底图', 'disabled'),
    ('不启用AI底图', 'disabled'),
    ('不要生成 AI 底图', 'disabled'),
    ('不生成AI底图', 'disabled'),
    ('生成AI底图', 'enabled'),
    ('启用AI底图', 'enabled'),
    ('不要关闭AI底图', 'enabled'),
])
def test_ppt_background_aliases_respect_negation(instruction, expected):
    values, missing = resolve_fields(policy('ppt-workflow'), f'做8页关于垃圾分类的PPT，{instruction}')
    assert not missing
    assert values['generate_background_images'] == expected


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
    })
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
    })
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
