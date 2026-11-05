from pathlib import Path
import json
import runpy
import sqlite3
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
FIXTURES = ROOT / 'local/local-runtime-manager/testdata/update-format'
OWNER = runpy.run_path(str(FIXTURES / 'producer.py'))
GENERATOR = runpy.run_path(str(ROOT / 'desktop/scripts/generate-update-format.py'))


class UpdateFormatLazyLLMLifecycleTest(unittest.TestCase):
    def test_complete_service_owner_matches_capture_and_supported_recipe(self):
        with tempfile.TemporaryDirectory(prefix='lazymind-service-format-') as directory:
            filename = Path(directory) / 'lazyllm.db'
            subprocess.run([sys.executable, '-I', '-B', str(FIXTURES / 'lazyllm_lifecycle.py'),
                            str(ROOT), str(filename)], check=True, capture_output=True,
                           text=True, timeout=60)
            captured = json.loads((FIXTURES / 'lazyllm-service.json').read_text())['shape']
            self.assertEqual(OWNER['snapshot'](filename, 'lazyllm'), captured)
            recipe = json.loads((ROOT / 'local/local-runtime-manager/update-format-v2.json').read_text())
            with sqlite3.connect(filename) as db:
                service_sha = GENERATOR['shape_hash'](db)[0]
            with sqlite3.connect(':memory:') as db:
                empty_sha = GENERATOR['shape_hash'](db)[0]
            self.assertIn(service_sha, recipe.get('lazyllmVariants', []))
            self.assertIn(empty_sha, recipe.get('lazyllmVariants', []))


if __name__ == '__main__':
    unittest.main()
