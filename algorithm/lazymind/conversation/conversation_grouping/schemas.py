"""Input for one frozen grouping batch or membership audit."""
from typing import Any

from pydantic import BaseModel, ConfigDict, Field


class GroupingRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')
    input: dict[str, Any] = Field(default_factory=dict)
    llm_config: dict[str, Any] = Field(default_factory=dict)
    options: dict[str, Any] = Field(default_factory=dict)
