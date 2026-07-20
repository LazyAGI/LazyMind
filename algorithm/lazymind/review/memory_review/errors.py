from __future__ import annotations


class MemoryPathError(ValueError):
    """Raised when a memory path is invalid or outside the allowed tree."""


class MemoryValidationError(ValueError):
    """Raised when memory content fails schema validation."""


class MemoryNotFoundError(LookupError):
    """Raised when a requested memory file does not exist."""


class MemoryStoreError(RuntimeError):
    """Raised when RemoteFS or storage operations fail."""
