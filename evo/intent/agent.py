from __future__ import annotations

from .models import IntentPlan, IntentRequest


class IntentAgent:
    def __init__(self, plans: dict[str, IntentPlan]):
        self.plans = plans

    def plan(self, request: IntentRequest) -> IntentPlan:
        try:
            plan = self.plans[request.message]
        except KeyError as exc:
            raise ValueError('no intent plan for message') from exc
        return IntentPlan(
            capability_id=plan.capability_id,
            operation_id=plan.operation_id,
            params=dict(plan.params),
            input_refs=list(plan.input_refs),
            depends_on=list(plan.depends_on),
            parent=plan.parent,
            source_message_id=request.message_id,
        )
