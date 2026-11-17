import json

import pytest

from lazymind.document_tools.paper.runtime_events import AgentCandidatePreview
from lazyllm.module.llms.onlinemodule.base.onlineChatModuleBase import LazyLLMOnlineChatModuleBase


@pytest.mark.parametrize('step,field', list(AgentCandidatePreview.FIELDS.items()))
def test_candidate_text_streams_before_the_submission_is_complete(step, field):
    module = LazyLLMOnlineChatModuleBase.__new__(LazyLLMOnlineChatModuleBase)
    chunks = []
    module._stream_sink = chunks.append
    preview = AgentCandidatePreview(step)
    text = '# Paper\n\n## Section\nEvidence: "observed" 中文.'
    arguments = json.dumps({'output': {field: text}, 'receipts': []}, ensure_ascii=True)
    split = arguments.index('Evidence:')
    module._emit_message_content({'choices': [{'delta': {'tool_calls': [{
        'index': 0, 'id': 'candidate-1', 'function': {'name': 'paper_submit_candidate',
                                                    'arguments': arguments[:split]}}]}}]}, True)
    first = [item for chunk in chunks for item in preview.feed(chunk)]
    assert any(item['type'] == 'artifact_stream' and '# Paper' in item['delta'] for item in first)
    assert all(item['slot'] == step + '_document' for item in first)
    chunks.clear()
    module._emit_message_content({'choices': [{'delta': {'tool_calls': [{
        'index': 0, 'function': {'arguments': arguments[split:]}}]}}]}, True)
    rest = [item for chunk in chunks for item in preview.feed(chunk)]
    rest += preview.feed({'tag': 'tool_calls', 'tool_calls': []})
    assert ''.join(item['delta'] for item in first + rest if item['type'] == 'artifact_stream') == text
    assert rest[-1]['type'] == 'artifact_stream_end'
    assert 'receipts' not in ''.join(item.get('delta', '') for item in first + rest)


def test_preview_ignores_sources_and_withdraws_a_rolled_back_candidate():
    preview = AgentCandidatePreview('write_document')
    def delta(name, args):
        return {'tag': 'tool_call_delta', 'delta': json.dumps([{'index': 0, 'function': {
            'name': name, 'arguments': args}}])}
    assert preview.feed(delta('paper_read_material', '{"secret":"source"}')) == []
    preview.feed({'tag': 'tool_calls'})
    assert preview.feed(delta('paper_submit_candidate', '{"output":{"manuscript":"# Draft'))
    aborted = preview.feed({'tag': 'model_output', 'model_output': {'phase': 'rollback'}})
    assert aborted[-1]['type'] == 'artifact_stream_abort'
    recovered = preview.feed(delta('paper_submit_candidate', '{"output":{"manuscript":"# Recovered"}}'))
    recovered += preview.feed({'tag': 'tool_calls'})
    assert ''.join(event.get('delta', '') for event in recovered) == '# Recovered'
