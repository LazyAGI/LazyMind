from pathlib import Path
import unittest


REPO = Path(__file__).resolve().parents[1]


def read(relative: str) -> str:
    path = (REPO / relative).resolve()
    path.relative_to(REPO)  # Contract tests must be self-contained in this repository.
    return path.read_text(encoding="utf-8")


class ManagedProviderPreBindingD2ContractTest(unittest.TestCase):
    def test_token_context_declares_two_explicit_modes(self) -> None:
        contract = read(
            "backend/scan-control-plane/internal/sourceengine/connector/feishu/types.go"
        )
        self.assertIn("ContextMode", contract)
        self.assertIn("source_binding", contract)
        self.assertIn("pre_binding_browse", contract)

    def test_connectors_use_pre_binding_only_for_browse_without_ids(self) -> None:
        notion = read(
            "backend/scan-control-plane/internal/sourceengine/connector/notion/helpers.go"
        )
        feishu = read(
            "backend/scan-control-plane/internal/sourceengine/connector/feishu/target.go"
        )
        for connector in (notion, feishu):
            self.assertIn("pre_binding_browse", connector)
            self.assertIn("datasource.browse", connector)
            self.assertIn("source_binding", connector)

    def test_http_resolver_has_no_implicit_or_privileged_pre_binding_fallback(self) -> None:
        resolver = read(
            "backend/scan-control-plane/internal/sourceengine/connector/feishu/http_clients.go"
        )
        self.assertIn("pre_binding_browse", resolver)
        self.assertIn("datasource.browse", resolver)
        self.assertNotIn('req.SourceID = "interactive:"', resolver)
        self.assertNotIn('req.BindingID = "interactive:"', resolver)

    def test_core_revalidates_pre_binding_owner_and_restricts_capability(self) -> None:
        service = read("backend/core/providerconnection/service.go")
        authorizer = read(
            "backend/core/providerconnection/source_binding_authorizer.go"
        )
        combined = service + authorizer
        self.assertIn("pre_binding_browse", combined)
        self.assertIn("datasource.browse", combined)
        self.assertIn("AuthorizePreBindingBrowse", combined)
        self.assertIn("request.UserID", combined)
        self.assertIn("request.TenantID", combined)

    def test_scan_pre_binding_authorizer_uses_connection_owner_not_fake_binding(self) -> None:
        handler = read(
            "backend/scan-control-plane/internal/server/provider_token_context.go"
        )
        self.assertIn("pre_binding_browse", handler)
        self.assertIn("datasource.browse", handler)
        self.assertIn("CanUseAuthConnection", handler)
        self.assertNotIn('"interactive:"', handler)


if __name__ == "__main__":
    unittest.main()
