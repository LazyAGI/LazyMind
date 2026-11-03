from unittest.mock import MagicMock, patch

from lazymind.chat.workflow.driver_agent import _clean_message, evaluate_step


def test_driver_message_removes_reasoning():
    value = _clean_message('<think>private</think>Retry prompt because required output is missing.')
    assert value == 'Retry prompt because required output is missing.'


def test_driver_message_has_hard_length_cap():
    assert len(_clean_message('x' * 500)) <= 303


def test_driver_evaluates_pinned_policy_acceptance_and_artifacts():
    model = MagicMock()
    model.share.return_value.return_value = 'Retry draft because report is missing.'
    with patch('lazymind.chat.workflow.driver_agent.AutoModel', return_value=model):
        result = evaluate_step(
            'artifact-pipeline', 'draft', 'Terminal status: failed',
            acceptance='Must save report', driver_prompt='Inspect every required output.',
            workflow_artifacts_summary='metadata exists; report missing', llm_config={},
        )
    assert result['message'].startswith('Retry draft')
    model.share.assert_called_once_with(stream=False)
    prompt_text = model.share.return_value.call_args.args[0]
    assert 'Must save report' in prompt_text
    assert 'report missing' in prompt_text
