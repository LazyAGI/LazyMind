class GatewayError(RuntimeError):
    """Stable API error raised by channel application services."""

    def __init__(
        self,
        http_status: int,
        code: str,
        message: str,
        retryable: bool = False,
    ):
        super().__init__(http_status, code, message, retryable)
        self.http_status = http_status
        self.code = code
        self.message = message
        self.retryable = retryable


class LazyMindError(RuntimeError):
    """LazyMind Core contract or transport failure."""


class LazyMindHTTPError(LazyMindError):
    def __init__(self, status_code: int, message: str):
        super().__init__(status_code, message)
        self.status_code = status_code
        self.message = message

    @property
    def retryable(self) -> bool:
        return self.status_code in {408, 425, 429, 500, 502, 503, 504}

    def __str__(self) -> str:
        return self.message


class InvalidStaticAssetError(LazyMindError):
    """A Core static-file reference cannot be safely refreshed or read."""


class RetryableLazyMindError(LazyMindError):
    """A Core transport failed before returning an application response."""


class RuntimeLeaseLostError(RuntimeError):
    """A fenced database writer no longer owns its distributed lease."""


class RetryableProviderSideEffectError(RuntimeError):
    """A provider may have accepted an idempotent side effect without a reply."""

    def __init__(  # noqa: B042 - retry metadata is intentionally keyword-only.
        self,
        message: str,
        *,
        retry_after_seconds: float | None = None,
    ):
        super().__init__(message)
        self.retry_after_seconds = retry_after_seconds
