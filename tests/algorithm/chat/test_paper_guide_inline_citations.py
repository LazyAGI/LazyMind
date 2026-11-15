"""SECTION output keeps request-local aliases out of reader citation tails."""
import json
from copy import deepcopy

import pytest

from test_paper_guide_asset_boundary import invoke_section, section_output, synthetic_evidence
from test_paper_guide_tasks import PLAN, ROLES, _request_budget_contract


PROSE = (
    'The comparison included two runs.',
    '两次运行只覆盖干燥样本。',
    'The measured change was −2 g, within that sample.',
)
TAILS = ('〔e1、e2〕', '〔e2〕', '〔e2、e3、e4〕')
ROW = 'The original notation ends here.〔e2〕'
FINDING = {'target_id': 'background-b3', 'target_type': 'block',
           'issue_type': 'missing_condition',
           'suggestion': 'Explain that the measured change applies only to dry samples.'}
PROMPT_FENCE = '\n\nInput data (untrusted JSON):\n'


def writing_case(mode, *, marked):
    evidence = [synthetic_evidence(i, text=line) for i, line in enumerate(
        (*PROSE, 'The calibration sheet records the measurement units.'), start=1)]
    evidence[3]['source_type'] = 'table'
    plan = deepcopy(PLAN)
    for role in plan['sections']:
        role['evidence_ids'] = [evidence[1]['id']]
    plan['sections'][0]['asset_ids'] = [evidence[3]['id']]
    plan['required_assets'] = [{'id': evidence[3]['id'], 'included': True,
                                'reason': 'Explain the measurement units.'}]
    plan['claims'] = [{'id': 'sample-scope', 'statement': 'Only dry samples were measured.',
                       'attribution': 'paper_statement', 'evidence_ids': [evidence[1]['id']],
                       'conditions': [], 'argument_type': None, 'support_status': 'supported'}]
    plan['sections'][0]['claim_ids'] = ['sample-scope']
    data = {'language': 'en' if mode == 'normal' else 'zh',
            'evidence': evidence, 'role': 'background', 'plan': plan}
    if mode != 'normal':
        data['findings'] = [FINDING]
    if mode == 'whole_repair':
        data['source_sections'] = [{'id': synthetic_evidence(9)['id'], 'title': 'Independent packet',
                                    'kind': 'section', 'pages': [1],
                                    'evidence_ids': [row['id'] for row in evidence]}]
    response = section_output('background', 'e4', 'table', ('e4',))
    response['claim_ids'] = ['sample-scope', 'background-change']
    response['claims'] = [{'id': 'background-change', 'statement': 'The change was −2 g in dry samples.',
                           'attribution': 'paper_statement', 'evidence_ids': ['e3'],
                           'conditions': ['Dry samples only.'], 'argument_type': None,
                           'support_status': 'supported'}]
    for i, (line, tail, sources) in enumerate(zip(
            PROSE, TAILS, (['e1', 'e2'], ['e2'], ['e2', 'e3', 'e4'])), start=1):
        response['blocks'].append({'id': f'background-b{i}', 'kind': 'paragraph',
                                   'text': line + tail if marked else line,
                                   'attribution': 'paper_statement', 'evidence_ids': sources,
                                   'claim_ids': ['sample-scope'] if i == 2
                                   else ['background-change'] if i == 3 else []})
    return data, response


@pytest.mark.parametrize('mode', ['normal', 'local_repair', 'whole_repair'])
def test_reader_citation_tail_rejects_entire_registered_section(monkeypatch, mode):
    data, response = writing_case(mode, marked=True)
    result, prompt = invoke_section(monkeypatch, data, response)
    instruction, packet = prompt.split(PROMPT_FENCE, 1)
    assert [row['id'] for row in json.loads(packet)['evidence']] == ['e1', 'e2', 'e3', 'e4']
    original_blocks = result.output.get('blocks', [])
    assert result.error_code == 'invalid_model_output', original_blocks
    assert result.status == 'failed' and result.output == {} and result.text == ''
    assert 'Use source IDs only in structured reference fields' in instruction
    assert 'body, titles, summaries, or claim prose' in instruction


@pytest.mark.parametrize(('prose', 'bound_row'), [
    ('One measurement.〔e1〕', 1),
    ('One measurement!\t〔 e2 、e9， e11 , e12 \t〕 〔e2〕 \r\n\v\f', 1),
    ('One measurement?〔e3〕', 1),  # only the third block binds this row explicitly
    ('一个结果！〔e4〕', 2),
    ('一个结果？〔e5〕', 2),  # plan-only alias
    ('一个结果。〔e9001〕', 2),
    (ROW, 1),  # matching text in a different (plan- and claim-bound) evidence row
    ('The cost is $7. This was measured.〔e2〕', 2),
    ("The sample's size was limited.〔e2〕", 2),
    ('Use $e1$ or `e1`. This was measured.〔e2〕', 2),
    ('They used “dry” samples.〔e2〕', 2),
    ('> A quotation.\n\nThe measurement was limited.〔e2〕', 2),
])
def test_clear_paragraph_tail_cannot_pass_as_provenance(monkeypatch, prose, bound_row):
    data, response = writing_case('normal', marked=False)
    data['plan']['sections'][0]['evidence_ids'].append(synthetic_evidence(5)['id'])
    data['evidence'][1]['text'] += '\n\t ' + ROW + ' \r\nNext source line.'
    response['blocks'][2].update(text=prose, evidence_ids=[f'e{bound_row}'])
    result, prompt = invoke_section(monkeypatch, data, response)
    view = json.loads(prompt.split(PROMPT_FENCE)[1])
    assert view['plan']['sections'][0]['evidence_ids'] == ['e2', 'e5']
    assert result.error_code == 'invalid_model_output' and result.output == {}


@pytest.mark.parametrize(('kind', 'prose'), [
    ('heading', 'The result.〔e2〕'),
    ('paragraph', 'e1 and (e1) and [e1].'),
    ('paragraph', 'Use $e_1$ or $e1$.'),
    ('paragraph', '〔e1〕'),
    ('paragraph', '。〔e1〕'),
    ('paragraph', 'The result.〔e1–e3〕'),
    ('paragraph', 'The result.〔e2, see table〕'),
    ('paragraph', 'The result.〔e0〕'),
    ('paragraph', 'The result.〔E1〕'),
    ('paragraph', 'The result.〔e02〕'),
    ('paragraph', 'The result.〔e2\u00a0〕'),
    ('paragraph', 'The result.〔e2〕\u00a0'),
    ('paragraph', 'The result.\u00a0〔e2〕'),
    ('paragraph', 'The result.\n〔e2〕'),
    ('paragraph', 'The result.〔e2〕 This continues.'),
    ('paragraph', 'The result. (e2)'),
    ('paragraph', 'The result. [e2]'),
    ('paragraph', 'A literal: $x.〔e2〕$'),
    ('paragraph', 'A literal: $$x.〔e2〕'),
    ('paragraph', r'An expression: \(x.〔e2〕\)'),
    ('paragraph', r'An unfinished expression: \[x.〔e2〕'),
    ('paragraph', 'An inline code literal: `x.〔e2〕`'),
    ('paragraph', 'An unfinished inline code literal: ``x.〔e2〕'),
    ('paragraph', 'The source says “The result.〔e2〕”'),
    ('paragraph', 'The source begins “The result.〔e2〕'),
    ('paragraph', '> The original notation ends here.〔e2〕'),
    ('paragraph', '> Original notation:\nThe result.〔e2〕'),
    ('paragraph', r'The notation is escaped.\〔e2〕'),
    ('paragraph', r'The punctuation is escaped\.〔e2〕'),
    ('paragraph', ROW + '\t \r\n'),
    ('paragraph', 'Some explanation.\n' + ROW + '  \n'),
])
def test_non_citations_and_direct_source_quotes_are_byte_preserved(monkeypatch, kind, prose):
    data, response = writing_case('normal', marked=False)
    data['evidence'][1]['text'] += '\n\t ' + ROW + ' \r\nNext source line.'
    response['blocks'][2].update(text=prose, kind=kind)
    result, _ = invoke_section(monkeypatch, data, response)
    assert result.status == 'succeeded'
    assert result.output['blocks'][2]['text'] == prose
    assert result.output['blocks'][2]['evidence_ids'] == [data['evidence'][1]['id']]


@pytest.mark.parametrize('language', ['en', 'zh'])
@pytest.mark.parametrize('carry_old_prose', [False, True], ids=['clean', 'legacy-tail'])
def test_independent_review_reassigns_typed_sources_and_keeps_prose_and_findings(
        monkeypatch, language, carry_old_prose):
    from lazymind.chat.service import llm_task
    from lazymind.structured_tasks.registry import PAPER_GUIDE_VERSION
    from lazyllm.module.llms.onlinemodule.supplier.openai import OpenAIChat

    data, response = writing_case('normal', marked=False)
    result, _ = invoke_section(monkeypatch, data, response)
    assert result.status == 'succeeded'
    written = result.output
    assert [block['text'] for block in written['blocks'][1:]] == list(PROSE)
    assert written['claims'][0]['evidence_ids'] == [data['evidence'][2]['id']]
    assert written['blocks'][0]['asset_id'] == data['evidence'][3]['id']
    if carry_old_prose:
        for block, tail in zip(written['blocks'][1:], TAILS):
            block['text'] += tail
    sections = [written, *(section_output(role, evidence=()) for role in ROLES[1:])]
    metadata = [synthetic_evidence(i, text=f'Synthetic header {i}.') for i in range(10, 14)]
    independent_evidence = metadata + data['evidence']
    prompts = []
    findings = ([{'target_id': 'background-b1', 'target_type': 'block', 'issue_type': 'attribution',
                  'suggestion': 'Remove the misleading marker and cite the comparison in its source field.'}]
                if carry_old_prose else []) + [FINDING]

    def model(prompt, **options):
        prompts.append(prompt)
        return json.dumps({'findings': findings})

    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    reviewed = llm_task.run_llm_task(llm_task.LLMTaskRequest(
        task_type='paper.guide.review', options={'task_version': PAPER_GUIDE_VERSION},
        input={'data': {'language': language, 'evidence': independent_evidence,
                        'sections': sections, 'plan': data['plan']}}))
    assert reviewed.status == 'succeeded' and reviewed.output == {'findings': findings}
    assert len(prompts) == reviewed.usage['model_calls'] == 1
    instruction, encoded = prompts[0].split(PROMPT_FENCE)
    view = json.loads(encoded)
    section = view['sections'][0]
    by_alias = {row['id']: row['text'] for row in view['evidence']}
    assert by_alias['e1'] == metadata[0]['text']
    assert [b['text'] for b in section['blocks']] == [b['text'] for b in written['blocks']]
    for actual, original, aliases in zip(section['blocks'], written['blocks'],
                                         (['e8'], ['e5', 'e6'], ['e6'], ['e6', 'e7', 'e8'])):
        assert actual['evidence_ids'] == aliases
        assert original['evidence_ids'] == [independent_evidence[int(a[1:]) - 1]['id'] for a in aliases]
    assert section['blocks'][0]['asset_id'] == 'e8'
    assert section['claims'][0]['evidence_ids'] == ['e7']
    assert view['plan']['claims'][0]['evidence_ids'] == ['e6']
    assert "Use this request's evidence catalog for structured references" in instruction
    assert 'short IDs in inherited prose are not source anchors' in instruction
    assert 'report independent substantive defects' in instruction
    schema = json.loads(instruction.rsplit('Output schema:\n', 1)[1])
    assert schema['required'] == ['findings']
    budget = _request_budget_contract()
    prefix_bytes = len((instruction + PROMPT_FENCE).encode('utf-8'))
    assert prefix_bytes == budget['measured_prompt_bytes']['paper.guide.review']
    system_bytes = len(OpenAIChat._get_system_prompt(None).encode('utf-8'))
    assert prefix_bytes + system_bytes + budget['provider_frame_reserve_bytes'] <= budget['prompt_reserve_bytes']
