"""Smart Charts - 智能图表生成与数据分析"""

__version__ = '7.0.0'

from .chart_generator import ChartGenerator, ChartType
from .data_parser import DataParser
from .data_transformer import DataTransformer, CHART_INPUT_SPEC, CodeValidationError
from .exceptions import (
    SmartChartsError,
    FileError,
    DataError,
    ChartError,
    TransformError,
    ErrorCode,
)

__all__ = [
    'ChartGenerator',
    'ChartType',
    'DataParser',
    'DataTransformer',
    'CHART_INPUT_SPEC',
    'CodeValidationError',
    'SmartChartsError',
    'FileError',
    'DataError',
    'ChartError',
    'TransformError',
    'ErrorCode',
]
