from pathlib import Path
import runpy
import unittest


ROOT = Path(__file__).resolve().parents[3]
PRODUCER = runpy.run_path(str(ROOT / 'local/local-runtime-manager/testdata/update-format/producer.py'))


class UpdateFormatAuthOwnerTest(unittest.TestCase):
    def test_auth_schema_matches_captured_owner(self):
        PRODUCER['check_fixture'](ROOT, 'auth')

    def test_auth_state_schema_matches_captured_owner(self):
        PRODUCER['check_fixture'](ROOT, 'auth-state')


if __name__ == '__main__':
    unittest.main()
