"""统一错误处理"""

from enum import Enum
from typing import Any, Dict, Optional


class ErrorCode(Enum):
    FILE_NOT_FOUND = 1001
    FILE_NOT_REGULAR = 1002
    FILE_FORMAT_INVALID = 1003
    FILE_SIZE_EXCEEDED = 1004
    DATA_PARSE_ERROR = 2001
    DATA_EMPTY = 2003
    TRANSFORM_EXEC_ERROR = 3001
    TRANSFORM_NO_RESULT = 3002
    TRANSFORM_INVALID_RESULT = 3003
    TRANSFORM_EMPTY_RESULT = 3004
    CHART_GENERATION_ERROR = 4001
    CHART_TYPE_UNSUPPORTED = 4002
    CHART_CONFIG_ERROR = 4003
    UNKNOWN_ERROR = 9999


class SmartChartsError(Exception):

    def __init__(
        self,
        message: str,
        code: ErrorCode = ErrorCode.UNKNOWN_ERROR,
        details: Optional[Dict[str, Any]] = None,
    ):
        self.message = message
        self.code = code
        self.details = details or {}
        super().__init__(f"[{code.name}] {message}")

    def to_dict(self) -> Dict[str, Any]:
        return {
            "error": self.message,
            "code": self.code.value,
            "code_name": self.code.name,
            "details": self.details,
        }


class FileError(SmartChartsError):
    pass


class DataError(SmartChartsError):
    pass


class ChartError(SmartChartsError):
    pass


class TransformError(SmartChartsError):
    pass
