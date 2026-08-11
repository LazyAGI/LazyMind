import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "check_migration_immutability", ROOT / "scripts" / "check_migration_immutability.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class MigrationImmutabilityCheckTest(unittest.TestCase):
    def test_allows_new_dev_migration_changed_within_branch(self):
        changes = [MODULE.MigrationChange(status="A", path="backend/core/migrations/dev_mode/v0_3/new.up.sql")]
        self.assertEqual([], MODULE.immutable_violations(changes))

    def test_rejects_modified_deleted_or_renamed_target_dev_migrations(self):
        changes = [
            MODULE.MigrationChange(status="M", path="backend/core/migrations/dev_mode/v0_3/old.up.sql"),
            MODULE.MigrationChange(status="D", path="backend/core/migrations/dev_mode/v0_3/old.down.sql"),
            MODULE.MigrationChange(
                status="R100",
                old_path="backend/core/migrations/dev_mode/v0_3/old.up.sql",
                path="backend/core/migrations/dev_mode/v0_3/renamed.up.sql",
            ),
        ]
        self.assertEqual(changes, MODULE.immutable_violations(changes))

    def test_integration_compares_against_target_revision(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self._git(root, "init")
            self._git(root, "config", "user.email", "test@example.com")
            self._git(root, "config", "user.name", "Test")
            existing = root / "backend/core/migrations/dev_mode/v0_3/001_existing.up.sql"
            existing.parent.mkdir(parents=True)
            existing.write_text("SELECT 1;\n", encoding="utf-8")
            self._git(root, "add", ".")
            self._git(root, "commit", "-m", "base")
            base = self._git(root, "rev-parse", "HEAD")
            new_file = root / "backend/core/migrations/dev_mode/v0_3/002_new.up.sql"
            new_file.write_text("SELECT 2;\n", encoding="utf-8")
            self._git(root, "add", ".")
            self._git(root, "commit", "-m", "add new migration")
            new_file.write_text("SELECT 2;\nSELECT 3;\n", encoding="utf-8")
            self._git(root, "add", ".")
            self._git(root, "commit", "-m", "edit new migration")
            self.assertEqual([], MODULE.immutable_violations(MODULE.changed_dev_migrations(root, base)))
            existing.write_text("SELECT 42;\n", encoding="utf-8")
            self._git(root, "add", ".")
            self._git(root, "commit", "-m", "edit old migration")
            violations = MODULE.immutable_violations(MODULE.changed_dev_migrations(root, base))
            self.assertEqual(1, len(violations))
            self.assertEqual("M", violations[0].status)
            self.assertTrue(violations[0].path.endswith("001_existing.up.sql"))

    def _git(self, root, *args):
        result = subprocess.run(["git", *args], cwd=root, check=True, text=True, stdout=subprocess.PIPE)
        return result.stdout.strip()


if __name__ == "__main__":
    unittest.main()
