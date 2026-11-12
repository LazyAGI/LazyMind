from pathlib import Path
import runpy
import unittest


ROOT = Path(__file__).resolve().parents[2]
PRODUCER = runpy.run_path(str(ROOT / 'local/local-runtime-manager/testdata/update-format/producer.py'))


class UpdateFormatLazyLLMOwnerTest(unittest.TestCase):
    def test_doc_manager_schema_matches_captured_owner(self):
        PRODUCER['check_fixture'](ROOT, 'lazyllm')

    def test_empty_segment_store_has_no_business_collections(self):
        PRODUCER['check_fixture'](ROOT, 'segments')


if __name__ == '__main__':
    unittest.main()
