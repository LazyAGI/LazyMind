from pathlib import Path
import runpy
import unittest


ROOT = Path(__file__).resolve().parents[3]
PRODUCER = runpy.run_path(str(ROOT / 'local/local-runtime-manager/testdata/update-format/producer.py'))


class UpdateFormatChannelOwnerTest(unittest.TestCase):
    def test_channel_schema_matches_captured_owner(self):
        PRODUCER['check_fixture'](ROOT, 'channel')


if __name__ == '__main__':
    unittest.main()
