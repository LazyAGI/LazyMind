from typing import Literal, Optional

from pydantic import BaseModel, ConfigDict


class MemoryReviewError(BaseModel):
    model_config = ConfigDict(extra='forbid')

    code: str
    message: str


class MemoryReviewResult(BaseModel):
    model_config = ConfigDict(extra='forbid')

    status: Literal['success', 'failed']
    task_id: str
    outcome: Literal['saved', 'no_changes', 'partial', 'failed']
    retryable: bool = False
    error: Optional[MemoryReviewError] = None
