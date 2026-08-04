import { describe, expect, it } from "vitest";
import { getCloudConnectionItems, mapCloudConnectionToFeishuAccount } from "./cloudConnection";
import type { CloudConnectionResponse } from "@/api/generated/auth-client";
import type { FeishuAuthAccount } from "../common/feishuAccounts";

describe("getCloudConnectionItems", () => {
  it("reads items from a flat payload", () => {
    expect(getCloudConnectionItems({ items: [{ connection_id: "c1" }] })).toEqual([
      { connection_id: "c1" },
    ]);
  });

  it("reads items nested under data", () => {
    expect(
      getCloudConnectionItems({ data: { items: [{ connection_id: "c1" }] } }),
    ).toEqual([{ connection_id: "c1" }]);
  });

  it("returns an empty array for unrecognized payloads", () => {
    expect(getCloudConnectionItems({})).toEqual([]);
  });
});

function buildConnection(
  overrides: Partial<CloudConnectionResponse> = {},
): CloudConnectionResponse {
  return {
    connection_id: "conn-1",
    status: "active",
    provider_account_meta: {},
    provider_options: {},
    ...overrides,
  } as CloudConnectionResponse;
}

describe("mapCloudConnectionToFeishuAccount", () => {
  it("maps a connected connection with a display name and granted scopes", () => {
    const connection = buildConnection({
      display_name: "Feishu Tenant",
      scope: "read write",
    });
    const account = mapCloudConnectionToFeishuAccount(connection);
    expect(account.status).toBe("connected");
    expect(account.name).toBe("Feishu Tenant");
    expect(account.connection?.grantedScopes).toEqual(["read", "write"]);
    expect(account.connection?.provider).toBe("feishu");
  });

  it("forces chatEnabled to false when not connected regardless of server value", () => {
    const connection = buildConnection({
      status: "expired",
      provider_options: { chat_enabled: true },
    });
    const account = mapCloudConnectionToFeishuAccount(connection);
    expect(account.chatEnabled).toBe(false);
  });

  it("falls back to a cached account's appSecret and chatEnabled when server data is absent", () => {
    const cached: FeishuAuthAccount[] = [
      {
        id: "acc-1",
        name: "Cached",
        appId: "cli_1",
        appSecret: "secret-1",
        chatEnabled: true,
        status: "connected",
        connection: {
          provider: "feishu",
          connectionId: "conn-1",
          status: "connected",
          accountName: "Cached",
          grantedScopes: [],
        },
        createdAt: "2026-01-01",
      },
    ];
    const connection = buildConnection({ provider_account_meta: { client_id: "cli_1" } });
    const account = mapCloudConnectionToFeishuAccount(connection, cached);
    expect(account.appSecret).toBe("secret-1");
    expect(account.chatEnabled).toBe(true);
  });
});
