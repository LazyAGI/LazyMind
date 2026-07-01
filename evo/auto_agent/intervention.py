from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, model_validator

from evo.artifact_runtime.utils import json_mapping_fingerprint

AutoInterventionKind = Literal['rerun_case', 'patch_judge_score']


class AutoIntervention(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    kind: AutoInterventionKind
    case_id: str
    field: str = ''
    value: Any = None
    source_ref: str = ''

    @model_validator(mode='after')
    def validate_kind_args(self) -> 'AutoIntervention':
        if not self.case_id.strip():
            raise ValueError('case_id is required')
        if self.kind == 'patch_judge_score' and not self.field.strip():
            raise ValueError('field is required for patch_judge_score')
        return self

    @property
    def fingerprint(self) -> str:
        payload = {'kind': self.kind, 'case_id': self.case_id}
        if self.kind == 'patch_judge_score':
            payload |= {'field': self.field, 'value': self.value}
        return json_mapping_fingerprint(payload, reject_reserved_envelope=False)

    def intent_frame_payload(self) -> dict[str, Any]:
        if self.kind == 'rerun_case':
            intent = {'kind': 'rerun_case', 'args': {'case_ref': self.case_id}}
        elif self.kind == 'patch_judge_score':
            intent = {
                'kind': 'patch_artifact',
                'args': {'case_ref': self.case_id, 'field': self.field, 'value': self.value},
            }
        else:
            raise ValueError(f'unsupported auto intervention: {self.kind}')
        return {
            'intent': intent,
            'source': {'text': f'auto_agent:{self.kind}:{self.case_id}'},
            'confidence': 1.0,
            'reason': 'typed auto-agent intervention',
        }
