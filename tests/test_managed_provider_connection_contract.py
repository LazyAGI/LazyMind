from pathlib import Path
import unittest


REPO = Path(__file__).resolve().parents[1]


def read(relative: str) -> str:
    path = (REPO / relative).resolve()
    path.relative_to(REPO)  # Contract tests must be self-contained in this repository.
    return path.read_text(encoding="utf-8")


def read_many(relatives: tuple[str, ...]) -> str:
    return "\n".join(read(path) for path in relatives)


class ManagedProviderConnectionContractTest(unittest.TestCase):
    def require_markers(self, text: str, markers: tuple[str, ...], label: str) -> None:
        for marker in markers:
            self.assertTrue(marker in text, f"{label} omitted {marker}")

    def forbid_markers(self, text: str, markers: tuple[str, ...], label: str) -> None:
        for marker in markers:
            self.assertFalse(marker in text, f"{label} contains forbidden {marker}")

    def test_core_owns_shared_managed_provider_bridge(self) -> None:
        required = [
            "backend/core/cloudclient/provider_connections.go",
            "backend/core/providerconnection/service.go",
            "backend/core/providerconnection/token_bridge.go",
        ]
        missing = [path for path in required if not (REPO / path).is_file()]
        self.assertFalse(
            missing, f"shared Core managed-provider bridge is missing: {missing}"
        )

    def test_cloudclient_exposes_only_managed_connection_and_access_lease_contracts(
        self,
    ) -> None:
        client = read("backend/core/cloudclient/provider_connections.go")
        self.require_markers(client, (
            "CreateProviderConnectionSession",
            "GetProviderConnectionSession",
            "CancelProviderConnectionSession",
            "ListProviderConnections",
            "ReauthorizeProviderConnection",
            "RevokeProviderConnection",
            "LeaseProviderAccessToken",
            "authorization_start_url",
            "auth_connection_id",
        ), "Core cloudclient Provider Connection contract")
        self.forbid_markers(client.lower(), (
            "client_secret",
            "client_secret_ref",
            "secret_key",
            "refresh_token",
            "vault_key",
        ), "Core cloudclient Provider Connection contract")

    def test_core_bridge_requires_owner_source_binding_consumer_and_capability(
        self,
    ) -> None:
        bridge = read_many(
            (
                "backend/core/providerconnection/service.go",
                "backend/core/providerconnection/token_bridge.go",
            )
        )
        self.require_markers(bridge, (
            "auth_connection_id",
            "user_id",
            "source_id",
            "binding_id",
            "consumer",
            "required_capability",
            "managed_oauth",
            "legacy_byo",
        ), "Core Provider Connection Bridge")
        self.assertNotIn("runtime_mode ==", bridge)
        self.assertNotIn("runtime_mode !=", bridge)

    def test_core_managed_bridge_uses_cloud_session_authority(self) -> None:
        service = read("backend/core/providerconnection/service.go")
        self.require_markers(service, (
            "cloudsession", "LeaseProviderAccessToken", "cloud_reauth_required"
        ), "Core managed Provider service")
        self.assertNotIn("provider refresh_token", service.lower())

    def test_auth_service_model_can_mirror_managed_connections_without_provider_secrets(
        self,
    ) -> None:
        model = read("backend/auth-service/models/cloud_auth_connection.py")
        self.require_markers(model, (
            "connection_method",
            "credential_location",
            "cloud_connection_id",
            "cloud_owner_user_id",
            "provider_workspace_id",
            "capability_contract_version",
        ), "auth-service connection model")

    def test_auth_service_migration_defaults_existing_rows_to_legacy_local(self) -> None:
        migrations = list(
            (REPO / "backend/auth-service/alembic/versions").glob(
                "*managed_provider_connection*.py"
            )
        )
        self.assertEqual(1, len(migrations), f"managed connection migrations: {migrations}")
        migration = migrations[0].read_text(encoding="utf-8").lower()
        self.require_markers(migration, (
            "connection_method",
            "credential_location",
            "cloud_connection_id",
            "legacy_byo",
            "local",
        ), "auth-service managed migration")
        self.forbid_markers(
            migration,
            ("client_secret", "refresh_token", "vault_key"),
            "auth-service managed migration",
        )

    def test_auth_service_managed_mirror_never_returns_local_provider_token(self) -> None:
        service = read("backend/auth-service/services/cloud_oauth_service.py")
        api = read("backend/auth-service/api/cloud_oauth.py")
        combined = service + "\n" + api
        self.require_markers(combined, (
            "managed_oauth", "credential_location", "MANAGED_TOKEN_REQUIRES_CORE_BRIDGE", "cloud_owner_user_id"
        ), "auth-service managed token boundary")

    def test_scan_control_plane_uses_core_token_bridge(self) -> None:
        client = read(
            "backend/scan-control-plane/internal/sourceengine/connector/feishu/http_clients.go"
        )
        self.require_markers(client, (
            "/v1/internal/provider-connections/", "consumer", "required_capability", "source_id", "binding_id"
        ), "scan Provider Token Resolver")
        self.assertNotIn("/api/authservice/v1/cloud/connections/", client)

    def test_notion_and_feishu_reuse_the_same_token_resolver_contract(self) -> None:
        feishu = read(
            "backend/scan-control-plane/internal/sourceengine/connector/feishu/types.go"
        )
        notion = read(
            "backend/scan-control-plane/internal/sourceengine/connector/notion/types.go"
        )
        for marker in (
            "ProviderTokenResolver",
            "Consumer",
            "RequiredCapability",
            "SourceID",
            "BindingID",
        ):
            self.assertTrue(marker in feishu, f"Feishu resolver contract omitted {marker}")
            self.assertTrue(marker in notion, f"Notion resolver contract omitted {marker}")

    def test_chat_resolves_each_connection_through_in_process_bridge(self) -> None:
        chat = read_many(
            (
                "backend/core/chat/feishu_token.go",
                "backend/core/modelconfig/model_config.go",
            )
        )
        self.require_markers(chat, (
            "ProviderConnectionBridge", "chat.read", "chat.search"
        ), "Core Chat Provider bridge")
        managed = chat.split("func LoadCloudProviderTokens", 1)[1].split("func LoadMaxInputTokens", 1)[0]
        self.assertNotIn("/v1/cloud/connections/%s/token", managed)

    def test_desktop_and_docker_share_managed_oauth_contract(self) -> None:
        frontend = read(
            "frontend/src/modules/dataSource/hooks/management/createOAuthEngine.ts"
        )
        self.require_markers(frontend, (
            "authorization_start_url", "apiCoreProviderConnectionsSessionsPost"
        ), "shared managed OAuth frontend")
        self.assertNotIn("client_secret", frontend.lower())

    def test_managed_flow_does_not_reuse_legacy_app_secret_storage(self) -> None:
        storage = read("frontend/src/modules/dataSource/common/feishuAccounts.ts")
        self.require_markers(storage, (
            "managed_oauth", "credential_location"
        ), "managed account storage")

    def test_desktop_and_docker_only_differ_in_url_opening_adapter(self) -> None:
        adapter = read(
            "frontend/src/modules/dataSource/oauth/openManagedAuthorization.ts"
        )
        self.require_markers(adapter, (
            "authorization_start_url", "window.open", "electron"
        ), "Desktop/Docker URL opening adapter")
        self.assertNotIn("client_id", adapter.lower())
        self.assertNotIn("client_secret", adapter.lower())

    def test_managed_connection_cloud_outage_never_falls_back_to_legacy(self) -> None:
        service = read("backend/core/providerconnection/service.go")
        self.require_markers(service, (
            "managed_oauth", "legacy_byo", "cloud_unavailable", "cached", "expires_at"
        ), "managed Cloud outage behavior")
        self.assertNotIn("managed_oauth -> legacy_byo", service)

    def test_managed_local_mirror_has_no_official_provider_secret_fields(self) -> None:
        managed_files = read_many(
            (
                "backend/core/providerconnection/service.go",
                "backend/core/providerconnection/token_bridge.go",
                "backend/auth-service/models/cloud_auth_connection.py",
            )
        ).lower()
        self.assertTrue(
            (REPO / "backend/core/providerconnection/service.go").is_file(),
            "Core managed Provider service is missing",
        )
        self.forbid_markers(managed_files, (
            "official_client_secret",
            "provider_refresh_token",
            "provider_secret_key",
            "vault_key",
        ), "managed local mirror")


if __name__ == "__main__":
    unittest.main()
