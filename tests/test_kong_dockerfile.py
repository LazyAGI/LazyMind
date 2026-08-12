import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


class KongDockerfileTest(unittest.TestCase):
    def test_custom_plugin_copy_source_exists_and_uses_kong_plugin_path(self):
        dockerfile = (ROOT / "kong" / "Dockerfile").read_text(encoding="utf-8")
        match = re.search(r"^COPY\s+(\S+)\s+(\S+)$", dockerfile, re.MULTILINE)

        self.assertIsNotNone(match)
        source, destination = match.groups()
        self.assertTrue((ROOT / "kong" / source).is_dir())
        self.assertEqual(
            "/usr/local/share/lua/5.1/kong/plugins/rbac-auth",
            destination,
        )


if __name__ == "__main__":
    unittest.main()
