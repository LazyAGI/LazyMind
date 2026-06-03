"""Unit tests for vocabulary evolution pipeline and service."""
from __future__ import annotations

from lazymind.review.vocab import (  # noqa: E402
    ActionPlanningModule,
    ChatHistoryRecord,
    HistoryChunker,
    SynonymCandidate,
    SynonymExtractionModule,
    VocabEvolutionRequest,
)
from lazymind.review.service import (  # noqa: E402
    VocabEvolutionService,
    resolve_word_group_apply_url,
    run_vocab_evolution,
)


class FakeLLM:
    def __init__(self, responses):
        self._responses = list(responses)
        self.calls = []

    def share(self, **kwargs):
        return self

    def __call__(self, payload, **kwargs):
        self.calls.append(payload)
        if not self._responses:
            raise AssertionError('FakeLLM received more calls than expected')
        return self._responses.pop(0)


def _history(message_id: str, text: str, result: str = '好的', *, user_id: str = 'u1') -> ChatHistoryRecord:
    return ChatHistoryRecord(
        user_id=user_id,
        conversation_id='c1',
        message_id=message_id,
        seq=1,
        raw_content='',
        content=text,
        result=result,
        create_time=None,
    )


def test_history_chunker_splits_long_user_messages_without_overlap():
    chunker = HistoryChunker()
    payload = {
        'request': VocabEvolutionRequest(max_chunk_chars=20),
        'user_id': 'u1',
        'histories': [
            _history('m1', '甲。乙。丙。丁。'),
            _history('m2', '戊。'),
        ],
    }
    result = chunker.forward(payload)
    assert [chunk['message_ids'] for chunk in result['chunks']] == [
        ['m1'],
        ['m1'],
        ['m1'],
        ['m1'],
        ['m2'],
    ]
    assert result['chunks'][0]['text'] == '[message_id=m1] 甲。'
    assert result['chunks'][-1]['text'] == '[message_id=m2] 戊。'


def test_synonym_extraction_module_validates_and_dedupes_candidates():
    llm = FakeLLM([[
        {
            'word': '苹果',
            'synonym': 'apple',
            'description': '水果语境',
            'reason': '用户明确说苹果就是 apple',
            'message_ids': ['m1'],
        },
        {
            'word': 'apple',
            'synonym': '苹果',
            'description': '',
            'reason': '',
            'message_ids': ['m2'],
        },
        {
            'word': '苹果',
            'synonym': '苹果',
            'description': '无效',
            'reason': '无效',
            'message_ids': ['m1'],
        },
        {
            'word': '香蕉',
            'synonym': 'banana',
            'description': '无效消息ID',
            'reason': '无效',
            'message_ids': ['missing'],
        },
    ]])
    module = SynonymExtractionModule(llm=llm)
    payload = {
        'request': VocabEvolutionRequest(max_pairs_per_chunk=4),
        'user_id': 'u1',
        'histories': [
            _history('m1', '请记住 苹果 就是 apple'),
            _history('m2', '以后 apple 指的就是 苹果'),
        ],
        'chunks': [{
            'chunk_id': 'c1',
            'message_ids': ['m1', 'm2'],
            'text': '[message_id=m1] 请记住 苹果 就是 apple\n[message_id=m2] 以后 apple 指的就是 苹果',
        }],
    }
    result = module.forward(payload)
    assert len(result['candidates']) == 1
    candidate = result['candidates'][0]
    assert candidate.word == '苹果'
    assert candidate.synonym == 'apple'
    assert candidate.message_ids == ['m1', 'm2']
    assert llm.calls == [{
        'max_pairs': '4',
        'history_segments': '[message_id=m1] 请记住 苹果 就是 apple\n[message_id=m2] 以后 apple 指的就是 苹果',
    }]


def test_action_planner_creates_new_group_when_vocab_is_empty():
    planner = ActionPlanningModule(
        llm=FakeLLM([]),
        fetch_vocab_groups_fn=lambda user_id, **kwargs: {},
    )
    payload = {
        'request': VocabEvolutionRequest(),
        'user_id': 'u1',
        'histories': [_history('m1', '记住 苹果 就是 apple')],
        'candidates': [
            SynonymCandidate(
                user_id='u1',
                word='苹果',
                synonym='apple',
                description='水果语境',
                reason='用户明确要求记住苹果就是 apple',
                message_ids=['m1'],
            )
        ],
    }
    result = planner.forward(payload)
    assert result['actions'] == [{
        'reason': '用户明确要求记住苹果就是 apple',
        'words': ['苹果', 'apple'],
        'description': '水果语境',
        'group_ids': [],
        'user_id': 'u1',
        'message_ids': ['m1'],
        'action': 'create_new_group',
    }]


def test_action_planner_creates_new_group_when_single_anchor_group_has_different_description():
    planner = ActionPlanningModule(
        llm=FakeLLM([]),
        fetch_vocab_groups_fn=lambda user_id, **kwargs: {
            'g-med': {
                'group_id': 'g-med',
                'description': '医学领域术语',
                'words': ['变白质'],
                'references': ['["m-old"]'],
            },
        },
    )
    payload = {
        'request': VocabEvolutionRequest(),
        'user_id': 'u1',
        'histories': [_history('m1', '请记住变白质在体育领域就是铅球垫子。')],
        'candidates': [
            SynonymCandidate(
                user_id='u1',
                word='变白质',
                synonym='铅球垫子',
                description='体育领域术语',
                reason='用户明确指定体育领域映射',
                message_ids=['m1'],
            )
        ],
    }
    result = planner.forward(payload)
    assert result['actions'] == [{
        'reason': '用户明确指定体育领域映射',
        'words': ['变白质', '铅球垫子'],
        'description': '体育领域术语',
        'group_ids': [],
        'user_id': 'u1',
        'message_ids': ['m1'],
        'action': 'create_new_group',
    }]


def test_action_planner_splits_add_and_conflict_for_multi_group_anchor():
    groups = {
        'g1': {'group_id': 'g1', 'description': '词族1', 'words': ['B', 'C', 'D'], 'references': []},
        'g2': {'group_id': 'g2', 'description': '词族2', 'words': ['B', 'U', 'H'], 'references': []},
        'g3': {'group_id': 'g3', 'description': '词族3', 'words': ['B', 'L', 'J'], 'references': []},
    }
    llm = FakeLLM([{
        'reason': 'K 与 B 在部分场景可归入 g1，但在 g2/g3 中存在歧义。',
        'group_ids_can_join': ['g1'],
        'conflict_group_ids': ['g2', 'g3'],
    }])
    planner = ActionPlanningModule(
        llm=llm,
        fetch_vocab_groups_fn=lambda user_id, **kwargs: groups,
    )
    payload = {
        'request': VocabEvolutionRequest(conflict_retries=1),
        'user_id': 'u1',
        'histories': [_history('m1', '记住 K 就是 B')],
        'candidates': [
            SynonymCandidate(
                user_id='u1',
                word='K',
                synonym='B',
                description='铁路工程语境',
                reason='用户明确说明 K 指的是 B',
                message_ids=['m1'],
            )
        ],
    }
    result = planner.forward(payload)
    assert result['actions'] == [
        {
            'reason': 'K 与 B 在部分场景可归入 g1，但在 g2/g3 中存在歧义。',
            'words': ['K'],
            'description': '',
            'group_ids': ['g1'],
            'user_id': 'u1',
            'message_ids': ['m1'],
            'action': 'add_to_group',
        },
        {
            'reason': 'K 与 B 在部分场景可归入 g1，但在 g2/g3 中存在歧义。',
            'words': ['K'],
            'description': '',
            'group_ids': ['g2', 'g3'],
            'user_id': 'u1',
            'message_ids': ['m1'],
            'action': 'conflict',
        },
    ]
    assert llm.calls == [{
        'candidate_word': 'K',
        'anchor_word': 'B',
        'description': '铁路工程语境',
        'evidence': '- [message_id=m1] 记住 K 就是 B',
        'existing_groups': (
            '[group_id=g1] description=词族1; words=B, C, D\n'
            '[group_id=g2] description=词族2; words=B, U, H\n'
            '[group_id=g3] description=词族3; words=B, L, J'
        ),
    }]


def test_action_planner_excludes_ruled_out_groups_without_conflict():
    groups = {
        'g1': {'group_id': 'g1', 'description': '铁路工程术语', 'words': ['B', '轨道路基', '基础梁'], 'references': []},
        'g2': {'group_id': 'g2', 'description': '财务报表术语', 'words': ['B', '预算余额', '报表基数'], 'references': []},
        'g3': {'group_id': 'g3', 'description': '化学实验代号', 'words': ['B', '碱液B', '试剂B'], 'references': []},
    }
    planner = ActionPlanningModule(
        llm=FakeLLM([{
            'reason': 'K 明确属于铁路工程语境，且已排除财务和化学语境。',
            'group_ids_can_join': ['g1'],
            'excluded_group_ids': ['g2', 'g3'],
            'conflict_group_ids': [],
        }]),
        fetch_vocab_groups_fn=lambda user_id, **kwargs: groups,
    )
    payload = {
        'request': VocabEvolutionRequest(conflict_retries=1),
        'user_id': 'u1',
        'histories': [
            _history('m1', '请记住，在铁路工程讨论里，K 就是 B。'),
            _history('m2', '这里的 B 是工程语境，不是财务术语，也不是化学试剂。'),
        ],
        'candidates': [
            SynonymCandidate(
                user_id='u1',
                word='K',
                synonym='B',
                description='铁路工程语境',
                reason='用户明确说明 K 是工程语境里的 B。',
                message_ids=['m1', 'm2'],
            )
        ],
    }
    result = planner.forward(payload)
    assert result['actions'] == [
        {
            'reason': 'K 明确属于铁路工程语境，且已排除财务和化学语境。',
            'words': ['K'],
            'description': '',
            'group_ids': ['g1'],
            'user_id': 'u1',
            'message_ids': ['m1', 'm2'],
            'action': 'add_to_group',
        }
    ]
    assert result['skipped_reasons'] == []


def test_action_planner_keeps_single_unresolved_group_as_conflict():
    groups = {
        'g-chem': {'group_id': 'g-chem', 'description': '化工领域术语', 'words': ['大轻轻', '反应釜'], 'references': []},
        'g-name': {'group_id': 'g-name', 'description': '用户指定昵称映射', 'words': ['大轻轻', '小轻轻'], 'references': []},
    }
    planner = ActionPlanningModule(
        llm=FakeLLM([{
            'reason': '已排除化工组，但昵称组仍需用户确认。',
            'group_ids_can_join': [],
            'excluded_group_ids': ['g-chem'],
            'conflict_group_ids': ['g-name'],
        }]),
        fetch_vocab_groups_fn=lambda user_id, **kwargs: groups,
    )
    payload = {
        'request': VocabEvolutionRequest(conflict_retries=1),
        'user_id': 'u1',
        'histories': [_history('m1', '请记住：大轻轻就是达青青')],
        'candidates': [
            SynonymCandidate(
                user_id='u1',
                word='大轻轻',
                synonym='达青青',
                description='人名指代',
                reason='用户明确指示二者为同一人',
                message_ids=['m1'],
            )
        ],
    }

    result = planner.forward(payload)

    assert result['actions'] == [
        {
            'reason': '已排除化工组，但昵称组仍需用户确认。',
            'words': ['达青青'],
            'description': '',
            'group_ids': ['g-name'],
            'user_id': 'u1',
            'message_ids': ['m1'],
            'action': 'conflict',
        }
    ]
    assert result['skipped_reasons'] == []


def test_vocab_evolution_request_accepts_user_id():
    request = VocabEvolutionRequest.from_value({'user_id': 'u1'})
    assert request.user_id == 'u1'


def test_vocab_evolution_service_returns_flat_actions():
    histories = {
        'u1': [{
            'user_id': 'u1', 'conversation_id': 'c1', 'message_id': 'm1', 'seq': 1,
            'raw_content': '', 'content': '记住 苹果 就是 apple', 'result': '好的',
            'create_time': None,
        }],
        'u2': [{
            'user_id': 'u2', 'conversation_id': 'c2', 'message_id': 'm2', 'seq': 1,
            'raw_content': '', 'content': '记住 民法 就是 民事法律', 'result': '好的',
            'create_time': None,
        }],
    }
    extraction_llm = FakeLLM([
        [{'word': '苹果', 'synonym': 'apple', 'description': '水果语境', 'reason': '明确同义', 'message_ids': ['m1']}],
        [{'word': '民法', 'synonym': '民事法律', 'description': '法律术语', 'reason': '明确同义', 'message_ids': ['m2']}],
    ])
    service = VocabEvolutionService(
        fetch_users_fn=lambda **kwargs: ['u1', 'u2'],
        fetch_histories_fn=lambda user_id, **kwargs: histories[user_id],
        fetch_vocab_groups_fn=lambda user_id, **kwargs: {},
        extraction_llm=extraction_llm,
        conflict_llm=FakeLLM([]),
    )
    actions = service.run({'lookback_days': 7})

    assert len(actions) == 2
    assert {item['user_id'] for item in actions} == {'u1', 'u2'}
    assert next(item for item in actions if item['user_id'] == 'u1')['action'] == 'create_new_group'
    assert next(item for item in actions if item['user_id'] == 'u2')['words'] == ['民法', '民事法律']
    assert next(item for item in actions if item['user_id'] == 'u1')['group_ids'] == '[]'
    assert next(item for item in actions if item['user_id'] == 'u1')['message_ids'] == '["m1"]'


def test_run_vocab_evolution_and_apply_posts_nested_action_list():
    class DummyService:
        def run(self, request):
            assert request == {'user_id': 'u1'}
            return [{
                'reason': '用户明确要求记住苹果就是 apple',
                'words': ['苹果', 'apple'],
                'description': '水果语境',
                'group_ids': '[]',
                'user_id': 'u1',
                'message_ids': '["m1"]',
                'action': 'create_new_group',
            }]

    posted = {}

    class DummyResponse:
        def raise_for_status(self):
            return None

    def fake_post(url, *, json, timeout):
        posted['url'] = url
        posted['json'] = json
        posted['timeout'] = timeout
        return DummyResponse()

    actions = run_vocab_evolution(
        {'user_id': 'u1'},
        service=DummyService(),
        apply_url='http://backend.local/api/core/inner/word_group:apply',
        post_fn=fake_post,
    )

    assert actions == posted['json']['action_list']
    assert posted == {
        'url': 'http://backend.local/api/core/inner/word_group:apply',
        'json': {
            'action_list': [{
                'reason': '用户明确要求记住苹果就是 apple',
                'words': ['苹果', 'apple'],
                'description': '水果语境',
                'group_ids': '[]',
                'user_id': 'u1',
                'message_ids': '["m1"]',
                'action': 'create_new_group',
            }],
        },
        'timeout': 10.0,
    }


def test_vocab_evolution_service_continues_when_one_user_fails():
    service = VocabEvolutionService(
        fetch_users_fn=lambda **kwargs: ['u1', 'u2'],
        fetch_histories_fn=lambda user_id, **kwargs: [],
        fetch_vocab_groups_fn=lambda user_id, **kwargs: {},
        extraction_llm=FakeLLM([]),
        conflict_llm=FakeLLM([]),
    )

    def fake_pipeline(payload):
        if payload['user_id'] == 'u1':
            raise RuntimeError('boom')
        return {
            'actions': [{
                'reason': '明确同义',
                'words': ['民法', '民事法律'],
                'description': '法律术语',
                'group_ids': [],
                'user_id': 'u2',
                'message_ids': ['m2'],
                'action': 'create_new_group',
            }],
            'skipped_reasons': [],
        }

    service._pipeline = fake_pipeline

    assert service.run({'lookback_days': 7}) == [{
        'reason': '明确同义',
        'words': ['民法', '民事法律'],
        'description': '法律术语',
        'group_ids': '[]',
        'user_id': 'u2',
        'message_ids': '["m2"]',
        'action': 'create_new_group',
    }]


# ---------------------------------------------------------------------------
# Tests for _merge_related_actions
# ---------------------------------------------------------------------------

def test_merge_related_actions_merges_overlapping_create_new_group():
    planner = ActionPlanningModule(
        llm=FakeLLM([]),
        fetch_vocab_groups_fn=lambda user_id, **kwargs: {},
    )
    payload = {
        'request': VocabEvolutionRequest(),
        'user_id': 'u1',
        'histories': [
            _history('m1', '记住 苹果 就是 apple'),
            _history('m2', '记住 苹果 就是 红富士'),
            _history('m3', '记住 香蕉 就是 banana'),
        ],
        'candidates': [
            SynonymCandidate(
                user_id='u1', word='苹果', synonym='apple',
                description='水果语境', reason='用户明确要求',
                message_ids=['m1'],
            ),
            SynonymCandidate(
                user_id='u1', word='苹果', synonym='红富士',
                description='水果语境', reason='用户明确要求',
                message_ids=['m2'],
            ),
            SynonymCandidate(
                user_id='u1', word='香蕉', synonym='banana',
                description='水果语境', reason='用户明确要求',
                message_ids=['m3'],
            ),
        ],
    }
    result = planner.forward(payload)
    # 苹果/apple and 苹果/红富士 should merge → [苹果, apple, 红富士]
    # 香蕉/banana is unrelated → separate create_new_group
    assert len(result['actions']) == 2
    create_actions = [a for a in result['actions'] if a['action'] == 'create_new_group']
    assert len(create_actions) == 2

    # Find the merged one (should have 3 words)
    merged = next(a for a in create_actions if len(a['words']) == 3)
    assert set(merged['words']) == {'苹果', 'apple', '红富士'}
    assert set(merged['message_ids']) == {'m1', 'm2'}

    # The other one should be the standalone 香蕉/banana
    standalone = next(a for a in create_actions if len(a['words']) == 2)
    assert set(standalone['words']) == {'香蕉', 'banana'}
    assert standalone['message_ids'] == ['m3']


def test_merge_related_actions_merges_add_to_group_same_target():
    groups = {
        'g-fruit': {
            'group_id': 'g-fruit', 'description': '水果',
            'words': ['苹果'], 'references': [],
        },
    }
    planner = ActionPlanningModule(
        llm=FakeLLM([]),
        fetch_vocab_groups_fn=lambda user_id, **kwargs: groups,
    )
    payload = {
        'request': VocabEvolutionRequest(),
        'user_id': 'u1',
        'histories': [
            _history('m1', '记住 apple 就是 苹果'),
            _history('m2', '记住 红富士 就是 苹果'),
        ],
        'candidates': [
            SynonymCandidate(
                user_id='u1', word='apple', synonym='苹果',
                description='水果', reason='用户明确要求',
                message_ids=['m1'],
            ),
            SynonymCandidate(
                user_id='u1', word='红富士', synonym='苹果',
                description='水果', reason='用户明确要求',
                message_ids=['m2'],
            ),
        ],
    }
    result = planner.forward(payload)
    assert result['actions'] == [
        {
            'reason': '用户明确要求',
            'words': ['apple', '红富士'],
            'description': '',
            'group_ids': ['g-fruit'],
            'user_id': 'u1',
            'message_ids': ['m1', 'm2'],
            'action': 'add_to_group',
        },
    ]


def test_merge_related_actions_handles_chained_overlap():
    """A-B, B-C, C-D → one merged group [A,B,C,D]."""
    planner = ActionPlanningModule(
        llm=FakeLLM([]),
        fetch_vocab_groups_fn=lambda user_id, **kwargs: {},
    )
    payload = {
        'request': VocabEvolutionRequest(),
        'user_id': 'u1',
        'histories': [
            _history('m1', '记住 A 就是 B'),
            _history('m2', '记住 B 就是 C'),
            _history('m3', '记住 C 就是 D'),
        ],
        'candidates': [
            SynonymCandidate(
                user_id='u1', word='A', synonym='B',
                description='测试', reason='明确同义',
                message_ids=['m1'],
            ),
            SynonymCandidate(
                user_id='u1', word='B', synonym='C',
                description='测试', reason='明确同义',
                message_ids=['m2'],
            ),
            SynonymCandidate(
                user_id='u1', word='C', synonym='D',
                description='测试', reason='明确同义',
                message_ids=['m3'],
            ),
        ],
    }
    result = planner.forward(payload)
    assert len(result['actions']) == 1
    action = result['actions'][0]
    assert action['action'] == 'create_new_group'
    assert set(action['words']) == {'A', 'B', 'C', 'D'}
    assert set(action['message_ids']) == {'m1', 'm2', 'm3'}


def test_merge_related_actions_does_not_merge_different_target_groups():
    groups = {
        'g-fruit': {
            'group_id': 'g-fruit', 'description': '水果',
            'words': ['苹果'], 'references': [],
        },
        'g-tech': {
            'group_id': 'g-tech', 'description': '科技',
            'words': ['苹果'], 'references': [],
        },
    }
    llm = FakeLLM([
        {
            'reason': 'apple 明确属于水果组',
            'group_ids_can_join': ['g-fruit'],
            'excluded_group_ids': ['g-tech'],
            'conflict_group_ids': [],
        },
        {
            'reason': 'apple_inc 明确属于科技组',
            'group_ids_can_join': ['g-tech'],
            'excluded_group_ids': ['g-fruit'],
            'conflict_group_ids': [],
        },
    ])
    planner = ActionPlanningModule(
        llm=llm,
        fetch_vocab_groups_fn=lambda user_id, **kwargs: groups,
    )
    payload = {
        'request': VocabEvolutionRequest(conflict_retries=1),
        'user_id': 'u1',
        'histories': [
            _history('m1', '记住 apple 是水果语境下的 苹果'),
            _history('m2', '记住 apple_inc 是科技公司的 苹果'),
        ],
        'candidates': [
            SynonymCandidate(
                user_id='u1', word='apple', synonym='苹果',
                description='水果语境', reason='明确同义',
                message_ids=['m1'],
            ),
            SynonymCandidate(
                user_id='u1', word='apple_inc', synonym='苹果',
                description='科技语境', reason='明确同义',
                message_ids=['m2'],
            ),
        ],
    }
    result = planner.forward(payload)
    # Two add_to_group actions to different groups should NOT merge
    assert len(result['actions']) == 2
    groups_targeted = sorted(a['group_ids'][0] for a in result['actions'])
    assert groups_targeted == ['g-fruit', 'g-tech']


def test_resolve_word_group_apply_url_prefers_exact_apply_url_env(monkeypatch):
    monkeypatch.setenv('LAZYMIND_WORD_GROUP_APPLY_URL', 'http://backend.local/api/core/inner/word_group:apply')
    monkeypatch.setenv('LAZYMIND_CORE_SERVICE_URL', 'http://core:8000')

    assert resolve_word_group_apply_url() == 'http://backend.local/api/core/inner/word_group:apply'


def test_resolve_word_group_apply_url_supports_direct_core_service_base(monkeypatch):
    monkeypatch.delenv('LAZYMIND_WORD_GROUP_APPLY_URL', raising=False)
    monkeypatch.setenv('LAZYMIND_CORE_SERVICE_URL', 'http://core:8000')

    assert resolve_word_group_apply_url() == 'http://core:8000/inner/word_group:apply'


def test_resolve_word_group_apply_url_supports_public_core_base(monkeypatch):
    monkeypatch.delenv('LAZYMIND_WORD_GROUP_APPLY_URL', raising=False)
    monkeypatch.setenv('LAZYMIND_CORE_SERVICE_URL', 'http://gateway.local/api/core')

    assert resolve_word_group_apply_url() == 'http://gateway.local/api/core/inner/word_group:apply'
