from lazymind.chat.runtime_events import RunOutcome
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
from lazymind.chat.service.run_metrics import RunMetricsTracker


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
    assert translator.last_metrics['provider_usages'][0]['prompt_cache_hit_tokens'] == 80
    assert 'provider_usages' not in metrics


def test_model_ttft_and_duration_exclude_preprocessing_queue_and_tool_time():
    class Clock:
        def __init__(self) -> None:
            self.t = 0.0

        def __call__(self) -> float:
            return self.t

        def add(self, seconds: float) -> None:
            self.t += seconds

    clock = Clock()
    translator = AgentEventFrameTranslator(query='q', run_id='run-1', clock=clock)
    clock.add(5.0)
    started_frames = translator.feed({
        'tag': 'runtime_event',
        'runtime_event': {
            'schema_version': 1,
            'event_id': 'started-1',
            'type': 'model_call_started',
            'data': {'model_call_id': 'call-1'},
        },
    })
    assert started_frames == []
    clock.add(0.4)
    translator.feed({'tag': 'think', 'delta': 'working'})
    clock.add(0.6)
    translator.feed({
        'tag': 'runtime_event',
        'runtime_event': {
            'schema_version': 1,
            'event_id': 'finished-1',
            'type': 'model_call_finished',
            'data': {
                'model_call_id': 'call-1',
                'kind': 'finish',
                'finish': 'tool_calls',
                'duration_ms': 1000,
            },
        },
    })
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
    clock.add(2.0)
    metrics = translator.finish_run(outcome=RunOutcome.SUCCEEDED)['performance_metrics']
    assert metrics['tool_ms'] == 1500
    assert metrics['model_ms'] == 1000
    assert metrics['ttft_ms'] == 400
    assert metrics['wall_ms'] == 16000


def test_missing_provider_cache_omits_hit_rate():
    tracker = RunMetricsTracker(clock=lambda: 0.0)
    metrics = tracker.snapshot(usage={'prompt_tokens': 10, 'completion_tokens': 2})
    assert 'cache_hit_rate' not in metrics
    assert 'cached_tokens' not in metrics
