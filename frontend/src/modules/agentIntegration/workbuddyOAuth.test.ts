import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  finishWorkBuddyAuthorization,
  startWorkBuddyAuthorization,
} from "./workbuddyOAuth";

const mocks = vi.hoisted(() => ({
  authorize: vi.fn(),
  callback: vi.fn(),
}));

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    oauthAuthorizeUrlApiAuthserviceV1CloudProviderOauthAuthorizeUrlPost: mocks.authorize,
    oauthCallbackApiAuthserviceV1CloudProviderOauthCallbackPost: mocks.callback,
  },
}));

describe("WorkBuddy OAuth", () => {
  beforeEach(() => {
    mocks.authorize.mockReset();
    mocks.callback.mockReset();
    localStorage.clear();
    sessionStorage.clear();
    vi.spyOn(window, "open").mockReturnValue({ closed: false } as Window);
  });

  it("opens the official authorization flow without exposing an app secret", async () => {
    mocks.authorize.mockResolvedValue({
      data: {
        connection_id: "connection-1",
        authorize_url: "https://www.workbuddy.cn/openapi/v2/authorize?state=state-1",
        state: "state-1",
      },
    });

    await startWorkBuddyAuthorization();

    expect(mocks.authorize).toHaveBeenCalledWith({
      provider: "workbuddy",
      cloudOAuthAuthorizeURLBody: {
        auth_mode: "oauth_user",
        redirect_uri: `${window.location.origin}/oauth/workbuddy/callback`,
        scope: "user.localassistant.readable user.localassistant.invokable",
        provider_options: { chat_enabled: true, chatEnabled: true },
      },
    });
    expect(JSON.stringify(mocks.authorize.mock.calls)).not.toContain("client_secret");
    expect(window.open).toHaveBeenCalledWith(
      expect.stringContaining("https://www.workbuddy.cn/openapi/v2/authorize"),
      "WorkBuddy OAuth",
      expect.any(String),
    );
  });

  it("exchanges the callback code using the stored state", async () => {
    mocks.authorize.mockResolvedValue({
      data: {
        connection_id: "connection-1",
        authorize_url: "https://www.workbuddy.cn/openapi/v2/authorize?state=state-1",
        state: "state-1",
      },
    });
    mocks.callback.mockResolvedValue({ data: { status: "ACTIVE" } });
    await startWorkBuddyAuthorization();

    await finishWorkBuddyAuthorization("auth-code", "state-1");

    expect(mocks.callback).toHaveBeenCalledWith({
      provider: "workbuddy",
      cloudOAuthCallbackBody: {
        tenant_id: "",
        connection_id: "connection-1",
        code: "auth-code",
        state: "state-1",
        redirect_uri: `${window.location.origin}/oauth/workbuddy/callback`,
      },
    });
  });
});
