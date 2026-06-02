from lazymind.chat.service.component import AgentEventFrameTranslator
from lazymind.chat.service.utils.citations import (
    CITATION_REFS_KEY,
    annotate_citations,
)


def test_translator_rewrites_citations_registered_by_tools():
    translator = AgentEventFrameTranslator(query='q')
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
    annotate_citations(item, translator.citation_state)

    translator.feed({
        'tag': 'tool_results',
        'tool_results': [{
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
    })

    assert item['citation_index'] == '1.1'
    assert item['ref'] == '[[1.1]]'
    assert translator.citation_state[CITATION_REFS_KEY]['1.1']['content'] == 'source text'

    frames = translator.feed({'tag': 'text', 'delta': 'Use [[1.1]].'})
    assert ''.join(frame['text'] for frame in frames) == 'Use [1](#source-1.1 "doc.md").'

    final_frames = translator.finish('')
    assert final_frames[-1]['sources'][0]['index'] == '1.1'
