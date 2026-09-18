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

import {
  getModelTypeForCapability,
  mapModelTypeToCapability,
} from "./ModelProvidersPage";

describe("custom model type mapping", () => {
  it("submits the canonical technical type for embedding models", () => {
    expect(getModelTypeForCapability("EMBEDDING")).toBe("embed");
  });

  it("maps canonical and legacy embedding types back to the embedding capability", () => {
    expect(mapModelTypeToCapability("embed")).toBe("EMBEDDING");
    expect(mapModelTypeToCapability("embedding")).toBe("EMBEDDING");
  });

  it("submits and reads the canonical lowercase type for vision-language models", () => {
    expect(getModelTypeForCapability("VLM")).toBe("vlm");
    expect(mapModelTypeToCapability("vlm")).toBe("VLM");
    expect(mapModelTypeToCapability("VLM")).toBe("VLM");
  });
});
