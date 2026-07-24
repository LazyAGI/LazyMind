from __future__ import annotations

import pytest

from lazymind.chat.engine.tools import calculator as calculator_mod


class TestSafeCalculator:
    def test_basic_arithmetic(self):
        result = calculator_mod.calculator('(12 * 13) / 6')
        assert result == {
            'success': True,
            'tool': 'calculator',
            'result': {
                'status': 'ok',
                'expression': '(12 * 13) / 6',
                'result': '26',
                'value': 26.0,
            },
        }

    def test_math_functions_and_constants(self):
        result = calculator_mod.calculator('sqrt(2) + sin(pi / 2)')
        assert result['success'] is True
        assert result['tool'] == 'calculator'
        assert result['result']['expression'] == 'sqrt(2) + sin(pi / 2)'
        assert abs(result['result']['value'] - (2 ** 0.5 + 1.0)) < 1e-9

    def test_rejects_code_execution(self):
        for expression in (
            "__import__('os').getcwd()",
            "open('/etc/passwd').read()",
            "().__class__",
            "lambda: 1",
            "[x for x in (1,)]",
        ):
            result = calculator_mod.calculator(expression)
            assert result['success'] is False
            assert 'error' in result

    def test_rejects_empty_expression(self):
        result = calculator_mod.calculator('   ')
        assert result['success'] is False


@pytest.mark.parametrize(
    ('expression', 'expected'),
    [
        ('2 + 3 * 4', 14),
        ('2 ** 10', 1024),
        ('factorial(5)', 120),
    ],
)
def test_safe_evaluate_cases(expression, expected):
    assert calculator_mod.safe_evaluate_expression(expression) == expected
