import json
from pathlib import Path
import re
import unittest


REPO = Path(__file__).resolve().parents[1]
FIXTURES = REPO / "backend/core/providerconnection/testdata/feishu-cli"


class FeishuCLIFirstStageContractTest(unittest.TestCase):
    def test_fcli_fixtures_are_fake_structured_and_do_not_contain_real_credentials(self) -> None:
        fixture_names = (
            "auth-login-waiting.json",
            "auth-login-complete.json",
            "auth-status-ready.json",
            "auth-waiting-admin-error.json",
        )
        payloads = []
        for name in fixture_names:
            path = FIXTURES / name
            self.assertTrue(path.is_file(), msg=f"missing FCLI fixture: {name}")
            payloads.append(json.loads(path.read_text(encoding="utf-8")))

        waiting, complete, status, waiting_admin = payloads
        self.assertEqual(waiting["expires_in"], 240)
        self.assertNotIn("interval", waiting)
        self.assertIn("example.invalid", waiting["verification_url"])
        self.assertEqual(complete["event"], "authorization_complete")
        self.assertEqual(status["identity"], "user")
        self.assertTrue(status["verified"])
        self.assertEqual(waiting_admin["error"]["subtype"], "missing_scope")

        serialized = json.dumps(payloads, ensure_ascii=False).lower()
        for forbidden in (
            "access_token",
            "refresh_token",
            "client_secret",
            "oauth_state",
            "document_content",
        ):
            self.assertNotIn(forbidden, serialized)

    def test_cli_runtime_is_version_pinned_and_process_execution_is_constrained(self) -> None:
        runner = REPO / "backend/core/providerconnection/feishu_cli_runner.go"
        self.assertTrue(
            runner.is_file(),
            msg="FCLI-001/FCLI-017/FCLI-026/FCLI-027: CLI runner is not implemented",
        )
        source = runner.read_text(encoding="utf-8")
        for required in (
            "LARKSUITE_CLI_CONFIG_DIR",
            "LARKSUITE_CLI_DATA_DIR",
            "feishuCLIProfileHomeDirectory",
            "LARKSUITE_CLI_NO_UPDATE_NOTIFIER",
            "LARKSUITE_CLI_NO_SKILLS_NOTIFIER",
            "CommandContext",
            "DisallowUnknownFields",
        ):
            self.assertIn(required, source)
        self.assertNotRegex(
            source,
            r'exec\.Command(?:Context)?\([^\n]*["\'](?:sh|bash|zsh)["\']',
        )
        self.assertNotIn("--recommend", source)
        self.assertNotIn('"--domain", "all"', source)

    def test_profile_store_is_owner_connection_isolated_and_does_not_use_global_home(self) -> None:
        store = REPO / "backend/core/providerconnection/feishu_cli_profile_store.go"
        self.assertTrue(
            store.is_file(),
            msg="FCLI-007/FCLI-008/FCLI-019/FCLI-030/FCLI-035: Profile Store is not implemented",
        )
        source = store.read_text(encoding="utf-8")
        for required in (
            "0700",
            "0600",
            "localUserID",
            "connectionID",
            "tenantKey",
            "openID",
        ):
            self.assertIn(required, source)
        self.assertNotIn(".lark-cli", source)
        self.assertNotRegex(source, r"os\.(?:UserHomeDir|ExpandEnv)")

    def test_device_flow_keeps_device_code_local_and_reuses_connection_state_machine(self) -> None:
        coordinator = REPO / "backend/core/providerconnection/feishu_cli_device_flow.go"
        self.assertTrue(
            coordinator.is_file(),
            msg="FCLI-003/FCLI-009-FCLI-015: Device Flow Coordinator is not implemented",
        )
        source = coordinator.read_text(encoding="utf-8")
        for required in (
            "APP_CREATION_WAITING_USER",
            "AUTH_WAITING_USER",
            "AUTH_WAITING_ADMIN",
            "AUTH_DEVICE_CODE_EXPIRED",
            "AUTH_CANCELED",
        ):
            self.assertIn(required, source)
        self.assertRegex(source, r'auth["\']?,\s*["\']login')
        self.assertIn("--no-wait", source)
        self.assertIn("--device-code", source)
        self.assertRegex(source, r'DeviceCode[^\n]+json:["\']-["\']')

    def test_connection_schema_has_explicit_cli_method_without_rewriting_old_rows(self) -> None:
        model = (REPO / "backend/auth-service/models/cloud_auth_connection.py").read_text(
            encoding="utf-8"
        )
        migrations = "\n".join(
            path.read_text(encoding="utf-8")
            for path in (REPO / "backend/auth-service/alembic/versions").glob("*.py")
        )
        combined = model + migrations
        self.assertIn("cli_personal_app", combined)
        self.assertRegex(combined, r"cli_sidecar|credential_location[^\n]+local")
        self.assertIn("managed_oauth", combined)
        self.assertIn("legacy_byo", combined)

    def test_primary_feishu_entry_uses_cli_and_has_no_cloud_oauth_fallback(self) -> None:
        provider_hook = (
            REPO / "frontend/src/modules/modelProvider/hooks/useCloudDocumentProviders.ts"
        ).read_text(encoding="utf-8")
        account_hook = (
            REPO / "frontend/src/modules/modelProvider/hooks/useFeishuAccounts.ts"
        ).read_text(encoding="utf-8")
        engine = (
            REPO / "frontend/src/modules/dataSource/hooks/management/createOAuthEngine.ts"
        ).read_text(encoding="utf-8")
        feishu_source = provider_hook + account_hook + engine
        self.assertRegex(feishu_source, r"startFeishuCLI(?:DeviceFlow|Session)")
        for block in (
            provider_hook.split("const handleManageFeishuAuth", 1)[-1].split(
                "const handleManageLocalSource", 1
            )[0],
            account_hook.split("const handleAddManagedAccount", 1)[-1].split(
                "const handleDeleteAccount", 1
            )[0],
        ):
            self.assertNotIn('startManagedOAuthSession("feishu")', block)
            self.assertNotIn('startCloudOAuth("feishu")', block)

    def test_cli_connection_uses_existing_feishu_connector_through_a_client_adapter(self) -> None:
        adapter = (
            REPO
            / "backend/scan-control-plane/internal/sourceengine/connector/feishu/cli_client.go"
        )
        connector = (
            REPO
            / "backend/scan-control-plane/internal/sourceengine/connector/feishu/connector.go"
        ).read_text(encoding="utf-8")
        self.assertTrue(
            adapter.is_file(),
            msg="FCLI-020-FCLI-025: Feishu CLI client adapter is not implemented",
        )
        source = adapter.read_text(encoding="utf-8")
        self.assertIn("FeishuClient", source)
        self.assertIn("--as", source)
        self.assertIn("user", source)
        self.assertNotIn("tenant_access_token", source)
        self.assertIn("type FeishuConnector struct", connector)
        self.assertEqual(connector.count("type FeishuConnector struct"), 1)

    def test_notion_managed_oauth_remains_a_protected_baseline(self) -> None:
        engine = (
            REPO / "frontend/src/modules/dataSource/hooks/management/createOAuthEngine.ts"
        ).read_text(encoding="utf-8")
        self.assertIn("const startManagedOAuth = async", engine)
        self.assertIn("startManagedOAuthSession(", engine)
        self.assertIn('if (provider === "notion")', engine)
        self.assertIn("refreshNotionAuthAccounts", engine)


if __name__ == "__main__":
    unittest.main()
