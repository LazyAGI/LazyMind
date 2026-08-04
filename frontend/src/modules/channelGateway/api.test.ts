import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({
  listChannelAccounts: vi.fn(),
  disconnectChannelAccount: vi.fn(),
  createConnectionSession: vi.fn(),
  getConnectionSession: vi.fn(),
  submitConnectionChallenge: vi.fn(),
  refreshConnectionSession: vi.fn(),
  cancelConnectionSession: vi.fn(),
}));

vi.mock("@/api/generated/channel-gateway-client", () => ({
  Configuration: class {},
  ChannelAccountsApiFactory: () => ({
    listChannelAccounts: apiMocks.listChannelAccounts,
    disconnectChannelAccount: apiMocks.disconnectChannelAccount,
  }),
  ConnectionSessionsApiFactory: () => ({
    createConnectionSession: apiMocks.createConnectionSession,
    getConnectionSession: apiMocks.getConnectionSession,
    submitConnectionChallenge: apiMocks.submitConnectionChallenge,
    refreshConnectionSession: apiMocks.refreshConnectionSession,
    cancelConnectionSession: apiMocks.cancelConnectionSession,
  }),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: {},
  BASE_URL: "http://localhost",
}));

import {
  cancelConnectionSession,
  createConnectionSession,
  disconnectChannelAccount,
  getConnectionSession,
  listChannelAccounts,
  refreshConnectionSession,
  submitConnectionChallenge,
} from "./api";

beforeEach(() => {
  Object.values(apiMocks).forEach((mockFn) => mockFn.mockReset());
});

describe("listChannelAccounts", () => {
  it("passes the provider through and returns the response data", async () => {
    apiMocks.listChannelAccounts.mockResolvedValue({ data: { items: [{ id: "a1" }] } });
    const result = await listChannelAccounts("wechat");
    expect(apiMocks.listChannelAccounts).toHaveBeenCalledWith({ provider: "wechat" });
    expect(result).toEqual({ items: [{ id: "a1" }] });
  });
});

describe("disconnectChannelAccount", () => {
  it("calls the disconnect endpoint with the account id", async () => {
    apiMocks.disconnectChannelAccount.mockResolvedValue({});
    await disconnectChannelAccount("acc-1");
    expect(apiMocks.disconnectChannelAccount).toHaveBeenCalledWith({ accountId: "acc-1" });
  });
});

describe("createConnectionSession", () => {
  it("builds the create payload without an idempotency key when omitted", async () => {
    apiMocks.createConnectionSession.mockResolvedValue({ data: { id: "s1" } });
    const result = await createConnectionSession("feishu");
    expect(apiMocks.createConnectionSession).toHaveBeenCalledWith({
      connectionSessionCreate: { provider: "feishu" },
      idempotencyKey: undefined,
    });
    expect(result).toEqual({ id: "s1" });
  });

  it("forwards the idempotency key when provided", async () => {
    apiMocks.createConnectionSession.mockResolvedValue({ data: { id: "s1" } });
    await createConnectionSession("wechat", { idempotencyKey: "key-1" });
    expect(apiMocks.createConnectionSession).toHaveBeenCalledWith({
      connectionSessionCreate: { provider: "wechat" },
      idempotencyKey: "key-1",
    });
  });
});

describe("getConnectionSession", () => {
  it("fetches the session by id", async () => {
    apiMocks.getConnectionSession.mockResolvedValue({ data: { id: "s1", status: "pending" } });
    const result = await getConnectionSession("s1");
    expect(apiMocks.getConnectionSession).toHaveBeenCalledWith({ sessionId: "s1" });
    expect(result.status).toBe("pending");
  });
});

describe("submitConnectionChallenge", () => {
  it("defaults the challenge type to numeric_code", async () => {
    apiMocks.submitConnectionChallenge.mockResolvedValue({ data: { id: "s1" } });
    await submitConnectionChallenge("s1", "123456");
    expect(apiMocks.submitConnectionChallenge).toHaveBeenCalledWith({
      sessionId: "s1",
      connectionChallengeSubmit: { type: "numeric_code", value: "123456" },
    });
  });

  it("uses a custom challenge type when provided", async () => {
    apiMocks.submitConnectionChallenge.mockResolvedValue({ data: { id: "s1" } });
    await submitConnectionChallenge("s1", "abc", "custom_type");
    expect(apiMocks.submitConnectionChallenge).toHaveBeenCalledWith({
      sessionId: "s1",
      connectionChallengeSubmit: { type: "custom_type", value: "abc" },
    });
  });
});

describe("refreshConnectionSession / cancelConnectionSession", () => {
  it("refreshes a session by id", async () => {
    apiMocks.refreshConnectionSession.mockResolvedValue({ data: { id: "s1", status: "pending" } });
    const result = await refreshConnectionSession("s1");
    expect(apiMocks.refreshConnectionSession).toHaveBeenCalledWith({ sessionId: "s1" });
    expect(result.id).toBe("s1");
  });

  it("cancels a session by id", async () => {
    apiMocks.cancelConnectionSession.mockResolvedValue({});
    await cancelConnectionSession("s1");
    expect(apiMocks.cancelConnectionSession).toHaveBeenCalledWith({ sessionId: "s1" });
  });
});
