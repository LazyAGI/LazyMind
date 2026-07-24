from __future__ import annotations

from typing import Any, Literal, Self

from pydantic import BaseModel, ConfigDict, Field, model_validator


class ServiceError(RuntimeError):
    def __init__(self, status_code: int, detail: object) -> None:
        super().__init__(status_code, detail)
        self.status_code = status_code
        self.detail = detail


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid')


class ThreadInputs(StrictModel):
    kb_id: list[str] = Field(default_factory=list)
    csv_data: list[dict[str, str]] = Field(default_factory=list)
    router_chat_url: str = Field(min_length=1)
    router_admin_url: str = Field(min_length=1)
    algorithm_id: str = Field(min_length=1)
    num_case: int = Field(gt=0)
    case_deadline_seconds: float = Field(default=300.0, gt=0)

    @model_validator(mode='after')
    def validate_sources(self) -> Self:
        self.kb_id = [item.strip() for item in self.kb_id if item.strip()]
        rows: list[dict[str, str]] = []
        for row in self.csv_data:
            if len(row) != 1:
                raise ValueError('each csv_data item must contain one kb_id and csv_path')
            kb_id, path = next(iter(row.items()))
            if not kb_id.strip() or not path.strip():
                raise ValueError('csv_data kb_id and csv_path must be non-empty')
            rows.append({kb_id.strip(): path.strip()})
        self.csv_data = rows
        if not self.kb_id and not self.csv_data:
            raise ValueError('inputs.kb_id or inputs.csv_data is required')
        return self


class ThreadCreate(StrictModel):
    mode: Literal['auto', 'interactive']
    title: str = ''
    inputs: ThreadInputs
    llm_config: dict[str, Any]

    @model_validator(mode='after')
    def validate_models(self) -> Self:
        required = ('llm', 'evo_llm', 'embed_main')
        missing = [name for name in required if not isinstance(self.llm_config.get(name), dict)]
        if missing:
            raise ValueError(f'llm_config requires model roles: {", ".join(missing)}')
        forbidden = {
            'eval_policy', 'repair_policy', 'candidate_config',
            'abtest_candidate_config',
        } & self.llm_config.keys()
        if forbidden:
            raise ValueError('llm_config cannot contain stage policy keys')
        return self


class CommandRequest(StrictModel):
    command_id: str = ''
    until_step: str = ''


class ControlRequest(StrictModel):
    command_id: str = ''


class MessageBody(StrictModel):
    message_id: str = Field(default='', max_length=160)
    text: str = Field(default='', max_length=20000)
    content: str = Field(default='', max_length=20000)

    def message_text(self) -> str:
        return self.text if self.text.strip() else self.content


class AlgorithmOwner(StrictModel):
    thread_id: str = Field(pattern=r'^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$')
    run_id: str = ''
    candidate_ref: str = ''

    @model_validator(mode='after')
    def validate_run(self) -> Self:
        if self.run_id and self.run_id != self.thread_id:
            raise ValueError('owner.run_id must match owner.thread_id')
        return self


class RegisterAlgorithmBody(StrictModel):
    algorithm_id: str = Field(min_length=1)
    name: str = ''
    code_path: str = Field(min_length=1)
    instance_count: int = Field(default=1, ge=1, le=4)
    config: dict[str, Any] = Field(default_factory=dict)
    owner: AlgorithmOwner
    wait_ready_seconds: float = Field(default=180.0, gt=0, le=900)
    cleanup_policy: Literal['thread_delete', 'manual'] = 'thread_delete'


class AlgorithmActionBody(StrictModel):
    action: Literal['healthcheck', 'restart', 'stop']
    wait_ready_seconds: float = Field(default=180.0, gt=0, le=900)


class AbStrategyBody(StrictModel):
    weights: dict[str, int] | None = None
    reason: str = ''
    owner: AlgorithmOwner | None = None


__all__ = [
    'AbStrategyBody', 'AlgorithmActionBody', 'AlgorithmOwner', 'CommandRequest',
    'ControlRequest', 'MessageBody', 'RegisterAlgorithmBody', 'StrictModel',
    'ServiceError', 'ThreadCreate', 'ThreadInputs',
]
