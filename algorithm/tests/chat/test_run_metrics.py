from lazymind.chat.runtime_events import RunOutcome
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
from lazymind.chat.service.run_metrics import RunMetricsTracker, snapshot_provider_usage


def test_snapshot_provider_usage_ignores_negative_tokens():
    assert snapshot_provider_usage({'prompt_tokens': -1, 'completion_tokens': 4}) == {
        'output_tokens': 4,
    }


def test_dsh_steps_count_model_and_each_tool():
    translator = AgentEventFrameTranslator(query='q', run_id='run-1', clock=lambda: 1.0)
    translator.feed({'tag': 'think', 'delta': 'hmm'})
    translator.feed({
        'tag': 'tool_calls',
        'tool_calls': [{'id': '1', 'function': {'name': 'grep', 'arguments': '{}'}},
                       {'id': '2', 'function': {'name': 'read_file', 'arguments': '{}'}}],
    })
    translator.feed({
        'tag': 'runtime_event',
        'runtime_event': {
            'schema_version': 1,
            'event_id': 'e1',
            'type': 'model_call_finished',
            'data': {
                'model_call_id': 'c1',
                'attempt_count': 1,
                'kind': 'finish',
                'finish': 'tool_calls',
                'has_semantic_output': True,
            },
        },
    })
    translator.feed({'tag': 'tool_results', 'tool_results': [{'id': '1'}, {'id': '2'}]})
    translator.feed({'tag': 'text', 'delta': 'done'})
    translator.feed({
        'tag': 'runtime_event',
        'runtime_event': {
            'schema_version': 1,
            'event_id': 'e2',
            'type': 'model_call_finished',
            'data': {
                'model_call_id': 'c2',
                'attempt_count': 1,
                'kind': 'finish',
                'finish': 'stop',
                'has_semantic_output': True,
            },
        },
    })
    frame = translator.finish_run(
        outcome=RunOutcome.SUCCEEDED,
        usage={'prompt_tokens': 100, 'completion_tokens': 20,
               'prompt_cache_hit_tokens': 80, 'prompt_cache_miss_tokens': 20},
        llm_config={'llm': {'model': 'deepseek-chat'}},
        turn_seq=3,
        max_input_tokens=1000,
    )
    metrics = frame['performance_metrics']
    assert 'metrics' not in frame['runtime_event']['data']
    assert metrics['steps'] == 4
    assert metrics['model_steps'] == 2
    assert metrics['tool_steps'] == 2
    assert metrics['turn_seq'] == 3
    assert metrics['model'] == 'deepseek-chat'
    assert metrics['cache_hit_rate'] == 0.8
    assert metrics['context_ratio'] == 0.1
    assert metrics['input_tokens'] == 100
    assert metrics['output_tokens'] == 20
    assert metrics['cached_tokens'] == 80
    assert 'prompt_tokens' not in metrics
    assert 'prompt_cache_hit_tokens' not in metrics
    assert metrics['provider_usages'][0]['prompt_cache_hit_tokens'] == 80


def test_measured_tool_duration_is_not_attributed_to_model():
    class Clock:
        def __init__(self) -> None:
            self.t = 0.0

        def __call__(self) -> float:
            return self.t

        def add(self, seconds: float) -> None:
            self.t += seconds

    clock = Clock()
    translator = AgentEventFrameTranslator(query='q', run_id='run-1', clock=clock)
    clock.add(2.0)
    translator.feed({
        'tag': 'tool_calls',
        'tool_calls': [{'id': '1', 'function': {'name': 'grep', 'arguments': '{}'}}],
    })
    clock.add(8.0)
    translator.feed({
        'tag': 'tool_results',
        'duration_ms': 1500,
        'tool_results': [{'id': '1'}],
    })
    clock.add(1.0)
    metrics = translator.finish_run(outcome=RunOutcome.SUCCEEDED)['performance_metrics']
    assert metrics['tool_ms'] == 1500
    assert metrics['model_ms'] == 9500


def test_missing_provider_cache_omits_hit_rate():
    tracker = RunMetricsTracker(clock=lambda: 0.0)
    metrics = tracker.snapshot(usage={'prompt_tokens': 10, 'completion_tokens': 2})
    assert 'cache_hit_rate' not in metrics
    assert 'cached_tokens' not in metrics


def test_explicit_cached_zero_persists_hit_rate():
    tracker = RunMetricsTracker(clock=lambda: 0.0)
    metrics = tracker.snapshot(usage={
        'prompt_tokens': 11062,
        'completion_tokens': 61,
        'prompt_tokens_details': {'cached_tokens': 0},
    })
    assert metrics['cached_tokens'] == 0
    assert metrics['cache_hit_rate'] == 0.0
    assert metrics['input_tokens'] == 11062


def test_snapshot_sums_provider_usages_and_uses_last_call_for_context():
    tracker = RunMetricsTracker(clock=lambda: 0.0)
    metrics = tracker.snapshot(
        usage={
            'prompt_tokens': 150,
            'completion_tokens': 30,
            'provider_usages': [
                {
                    'prompt_tokens': 100,
                    'completion_tokens': 10,
                    'prompt_tokens_details': {'cached_tokens': 80},
                },
                {
                    'prompt_tokens': 50,
                    'completion_tokens': 20,
                    'prompt_tokens_details': {'cached_tokens': 0},
                },
            ],
        },
        max_input_tokens=1000,
    )
    assert metrics['input_tokens'] == 150
    assert metrics['output_tokens'] == 30
    assert metrics['cached_tokens'] == 80
    assert metrics['cache_hit_rate'] == 80 / 150
    assert metrics['context_ratio'] == 0.05
    assert len(metrics['provider_usages']) == 2


def test_model_call_event_usage_is_preferred_over_global_usage_map():
    translator = AgentEventFrameTranslator(query='q', run_id='run-1', clock=lambda: 0.0)
    translator.feed({
        'tag': 'runtime_event',
        'runtime_event': {
            'schema_version': 1,
            'event_id': 'e1',
            'type': 'model_call_finished',
            'data': {
                'model_call_id': 'call-1',
                'kind': 'finish',
                'has_semantic_output': True,
                'usage': {'prompt_tokens': 10, 'completion_tokens': 2},
            },
        },
    })
    metrics = translator.finish_run(
        outcome=RunOutcome.SUCCEEDED,
        usage={'prompt_tokens': 999, 'completion_tokens': 999},
    )['performance_metrics']
    assert metrics['input_tokens'] == 10
    assert metrics['output_tokens'] == 2
