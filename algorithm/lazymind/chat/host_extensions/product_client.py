from typing import Any, Dict
from urllib.parse import quote
from lazymind.workflow_sdk import WorkflowClient


class ProductClient:
    def __init__(self, client: WorkflowClient):
        self.client = client

    def get_product_stage_relay(self, session_id: str) -> Dict[str, Any]:
        return self.client._read(
            f'/workflow-sessions/{quote(session_id, safe="")}/product-stage-relay',
        ).result

    def get_product_project_artifact(self, session_id: str, stage: str) -> Dict[str, Any]:
        """Read a stage's selected shared-project revision, without starting a run."""
        return self.client._read(
            f'/workflow-sessions/{quote(session_id, safe="")}/product-artifacts/{quote(stage, safe="")}',
        ).result

    def relay_product_stage(self, session_id: str, *, action: str, selected_stage: str,
                            expected_state_version: int, command_id: str,
                            request_context: str = '', user_message: str = '') -> Dict[str, Any]:
        """Record a sourced user choice and prepare one new stage without executing it."""
        payload = {
            'action': action, 'selected_stage': selected_stage,
            'expected_state_version': expected_state_version, 'idempotency_key': command_id,
            'request_context': request_context, 'user_message': user_message,
        }
        return self.client._decode(self.client.transport.post(
            self.client.base_url + f'/workflow-sessions/{quote(session_id, safe="")}/product-stage-relay',
            json=payload, headers=self.client._headers(command_id), timeout=self.client.timeout,
        )).result
