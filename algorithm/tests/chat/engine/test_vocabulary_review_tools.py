from lazymind.chat.engine.tools import vocabulary_review


def test_register_review_words_aggregates_weighted_score_once_per_word(monkeypatch):
    calls = []
    monkeypatch.setattr(
        vocabulary_review,
        'post_core_api',
        lambda path, payload: calls.append((path, payload)) or {'data': {'accepted': True}},
    )
    monkeypatch.setattr(
        vocabulary_review,
        'get_review_words',
        lambda count=5: {
            'words': [], 'remaining': 0, 'complete': True, 'report': {'accuracy': .75},
        },
    )
    result = vocabulary_review.register_review_words([
        {'word_id': 'word-1', 'correct': True, 'weight': 3},
        {'word_id': 'word-1', 'correct': False, 'weight': 1},
    ])
    assert calls == [('/vocabulary/review/sessions/active/answers:register', {'word_id': 'word-1', 'score': .75})]
    assert 'This review session is complete' in result
    assert '75.0% accuracy' in result
    assert 'word-1' not in result


def test_create_questions_get_tool_owned_weights(monkeypatch):
    written = {}
    monkeypatch.setattr(
        vocabulary_review,
        'post_core_api',
        lambda path, payload: {
            'data': {
                'session': {'id': 'session-1'},
                'items': [{'word_id': 'word-1', 'term': 'diverse'}],
            },
        },
    )
    monkeypatch.setattr(
        vocabulary_review,
        '_write_agent_data',
        lambda event, **payload: written.update({'event': event, **payload}),
    )
    vocabulary_review.ask_words('create', 'fill', questions=[
        {'word': 'diverse', 'difficulty': 'basic', 'grading_criteria': 'meaning', 'text': 'Meaning?', 'type': 'text'},
        {'word': 'diverse', 'difficulty': 'advanced', 'grading_criteria': 'usage', 'text': 'Use it.', 'type': 'text'},
    ])
    assert [item['weight'] for item in written['review_hook']['items']] == [3.0, 1.0]
    assert all(item['word_id'] == 'word-1' for item in written['review_hook']['items'])


def test_cloze_signs_only_structured_correct_answers(monkeypatch):
    written, calls = {}, []

    def fake_post(path, payload):
        calls.append((path, payload))
        return {
            'data': {
                'session': {'id': 'session-1'},
                'items': [
                    {'word_id': f'word-{i}', 'term': term}
                    for i, term in enumerate(payload['terms'])
                ],
            },
        }

    monkeypatch.setattr(vocabulary_review, 'post_core_api', fake_post)
    monkeypatch.setattr(
        vocabulary_review,
        '_write_agent_data',
        lambda event, **payload: written.update({'event': event, **payload}),
    )
    questions = [
        {
            'correct_answer': f'candidate{i}', 'difficulty': 'basic',
            'grading_criteria': 'exact', 'text': f'Blank {i}', 'type': 'text',
        }
        for i in range(10)
    ]
    vocabulary_review.ask_words('create', 'cloze', questions=questions)
    assert calls[0][1]['terms'] == [f'candidate{i}' for i in range(10)]
    assert written['review_hook']['kind'] == 'vocabulary_review_objective'
    assert all('correct_answer' not in item for item in written['review_hook']['items'])


def test_get_review_words_is_preview_without_session_id(monkeypatch):
    calls = []
    monkeypatch.setattr(
        vocabulary_review,
        'get_core_api',
        lambda path, params: calls.append((path, params)) or {
            'session': {'id': 'internal'},
            'questions': [{'word': {'id': 'w', 'term': 'diverse', 'meaning': '各异'}}],
        },
    )
    result = vocabulary_review.get_review_words(count=200)
    assert calls == [('/vocabulary/review/sessions/active/candidates', {'count': 200})]
    assert result['words'][0]['term'] == 'diverse'
    assert 'session_id' not in result
    assert 'count >= 20' in result['cloze_hint']
