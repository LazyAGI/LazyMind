from __future__ import annotations

import pytest

from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.calculator import calculator
from lazymind.chat.engine.tools.infra import safe_evaluate_expression


class TestSafeCalculator:
    def test_basic_arithmetic(self):
        result = calculator('(12 * 13) / 6')
        assert result == {
            'status': 'ok',
            'expression': '(12 * 13) / 6',
            'result': '26',
            'value': 26.0,
        }

    def test_math_functions_and_constants(self):
        result = calculator('sqrt(2) + sin(pi / 2)')
        assert result['expression'] == 'sqrt(2) + sin(pi / 2)'
        assert abs(result['value'] - (2 ** 0.5 + 1.0)) < 1e-9

    def test_rejects_code_execution(self):
        for expression in (
            '__import__(\'os\').getcwd()',
            'open(\'/etc/passwd\').read()',
            '().__class__',
            'lambda: 1',
            '[x for x in (1,)]',
        ):
            with pytest.raises(ToolExecutionError):
                calculator(expression)

    def test_rejects_empty_expression(self):
        with pytest.raises(ToolExecutionError, match='expression is required'):
            calculator('   ')


@pytest.mark.parametrize(
    ('expression', 'expected'),
    [
        ('2 + 3 * 4', 14),
        ('2 ** 10', 1024),
        ('factorial(5)', 120),
    ],
)
def test_safe_evaluate_cases(expression, expected):
    assert safe_evaluate_expression(expression) == expected
