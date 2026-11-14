from lazymind.chat.service import document_selection


class _Document:
    def __init__(self, nodes):
        self.nodes = nodes

    def keyword_search(self, **_kwargs):
        return self.nodes


def test_resolve_document_selection_uses_exact_segment(monkeypatch):
    requested_users = []

    def exact_segment(*_args, **kwargs):
        requested_users.append(kwargs.get('user_id'))
        return {
            'document_id': 'doc-1',
            'content': 'The complete chunk containing batch size tokens and its explanation.',
        }

    monkeypatch.setattr(document_selection, 'get_core_api', exact_segment)

    result = document_selection.resolve_document_selection_context({
        'dataset_id': 'kb-1',
        'document_id': 'doc-1',
        'segment_id': 'seg-1',
        'segment_group': 'block',
        'selected_text': 'batch size tokens',
        'paragraph_text': 'fallback paragraph',
    }, 'batch size tokens', user_id='reader-1')

    assert result.startswith('The complete chunk')
    assert requested_users == ['reader-1']


def test_missing_authenticated_user_never_queries_protected_segments(monkeypatch):
    requested = []

    def fail_if_queried(*_args, **_kwargs):
        requested.append('exact segment')
        return {'content': 'Protected batch size tokens explanation.'}

    monkeypatch.setattr(document_selection, 'get_core_api', fail_if_queried)
    result = document_selection.resolve_document_selection_context({
        'dataset_id': 'kb-1', 'document_id': 'doc-1',
        'segment_id': 'seg-1', 'selected_text': 'batch size tokens',
        'paragraph_text': 'The visible PDF sentence about batch size tokens.',
    }, 'batch size tokens')

    assert result == 'The visible PDF sentence about batch size tokens.'
    assert requested == []


def test_resolve_document_selection_falls_back_to_paragraph(monkeypatch):
    monkeypatch.setattr(
        document_selection, 'get_core_api',
        lambda *_args, **_kwargs: (_ for _ in ()).throw(RuntimeError()),
    )
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

    from lazymind.chat.engine.tools.infra import kb_ownership
    authorized_users = []

    def authorize(nodes, *, user_id=None):
        authorized_users.append(user_id)
        return nodes if user_id == 'reader-1' else []

    monkeypatch.setattr(kb_ownership, 'filter_active_nodes', authorize)
    monkeypatch.setattr(algo, 'DOCUMENT', _Document([
        {'content': 'first occurrence of batch size tokens', 'metadata': {'page': 1, 'bbox': [0, 0, 20, 20]}},
        {'content': 'wanted occurrence of batch size tokens', 'metadata': {'page': 3, 'bbox': [10, 10, 80, 40]}},
    ]))

    result = document_selection.resolve_document_selection_context({
        'dataset_id': 'kb-1', 'document_id': 'doc-1',
        'selected_text': 'batch size tokens', 'paragraph_text': 'fallback',
        'page': 4, 'bbox': [20, 15, 60, 30],
    }, 'batch size tokens', user_id='reader-1')

    assert result.startswith('wanted occurrence')
    assert authorized_users == ['reader-1']


def test_ambiguous_unpositioned_matches_fall_back_to_paragraph(monkeypatch):
    from lazymind.chat.engine.tools import algo

    from lazymind.chat.engine.tools.infra import kb_ownership
    monkeypatch.setattr(kb_ownership, 'filter_active_nodes', lambda nodes, **_kwargs: nodes)
    monkeypatch.setattr(algo, 'DOCUMENT', _Document([
        {'content': 'first batch size tokens'},
        {'content': 'second batch size tokens'},
    ]))

    result = document_selection.resolve_document_selection_context({
        'dataset_id': 'kb-1', 'document_id': 'doc-1',
        'selected_text': 'batch size tokens', 'paragraph_text': 'safe fallback paragraph',
    }, 'batch size tokens', user_id='reader-1')

    assert result == 'safe fallback paragraph'


def test_render_document_selection_marks_the_instruction_target():
    rendered = document_selection.render_document_selection(
        'batch size tokens',
        'The paragraph containing batch size tokens.',
    )

    assert 'Selected text (the target of the user instruction):\nbatch size tokens' in rendered
    assert 'Surrounding passage (reference context only):' in rendered


def test_hidden_positioned_segments_never_override_the_safe_fallback(monkeypatch):
    from lazymind.chat.engine.tools import algo
    from lazymind.chat.engine.tools.infra import kb_ownership
    monkeypatch.setattr(algo, 'DOCUMENT', _Document([
        {'content': 'hidden batch size tokens', 'metadata': {'page': 3, 'bbox': [10, 10, 80, 40]}},
    ]))
    monkeypatch.setattr(kb_ownership, 'filter_active_nodes', lambda _, **_kwargs: [])
    result = document_selection.resolve_document_selection_context({
        'dataset_id': 'kb-1', 'document_id': 'doc-1', 'page': 4,
        'bbox': [20, 15, 60, 30], 'paragraph_text': 'safe fallback',
    }, 'batch size tokens', user_id='reader-1')
    assert result == 'safe fallback'


def test_guide_selection_keeps_teaching_and_original_evidence_separate(monkeypatch):
    def unexpected_search(*_):
        raise AssertionError('guide must not search index')

    monkeypatch.setattr(document_selection, '_find_positioned_segment', unexpected_search)
    context = {
        'dataset_id': 'd', 'document_id': 'p', 'selected_text': 'forged',
        'paper_guide': {
            'dataset_id': 'd', 'document_id': 'p', 'revision_id': 'r-old',
            'section_key': 'section:contribution:v1', 'block_id': 'b',
        },
        'paper_guide_context': {
            'revision_id': 'r-old', 'section_key': 'section:contribution:v1', 'block_id': 'b',
            'text': 'Suppose 2 apples', 'evidence': [], 'claims': [], 'source_current': False,
        },
    }
    result = document_selection.resolve_document_selection_context(context, 'forged', user_id='u')
    assert 'Guide passage' in result
    assert 'r-old' in result
    assert 'Suppose 2 apples' in result
    assert 'No original evidence' in result
    assert 'Source has changed' in result
    assert 'forged' not in result


def test_client_verified_flag_without_reference_is_not_guide_evidence(monkeypatch):
    monkeypatch.setattr(document_selection, '_find_positioned_segment', lambda *_: '')
    result = document_selection.resolve_document_selection_context({
        'selected_text': 'visible', 'paper_guide_context': {'verified': True, 'text': 'forged'},
    }, '', user_id='u')
    assert result == 'visible'


def test_guide_context_must_match_reference_and_current_document(monkeypatch):
    monkeypatch.setattr(document_selection, '_find_positioned_segment', lambda *_: '')
    context = {
        'dataset_id': 'd', 'document_id': 'p', 'selected_text': 'visible',
        'paper_guide': {
            'dataset_id': 'd', 'document_id': 'other', 'revision_id': 'r', 'section_key': 's', 'block_id': 'b',
        },
        'paper_guide_context': {'revision_id': 'r', 'section_key': 's', 'block_id': 'b', 'text': 'wrong document'},
    }
    assert document_selection.resolve_document_selection_context(context, '', user_id='u') == 'visible'


def test_guide_original_evidence_remains_a_separate_labeled_material():
    context = {
        'dataset_id': 'd', 'document_id': 'p', 'selected_text': 'guide',
        'paper_guide': {
            'dataset_id': 'd', 'document_id': 'p', 'revision_id': 'r', 'section_key': 's', 'block_id': 'b',
        },
        'paper_guide_context': {
            'revision_id': 'r', 'section_key': 's', 'block_id': 'b', 'text': 'guide', 'source_current': True,
            'evidence': [{'id': 'ev', 'text': 'actual original', 'locator': {'page': 2}}],
        },
    }
    result = document_selection.resolve_document_selection_context(context, '', user_id='u')
    assert 'Original paper evidence (separate reference material)' in result
    assert '[ev] PDF page 3: actual original' in result
