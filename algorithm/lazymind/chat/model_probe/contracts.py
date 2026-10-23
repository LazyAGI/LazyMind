"""Internal, credential-bearing input and credential-free probe result contracts."""
from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True)
class ProbeInput:
    model: str
    model_type: str
    source: str
    url: str
    api_key: str = field(default='', repr=False)
    skip_auth: bool = False
    vision: bool = False


@dataclass
class ProbePlan:
    endpoint: str
    response_kind: str
    json: dict[str, Any] | None = None
    data: dict[str, str] | None = None
    files: dict[str, Any] | None = None
    auth: str = 'bearer'


class UnsupportedProbe(Exception):
    def __init__(self, code: str):
        super().__init__(code)
        self.code = code
