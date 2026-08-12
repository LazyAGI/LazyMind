import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "check_workflow_naming", ROOT / "scripts" / "check_workflow_naming.py"  # noqa: Q000
)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class WorkflowNamingCheckTest(unittest.TestCase):
    def _scan(self, relative_path, contents):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / relative_path
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(contents, encoding="utf-8")  # noqa: Q000
            return MODULE.scan_paths([path])

    def test_rejects_public_domain_types_routes_and_payloads(self):
        cases = (
            ("handler.go", 'type PluginSession struct{}\n'),  # noqa: Q000
            ("client.py", "payload = {'plugin_id': value}\n"),  # noqa: Q000
            ("api.ts", "fetch('/api/plugins/start')\n"),  # noqa: Q000
            ("config.yaml", "plugin_name: demo\n"),  # noqa: Q000
        )
        for path, contents in cases:
            with self.subTest(path=path):
                self.assertTrue(self._scan(path, contents))

    def test_allows_only_physical_names_in_gorm_mapping(self):
        clean = (
            'WorkflowID string `gorm:"column:plugin_id" json:"workflow_id"`\n'  # noqa: Q000
            'func (WorkflowSession) TableName() string { return "plugin_sessions" }\n'  # noqa: Q000
        )
        self.assertEqual([], self._scan("repository.go", clean))  # noqa: Q000

        leaked = 'PluginID string `gorm:"column:plugin_id" json:"plugin_id"`\n'  # noqa: Q000
        violations = self._scan("repository.go", leaked)  # noqa: Q000
        self.assertEqual({"PluginID", "plugin_id"}, {item.token for item in violations})  # noqa: Q000

    def test_allows_sql_identifiers_but_not_public_aliases(self):
        self.assertEqual(
            [],
            self._scan("migration.sql", "ALTER TABLE plugin_sessions ADD COLUMN origin_ref TEXT;\n"),  # noqa: Q000
        )
        violations = self._scan(
            "repository.py",  # noqa: Q000
            "SELECT plugin_id AS plugin_id FROM plugin_sessions\n",  # noqa: Q000
        )
        self.assertIn("plugin_id", {item.token for item in violations})  # noqa: Q000

    def test_migration_allowlist_is_limited_to_physical_identifiers(self):
        with tempfile.TemporaryDirectory() as directory:
            migration = Path(directory) / 'migrations' / '001_expand.sql'
            migration.parent.mkdir(parents=True)
            migration.write_text(
                'ALTER TABLE plugin_sessions ADD COLUMN plugin_ref TEXT;\n'
                '-- /api/plugins and plugin-sessions are not database identifiers\n',
                encoding='utf-8',
            )
            tokens = {item.token for item in MODULE.scan_paths([migration])}
        self.assertNotIn('plugin_sessions', tokens)
        self.assertNotIn('plugin_ref', tokens)
        self.assertIn('/api/plugins', tokens)
        self.assertIn('plugin-sessions', tokens)

    def test_published_migration_releases_are_immutable(self):
        self.assertEqual(
            [],
            self._scan(
                'backend/core/migrations/dev_mode/v0_2/001_legacy.sql',
                '-- Plugin session and /api/plugins are historical text\n',
            ),
        )
        self.assertTrue(self._scan(
            'backend/core/migrations/dev_mode/v0_3/001_current.sql',
            '-- Plugin session is not allowed in the current release\n',
        ))

    def test_does_not_allow_a_whole_persistence_file(self):
        violations = self._scan(
            "persistence_adapter.go",  # noqa: Q000
            'type PluginSession struct{}\nfunc route() string { return "/api/plugins" }\n',  # noqa: Q000
        )
        self.assertEqual(2, len(violations))

    def test_exact_persistence_file_still_rejects_public_domain_names(self):
        path = ROOT / 'backend/core/workflow/store.go'
        self.assertTrue(MODULE._is_physical_name_allowed(path, '', 'plugin_sessions', 0))
        self.assertFalse(MODULE._is_physical_name_allowed(path, '', 'PluginSession', 0))
        self.assertFalse(MODULE._is_physical_name_allowed(path, '', '/api/workflows', 0))

    def test_ignores_unrelated_plain_english_word(self):
        self.assertEqual([], self._scan("comment.py", "# load a plugin dynamically\n"))  # noqa: Q000


if __name__ == "__main__":  # noqa: Q000
    unittest.main()
