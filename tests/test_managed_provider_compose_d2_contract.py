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
            "./data/core/cloud-session:/var/lib/lazymind/cloud-session", core
        )
        self.assertIn(
            "${LAZYMIND_CLOUD_TOKEN_STORE_KEY_FILE:-./data/core/cloud-session/key}:/run/secrets/lazymind-cloud-token-store-key:ro",
            core,
        )

    def test_scan_control_plane_cannot_mount_cloud_session_credentials(self) -> None:
        compose = (REPO / "docker-compose.yml").read_text(encoding="utf-8")
        scan = compose_service(compose, "scan-control-plane")

        self.assertNotIn("/var/lib/lazymind/cloud-session", scan)
        self.assertNotIn("/run/secrets/lazymind-cloud-token-store-key", scan)

    def test_internal_service_token_uses_one_repository_external_docker_secret(self) -> None:
        compose = (REPO / "docker-compose.yml").read_text(encoding="utf-8")

        self.assertTrue(
            "LAZYMIND_INTERNAL_SERVICE_TOKEN_FILE" in compose,
            msg="Compose does not declare a repository-external internal token file",
        )
        self.assertTrue(
            "file: ${LAZYMIND_INTERNAL_SERVICE_TOKEN_FILE:?LAZYMIND_INTERNAL_SERVICE_TOKEN_FILE is required}"
            in compose,
            msg="Compose does not source the internal token Docker Secret from the required file",
        )
        for service in ("auth-service", "core", "scan-control-plane", "chat"):
            block = compose_service(compose, service)
            self.assertNotRegex(
                block,
                r"(?m)^\s+LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN:\s*",
                msg=f"{service} exposes the shared internal token through Compose environment",
            )
            self.assertTrue(
                "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE: /run/secrets/lazymind-internal-service-token"
                in block,
                msg=f"{service} does not receive the internal token file path",
            )
            self.assertTrue(
                "lazymind-internal-service-token" in block,
                msg=f"{service} does not mount the shared internal token Secret",
            )

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
        managed = oauth_engine.split("const startManagedOAuth", 1)[-1].split(
            "const refreshNotionAuthAccounts", 1
        )[0]
        self.assertIn("reserveManagedAuthorizationPopup", managed)
        self.assertIn("await axiosInstance.post", managed)
        self.assertLess(
            managed.index("reserveManagedAuthorizationPopup"),
            managed.index("await axiosInstance.post"),
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
