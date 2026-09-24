import { beforeEach, describe, expect, it, vi } from "vitest";
import { FEISHU_DEFAULT_SCOPES } from "../constants/options";
import { requestFeishuDataSourceAuthorizeUrl } from "./api";

const mocks = vi.hoisted(() => ({ authorize: vi.fn() }));
vi.mock("../api/clients", () => ({ dataSourceCloudOauthApi: {
  oauthAuthorizeUrlApiAuthserviceV1CloudProviderOauthAuthorizeUrlPost: mocks.authorize,
} }));
vi.mock("@/components/request", () => ({ getLocalizedErrorMessage: () => "fixture error" }));

beforeEach(() => {
  vi.clearAllMocks();
  sessionStorage.clear();
  mocks.authorize.mockResolvedValue({ data: {
    authorize_url: "https://accounts.feishu.cn/fixture",
    connection_id: "fixture-connection", state: "fixture-state",
  } });
});

describe("Feishu BYO authorize request", () => {
  it("requests document, drive and wiki writes as well as existing reads", async () => {
    await requestFeishuDataSourceAuthorizeUrl({
      tenantId: "fixture-tenant", appId: "cli_fixture", appSecret: "fixture-secret-not-real",
      scopes: FEISHU_DEFAULT_SCOPES,
    });
    const request = mocks.authorize.mock.calls[0][0];
    expect(request.provider).toBe("feishu");
    expect(request.cloudOAuthAuthorizeURLBody).toMatchObject({
      auth_mode: "oauth_user", client_id: "cli_fixture", client_secret: "fixture-secret-not-real",
    });
    expect(request.cloudOAuthAuthorizeURLBody.scope.split(" ")).toEqual(expect.arrayContaining([
      "offline_access", "drive:drive", "drive:drive:readonly", "drive:drive.metadata:readonly",
      "wiki:wiki", "wiki:node:retrieve", "docx:document",
    ]));
  });

  it("reauthorizes the same connection using server-owned credentials and scope defaults", async () => {
    await requestFeishuDataSourceAuthorizeUrl({
      tenantId: "fixture-tenant", reauthorizeConnectionId: "fixture-existing",
      scopes: FEISHU_DEFAULT_SCOPES,
    });
    expect(mocks.authorize.mock.calls[0][0]).toEqual({
      provider: "feishu",
      cloudOAuthAuthorizeURLBody: {
        auth_mode: "oauth_user", reauthorize_connection_id: "fixture-existing",
        redirect_uri: expect.any(String),
      },
    });
  });
});
