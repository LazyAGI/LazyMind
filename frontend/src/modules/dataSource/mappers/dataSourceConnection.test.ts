import { describe, expect, it } from "vitest";
import {
  getCloudConnectionItems,
  mapCloudConnectionToDataSourceConnection,
  mapCloudConnectionToFeishuAccount,
  mapCloudConnectionToNotionAccount,
} from "./dataSourceConnection";
import type { CloudConnectionResponse } from "@/api/generated/auth-client";

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

describe("getCloudConnectionItems", () => {
  it("reads items from flat or nested payloads and defaults to empty array", () => {
    expect(getCloudConnectionItems({ items: [{ connection_id: "c1" }] })).toEqual([
      { connection_id: "c1" },
    ]);
    expect(
      getCloudConnectionItems({ data: { items: [{ connection_id: "c2" }] } }),
    ).toEqual([{ connection_id: "c2" }]);
    expect(getCloudConnectionItems({})).toEqual([]);
  });
});

describe("mapCloudConnectionToFeishuAccount", () => {
  it("maps a connected connection to a feishu account", () => {
    const account = mapCloudConnectionToFeishuAccount(
      buildConnection({ display_name: "Feishu Tenant" }),
    );
    expect(account.name).toBe("Feishu Tenant");
    expect(account.connection?.provider).toBe("feishu");
    expect(account.status).toBe("connected");
  });
});

describe("mapCloudConnectionToNotionAccount", () => {
  it("prefers workspace_name/owner_name for the display name", () => {
    const account = mapCloudConnectionToNotionAccount(
      buildConnection({ provider_account_meta: { owner_name: "Owner A" } }),
    );
    expect(account.name).toBe("Owner A");
    expect(account.connection?.provider).toBe("notion");
  });

  it("includes an avatar url sourced from provider metadata", () => {
    const account = mapCloudConnectionToNotionAccount(
      buildConnection({ provider_account_meta: { avatar_url: "https://a.png" } }),
    );
    expect(account.connection?.avatarUrl).toBe("https://a.png");
  });
});

describe("mapCloudConnectionToDataSourceConnection", () => {
  it("builds a minimal connection descriptor with the given provider", () => {
    const connection = buildConnection({
      display_name: "Tenant A",
      provider_tenant_key: "tk-1",
      provider_account_id: "acc-1",
    });
    const result = mapCloudConnectionToDataSourceConnection(connection, "notion");
    expect(result).toMatchObject({
      provider: "notion",
      connectionId: "conn-1",
      status: "connected",
      accountName: "Tenant A",
      tenantKey: "tk-1",
      openId: "acc-1",
    });
  });

  it("falls back through provider_account_id and connection_id for the display name", () => {
    const connection = buildConnection({ provider_account_id: "acc-2" });
    const result = mapCloudConnectionToDataSourceConnection(connection, "feishu");
    expect(result.accountName).toBe("acc-2");
  });
});
