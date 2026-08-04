import { describe, expect, it } from "vitest";
import type { CloudConnectionResponse } from "@/api/generated/auth-client";
import {
  formatValidFeishuAccountNames,
  getFeishuConnectionAppId,
  isFeishuAccountAuthValid,
  isFeishuAppId,
  normalizeFeishuAccountStatus,
  parseFeishuOAuthCallbackInput,
  resolveCloudAuthConnection,
  splitScopes,
} from "./feishuAccount";
import type { FeishuAuthAccount } from "../common/feishuAccounts";

describe("isFeishuAppId", () => {
  it("matches the cli_ prefixed app id pattern", () => {
    expect(isFeishuAppId("cli_abc123")).toBe(true);
    expect(isFeishuAppId("CLI_ABC123")).toBe(true);
  });

  it("rejects non-matching or missing values", () => {
    expect(isFeishuAppId("abc123")).toBe(false);
    expect(isFeishuAppId(undefined)).toBe(false);
  });
});

describe("getFeishuConnectionAppId", () => {
  it("finds an app id from provider_account_meta fields", () => {
    const connection = {
      provider_account_meta: { client_id: "cli_meta1" },
    } as CloudConnectionResponse;
    expect(getFeishuConnectionAppId(connection)).toBe("cli_meta1");
  });

  it("falls back to provider_account_id when it matches the pattern", () => {
    const connection = {
      provider_account_meta: {},
      provider_account_id: "cli_fallback",
    } as CloudConnectionResponse;
    expect(getFeishuConnectionAppId(connection)).toBe("cli_fallback");
  });

  it("returns undefined when no candidate matches the app id pattern", () => {
    const connection = { provider_account_meta: {} } as CloudConnectionResponse;
    expect(getFeishuConnectionAppId(connection)).toBeUndefined();
  });
});

describe("normalizeFeishuAccountStatus", () => {
  it("maps connected-like tokens", () => {
    expect(normalizeFeishuAccountStatus("active")).toBe("connected");
    expect(normalizeFeishuAccountStatus("succeeded")).toBe("connected");
  });

  it("maps expired/error tokens", () => {
    expect(normalizeFeishuAccountStatus("expired")).toBe("expired");
    expect(normalizeFeishuAccountStatus("failed")).toBe("error");
  });

  it("defaults to pending for anything else", () => {
    expect(normalizeFeishuAccountStatus(undefined)).toBe("pending");
    expect(normalizeFeishuAccountStatus("weird")).toBe("pending");
  });
});

describe("isFeishuAccountAuthValid", () => {
  it("requires status connected and a non-empty connectionId", () => {
    const account = {
      status: "connected",
      connection: { connectionId: "c1" },
    } as unknown as FeishuAuthAccount;
    expect(isFeishuAccountAuthValid(account)).toBe(true);
  });

  it("returns false when status is not connected or connectionId is blank", () => {
    expect(
      isFeishuAccountAuthValid({
        status: "pending",
        connection: { connectionId: "c1" },
      } as unknown as FeishuAuthAccount),
    ).toBe(false);
    expect(
      isFeishuAccountAuthValid({
        status: "connected",
        connection: { connectionId: "  " },
      } as unknown as FeishuAuthAccount),
    ).toBe(false);
  });
});

describe("resolveCloudAuthConnection", () => {
  const accounts: FeishuAuthAccount[] = [
    {
      id: "acc-1",
      name: "Acc 1",
      appId: "cli_1",
      appSecret: "",
      chatEnabled: false,
      status: "connected",
      connection: { provider: "feishu", connectionId: "conn-1", status: "connected", accountName: "Acc 1", grantedScopes: [] },
      createdAt: "2026-01-01",
    },
  ];

  it("returns the preferred connection when it has a connectionId", () => {
    const preferred = accounts[0].connection;
    expect(resolveCloudAuthConnection(preferred, null, accounts, "feishu")).toBe(
      preferred,
    );
  });

  it("returns null when there is neither preferred nor authConnectionId", () => {
    expect(resolveCloudAuthConnection(null, null, accounts, "feishu")).toBeNull();
  });

  it("matches an existing cached account by connectionId", () => {
    const result = resolveCloudAuthConnection(null, "conn-1", accounts, "feishu");
    expect(result).toBe(accounts[0].connection);
  });

  it("builds a minimal placeholder connection when no account matches", () => {
    const result = resolveCloudAuthConnection(null, "conn-unknown", [], "feishu");
    expect(result).toMatchObject({
      provider: "feishu",
      connectionId: "conn-unknown",
      status: "connected",
    });
  });
});

describe("formatValidFeishuAccountNames", () => {
  const accounts = [
    { connection: { accountName: "A" } },
    { connection: { accountName: "B" } },
    { name: "C" },
    { name: "D" },
  ] as unknown as FeishuAuthAccount[];

  it("joins names up to the max display count with a Chinese separator", () => {
    expect(formatValidFeishuAccountNames(accounts, 2)).toBe("A、B...");
  });

  it("does not append ellipsis when everything fits", () => {
    expect(formatValidFeishuAccountNames(accounts, 10)).toBe("A、B、C、D");
  });
});

describe("splitScopes", () => {
  it("splits on commas/whitespace and trims", () => {
    expect(splitScopes("a, b  c")).toEqual(["a", "b", "c"]);
  });

  it("returns an empty array for empty input", () => {
    expect(splitScopes(undefined)).toEqual([]);
    expect(splitScopes("")).toEqual([]);
  });
});

describe("parseFeishuOAuthCallbackInput", () => {
  it("parses code/state from a full URL", () => {
    expect(
      parseFeishuOAuthCallbackInput("https://example.com/cb?code=abc&state=xyz"),
    ).toEqual({ code: "abc", state: "xyz" });
  });

  it("parses code/state from a bare query string", () => {
    expect(parseFeishuOAuthCallbackInput("?code=abc&state=xyz")).toEqual({
      code: "abc",
      state: "xyz",
    });
  });

  it("parses code/state via regex fallback for malformed input", () => {
    expect(parseFeishuOAuthCallbackInput("code=abc&state=xyz&other=1")).toEqual({
      code: "abc",
      state: "xyz",
    });
  });

  it("returns null when code or state is missing", () => {
    expect(parseFeishuOAuthCallbackInput("?code=abc")).toBeNull();
    expect(parseFeishuOAuthCallbackInput("")).toBeNull();
  });
});
