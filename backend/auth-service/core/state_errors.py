class StateBackendError(RuntimeError):
    """Base error raised by the configured short-lived state backend."""


class StateBackendAuthenticationError(StateBackendError):
    """Raised when the configured state backend rejects credentials."""
