from pathlib import Path
import re
import unittest


REPO = Path(__file__).resolve().parents[1]


def compose_service(source: str, service: str) -> str:
    marker = f"\n  {service}:\n"
    start = source.find(marker)
    if start < 0:
        return ""
    body = source[start + len(marker) :]
    next_service = re.search(r"(?m)^  [a-zA-Z0-9][a-zA-Z0-9_-]*:\s*$", body)
    return body[: next_service.start()] if next_service else body


class ManagedProviderComposeD2ContractTest(unittest.TestCase):
    def test_core_owns_encrypted_cloud_session_storage(self) -> None:
        compose = (REPO / "docker-compose.yml").read_text(encoding="utf-8")
        core = compose_service(compose, "core")

        self.assertIn("LAZYMIND_CLOUD_TOKEN_STORE: encrypted-file", core)
        self.assertIn(
            "${LAZYMIND_CLOUD_SESSION_DIR:-./data/core/cloud-session}:/var/lib/lazymind/cloud-session", core
        )
        self.assertIn(
            "source: ${LAZYMIND_CLOUD_TOKEN_STORE_KEY_FILE:-/dev/null}",
            core,
        )
        self.assertIn("target: /run/secrets/lazymind-cloud-token-store-key", core)
        self.assertIn("create_host_path: false", core)

    def test_cloud_and_feishu_options_do_not_gate_base_core_startup(self) -> None:
        compose = (REPO / "docker-compose.yml").read_text(encoding="utf-8")
        core = compose_service(compose, "core")
        sidecar = compose_service(compose, "feishu-cli-sidecar")
        self.assertIn('profiles: ["feishu-cli"]', sidecar)
        self.assertNotIn("      feishu-cli-sidecar:", core)
        self.assertIn("LAZYMIND_CLIENT_INSTANCE_ID: ${LAZYMIND_CLIENT_INSTANCE_ID:-}", core)
        self.assertIn("file: ${LAZYMIND_FEISHU_CLI_SIDECAR_HMAC_FILE:-/dev/null}", compose)
        self.assertNotIn("${LAZYMIND_FEISHU_CLI_SIDECAR_HMAC_FILE:?", compose)

    def test_manifest_options_map_host_files_to_container_files(self) -> None:
        compose = (REPO / "docker-compose.yml").read_text(encoding="utf-8")
        core = compose_service(compose, "core")
        for variable, filename in (
            ("LAZYMIND_CREDENTIAL_MANIFEST_TRUST_PUBLIC_KEY_FILE", "credential-manifest-signing-public.der"),
            ("LAZYMIND_CREDENTIAL_MANIFEST_BOOTSTRAP_PAYLOAD_FILE", "credential-manifest.json"),
            ("LAZYMIND_CREDENTIAL_MANIFEST_BOOTSTRAP_SIGNATURE_FILE", "credential-manifest.sig"),
        ):
            target = f"/run/lazymind-cloud/{filename}"
            self.assertIn(f"source: ${{{variable}:-/dev/null}}", core)
            self.assertIn(f"target: {target}", core)
            self.assertIn(f"{variable}: ${{{variable}:+{target}}}", core)

    def test_scan_control_plane_cannot_mount_cloud_session_credentials(self) -> None:
        compose = (REPO / "docker-compose.yml").read_text(encoding="utf-8")
        scan = compose_service(compose, "scan-control-plane")

        self.assertNotIn("/var/lib/lazymind/cloud-session", scan)
        self.assertNotIn("/run/secrets/lazymind-cloud-token-store-key", scan)

    def test_internal_service_token_is_initialized_once_and_shared_read_only(self) -> None:
        compose = (REPO / "docker-compose.yml").read_text(encoding="utf-8")
        initializer = compose_service(compose, "internal-service-token-init")
        self.assertNotIn("${LAZYMIND_INTERNAL_SERVICE_TOKEN_FILE:?", compose)
        self.assertIn("file: ${LAZYMIND_INTERNAL_SERVICE_TOKEN_FILE:-/dev/null}", compose)
        self.assertIn("init-internal-service-token.sh", initializer)
        self.assertIn("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN: ${LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN:-}", initializer)
        for service in ("auth-service", "core", "scan-control-plane", "chat", "feishu-cli-sidecar"):
            block = compose_service(compose, service)
            self.assertNotRegex(block, r"(?m)^\s+LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN:\s*")
            self.assertIn("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE: /run/secrets/internal-service/token", block)
            self.assertIn("internal-service-credentials:/run/secrets/internal-service:ro", block)
            self.assertIn("internal-service-token-init:\n        condition: service_completed_successfully", block)

    def test_all_runtime_consumers_support_internal_service_token_file(self) -> None:
        consumers = (
            "backend/core/main.go",
            "backend/scan-control-plane/internal/config/config.go",
            "backend/auth-service/core/deps.py",
            "algorithm/lazymind/config.py",
        )
        for relative in consumers:
            source = (REPO / relative).read_text(encoding="utf-8")
            self.assertTrue(
                "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE" in source,
                msg=f"{relative} cannot read the Compose Docker Secret file",
            )

    def test_compose_web_reserves_oauth_popups_before_async_session_requests(self) -> None:
        main_layout = (REPO / "frontend/src/layouts/MainLayout.tsx").read_text(
            encoding="utf-8"
        )
        cloud_login = main_layout.split("const handleCloudLogin", 1)[-1].split(
            "const handleCloudRegister", 1
        )[0]
        self.assertIn("reserveCloudLoginPopup", cloud_login)
        self.assertLess(
            cloud_login.index("reserveCloudLoginPopup"),
            cloud_login.index("await beginCloudLogin"),
        )

        oauth_engine = (
            REPO
            / "frontend/src/modules/dataSource/hooks/management/createOAuthEngine.ts"
        ).read_text(encoding="utf-8")
        managed = oauth_engine.split("export async function startManagedOAuthSession", 1)[-1].split(
            "export function createOAuthEngine", 1
        )[0]
        self.assertIn("reserveManagedAuthorizationPopup", managed)
        self.assertIn("await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsSessionsPost", managed)
        self.assertLess(
            managed.index("reserveManagedAuthorizationPopup"),
            managed.index("await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsSessionsPost"),
        )
        self.assertNotIn("await fetch", managed)

    def test_compose_publishes_cloud_loopback_callback_to_host_only(self) -> None:
        compose = (REPO / "docker-compose.yml").read_text(encoding="utf-8")
        core = compose_service(compose, "core")

        self.assertIn(
            'LAZYMIND_CLOUD_CALLBACK_LISTEN_ADDRESS: "0.0.0.0:${LAZYMIND_CLOUD_CALLBACK_PORT:-18081}"',
            core,
            msg="Core does not bind the fixed Compose callback port inside its container",
        )
        self.assertIn(
            '"127.0.0.1:${LAZYMIND_CLOUD_CALLBACK_PORT:-18081}:${LAZYMIND_CLOUD_CALLBACK_PORT:-18081}"',
            core,
            msg="Compose does not publish the callback port on host loopback only",
        )
        self.assertNotIn(
            '"0.0.0.0:${LAZYMIND_CLOUD_CALLBACK_PORT:-18081}:',
            core,
            msg="Compose exposes the callback listener beyond host loopback",
        )


if __name__ == "__main__":
    unittest.main()
