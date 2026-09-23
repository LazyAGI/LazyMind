import providerPage from "./pages/ModelProvidersPage.tsx?raw";
import zhCN from "../../i18n/locales/zh-CN.ts?raw";
import enUS from "../../i18n/locales/en-US.ts?raw";
import { describe, expect, it } from "vitest";

import { deriveCredentialRestoreView } from "./credentialRestoreModel";

describe("Credential Vault cross-PC restore", () => {
  it("shows discovery only and never auto-starts restore on a new PC", () => {
    const view = deriveCredentialRestoreView({
      available: true,
      requiresExplicitAction: true,
      backupCount: 3,
      status: "idle",
    });

    expect(view).toMatchObject({
      state: "available",
      backupCount: 3,
      canStart: true,
      requiresExplicitAction: true,
    });
    expect(view.modeOptions).toEqual(["trusted_device", "temporary"]);
  });

  it("maps progress, conflict, recent-auth, expiry and dependency failure to recoverable states", () => {
    expect(deriveCredentialRestoreView({
      available: true,
      requiresExplicitAction: true,
      backupCount: 21,
      status: "running",
      completedRecords: 20,
      totalRecords: 21,
    })).toMatchObject({ state: "progress", completedRecords: 20, totalRecords: 21, canStart: false });

    expect(deriveCredentialRestoreView({
      available: true,
      requiresExplicitAction: true,
      backupCount: 1,
      status: "conflict",
    })).toMatchObject({ state: "conflict", canRetry: false });

    for (const failureCode of ["3080007", "3080008", "3080011"]) {
      expect(deriveCredentialRestoreView({
        available: true,
        requiresExplicitAction: true,
        backupCount: 1,
        status: failureCode === "3080008" ? "expired" : "failed",
        failureCode,
      })).toMatchObject({ state: "failed", canRetry: true });
    }
  });

  it("requires a confirmation surface with honest trust-boundary copy and no key export", () => {

    expect(providerPage).toContain("CredentialRestorePanel");
    for (const locale of [zhCN, enUS]) {
      for (const marker of [
        "credentialRestore",
        "restoreToThisComputer",
        "trustedDevice",
        "temporaryUse",
        "recoveryTrustBoundary",
        "recentAuthenticationRequired",
        "restoreConflict",
      ]) {
        expect(locale).toContain(marker);
      }
    }
    expect(providerPage).not.toMatch(/apiKeyPreview|copyCredential|exportCredential|autoRestore/);
  });

  it("keeps double-submit disabled while confirmation or recovery is active", () => {
    for (const status of ["confirming", "pending", "running"] as const) {
      expect(deriveCredentialRestoreView({
        available: true,
        requiresExplicitAction: true,
        backupCount: 2,
        status,
      }).canStart).toBe(false);
    }
  });
});
