import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import CloudUsageSettings from "./CloudUsageSettings";

const mocks = vi.hoisted(() => ({
  beginCloudLogin: vi.fn(),
  closeCloudLoginPopup: vi.fn(),
  fetchCloudTokenPlan: vi.fn(),
  getCloudSession: vi.fn(),
  openCloudLogin: vi.fn(),
  reserveCloudLoginPopup: vi.fn(),
}));

vi.mock("@/runtime/cloud/session", () => ({
  beginCloudLogin: mocks.beginCloudLogin,
  getCloudSession: mocks.getCloudSession,
	isCloudBusinessAvailable: (session: any) => session?.state === "signed_in" && session?.configured !== false && session?.reachability !== "unreachable",
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
}));

vi.mock("@/runtime/desktopBridge", () => ({
  closeCloudLoginPopup: mocks.closeCloudLoginPopup,
  openCloudLogin: mocks.openCloudLogin,
  reserveCloudLoginPopup: mocks.reserveCloudLoginPopup,
}));

vi.mock("./cloudUsageApi", () => ({
  fetchCloudTokenPlan: mocks.fetchCloudTokenPlan,
}));

const copy: Record<string, string> = {
  "settingsPage.cloudUsage.title": "Cloud usage",
  "settingsPage.cloudUsage.description": "See the current allowance for this Cloud account.",
  "settingsPage.cloudUsage.model": "Model",
  "settingsPage.cloudUsage.quota": "Quota",
  "settingsPage.cloudUsage.used": "Used",
  "settingsPage.cloudUsage.remaining": "Remaining",
  "settingsPage.cloudUsage.tokens": "tokens",
  "settingsPage.cloudUsage.missingUsage": "Some requests did not include complete metering data.",
  "settingsPage.cloudUsage.signedOutTitle": "Sign in to view Cloud usage",
  "settingsPage.cloudUsage.signedOutDescription": "Usage belongs to your Cloud account and is not stored on this device.",
  "settingsPage.cloudUsage.signIn": "Sign in to LazyMind Cloud",
  "settingsPage.cloudUsage.inactiveTitle": "No active Cloud plan",
  "settingsPage.cloudUsage.inactiveDescription": "There is no current allowance to display.",
  "settingsPage.cloudUsage.loadErrorTitle": "Cloud usage is unavailable",
  "settingsPage.cloudUsage.loadErrorDescription": "Your local settings and Providers are unaffected.",
  "common.retry": "Retry",
};

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "en-US" },
    t: (key: string, values?: Record<string, unknown>) => {
      void values;
      return copy[key] || key;
    },
  }),
}));

const activePlan = {
  status: "active" as const,
  modelQuotas: [{
    publicModelKey: "lazymind-text-default",
    capability: "llm",
    meterUnit: "token",
    periodicQuota: 10_000,
  }],
  usage: [{
    publicModelKey: "lazymind-text-default",
    meterUnit: "token",
    periodicQuota: 10_000,
    usedAmount: 2_500,
    remainingAmount: 7_500,
    missingUsageCount: 2,
  }],
};

describe("CloudUsageSettings", () => {
  beforeEach(() => {
    mocks.beginCloudLogin.mockReset().mockResolvedValue({ authorization_url: "https://cloud.example/zh/desktop/authorize" });
    mocks.closeCloudLoginPopup.mockReset();
    mocks.fetchCloudTokenPlan.mockReset().mockResolvedValue(activePlan);
    mocks.getCloudSession.mockReset().mockResolvedValue({ configured: true, reachability: "reachable", state: "signed_in", username: "fixture-user" });
    mocks.openCloudLogin.mockReset().mockResolvedValue({ ok: true });
    mocks.reserveCloudLoginPopup.mockReset().mockReturnValue({ close: vi.fn() });
  });

  it("presents active usage as a Desktop settings table with server-owned values", async () => {
    render(<CloudUsageSettings />);

    expect(await screen.findByRole("heading", { name: "Cloud usage" })).toBeInTheDocument();
    expect(screen.getByRole("table", { name: "Cloud usage" })).toBeInTheDocument();
    expect(screen.getByText("lazymind-text-default")).toBeInTheDocument();
    expect(screen.getByText("10,000")).toBeInTheDocument();
    expect(screen.getByText("2,500")).toBeInTheDocument();
    expect(screen.getByText("7,500")).toBeInTheDocument();
    expect(screen.getByText("Some requests did not include complete metering data.")).toHaveAttribute("role", "status");
    expect(document.querySelector(".settings-cloud-usage-summary")).toBeNull();
    expect(screen.queryByText("Free Plan · v3")).not.toBeInTheDocument();
    expect(screen.queryByText("Next refresh")).not.toBeInTheDocument();
  });

  it("keeps signed-out state local and starts the existing trusted Desktop login flow", async () => {
    mocks.getCloudSession.mockResolvedValueOnce({ state: "signed_out" });
    render(<CloudUsageSettings />);

    expect(await screen.findByRole("heading", { name: "Sign in to view Cloud usage" })).toBeInTheDocument();
    expect(mocks.fetchCloudTokenPlan).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Sign in to LazyMind Cloud" }));
    await waitFor(() => expect(mocks.openCloudLogin).toHaveBeenCalledWith(
      "https://cloud.example/zh/desktop/authorize",
      expect.anything(),
    ));
  });

  it("does not apply Cloud usage when a configured Cloud address is unreachable", async () => {
    mocks.getCloudSession.mockResolvedValueOnce({
      configured: true,
      reachability: "unreachable",
      state: "signed_in",
    });
    render(<CloudUsageSettings />);

    expect(await screen.findByRole("alert")).toHaveTextContent("Cloud usage is unavailable");
    expect(mocks.fetchCloudTokenPlan).not.toHaveBeenCalled();
  });

  it("does not apply Cloud usage when no Cloud address is configured", async () => {
    mocks.getCloudSession.mockResolvedValueOnce({
      configured: false,
      reachability: "unknown",
      state: "signed_in",
    });
    render(<CloudUsageSettings />);

    expect(await screen.findByRole("heading", { name: "Sign in to view Cloud usage" })).toBeInTheDocument();
    expect(mocks.fetchCloudTokenPlan).not.toHaveBeenCalled();
  });

  it("shows inactive entitlement without zero-valued quota rows or an upgrade action", async () => {
    mocks.fetchCloudTokenPlan.mockResolvedValueOnce({
      status: "inactive",
      modelQuotas: [],
      usage: [],
    });
    render(<CloudUsageSettings />);

    expect(await screen.findByRole("heading", { name: "No active Cloud plan" })).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /upgrade|purchase|buy/i })).not.toBeInTheDocument();
  });

  it("contains Cloud failures to this settings page and retries only after user action", async () => {
    mocks.fetchCloudTokenPlan
      .mockRejectedValueOnce(new Error("upstream secret-canary"))
      .mockResolvedValueOnce(activePlan);
    render(<CloudUsageSettings />);

    expect(await screen.findByRole("alert")).toHaveTextContent("Cloud usage is unavailable");
    expect(screen.queryByText("secret-canary")).not.toBeInTheDocument();
    expect(mocks.fetchCloudTokenPlan).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(mocks.fetchCloudTokenPlan).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("lazymind-text-default")).toBeInTheDocument();
  });

  it("clears the previous account usage when the Cloud session changes", async () => {
    mocks.getCloudSession
      .mockResolvedValueOnce({ configured: true, reachability: "reachable", state: "signed_in", username: "first-user" })
      .mockResolvedValueOnce({ state: "signed_out" });
    render(<CloudUsageSettings />);
    expect(await screen.findByText("lazymind-text-default")).toBeInTheDocument();

    window.dispatchEvent(new Event("lazymind:cloud-session-changed"));

    expect(await screen.findByRole("heading", { name: "Sign in to view Cloud usage" })).toBeInTheDocument();
    expect(screen.queryByText("lazymind-text-default")).not.toBeInTheDocument();
    expect(mocks.fetchCloudTokenPlan).toHaveBeenCalledTimes(1);
  });
});
