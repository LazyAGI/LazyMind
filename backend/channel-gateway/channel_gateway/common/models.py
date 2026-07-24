from typing import Any

from pydantic import BaseModel, Field


class ConnectionSessionCreate(BaseModel):
    provider: str = Field(min_length=1, max_length=32)


class ConnectionChallengeSubmit(BaseModel):
    type: str = Field(default='numeric_code', max_length=32)
    value: str = Field(min_length=1, max_length=12)


class QRCodeView(BaseModel):
    payload: str
    version: int
    expires_at: str


class ChallengeView(BaseModel):
    type: str
    prompt: str
    input_mode: str


class AccountView(BaseModel):
    id: str
    provider: str
    label: str
    status: str
    runtime_status: str
    connected_at: str | None
    last_poll_at: str | None
    last_message_at: str | None
    last_error: str | None
    updated_at: str


class SessionErrorView(BaseModel):
    code: str
    message: str
    retryable: bool


class ConnectionSessionView(BaseModel):
    id: str
    provider: str
    mode: str
    status: str
    revision: int
    message: str
    qr: QRCodeView | None
    challenge: ChallengeView | None
    poll_after_ms: int
    allowed_actions: list[str]
    account: AccountView | None
    error: SessionErrorView | None


class AccountListView(BaseModel):
    items: list[AccountView]


class ErrorEnvelope(BaseModel):
    error: dict[str, Any]
