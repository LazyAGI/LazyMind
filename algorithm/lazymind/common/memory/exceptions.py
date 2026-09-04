from __future__ import annotations

from typing import Any, Iterable


class MemoryPartialApplyError(RuntimeError):
    """Raised when a multi-file memory mutation cannot be fully rolled back."""

    def __init__(
        self,
        message: str,
        operation: str,
        applied: Iterable[str],
        failed: Iterable[str],
        item: Any = None,
        metadata: dict[str, Any] | None = None,
    ) -> None:
        self.message = str(message)
        self.operation = str(operation)
        self.applied = tuple(str(step) for step in applied)
        self.failed = tuple(str(step) for step in failed)
        self.item = item
        self.metadata = dict(metadata or {})
        super().__init__(self.message, self.operation, self.applied, self.failed, item, self.metadata)

    def __str__(self) -> str:
        return self.message
