from __future__ import annotations

import pytest

from lazyllm.tools.agent import ToolExecutionError

from lazymind.common.memory import MemoryPartialApplyError, PreferenceItem
from lazymind.common.memory.validation.preference import render_preference_index
from lazymind.config import config as _cfg
from lazymind.review.preference_organizer import tools as organizer_tools
from lazymind.review.preference_organizer.compactor import (
    make_preference_organizer_compactor,
)
from lazymind.review.preference_organizer.prompts import (
    build_preference_organizer_prompt,
)
from lazymind.review.preference_organizer.schemas import PreferenceStateData
from lazymind.review.preference_organizer.state import (
    PreferenceStateSnapshot,
    load_preference_state,
    target_item_count,
    target_reached,
)
from lazymind.review.preference_organizer.tools import (
    ChangeBudget,
    PreferenceOrganizerGate,
)
from lazymind.review.service import preference_organizer as service


def _snapshot(
    count: int,
    *,
    projection_chars: int = 3000,
    truncated: bool = False,
    etag: str = 'etag',
):
    return PreferenceStateSnapshot(
        content='preferences: []\n',
        items=tuple(),
        data=PreferenceStateData(
            stored_items=count,
            full_projection_chars=6000 if truncated else projection_chars,
            projected_items=count - 1 if truncated else count,
            projected_chars=5000 if truncated else projection_chars,
            projection_truncated=truncated,
            etag=etag,
        ),
    )


def _empty_plan(label: str = 'No safe changes') -> str:
    return (
        f'## UNCERTAIN / KEEP\n{label}\n\n'
        '## AUTHORIZED OPERATIONS\n```json\n[]\n```'
    )


def _gate(pass_number: int = 1):
    return PreferenceOrganizerGate(
        pass_number=pass_number,
        budget=ChangeBudget(maximum=50),
        hard_min_items=15,
    )


def _snapshot_with_items(items, *, etag='etag'):
    return PreferenceStateSnapshot(
        content=render_preference_index('', list(items)),
        items=tuple(items),
        data=PreferenceStateData(
            stored_items=len(items), full_projection_chars=1000,
            projected_items=len(items), projected_chars=1000,
            projection_truncated=False, etag=etag,
        ),
    )


def _valid_reference(*, kind='memory_review', conversation_id='conversation-1'):
    return (
        '---\n'
        'name: source\n'
        'summary: Source preference\n'
        "created_at: '2026-01-01T00:00:00+00:00'\n"
        "updated_at: '2026-01-02T00:00:00+00:00'\n"
        'source:\n'
        f'  kind: {kind}\n'
        f'  conversation_id: {conversation_id}\n'
        '---\n'
        '## Application Scenarios\nWhen relevant.\n\n'
        '## Preference Details\nPreserve the behavior.\n\n'
        '## Reason\nExplicit user evidence.\n'
    )


@pytest.mark.parametrize('compression_kind', ['compacted', 'spilled', 'summary'])
def test_compactor_reinjects_exact_gate_plan_after_compression(compression_kind):
    gate = _gate()
    plan = _empty_plan('Keep pref.a and pref.b.')
    gate.submit(plan, _snapshot(50))

    def compressed_base(history, keep_full_turns=None, **kwargs):
        kwargs['runtime_state']['entries'] = [{
            'source_start': 0,
            'source_end': 1,
            'message': {'role': 'user', 'content': 'summary'},
            'kind': compression_kind,
            'model_visible': True,
        }]
        return history, []

    runtime_state = {}
    compact = make_preference_organizer_compactor(
        gate, base_compactor=compressed_base,
    )
    prior, current = compact([], runtime_state=runtime_state)

    assert prior == []
    assert len(current) == 1
    assert plan in current[0]['content']
    assert gate.plan_hash in current[0]['content']
    assert 'only allowed action set' in current[0]['content']
    assert runtime_state['preference_organizer_plan_injection_hash'] == gate.plan_hash


def test_compactor_does_not_duplicate_plan_in_the_same_projection():
    gate = _gate()
    gate.submit(_empty_plan('Keep this pass only.'), _snapshot(50))

    def compressed_base(history, keep_full_turns=None, **kwargs):
        kwargs['runtime_state']['entries'] = [{
            'source_start': 0, 'source_end': 1,
            'message': {'role': 'user', 'content': 'summary'},
            'kind': 'summary', 'model_visible': True,
        }]
        return [], list(history)

    compact = make_preference_organizer_compactor(
        gate, base_compactor=compressed_base,
    )
    _prior, first = compact([], runtime_state={})
    _prior, second = compact(first, runtime_state={})
    marker = f'<preference_organizer_plan hash="{gate.plan_hash}"'
    assert sum(marker in item['content'] for item in second) == 1


def test_second_pass_compactor_never_injects_first_pass_plan():
    first_gate = _gate(1)
    first_plan = _empty_plan('first-pass-only')
    first_gate.submit(first_plan, _snapshot(50))
    second_gate = _gate(2)
    second_plan = _empty_plan('second-pass-only')
    second_gate.submit(second_plan, _snapshot(50, etag='second'))

    def compressed_base(history, keep_full_turns=None, **kwargs):
        kwargs['runtime_state']['entries'] = [{
            'source_start': 0, 'source_end': 1,
            'message': {'role': 'user', 'content': 'summary'},
            'kind': 'summary', 'model_visible': True,
        }]
        return [], []

    compact = make_preference_organizer_compactor(
        second_gate, base_compactor=compressed_base,
    )
    _prior, current = compact([], runtime_state={})
    assert second_plan in current[0]['content']
    assert first_plan not in current[0]['content']


def test_compactor_does_not_inject_before_gate_or_without_compression():
    gate = _gate()

    def full_base(history, keep_full_turns=None, **kwargs):
        kwargs['runtime_state']['entries'] = [{
            'source_start': 0,
            'source_end': 1,
            'message': {'role': 'user', 'content': 'full'},
            'kind': 'full',
            'model_visible': True,
        }]
        return history, []

    compact = make_preference_organizer_compactor(gate, base_compactor=full_base)
    assert compact([], runtime_state={}) == ([], [])

    gate.submit(_empty_plan(), _snapshot(50))
    assert compact([], runtime_state={}) == ([], [])


def test_gate_rejects_write_tool_or_order_outside_authorized_plan():
    item = PreferenceItem(
        name='pref.old', summary='old', ref='references/old.md',
        created_at='2026-01-01T00:00:00+00:00',
        updated_at='2026-01-01T00:00:00+00:00',
    )
    snapshot = PreferenceStateSnapshot(
        content='preferences: []\n',
        items=(item,),
        data=PreferenceStateData(
            stored_items=21, full_projection_chars=1000, projected_items=21,
            projected_chars=1000, projection_truncated=False, etag='etag',
        ),
    )
    gate = _gate()
    gate.submit(
        '## DELETE\nDelete the invalid extraction.\n\n'
        '## AUTHORIZED OPERATIONS\n```json\n'
        '[{"operation_id":"delete-1","action":"delete","name":"pref.old",'
        '"reason_code":"invalid","retained_or_replacement_name":""}]\n```',
        snapshot,
    )

    with pytest.raises(ToolExecutionError, match='not merge'):
        gate.require_operation('merge', 'delete-1')
    with pytest.raises(ToolExecutionError, match='expected'):
        gate.require_operation('delete', 'delete-2')


def test_merge_writes_new_reference_and_index_before_source_cleanup(monkeypatch):
    items = [
        PreferenceItem(
            name='pref.a', summary='A', ref='references/a.md',
            created_at='2026-01-01T00:00:00+00:00',
            updated_at='2026-01-02T00:00:00+00:00',
        ),
        PreferenceItem(
            name='pref.keep', summary='Keep', ref='references/keep.md',
            created_at='2026-01-02T00:00:00+00:00',
            updated_at='2026-01-02T00:00:00+00:00',
        ),
        PreferenceItem(
            name='pref.b', summary='B', ref='references/b.md',
            created_at='2026-01-03T00:00:00+00:00',
            updated_at='2026-01-03T00:00:00+00:00',
        ),
    ]
    events = []

    class Store:
        def read_reference(self, ref):
            return _valid_reference()

        def write(self, path, content):
            events.append(('write', path, content))

        def delete_reference(self, name):
            events.append(('delete_reference', name))

    store = Store()
    monkeypatch.setattr(organizer_tools, 'MemoryStore', lambda: store)
    organizer_tools._merge_preferences(
        _snapshot_with_items(items),
        source_names=['pref.a', 'pref.b'],
        name='pref.ab',
        summary='Merged',
        scenario='When answering',
        details='Preserve both A and B.',
        reason='Same scope.',
    )

    assert events[0][0] == 'write' and events[0][1].endswith('/ab.md')
    assert events[1][0:2] == ('write', 'memory/users/preference.yaml')
    assert [event[0] for event in events[2:]] == [
        'delete_reference', 'delete_reference',
    ]
    assert events[1][2].index('pref.ab') < events[1][2].index('pref.keep')


def test_episode_create_failure_preserves_preference(monkeypatch):
    item = PreferenceItem(
        name='pref.project', summary='Project preference',
        ref='references/project.md',
        created_at='2026-01-01T00:00:00+00:00',
        updated_at='2026-01-02T00:00:00+00:00',
    )
    removed = []

    class Store:
        def read_reference(self, ref):
            return _valid_reference()

        def remove_preference_with_reference(self, name):
            removed.append(name)

    class EpisodeStore:
        def create(self, user_id, episode):
            raise RuntimeError('episode create failed')

    monkeypatch.setattr(organizer_tools, 'MemoryStore', lambda: Store())
    monkeypatch.setattr(organizer_tools, 'get_episode_store', lambda: EpisodeStore())
    monkeypatch.setattr(organizer_tools, '_agentic_value', lambda key: 'user-1')

    with pytest.raises(RuntimeError, match='episode create failed'):
        organizer_tools._move_preference_to_episode(
            _snapshot_with_items([item]), item.name, 'Project retrieval summary',
        )
    assert removed == []


def test_move_to_episode_inherits_source_and_created_time(monkeypatch):
    item = PreferenceItem(
        name='pref.project', summary='Project preference',
        ref='references/project.md',
        created_at='2026-01-01T00:00:00+00:00',
        updated_at='2026-01-02T00:00:00+00:00',
    )
    captured = {}

    class Store:
        def read_reference(self, ref):
            return _valid_reference(
                kind='chat_explicit', conversation_id='conversation-9',
            )

        def remove_preference_with_reference(self, name):
            captured['removed'] = name

    class Result:
        id = 'episode-1'

    class EpisodeStore:
        def create(self, user_id, episode):
            captured['user_id'] = user_id
            captured['episode'] = episode
            return Result()

    monkeypatch.setattr(organizer_tools, 'MemoryStore', lambda: Store())
    monkeypatch.setattr(organizer_tools, 'get_episode_store', lambda: EpisodeStore())
    monkeypatch.setattr(organizer_tools, '_agentic_value', lambda key: 'user-1')

    episode_id = organizer_tools._move_preference_to_episode(
        _snapshot_with_items([item]), item.name, 'Project retrieval summary',
    )

    assert episode_id == 'episode-1'
    assert captured['removed'] == item.name
    assert captured['episode'].occurred_at_ms == 1767225600000
    assert captured['episode'].source.kind == 'chat_explicit'
    assert captured['episode'].source.conversation_id == 'conversation-9'


def test_move_cleanup_failure_is_partial_after_episode_creation(monkeypatch):
    item = PreferenceItem(
        name='pref.project', summary='Project preference',
        ref='references/project.md',
        created_at='2026-01-01T00:00:00+00:00',
        updated_at='2026-01-02T00:00:00+00:00',
    )

    class Store:
        def read_reference(self, ref):
            return _valid_reference()

        def remove_preference_with_reference(self, name):
            raise RuntimeError('cleanup failed')

    class Result:
        id = 'episode-1'

    class EpisodeStore:
        def create(self, user_id, episode):
            return Result()

    monkeypatch.setattr(organizer_tools, 'MemoryStore', lambda: Store())
    monkeypatch.setattr(organizer_tools, 'get_episode_store', lambda: EpisodeStore())
    monkeypatch.setattr(organizer_tools, '_agentic_value', lambda key: 'user-1')

    with pytest.raises(MemoryPartialApplyError) as captured:
        organizer_tools._move_preference_to_episode(
            _snapshot_with_items([item]), item.name, 'Project retrieval summary',
        )
    assert captured.value.applied == ('episode',)
    assert captured.value.failed == ('preference_cleanup',)


def test_organizer_model_sees_full_items_and_counts_but_no_projection_stats(monkeypatch):
    item = PreferenceItem(
        name='pref.fact.verify_latest', summary='Verify current facts',
        ref='references/fact-verify-latest.md',
        created_at='2026-08-15T00:00:00+00:00',
        updated_at='2026-08-16T00:00:00+00:00',
    )
    snapshot = _snapshot_with_items([item])
    monkeypatch.setattr(organizer_tools, 'load_preference_state', lambda: snapshot)

    response = organizer_tools.PreferenceOrganizerAnalyzeTools(_gate()).read_preference_state()
    prompt = build_preference_organizer_prompt(
        snapshot,
        pass_number=1,
        preferred_min_items=20,
        hard_min_items=15,
        target_items=30,
        changes_remaining=50,
    )

    assert set(response) == {'stored_items', 'etag', 'preferences'}
    assert response['preferences'][0] == item.__dict__
    assert item.name in prompt and item.ref in prompt
    assert item.created_at in prompt and item.updated_at in prompt
    for forbidden in (
        'full_projection_chars', 'projected_chars', 'projection_truncated',
        'target_prompt_percent', '%',
    ):
        assert forbidden not in prompt
        assert forbidden not in str(response)


def test_soft_floor_allows_plan_to_reach_eighteen_but_hard_floor_remains():
    items = [
        PreferenceItem(
            name=f'pref.narrow.{index}', summary=f'Narrow {index}',
            ref=f'references/narrow-{index}.md',
            created_at='2026-01-01T00:00:00+00:00',
            updated_at='2026-01-01T00:00:00+00:00',
        )
        for index in range(19)
    ]
    gate = _gate()
    gate.submit(
        '## MOVE TO EPISODE\nMove one narrow rule.\n\n'
        '## AUTHORIZED OPERATIONS\n```json\n'
        '[{"operation_id":"move-1","action":"move_to_episode",'
        '"name":"pref.narrow.0","episode_summary":"Narrow retrieval rule"}]\n```',
        _snapshot_with_items(items),
    )
    assert len(gate.authorized_operations) == 1

    with pytest.raises(ToolExecutionError, match='hard minimum'):
        _gate().submit(
            '## MOVE TO EPISODE\nUnsafe move.\n\n'
            '## AUTHORIZED OPERATIONS\n```json\n'
            '[{"operation_id":"move-1","action":"move_to_episode",'
            '"name":"pref.narrow.0","episode_summary":"Narrow retrieval rule"}]\n```',
            _snapshot_with_items(items[:15]),
        )


def test_count_target_uses_worst_retained_items_and_clamps_to_hard_floor():
    items = [
        PreferenceItem(
            name=f'pref.item.{index}', summary='x' * length,
            ref=f'references/item-{index}.md',
            created_at='2026-01-01T00:00:00+00:00',
            updated_at='2026-01-01T00:00:00+00:00',
        )
        for index, length in enumerate([10, 20, 30, 40, 50])
    ]
    with _cfg.temp('preference_context_max_chars', 100):
        target = target_item_count(
            _snapshot_with_items(items),
            preferred_target_items=30,
            hard_min_items=3,
            target_prompt_percent=40,
        )
    assert target == 3

    many_items = [
        PreferenceItem(
            name=f'pref.small.{index}', summary='x',
            ref=f'references/small-{index}.md',
            created_at='2026-01-01T00:00:00+00:00',
            updated_at='2026-01-01T00:00:00+00:00',
        )
        for index in range(40)
    ]
    with _cfg.temp('preference_context_max_chars', 5000):
        target = target_item_count(
            _snapshot_with_items(many_items),
            preferred_target_items=100,
            hard_min_items=15,
            target_prompt_percent=40,
        )
    assert target == 30


def test_projection_target_is_strict_and_rejects_truncation():
    with _cfg.temp('preference_context_max_chars', 5000):
        assert target_reached(
            _snapshot(30, projection_chars=1999).data,
            target_prompt_percent=40,
        )
        assert not target_reached(
            _snapshot(30, projection_chars=2000).data,
            target_prompt_percent=40,
        )
        assert not target_reached(
            _snapshot(30, projection_chars=1000, truncated=True).data,
            target_prompt_percent=40,
        )


def test_organizer_rejects_invalid_complete_preference_index():
    class Store:
        def read_preference(self):
            return 'preferences:\n- summary: missing required fields\n'

    with pytest.raises(ValueError, match='requires'):
        load_preference_state(Store())


def test_organizer_runs_at_most_two_fresh_passes(monkeypatch):
    snapshots = iter([
        _snapshot(50, truncated=True, etag='initial'),
        _snapshot(50, truncated=True, etag='before-1'),
        _snapshot(50, truncated=True, etag='after-1'),
        _snapshot(50, truncated=True, etag='before-2'),
        _snapshot(30, projection_chars=1000, etag='after-2'),
    ])
    monkeypatch.setattr(service, 'load_preference_state', lambda: next(snapshots))
    gates = []

    def run_pass(*, gate, before, **kwargs):
        gates.append(gate)
        gate.submit(_empty_plan(f'pass {gate.pass_number}'), before)
        gate.budget.commit(1)
        gate.operations.append({'action': 'test-change'})
        return ''

    monkeypatch.setattr(service, '_run_organizer_pass', run_pass)
    result = service.organize_preferences(
        task_id='preference_organizer_task-1',
        user_id='user-1',
    )

    assert result.status == 'success'
    assert result.outcome == 'organized'
    assert result.result is not None
    assert result.result.passes_attempted == 2
    assert len(result.result.passes) == 2
    assert gates[0] is not gates[1]
    assert gates[0].budget is gates[1].budget
    assert gates[0].plan_hash != gates[1].plan_hash


def test_first_pass_target_stops_without_second_pass(monkeypatch):
    snapshots = iter([
        _snapshot(50, truncated=True, etag='initial'),
        _snapshot(50, truncated=True, etag='before-1'),
        _snapshot(30, projection_chars=1000, etag='after-1'),
    ])
    monkeypatch.setattr(service, 'load_preference_state', lambda: next(snapshots))
    calls = []

    def run_pass(*, gate, before, **kwargs):
        calls.append(gate.pass_number)
        gate.submit(_empty_plan('Validation is already sufficient.'), before)
        gate.budget.commit(1)
        gate.operations.append({'action': 'test-change'})
        return ''

    monkeypatch.setattr(service, '_run_organizer_pass', run_pass)
    result = service.organize_preferences(
        task_id='preference_organizer_task-one-pass',
        user_id='user-1',
    )

    assert result.outcome == 'organized'
    assert calls == [1]


def test_first_no_safe_change_pass_stops_without_second_pass(monkeypatch):
    snapshots = iter([
        _snapshot(50, truncated=True, etag='initial'),
        _snapshot(50, truncated=True, etag='before-1'),
        _snapshot(50, truncated=True, etag='after-1'),
        _snapshot(50, truncated=True, etag='before-2'),
        _snapshot(50, truncated=True, etag='after-2'),
    ])
    monkeypatch.setattr(service, 'load_preference_state', lambda: next(snapshots))
    calls = []

    def run_pass(*, gate, before, **kwargs):
        calls.append(gate.pass_number)
        gate.submit(_empty_plan(f'No safe changes in pass {gate.pass_number}.'), before)
        return ''

    monkeypatch.setattr(service, '_run_organizer_pass', run_pass)
    result = service.organize_preferences(
        task_id='preference_organizer_task-no-safe',
        user_id='user-1',
    )

    assert result.status == 'success'
    assert result.outcome == 'no_safe_changes'
    assert calls == [1]
    assert result.result is not None
    assert result.result.total_changes == 0
    assert not result.result.target_reached
    assert result.result.stop_reason == 'no_further_safe_changes'


def test_second_pass_no_safe_changes_reports_organized_with_remaining(monkeypatch):
    snapshots = iter([
        _snapshot(50, truncated=True, etag='initial'),
        _snapshot(50, truncated=True, etag='before-1'),
        _snapshot(49, truncated=True, etag='after-1'),
        _snapshot(49, truncated=True, etag='before-2'),
        _snapshot(49, truncated=True, etag='after-2'),
    ])
    monkeypatch.setattr(service, 'load_preference_state', lambda: next(snapshots))
    calls = []

    def run_pass(*, gate, before, **kwargs):
        calls.append(gate.pass_number)
        gate.submit(_empty_plan(f'pass {gate.pass_number}'), before)
        if gate.pass_number == 1:
            gate.budget.commit(1)
            gate.operations.append({'action': 'test-change'})
        return ''

    monkeypatch.setattr(service, '_run_organizer_pass', run_pass)
    result = service.organize_preferences(
        task_id='preference_organizer_task-remaining',
        user_id='user-1',
    )

    assert result.status == 'success'
    assert result.outcome == 'organized_with_remaining'
    assert calls == [1, 2]
    assert result.result is not None
    assert result.result.total_changes == 1
    assert not result.result.target_reached
    assert result.result.stop_reason == 'no_further_safe_changes'


def test_partial_first_pass_never_starts_second_pass(monkeypatch):
    snapshots = iter([
        _snapshot(50, truncated=True, etag='initial'),
        _snapshot(50, truncated=True, etag='before-1'),
        _snapshot(49, truncated=True, etag='after-1'),
    ])
    monkeypatch.setattr(service, 'load_preference_state', lambda: next(snapshots))
    calls = []

    def run_pass(*, gate, before, **kwargs):
        calls.append(gate.pass_number)
        gate.submit(_empty_plan('No additional safe write.'), before)
        gate.terminal_outcome = 'partial'
        gate.terminal_error = 'index applied; reference cleanup failed'
        return ''

    monkeypatch.setattr(service, '_run_organizer_pass', run_pass)
    result = service.organize_preferences(
        task_id='preference_organizer_task-2',
        user_id='user-1',
    )

    assert result.status == 'failed'
    assert result.outcome == 'partial'
    assert calls == [1]


@pytest.mark.parametrize(
    ('terminal_outcome', 'reported_outcome'),
    [
        ('stale_state', 'stale_state'),
        ('failed', 'failed'),
        ('budget_exhausted', 'budget_exhausted'),
    ],
)
def test_unsafe_terminal_first_pass_never_starts_second(
    monkeypatch,
    terminal_outcome,
    reported_outcome,
):
    snapshots = iter([
        _snapshot(50, truncated=True, etag='initial'),
        _snapshot(50, truncated=True, etag='before-1'),
        _snapshot(50, truncated=True, etag='after-1'),
    ])
    monkeypatch.setattr(service, 'load_preference_state', lambda: next(snapshots))
    calls = []

    def run_pass(*, gate, before, **kwargs):
        calls.append(gate.pass_number)
        gate.submit(_empty_plan('Stop safely.'), before)
        gate.terminal_outcome = terminal_outcome
        gate.terminal_error = terminal_outcome
        return ''

    monkeypatch.setattr(service, '_run_organizer_pass', run_pass)
    result = service.organize_preferences(
        task_id=f'preference_organizer_task-{terminal_outcome}',
        user_id='user-1',
    )

    assert result.outcome == reported_outcome
    assert calls == [1]
