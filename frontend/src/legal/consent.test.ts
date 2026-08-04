import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: vi.fn(),
  patchUserUiPreferences: vi.fn(),
}));

import {
  consumeUserAgreementReadFlag,
  isAcceptedUserAgreementVersion,
  markUserAgreementRead,
  persistUserAgreementAccepted,
  syncUserAgreementFromServer,
  USER_AGREEMENT_VERSION,
} from "./consent";
import {
  fetchUserUiPreferences,
  patchUserUiPreferences,
} from "@/modules/user/uiPreferencesApi";

describe("isAcceptedUserAgreementVersion", () => {
  it("matches only the current agreement version", () => {
    expect(isAcceptedUserAgreementVersion(USER_AGREEMENT_VERSION)).toBe(true);
    expect(isAcceptedUserAgreementVersion("V0.1")).toBe(false);
    expect(isAcceptedUserAgreementVersion(undefined)).toBe(false);
    expect(isAcceptedUserAgreementVersion(null)).toBe(false);
  });
});

describe("syncUserAgreementFromServer", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns true when the server reports the current version accepted", async () => {
    vi.mocked(fetchUserUiPreferences).mockResolvedValue({
      accepted_user_agreement_version: USER_AGREEMENT_VERSION,
    } as never);
    await expect(syncUserAgreementFromServer()).resolves.toBe(true);
  });

  it("returns false (fail closed) when the request throws", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.mocked(fetchUserUiPreferences).mockRejectedValue(new Error("network error"));
    await expect(syncUserAgreementFromServer()).resolves.toBe(false);
  });
});

describe("persistUserAgreementAccepted", () => {
  it("patches the accepted version to the server", async () => {
    vi.mocked(patchUserUiPreferences).mockResolvedValue({} as never);
    await persistUserAgreementAccepted();
    expect(patchUserUiPreferences).toHaveBeenCalledWith({
      accepted_user_agreement_version: USER_AGREEMENT_VERSION,
    });
  });
});

describe("markUserAgreementRead / consumeUserAgreementReadFlag", () => {
  it("marks and consumes the session flag once", () => {
    sessionStorage.clear();
    markUserAgreementRead();
    expect(consumeUserAgreementReadFlag()).toBe(true);
    // Flag is removed after being consumed.
    expect(consumeUserAgreementReadFlag()).toBe(false);
  });

  it("returns false when sessionStorage access throws", () => {
    const spy = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(consumeUserAgreementReadFlag()).toBe(false);
    spy.mockRestore();
  });
});
