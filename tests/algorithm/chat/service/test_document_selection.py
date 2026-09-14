from lazymind.chat.service import document_selection


class _Document:
    def __init__(self, nodes):
        self.nodes = nodes

    def keyword_search(self, **_kwargs):
        return self.nodes


def test_resolve_document_selection_uses_exact_segment(monkeypatch):
    monkeypatch.setattr(document_selection, 'get_core_api', lambda *_args, **_kwargs: {
        'document_id': 'doc-1',
        'content': 'The complete chunk containing batch size tokens and its explanation.',
    })

    result = document_selection.resolve_document_selection_context({
        'dataset_id': 'kb-1',
        'document_id': 'doc-1',
        'segment_id': 'seg-1',
        'segment_group': 'block',
        'selected_text': 'batch size tokens',
        'paragraph_text': 'fallback paragraph',
    }, 'batch size tokens')

    assert result.startswith('The complete chunk')


def test_resolve_document_selection_falls_back_to_paragraph(monkeypatch):
    monkeypatch.setattr(document_selection, 'get_core_api', lambda *_args, **_kwargs: (_ for _ in ()).throw(RuntimeError()))
    monkeypatch.setattr(document_selection, '_find_positioned_segment', lambda *_args: '')

    result = document_selection.resolve_document_selection_context({
        'dataset_id': 'kb-1',
        'document_id': 'doc-1',
        'segment_id': 'missing',
        'selected_text': 'batch size tokens',
        'paragraph_text': 'The paragraph containing batch size tokens.',
    }, 'batch size tokens')

    assert result == 'The paragraph containing batch size tokens.'


def test_positioned_lookup_uses_page_and_bbox_to_disambiguate(monkeypatch):
    from lazymind.chat.engine.tools import algo

    monkeypatch.setattr(algo, 'DOCUMENT', _Document([
        {'content': 'first occurrence of batch size tokens', 'metadata': {'page': 1, 'bbox': [0, 0, 20, 20]}},
        {'content': 'wanted occurrence of batch size tokens', 'metadata': {'page': 3, 'bbox': [10, 10, 80, 40]}},
    ]))

    result = document_selection.resolve_document_selection_context({
        'dataset_id': 'kb-1', 'document_id': 'doc-1',
        'selected_text': 'batch size tokens', 'paragraph_text': 'fallback',
        'page': 4, 'bbox': [20, 15, 60, 30],
    }, 'batch size tokens')

    assert result.startswith('wanted occurrence')


def test_ambiguous_unpositioned_matches_fall_back_to_paragraph(monkeypatch):
    from lazymind.chat.engine.tools import algo

    monkeypatch.setattr(algo, 'DOCUMENT', _Document([
        {'content': 'first batch size tokens'},
        {'content': 'second batch size tokens'},
    ]))

    result = document_selection.resolve_document_selection_context({
        'dataset_id': 'kb-1', 'document_id': 'doc-1',
        'selected_text': 'batch size tokens', 'paragraph_text': 'safe fallback paragraph',
    }, 'batch size tokens')

    assert result == 'safe fallback paragraph'


def test_render_document_selection_marks_the_instruction_target():
    rendered = document_selection.render_document_selection(
        'batch size tokens',
        'The paragraph containing batch size tokens.',
    )

    assert 'Selected text (the target of the user instruction):\nbatch size tokens' in rendered
    assert 'Surrounding passage (reference context only):' in rendered
