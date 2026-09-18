import { describe, expect, it, vi } from "vitest";

vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code }));

vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
  getCloudSession: vi.fn(),
	isCloudBusinessAvailable: () => false,
  beginCloudLogin: vi.fn(),
}));

vi.mock("@/runtime/desktopBridge", () => ({
  reserveCloudLoginPopup: vi.fn(),
  closeCloudLoginPopup: vi.fn(),
  openCloudLogin: vi.fn(),
  openCloudTokenPlan: vi.fn(),
}));

vi.mock("../api", () => ({
  modelProvidersApi: {},
  modelProvidersDefaultApi: {},
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
  getCredentialBackupStatus: vi.fn(),
  getCredentialRestoreDiscovery: vi.fn(),
  getCredentialRestoreOperation: vi.fn(),
  setCredentialBackupEnabled: vi.fn(),
  startCredentialRestore: vi.fn(),
  cancelCredentialRestore: vi.fn(),
}));

import { resolveSavedProviderGroupVerified } from "./ModelProvidersPage";

describe("saved provider group verification state", () => {
  it("preserves an authoritative unverified response", () => {
    expect(resolveSavedProviderGroupVerified({
      is_verified: false,
      check: { success: true },
    })).toBe(false);
  });

  it("preserves an authoritative verified response", () => {
    expect(resolveSavedProviderGroupVerified({
      is_verified: true,
      check: { success: false },
    })).toBe(true);
  });

  it("falls back to the legacy check result when is_verified is absent", () => {
    expect(resolveSavedProviderGroupVerified({ check: { success: true } })).toBe(true);
    expect(resolveSavedProviderGroupVerified({ check: { success: false } })).toBe(false);
    expect(resolveSavedProviderGroupVerified({})).toBe(false);
  });
});
