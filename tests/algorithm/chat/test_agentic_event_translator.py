from lazymind.chat.service.component import AgentEventFrameTranslator
from lazymind.chat.service.utils.citations import CITATION_REFS_KEY
from lazyllm.tools.agent import AgentEvent


def test_translator_registers_citations_from_tool_results_before_text_rewrite():
    citation_state = {}
    translator = AgentEventFrameTranslator(query='q', citation_state=citation_state)
    item = {
        'uid': 'node-1',
        'text': 'source text',
        'docid': 'doc-1',
        'kb_id': 'kb-1',
        'group': 'block',
        'number': 3,
        'metadata': {'file_name': 'doc.md'},
        'global_metadata': {'docid': 'doc-1', 'kb_id': 'kb-1', 'file_name': 'doc.md'},
    }

    translator.feed(AgentEvent(
        type='agent.tool.results',
        tool_results=[{
            'id': 'call-1',
            'name': 'kb_search',
            'result': {
                'success': True,
                'tool': 'kb_search',
                'result': {
                    'total': 1,
                    'items': [item],
                },
            },
        }],
    ))

    assert item['citation_index'] == '1.1'
    assert item['ref'] == '[[1.1]]'
    assert citation_state[CITATION_REFS_KEY]['1.1']['content'] == 'source text'

    frames = translator.feed(AgentEvent(type='agent.text.delta', delta='Use [[1.1]].'))
    assert ''.join(frame['text'] for frame in frames) == 'Use [1](#source-1.1 "doc.md").'

    final_frames = translator.finish('')
    assert final_frames[-1]['sources'][0]['index'] == '1.1'
