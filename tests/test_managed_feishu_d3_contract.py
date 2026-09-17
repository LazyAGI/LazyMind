from pathlib import Path
import re
import unittest


REPO = Path(__file__).resolve().parents[1]


class ManagedFeishuD3ContractTest(unittest.TestCase):
    def test_feishu_entry_uses_cli_when_managed_service_is_available(self) -> None:
        source = (
            REPO / "frontend/src/modules/modelProvider/hooks/useCloudDocumentProviders.ts"
        ).read_text(encoding="utf-8")
        self.assertIn("const isFeishuSetupReady = true;", source)
        handler = source.split("const handleManageFeishuAuth", 1)[-1].split(
            "const handleManageLocalSource", 1
        )[0]
        self.assertIn("startFeishuCLISession", handler)
        self.assertIn("refreshManagedAvailability", handler)
        self.assertIn('openCloudSetupModal("feishu", "auth")', handler)
        self.assertIn("isFeishuAuthValid", handler)
        self.assertIn("navigate(CLOUD_DOCUMENTS_FEISHU_PATH)", handler)

    def test_managed_feishu_default_scopes_are_minimal_and_read_only(self) -> None:
        source = (
            REPO / "frontend/src/modules/dataSource/constants/options.ts"
        ).read_text(encoding="utf-8")
        block = source.split("export const FEISHU_DEFAULT_SCOPES", 1)[-1].split(
            "];", 1
        )[0]
        for required in (
            "offline_access",
            "drive:drive:readonly",
            "drive:drive.metadata:readonly",
            "wiki:space:retrieve",
            "wiki:node:read",
            "wiki:node:retrieve",
            "docx:document:readonly",
        ):
            self.assertIn(f'"{required}"', block)
        for forbidden in (
            "drive:drive",
            "wiki:wiki",
            "wiki:wiki:readonly",
            "docx:document",
        ):
            self.assertIsNone(
                re.search(rf'"{re.escape(forbidden)}"', block),
                msg=f"managed default scope grants write capability: {forbidden}",
            )

    def test_provider_token_contract_carries_and_checks_user_subject(self) -> None:
        types = (
            REPO
            / "backend/scan-control-plane/internal/sourceengine/connector/feishu/types.go"
        ).read_text(encoding="utf-8")
        connector = (
            REPO
            / "backend/scan-control-plane/internal/sourceengine/connector/feishu/connector.go"
        ).read_text(encoding="utf-8")
        core = (
            REPO / "backend/core/providerconnection/service.go"
        ).read_text(encoding="utf-8")
        self.assertIn("SubjectType", types)
        self.assertIn("SubjectType", core)
        self.assertRegex(connector, r"(?s)SubjectType.*user")

    def test_desktop_and_compose_keep_one_managed_feishu_oauth_engine(self) -> None:
        engine = (
            REPO
            / "frontend/src/modules/dataSource/hooks/management/createOAuthEngine.ts"
        ).read_text(encoding="utf-8")
        self.assertIn("startManagedOAuthSession", engine)
        self.assertIn(
            "startManagedOAuth(provider, options?.reauthorizeConnectionId)", engine
        )
        self.assertIn("reserveManagedAuthorizationPopup", engine)
        self.assertIn("apiCoreProviderConnectionsSessionsPost", engine)
        self.assertNotIn("axiosInstance.post", engine)
        self.assertNotIn("runtime_mode", engine)

    def test_managed_feishu_reauthorization_never_enters_legacy_byo(self) -> None:
        provider_hook = (
            REPO / "frontend/src/modules/modelProvider/hooks/useCloudDocumentProviders.ts"
        ).read_text(encoding="utf-8")
        account_hook = (
            REPO / "frontend/src/modules/modelProvider/hooks/useFeishuAccounts.ts"
        ).read_text(encoding="utf-8")
        mapper = (
            REPO / "frontend/src/modules/dataSource/mappers/cloudConnection.ts"
        ).read_text(encoding="utf-8")

        manage_handler = provider_hook.split(
            "const handleManageFeishuAuth", 1
        )[-1].split("const handleManageLocalSource", 1)[0]
        self.assertIn("isFeishuAuthValid", manage_handler)
        self.assertIn("CLOUD_DOCUMENTS_FEISHU_PATH", manage_handler)
        self.assertIn("navigate", manage_handler)

        authorize_handler = account_hook.split(
            "const handleAuthorizeAccount", 1
        )[-1].split("const handleDeleteAccount", 1)[0]
        self.assertIn("connection_method", authorize_handler)
        self.assertIn("managed_oauth", authorize_handler)
        self.assertIn("startFeishuCLISession", authorize_handler)
        self.assertIn('connection.connection_method === "managed_oauth"', mapper)
        self.assertIn('connection.credential_location === "cloud"', mapper)


if __name__ == "__main__":
    unittest.main()
