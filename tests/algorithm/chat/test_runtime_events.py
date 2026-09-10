import pytest
from lazymind.chat.runtime_events import RunAccumulator, RunOutcome


def test_successful_model_terminal_does_not_mask_downstream_runtime_failure():
    accumulator = RunAccumulator(run_id='run-1', last_model_terminal={
        'kind': 'finish',
        'finish': 'stop',
        'has_semantic_output': True,
    })

    assert accumulator.finish(outcome=RunOutcome.FAILED)['data']['code'] == 'runtime_failure'


def test_run_accumulator_user_cancel_is_an_explicit_terminal():
    accumulator = RunAccumulator(
        run_id='run-1',
        semantic_output=True,
        last_model_terminal={
            'model_call_id': 'call-1',
            'kind': 'finish',
            'finish': 'tool_calls',
            'has_semantic_output': True,
        },
    )

    event = accumulator.finish(outcome=RunOutcome.CANCELLED)

    assert event['data'] == {
        'status': 'cancelled',
        'reason': 'user_cancelled',
        'partial_output': True,
        'model_call_id': 'call-1',
    }
    with pytest.raises(RuntimeError, match='already has a terminal'):
        accumulator.finish(outcome=RunOutcome.CANCELLED)
