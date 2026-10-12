import importlib.util
from pathlib import Path
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'desktop/scripts/materialize-python-component-lock.py'
spec = importlib.util.spec_from_file_location('materialize_python_component_lock', SCRIPT)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class MaterializePythonComponentLockTests(unittest.TestCase):
    def test_replaces_lazyllm_pin_without_changing_source(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root / 'source.lock'
            output = root / 'effective.lock'
            source.write_text('numpy==1.26.4\nlazyllm==1.3.0\n', encoding='utf-8')

            module.materialize(source, output, '1.4.0a0')

            self.assertEqual(source.read_text(encoding='utf-8'), 'numpy==1.26.4\nlazyllm==1.3.0\n')
            self.assertEqual(output.read_text(encoding='utf-8'), 'numpy==1.26.4\nlazyllm==1.4.0a0\n')

    def test_requires_exactly_one_lazyllm_pin(self):
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp) / 'source.lock'
            output = Path(temp) / 'effective.lock'
            for contents in ('numpy==1.26.4\n', 'lazyllm==1\nlazyllm==2\n'):
                source.write_text(contents, encoding='utf-8')
                with self.subTest(contents=contents), self.assertRaisesRegex(RuntimeError, 'exactly one'):
                    module.materialize(source, output, '1.4.0a0')


if __name__ == '__main__':
    unittest.main()
