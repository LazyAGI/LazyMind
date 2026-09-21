import providerPage from "./pages/ModelProvidersPage.tsx?raw";
import zhCN from "../../i18n/locales/zh-CN.ts?raw";
import enUS from "../../i18n/locales/en-US.ts?raw";
import { describe, expect, it } from "vitest";

import { deriveCredentialBackupView } from "./credentialBackupModel";

describe("Credential Vault provider backup", () => {
  it("is disabled by default and exposes only counts/status", () => {
    const view = deriveCredentialBackupView({
      enabled: false,
      backedUp: 0,
      pending: 0,
      failed: 0,
    });

    expect(view).toEqual({
      enabled: false,
      summary: "disabled",
      backedUp: 0,
      pending: 0,
      failed: 0,
    });
    expect(JSON.stringify(view).toLowerCase()).not.toContain("api_key");
    expect(JSON.stringify(view).toLowerCase()).not.toContain("api key preview");
  });

  it("prioritizes failed then pending backup status without exposing credentials", () => {
    expect(deriveCredentialBackupView({ enabled: true, backedUp: 3, pending: 1, failed: 0 }).summary).toBe("pending");
    expect(deriveCredentialBackupView({ enabled: true, backedUp: 3, pending: 1, failed: 1 }).summary).toBe("failed");
    expect(deriveCredentialBackupView({ enabled: true, backedUp: 3, pending: 0, failed: 0 }).summary).toBe("healthy");
  });

  it("integrates an account-level panel and safe localized copy on the Provider page", () => {

    expect(providerPage).toContain("CredentialBackupPanel");
    for (const locale of [zhCN, enUS]) {
      expect(locale).toContain("credentialBackup");
      expect(locale).toContain("backedUpCount");
      expect(locale).toContain("pendingCount");
      expect(locale).toContain("failedCount");
    }
    expect(providerPage).not.toMatch(/apiKeyPreview|copyCredential|exportCredential/);
  });
});
