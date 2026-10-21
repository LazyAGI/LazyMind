"""Product domain tools, exposed by its explicit Host capability declaration."""
import json
import uuid
from lazyllm.tools import fc_register
from lazymind.workflow_sdk import WorkflowClientError
from .product_client import ProductClient


def contribute(context, config, bind_successor=None):
    session_id = str((context or {}).get('session_id') or '')
    if not session_id:
        # A new session must first execute and publish a stage. Its next turn
        # receives a pinned context; discovery must not expose unrelated controls.
        return [], []

    def client():
        from lazymind.chat.workflow.workflow_manager import _client
        return ProductClient(_client())

    @fc_register(host_file='NONE')
    def get_product_stage_options():
        """Read this project's stage options and shared deliverables."""
        return client().get_product_stage_relay(session_id)

    @fc_register(host_file='NONE')
    def read_product_project_artifact(stage: str):
        """Read an existing product stage without executing or switching stages."""
        return client().get_product_project_artifact(session_id, stage)

    @fc_register(host_file='NONE')
    def relay_product_stage(action: str, selected_stage: str = ''):
        """After explicit current user instruction, continue, switch-stage or finish.
        After switching, read get_ready_steps and continue through the ordinary Workflow tools.
        """
        nonlocal session_id
        query = str(config.get('workflow_current_query') or config.get('query') or '').strip()
        if not query:
            raise WorkflowClientError('PRODUCT_APPROVAL_NOT_FOUND', 'A current user message is required.')
        api = client()
        commands = config.setdefault('product_stage_relay_commands', {})
        key = json.dumps([session_id, action, selected_stage, query], ensure_ascii=False)
        if key not in commands:
            summary = api.get_product_stage_relay(session_id)
            if not summary.get('can_relay'):
                raise WorkflowClientError(
                    'PRODUCT_STAGE_NOT_READY', str(summary.get('reason') or 'Current stage is not complete.'),
                )
            commands[key] = {'id': str(uuid.uuid4()), 'version': summary['state_version']}
        command = commands[key]
        result = api.relay_product_stage(
            session_id, action=action, selected_stage=selected_stage,
            expected_state_version=command['version'], command_id=command['id'],
            request_context=query, user_message=query,
        )
        if result.get('session_id') != session_id:
            if bind_successor is None:
                raise WorkflowClientError(
                    'WORKFLOW_SESSION_HANDOFF_INVALID', 'This Host cannot adopt the prepared successor.',
                )
            bind_successor(result)
            session_id = result['session_id']
            return {
                **result,
                'next_action': 'Read get_ready_steps and advance the prepared session with the ordinary Workflow tools.',
            }
        return {**result, '_agent_control': {'stop': True, 'reason': 'workflow_completed', 'final_text': '本轮产品工作流已结束。'}}

    return [get_product_stage_options, read_product_project_artifact, relay_product_stage], []
