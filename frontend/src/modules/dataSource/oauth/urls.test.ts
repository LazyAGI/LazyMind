import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("./storage", () => ({
  loadPendingCloudOAuthSession: vi.fn(),
}));

vi.mock("@/modules/modelProvider/utils/cloudDocumentUrls", () => ({
  getCloudDocumentsUrl: (provider: string) => `/documents/${provider}`,
}));

import { loadPendingCloudOAuthSession } from "./storage";
import {
  getAppUrl,
  getCloudDataSourceCallbackUrl,
  getCloudDataSourceOAuthReturnUrl,
  getDataSourceManagementUrl,
  getFeishuDataSourceCallbackUrl,
  normalizeSameOriginReturnUrl,
} from "./urls";

describe("getAppUrl", () => {
  afterEach(() => {
    delete (window as Window & { BASENAME?: string }).BASENAME;
  });

  it("builds an absolute URL using window.location.origin", () => {
    expect(getAppUrl("/foo")).toBe(`${window.location.origin}/foo`);
  });

  it("prefixes with BASENAME and normalizes the leading slash", () => {
    (window as Window & { BASENAME?: string }).BASENAME = "/app/";
    expect(getAppUrl("foo")).toBe(`${window.location.origin}/app/foo`);
  });
});

describe("normalizeSameOriginReturnUrl", () => {
  it("falls back to the management URL when no value is provided", () => {
    expect(normalizeSameOriginReturnUrl(undefined)).toBe(getDataSourceManagementUrl());
  });

  it("falls back for cross-origin URLs", () => {
    expect(normalizeSameOriginReturnUrl("https://evil.example.com/x")).toBe(
      getDataSourceManagementUrl(),
    );
  });

  it("falls back when the path is an OAuth callback route", () => {
    const callbackUrl = `${window.location.origin}/oauth/feishu/callback`;
    expect(normalizeSameOriginReturnUrl(callbackUrl)).toBe(getDataSourceManagementUrl());
  });

  it("returns the same-origin URL unchanged when it is valid", () => {
    const validUrl = `${window.location.origin}/some/page`;
    expect(normalizeSameOriginReturnUrl(validUrl)).toBe(validUrl);
  });
});

describe("getFeishuDataSourceCallbackUrl / getCloudDataSourceCallbackUrl", () => {
  it("builds the feishu-specific callback path", () => {
    expect(getFeishuDataSourceCallbackUrl()).toBe(
      `${window.location.origin}/oauth/feishu/callback`,
    );
    expect(getCloudDataSourceCallbackUrl("feishu")).toBe(
      getFeishuDataSourceCallbackUrl(),
    );
  });

  it("builds a generic provider callback path for other providers", () => {
    expect(getCloudDataSourceCallbackUrl("notion")).toBe(
      `${window.location.origin}/oauth/notion/data-source/callback`,
    );
  });
});

describe("getCloudDataSourceOAuthReturnUrl", () => {
  afterEach(() => {
    vi.mocked(loadPendingCloudOAuthSession).mockReset();
  });

  it("returns the management URL when there is no state", () => {
    expect(getCloudDataSourceOAuthReturnUrl("feishu")).toBe(
      getDataSourceManagementUrl("feishu"),
    );
  });

  it("resolves the pending session's returnUrl when a state is present", () => {
    vi.mocked(loadPendingCloudOAuthSession).mockReturnValue({
      returnUrl: `${window.location.origin}/wizard`,
    } as never);
    expect(getCloudDataSourceOAuthReturnUrl("feishu", "state-1")).toBe(
      `${window.location.origin}/wizard`,
    );
  });
});
