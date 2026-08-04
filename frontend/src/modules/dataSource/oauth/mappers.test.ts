import { describe, expect, it } from "vitest";
import { hasBusinessError, normalizeConnection } from "./mappers";

describe("hasBusinessError", () => {
  it("returns false when the code is absent, 0, or 200", () => {
    expect(hasBusinessError({})).toBe(false);
    expect(hasBusinessError({ code: "0" })).toBe(false);
    expect(hasBusinessError({ code: 200 })).toBe(false);
  });

  it("returns true for a non-success code", () => {
    expect(hasBusinessError({ code: "500" })).toBe(true);
  });

  it("reads the code from a nested data payload", () => {
    expect(hasBusinessError({ data: { code: "403" } })).toBe(true);
  });
});

describe("normalizeConnection", () => {
  it("normalizes a nested connection object with granted scopes and status", () => {
    const result = normalizeConnection({
      data: {
        connection: {
          connection_id: "conn-1",
          status: "active",
          account_name: "Tenant A",
          granted_scopes: ["a", "b"],
        },
      },
    });
    expect(result.connectionId).toBe("conn-1");
    expect(result.status).toBe("connected");
    expect(result.accountName).toBe("Tenant A");
    expect(result.grantedScopes).toEqual(["a", "b"]);
    expect(result.provider).toBe("feishu");
  });

  it("falls back to the raw payload fields when there is no nested connection", () => {
    const result = normalizeConnection(
      { connection_id: "conn-2", status: "expired", scope: "read, write" },
      "fallback-id",
      "notion",
    );
    expect(result.connectionId).toBe("conn-2");
    expect(result.status).toBe("expired");
    expect(result.grantedScopes).toEqual(["read", "write"]);
    expect(result.provider).toBe("notion");
  });

  it("uses the fallbackConnectionId when no connection id can be resolved", () => {
    const result = normalizeConnection({}, "fallback-id");
    expect(result.connectionId).toBe("fallback-id");
  });

  it("masks access and refresh tokens", () => {
    const result = normalizeConnection({
      access_token: "abcdefghijklmnop",
      refresh_token: "short",
    });
    expect(result.accessTokenMasked).toMatch(/\.\.\./);
    expect(result.refreshTokenMasked).toContain("***");
  });
});
