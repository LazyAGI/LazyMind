"""Conversation API contracts; model selection belongs to Core."""
from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator


class TitleInput(BaseModel):
    model_config = ConfigDict(extra='forbid')
    text: str = ''
    data: dict[str, Any] = Field(default_factory=dict)
    messages: list[dict[str, Any]] = Field(default_factory=list)


class TitleRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')
    input: TitleInput = Field(default_factory=TitleInput)
    llm_config: dict[str, Any] = Field(default_factory=dict)
    options: dict[str, Any] = Field(default_factory=dict)


class TitleBatchInput(BaseModel):
    model_config = ConfigDict(extra='forbid')
    id: str = Field(min_length=1)
    input: TitleInput


class BatchTitleRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')
    items: list[TitleBatchInput] = Field(min_length=1, max_length=20)
    llm_config: dict[str, Any] = Field(default_factory=dict)
    options: dict[str, Any] = Field(default_factory=dict)

    @model_validator(mode='after')
    def unique_ids(self):
        if len({item.id for item in self.items}) != len(self.items):
            raise ValueError('duplicate conversation ID')
        return self


class ConversationResult(BaseModel):
    status: Literal['succeeded', 'failed']
    task_id: str
    output: dict[str, Any] = Field(default_factory=dict)
    text: str = ''
    files: list = Field(default_factory=list)
    tool_call_turns: int = 0
    usage: dict[str, Any] = Field(default_factory=dict)
    error: str | None = None
    error_code: str | None = None
    retryable: bool = False
