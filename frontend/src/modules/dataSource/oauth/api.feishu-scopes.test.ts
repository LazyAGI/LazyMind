import { beforeEach, expect, it, vi } from "vitest";
import { FEISHU_DEFAULT_SCOPES } from "../constants/options";
import { requestFeishuDataSourceAuthorizeUrl } from "./api";

const mocks = vi.hoisted(() => ({ authorize: vi.fn() }));
vi.mock("@/modules/dataSource/api/clients", () => ({ dataSourceCloudOauthApi: {
  oauthAuthorizeUrlApiAuthserviceV1CloudProviderOauthAuthorizeUrlPost: mocks.authorize,
} }));
vi.mock("@/i18n", () => ({ default: { t: (key: string) => key } }));
vi.mock("@/components/request", () => ({ getLocalizedErrorMessage: () => "fixture-error" }));

beforeEach(() => {
  vi.clearAllMocks();
  sessionStorage.clear();
  mocks.authorize.mockResolvedValue({ data: {
    authorize_url: "https://example.test/authorize", connection_id: "fixture-connection", state: "fixture-state",
  } });
});

it("requests exactly the original OAuth permissions for a newly added account", async () => {
  await requestFeishuDataSourceAuthorizeUrl({
    tenantId: "", appId: "cli_fixture", appSecret: "fixture-secret", scopes: FEISHU_DEFAULT_SCOPES,
  });
  const request = mocks.authorize.mock.calls[0][0];
  expect(request.provider).toBe("feishu");
  expect(request.cloudOAuthAuthorizeURLBody.scope.split(" ").sort()).toEqual([
    "offline_access", "drive:drive", "drive:drive:readonly", "drive:drive.metadata:readonly",
    "wiki:wiki", "wiki:wiki:readonly", "wiki:node:retrieve", "docx:document",
  ].sort());
});

it("keeps reauthorization on the existing connection and server default permissions", async () => {
  await requestFeishuDataSourceAuthorizeUrl({
    tenantId: "", scopes: FEISHU_DEFAULT_SCOPES, reauthorizeConnectionId: "fixture-existing",
  });
  expect(mocks.authorize.mock.calls[0][0].cloudOAuthAuthorizeURLBody).toEqual({
    auth_mode: "oauth_user", redirect_uri: `${window.location.origin}/oauth/feishu/callback`,
    reauthorize_connection_id: "fixture-existing",
  });
});
