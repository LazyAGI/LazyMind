from pathlib import Path
import unittest


REPO = Path(__file__).resolve().parents[1]


def read(relative: str) -> str:
    path = (REPO / relative).resolve()
    path.relative_to(REPO)  # Contract tests must be self-contained in this repository.
    return path.read_text(encoding="utf-8")


class ManagedProviderRevocationD2ContractTest(unittest.TestCase):
    def test_reauthorization_completion_evicts_cached_provider_lease(self) -> None:
        service = read("backend/core/providerconnection/service.go")
        completed = service.split(
            'if session.Status == "COMPLETED" && session.AuthConnectionID != ""', 1
        )[-1].split("return session, nil", 1)[0]
        self.assertIn("delete(service.cached, session.AuthConnectionID)", completed)

    def test_failure_report_reuses_core_bridge_and_never_contains_provider_body(self) -> None:
        cloud_client = read("backend/core/cloudclient/provider_connections.go")
        core_bridge = read("backend/core/providerconnection/token_bridge.go")
        core_routes = read("backend/core/routes.go")
        combined = cloud_client + core_bridge + core_routes
        self.assertIn("ReportProviderAccessTokenFailure", combined)
        self.assertIn("access-token:report", combined)
        self.assertNotIn("provider_error_body", combined)

    def test_notion_invalid_token_is_reported_with_original_context(self) -> None:
        notion = read(
            "backend/scan-control-plane/internal/sourceengine/connector/notion/connector.go"
        )
        resolver = read(
            "backend/scan-control-plane/internal/sourceengine/connector/feishu/types.go"
        )
        combined = notion + resolver
        self.assertIn("ReportTokenFailure", combined)
        self.assertIn("invalid_token", combined)
        for marker in ("SourceID", "BindingID", "Consumer", "RequiredCapability"):
            self.assertIn(marker, combined)


if __name__ == "__main__":
    unittest.main()
