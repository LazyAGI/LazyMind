import { afterEach, describe, expect, it } from "vitest";
import {
  clearPendingCloudOAuthSession,
  consumeCloudDataSourceOAuthResult,
  consumeFeishuDataSourceWizardDraft,
  loadPendingCloudOAuthSession,
  peekFeishuDataSourceWizardDraft,
  saveCloudDataSourceOAuthResult,
  saveFeishuDataSourceWizardDraft,
  savePendingCloudOAuthSession,
} from "./storage";

describe("pending OAuth session (feishu)", () => {
  afterEach(() => {
    sessionStorage.clear();
    localStorage.clear();
  });

  it("saves and loads a pending feishu session by state", () => {
    savePendingCloudOAuthSession("feishu", {
      state: "state-1",
      returnUrl: "/return",
    } as never);
    const loaded = loadPendingCloudOAuthSession("feishu", "state-1");
    expect(loaded).toMatchObject({ state: "state-1", returnUrl: "/return" });
  });

  it("returns null when the state does not match any stored session", () => {
    expect(loadPendingCloudOAuthSession("feishu", "missing")).toBeNull();
  });

  it("clears a pending session by state", () => {
    savePendingCloudOAuthSession("feishu", { state: "state-2" } as never);
    clearPendingCloudOAuthSession("feishu", "state-2");
    expect(loadPendingCloudOAuthSession("feishu", "state-2")).toBeNull();
  });
});

describe("pending OAuth session (notion)", () => {
  afterEach(() => {
    sessionStorage.clear();
    localStorage.clear();
  });

  it("saves and loads using a provider-scoped storage key", () => {
    savePendingCloudOAuthSession("notion", { state: "n-1" } as never);
    expect(loadPendingCloudOAuthSession("notion", "n-1")).toMatchObject({ state: "n-1" });
    expect(loadPendingCloudOAuthSession("feishu", "n-1")).toBeNull();
  });
});

describe("OAuth result round trip", () => {
  afterEach(() => {
    sessionStorage.clear();
  });

  it("saves and consumes (removing) the OAuth result once", () => {
    saveCloudDataSourceOAuthResult("feishu", { status: "success" } as never);
    expect(consumeCloudDataSourceOAuthResult("feishu")).toEqual({ status: "success" });
    expect(consumeCloudDataSourceOAuthResult("feishu")).toBeNull();
  });
});

describe("wizard draft", () => {
  afterEach(() => {
    sessionStorage.clear();
  });

  it("peeks without removing and consumes by removing", () => {
    saveFeishuDataSourceWizardDraft({ step: 1 } as never);
    expect(peekFeishuDataSourceWizardDraft()).toEqual({ step: 1 });
    expect(peekFeishuDataSourceWizardDraft()).toEqual({ step: 1 });
    expect(consumeFeishuDataSourceWizardDraft()).toEqual({ step: 1 });
    expect(peekFeishuDataSourceWizardDraft()).toBeNull();
  });
});
