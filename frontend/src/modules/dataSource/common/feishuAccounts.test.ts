import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  FEISHU_AUTH_ACCOUNTS_STORAGE_KEY,
  clearFeishuAppSetup,
  createFeishuAccountId,
  getOAuthStateFromConnection,
  loadFeishuAppSetup,
  loadFeishuAuthAccounts,
  persistFeishuAppSetup,
  persistFeishuAuthAccounts,
} from "./feishuAccounts";
import { FEISHU_APP_SETUP_STORAGE_KEY } from "../constants/options";
import type { FeishuAuthAccount } from "./feishuAccounts";

describe("createFeishuAccountId", () => {
  it("generates ids prefixed with feishu-account-", () => {
    const id = createFeishuAccountId();
    expect(id.startsWith("feishu-account-")).toBe(true);
  });

  it("generates distinct ids across calls", () => {
    const first = createFeishuAccountId();
    const second = createFeishuAccountId();
    expect(first).not.toBe(second);
  });
});

describe("feishu app setup persistence", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("returns null when no setup is stored", () => {
    expect(loadFeishuAppSetup()).toBeNull();
  });

  it("persists and reloads a valid app setup", () => {
    persistFeishuAppSetup({ appId: "app-1", appSecret: "secret-1" });
    expect(loadFeishuAppSetup()).toEqual({ appId: "app-1", appSecret: "secret-1" });
  });

  it("returns null when the stored setup is missing appId or appSecret", () => {
    localStorage.setItem(FEISHU_APP_SETUP_STORAGE_KEY, JSON.stringify({ appId: "" }));
    expect(loadFeishuAppSetup()).toBeNull();
  });

  it("returns null when the stored value is malformed JSON", () => {
    localStorage.setItem(FEISHU_APP_SETUP_STORAGE_KEY, "{not-json");
    expect(loadFeishuAppSetup()).toBeNull();
  });

  it("clears the stored app setup", () => {
    persistFeishuAppSetup({ appId: "app-1", appSecret: "secret-1" });
    clearFeishuAppSetup();
    expect(loadFeishuAppSetup()).toBeNull();
  });
});

describe("feishu auth accounts persistence", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.useRealTimers();
  });

  it("returns an empty array when nothing is stored", () => {
    expect(loadFeishuAuthAccounts()).toEqual([]);
  });

  it("returns an empty array when the stored value is not an array", () => {
    localStorage.setItem(FEISHU_AUTH_ACCOUNTS_STORAGE_KEY, JSON.stringify({ foo: "bar" }));
    expect(loadFeishuAuthAccounts()).toEqual([]);
  });

  it("returns an empty array for malformed JSON", () => {
    localStorage.setItem(FEISHU_AUTH_ACCOUNTS_STORAGE_KEY, "not-json{{");
    expect(loadFeishuAuthAccounts()).toEqual([]);
  });

  it("filters out entries missing appId or appSecret and fills in defaults", () => {
    localStorage.setItem(
      FEISHU_AUTH_ACCOUNTS_STORAGE_KEY,
      JSON.stringify([
        { appId: "app-1", appSecret: "secret-1" },
        { appId: "", appSecret: "secret-2" },
        { appId: "app-3" },
      ]),
    );

    const accounts = loadFeishuAuthAccounts();
    expect(accounts).toHaveLength(1);
    expect(accounts[0].appId).toBe("app-1");
    expect(accounts[0].name).toBe("app-1");
    expect(accounts[0].status).toBe("pending");
    expect(accounts[0].id.startsWith("feishu-account-")).toBe(true);
  });

  it("preserves explicit id, name, status and connection fields", () => {
    localStorage.setItem(
      FEISHU_AUTH_ACCOUNTS_STORAGE_KEY,
      JSON.stringify([
        {
          id: "account-1",
          name: "My Feishu",
          appId: "app-1",
          appSecret: "secret-1",
          chatEnabled: true,
          status: "connected",
          connection: { status: "connected" },
          createdAt: "2024-01-01T00:00:00.000Z",
        },
      ]),
    );

    const [account] = loadFeishuAuthAccounts();
    expect(account.id).toBe("account-1");
    expect(account.name).toBe("My Feishu");
    expect(account.chatEnabled).toBe(true);
    expect(account.status).toBe("connected");
    expect(account.connection).toEqual({ status: "connected" });
    expect(account.createdAt).toBe("2024-01-01T00:00:00.000Z");
  });

  it("persists and reloads a list of accounts", () => {
    const accounts: FeishuAuthAccount[] = [
      {
        id: "account-1",
        name: "Account 1",
        appId: "app-1",
        appSecret: "secret-1",
        chatEnabled: false,
        status: "pending",
        connection: null,
        createdAt: "2024-01-01T00:00:00.000Z",
      },
    ];
    persistFeishuAuthAccounts(accounts);
    expect(loadFeishuAuthAccounts()).toEqual(accounts);
  });
});

describe("getOAuthStateFromConnection", () => {
  it("returns pending when there is no connection", () => {
    expect(getOAuthStateFromConnection(null)).toBe("pending");
    expect(getOAuthStateFromConnection(undefined)).toBe("pending");
  });

  it("maps connection statuses to oauth states", () => {
    expect(getOAuthStateFromConnection({ status: "connected" } as any)).toBe("connected");
    expect(getOAuthStateFromConnection({ status: "expired" } as any)).toBe("expired");
    expect(getOAuthStateFromConnection({ status: "error" } as any)).toBe("error");
  });

  it("defaults to pending for unrecognized statuses", () => {
    expect(getOAuthStateFromConnection({ status: "weird" } as any)).toBe("pending");
  });
});
