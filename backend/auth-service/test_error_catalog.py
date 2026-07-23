import ast
import json
import unittest
from pathlib import Path

from core.errors import ErrorCodes, _EXCEPTION_PREFIXES, app_exception_from_exception


SERVICE_ROOT = Path(__file__).resolve().parent
REPO_ROOT = SERVICE_ROOT.parents[1]


class ErrorCatalogTest(unittest.TestCase):
    def test_error_codes_have_one_message_and_translations(self):
        translations = json.loads((REPO_ROOT / 'i18n/errors/auth-service.json').read_text(encoding='utf-8'))
        messages_by_code: dict[int, str] = {}
        for name, value in vars(ErrorCodes).items():
            if name.startswith('_') or not isinstance(value, tuple):
                continue
            _, code, message = value
            self.assertNotIn(code, messages_by_code, f'duplicate auth error code {code}')
            messages_by_code[code] = message
            localized = translations.get(str(code))
            self.assertIsNotNone(localized, f'{name} ({code}) is missing i18n')
            self.assertEqual(message, localized.get('en-US'), f'{name} ({code}) English message mismatch')
            self.assertTrue((localized.get('zh-CN') or '').strip(), f'{name} ({code}) is missing zh-CN')

    def test_raw_exception_prefixes_are_catalogued(self):
        registered = {prefix.lower() for prefix, _ in _EXCEPTION_PREFIXES}
        constructors = {'RuntimeError', 'ValueError', 'CloudProviderError'}
        for path in SERVICE_ROOT.rglob('*.py'):
            if path.name.startswith('test_') or 'alembic' in path.parts:
                continue
            tree = ast.parse(path.read_text(encoding='utf-8'), filename=str(path))
            for node in ast.walk(tree):
                if not isinstance(node, ast.Raise) or not isinstance(node.exc, ast.Call):
                    continue
                function = node.exc.func
                if not isinstance(function, ast.Name) or function.id not in constructors or not node.exc.args:
                    continue
                prefix = _leading_text(node.exc.args[0]).strip().rstrip(':').strip()
                self.assertTrue(prefix, f'{path}:{node.lineno} has no stable exception prefix')
                self.assertTrue(
                    any(prefix.lower().startswith(item) or item.startswith(prefix.lower()) for item in registered),
                    f'{path}:{node.lineno} exception prefix {prefix!r} is not catalogued',
                )
                resolved = app_exception_from_exception(RuntimeError(prefix + ': detail'))
                self.assertNotEqual(ErrorCodes.INTERNAL_ERROR[1], resolved.code)

    def test_stable_cloud_error_causes_do_not_use_generic_codes(self):
        generic_names = {
            'CLOUD_CREDENTIAL_INVALID',
            'CLOUD_AUTH_MODE_INVALID',
            'CLOUD_TOKEN_UNAVAILABLE',
        }
        for path in SERVICE_ROOT.rglob('*.py'):
            if path.name.startswith('test_') or 'alembic' in path.parts:
                continue
            tree = ast.parse(path.read_text(encoding='utf-8'), filename=str(path))
            for node in ast.walk(tree):
                if (
                    not isinstance(node, ast.Call)
                    or not isinstance(node.func, ast.Name)
                    or node.func.id != 'raise_error'
                ):
                    continue
                if not node.args or not isinstance(node.args[0], ast.Attribute):
                    continue
                if node.args[0].attr not in generic_names:
                    continue
                extra = next((item.value for item in node.keywords if item.arg == 'extra_msg'), None)
                stable_extra = extra.value if isinstance(extra, ast.Constant) and isinstance(extra.value, str) else None
                self.assertFalse(
                    stable_extra is not None,
                    f'{path}:{node.lineno} stable cause {stable_extra!r} uses generic {node.args[0].attr}',
                )


def _leading_text(node: ast.AST) -> str:
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    if isinstance(node, ast.JoinedStr):
        parts: list[str] = []
        for value in node.values:
            if not isinstance(value, ast.Constant) or not isinstance(value.value, str):
                break
            parts.append(value.value)
        return ''.join(parts)
    return ''


if __name__ == '__main__':
    unittest.main()
