from __future__ import annotations

from typing import Any, Iterable


class PreferenceCapacityExceededError(ValueError):
    """Raised when adding a preference would exceed Chat's configured limit."""

    def __init__(
        self,
        current_items: int,
        attempted_items: int,
        max_items: int,
    ) -> None:
        self.current_items = current_items
        self.attempted_items = attempted_items
        self.max_items = max_items
        super().__init__(current_items, attempted_items, max_items)

    def __str__(self) -> str:
        return (
            'preference add rejected: capacity is full; '
            f'current_items={self.current_items} '
            f'attempted_items={self.attempted_items} '
            f'max_items={self.max_items}; the new preference was not saved '
            'and existing preferences were unchanged'
        )


class MemoryPartialApplyError(RuntimeError):
    """Raised when a multi-file memory mutation cannot be fully rolled back."""

    def __init__(
        self,
        message: str,
        operation: str,
        applied: Iterable[str],
        failed: Iterable[str],
        item: Any = None,
    ) -> None:
        self.message = str(message)
        self.operation = str(operation)
        self.applied = tuple(str(step) for step in applied)
        self.failed = tuple(str(step) for step in failed)
        self.item = item
        super().__init__(self.message, self.operation, self.applied, self.failed, item)

    def __str__(self) -> str:
        return self.message
