import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchCloudTokenPlan, readCloudTokenPlan } from "./cloudUsageApi";

const get = vi.hoisted(() => vi.fn());

vi.mock("@/components/request", () => ({
  axiosInstance: { defaults: {}, request: (options: { url: string }) => get(options.url, options) },
  BASE_URL: "/desktop",
}));

const activePlan = {
  status: "active",
  model_quotas: [{
    public_model_key: "lazymind-text-default",
    capability: "llm",
    meter_unit: "token",
    periodic_quota: 10_000,
  }],
  usage: [{
    public_model_key: "lazymind-text-default",
    meter_unit: "token",
    periodic_quota: 10_000,
    used_amount: 2_500,
    remaining_amount: 7_500,
    missing_usage_count: 2,
  }],
};

describe("Desktop Cloud usage API", () => {
  beforeEach(() => get.mockReset());

  it.each([activePlan, { data: activePlan }])("reads the local Core envelope without contacting Cloud from Renderer", async (payload) => {
    get.mockResolvedValueOnce({ data: payload });
    const signal = new AbortController().signal;

    const plan = await fetchCloudTokenPlan(signal);

    expect(get).toHaveBeenCalledWith(
      "/desktop/api/core/cloud/token-plan",
      expect.objectContaining({ signal, silentError: true }),
    );
    expect(plan.status).toBe("active");
    expect(plan.modelQuotas[0]).toEqual(expect.objectContaining({
      publicModelKey: "lazymind-text-default",
      periodicQuota: 10_000,
    }));
    expect(plan.usage[0]).toEqual(expect.objectContaining({
      usedAmount: 2_500,
      remainingAmount: 7_500,
      missingUsageCount: 2,
    }));
  });

  it("accepts inactive as a real empty entitlement instead of fabricating zero usage", () => {
    expect(readCloudTokenPlan({ status: "inactive" })).toEqual({
      status: "inactive",
      modelQuotas: [],
      usage: [],
    });
  });

  it.each([
    null,
    { status: "trial" },
    { status: "active", model_quotas: [], usage: [] },
    { ...activePlan, usage: [{ ...activePlan.usage[0], remaining_amount: -1 }] },
    { ...activePlan, access_token: "secret-canary" },
  ])("rejects malformed or unbounded account data: %j", (payload) => {
    expect(() => readCloudTokenPlan(payload)).toThrow();
  });

  it("does not automatically retry a failed private account request", async () => {
    const failure = new Error("network unavailable with secret-canary");
    get.mockRejectedValueOnce(failure);

    await expect(fetchCloudTokenPlan()).rejects.toBe(failure);
    expect(get).toHaveBeenCalledTimes(1);
  });
});
