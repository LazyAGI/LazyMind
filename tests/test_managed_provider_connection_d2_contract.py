from pathlib import Path
import re
import unittest


REPO = Path(__file__).resolve().parents[1]


def read(relative: str) -> str:
    path = (REPO / relative).resolve()
    path.relative_to(REPO)  # Contract tests must be self-contained in this repository.
    return path.read_text(encoding="utf-8")


class ManagedProviderConnectionD2ContractTest(unittest.TestCase):
    def test_desktop_and_compose_use_one_business_bridge(self) -> None:
        bridge = read("backend/core/providerconnection/service.go")
        self.assertIn("managed_oauth", bridge)
        self.assertIn("legacy_byo", bridge)
        self.assertNotIn("runtime_mode ==", bridge)
        self.assertNotIn("runtime_mode !=", bridge)
        compose = read("docker-compose.yml")
        self.assertIn("LAZYMIND_CLIENT_INSTANCE_ID", compose)
        self.assertIn("LAZYMIND_CLOUD_TOKEN_STORE: encrypted-file", compose)
        self.assertNotIn("dev-internal-service-token", compose)

    def test_sqlite_and_postgres_share_managed_mirror_defaults(self) -> None:
        migration = read(
            "backend/auth-service/alembic/versions/f5a6b7c8d9e0_managed_provider_connection.py"
        )
        self.assertIn("server_default='legacy_byo'", migration)
        self.assertIn("server_default='local'", migration)
        self.assertNotIn("batch_alter_table", migration)
        model = read("backend/auth-service/models/cloud_auth_connection.py")
        for field in (
            "connection_method",
            "credential_location",
            "cloud_connection_id",
            "cloud_owner_user_id",
        ):
            self.assertIn(field, model)

    def test_managed_requests_stay_managed_and_signed_out_setup_remains_available(self) -> None:
        engine = read(
            "frontend/src/modules/dataSource/hooks/management/createOAuthEngine.ts"
        )
        self.assertIn("return startManagedOAuth(provider, options?.reauthorizeConnectionId)", engine)
        self.assertIn("ctx.cloudManagedOAuthAvailable === false", engine)
        self.assertIn("legacyOAuthCredentials", engine)
        managed = read(
            "frontend/src/modules/dataSource/oauth/openManagedAuthorization.ts"
        )
        self.assertIn("authorization_start_url", managed)
        self.assertNotIn("appSecret", managed)
        self.assertNotIn("access_token", managed)

        hub = read(
            "frontend/src/modules/modelProvider/hooks/useCloudDocumentProviders.ts"
        )
        self.assertIn("const isNotionSetupReady = true", hub)
        self.assertIn('return ctx.startCloudOAuth("notion")', hub)
        self.assertIn('openCloudSetupModal("notion", "auth")', hub)
        self.assertIn("if (!await refreshManagedAvailability())", hub)

        panel = read(
            "frontend/src/modules/modelProvider/components/CloudDocumentProviderPanel.tsx"
        )
        self.assertIn("notionConnectAction", panel)

    def test_electron_only_opens_cloud_start_path(self) -> None:
        navigation = read("desktop/electron/src/external-navigation.js")
        self.assertIn("MANAGED_PROVIDER_AUTHORIZATION_PATH", navigation)
        self.assertIn("provider-authorization", navigation)
        self.assertIn("target.search === \"\"", navigation)
        self.assertIn("target.hash === \"\"", navigation)

    def test_core_cache_never_extends_provider_lease(self) -> None:
        bridge = read("backend/core/providerconnection/service.go")
        self.assertIn("item.expiresAt.After(now)", bridge)
        self.assertIn("LeaseProviderAccessToken", bridge)
        self.assertIn("ErrCloudUnavailable", bridge)
        self.assertNotIn("Add(24 * time.Hour)", bridge)

    def test_notion_connector_keeps_page_database_block_and_markdown_paths(self) -> None:
        connector = read(
            "backend/scan-control-plane/internal/sourceengine/connector/notion/client.go"
        )
        for marker in (
            "GetPage",
            "GetDatabase",
            "ListBlockChildren",
            "Search",
            "PageToMarkdown",
            "DatabaseToMarkdown",
        ):
            self.assertIn(marker, connector)

    def test_managed_notion_completion_enables_chat_before_success(self) -> None:
        engine = read(
            "frontend/src/modules/dataSource/hooks/management/createOAuthEngine.ts"
        )
        completed_branch = engine.split("const startManagedOAuth = async", 1)[1].split(
            "const startCloudOAuth = async", 1
        )[0]
        self.assertLess(completed_branch.index("await enableCloudConnectionForChat"), completed_branch.index("return true"))
        self.assertIn(
            "enableCloudConnectionForChat",
            completed_branch,
            "managed Notion must not show immediate Chat success while chat_enabled remains false",
        )

    def test_connector_requires_real_source_and_binding_context(self) -> None:
        token_client = read(
            "backend/scan-control-plane/internal/sourceengine/connector/feishu/http_clients.go"
        )
        self.assertNotIn(
            'req.SourceID = "interactive:" + connectionID',
            token_client,
            "managed Connector requests must not synthesize SourceID",
        )
        self.assertNotIn(
            'req.BindingID = "interactive:" + connectionID',
            token_client,
            "managed Connector requests must not synthesize BindingID",
        )

    def test_core_authorizes_source_and_binding_before_cloud_lease(self) -> None:
        bridge = read("backend/core/providerconnection/service.go")
        managed = bridge.split(
            "func (service *Service) resolveManagedAccessToken", 1
        )[-1].split("func (service *Service) cloudAccessToken", 1)[0]
        ownership_check = re.search(
            r"(?:Registry|Authorizer)\.[A-Za-z0-9_]+\([^)]*request\.SourceID[^)]*request\.BindingID",
            managed,
            re.DOTALL,
        )
        self.assertIsNotNone(
            ownership_check,
            "Core must validate local user/Source/Binding ownership before requesting a Cloud Lease",
        )


if __name__ == "__main__":
    unittest.main()
