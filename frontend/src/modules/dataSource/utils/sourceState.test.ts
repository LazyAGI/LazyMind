import { describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code?: string, fallback = "") =>
    code ? `localized:${code}` : fallback,
}));

import {
  buildDocumentStatusDetail,
  getSourceStateMeta,
  getSyncStateMeta,
  normalizePendingAction,
  normalizeSourceState,
  normalizeSyncState,
  resolveSourceState,
  resolveSyncState,
  sourceStateToFileUpdate,
} from "./sourceState";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as unknown as TFunction;

describe("normalizeSourceState / normalizeSyncState / normalizePendingAction", () => {
  it("normalizes valid tokens case-insensitively", () => {
    expect(normalizeSourceState("new")).toBe("NEW");
    expect(normalizeSyncState("running")).toBe("RUNNING");
    expect(normalizePendingAction("create")).toBe("CREATE");
  });

  it("returns undefined for invalid or missing tokens", () => {
    expect(normalizeSourceState("bogus")).toBeUndefined();
    expect(normalizeSourceState(undefined)).toBeUndefined();
    expect(normalizeSyncState("bogus")).toBeUndefined();
    expect(normalizePendingAction(undefined)).toBeUndefined();
  });
});

describe("sourceStateToFileUpdate", () => {
  it("maps each SourceStateValue to its legacy equivalent", () => {
    expect(sourceStateToFileUpdate("NEW")).toBe("new");
    expect(sourceStateToFileUpdate("MODIFIED")).toBe("changed");
    expect(sourceStateToFileUpdate("DELETED")).toBe("deleted");
    expect(sourceStateToFileUpdate("UNCHANGED")).toBe("unchanged");
    expect(sourceStateToFileUpdate(undefined)).toBe("unchanged");
  });
});

describe("resolveSourceState", () => {
  it("uses the explicit source_state when valid", () => {
    expect(resolveSourceState({ source_state: "MODIFIED" })).toBe("MODIFIED");
  });

  it("falls back to legacy update_type/has_update derivation", () => {
    expect(resolveSourceState({ update_type: "created" })).toBe("NEW");
    expect(resolveSourceState({ update_type: "modified" })).toBe("MODIFIED");
    expect(resolveSourceState({ update_type: "removed" })).toBe("DELETED");
    expect(resolveSourceState({})).toBe("UNCHANGED");
  });
});

describe("resolveSyncState", () => {
  it("normalizes or defaults to IDLE", () => {
    expect(resolveSyncState({ sync_state: "PENDING" })).toBe("PENDING");
    expect(resolveSyncState({})).toBe("IDLE");
  });
});

describe("getSourceStateMeta", () => {
  it("maps each state to color/text/tone", () => {
    expect(getSourceStateMeta("NEW", t)).toEqual({
      color: "success",
      text: "admin.dataSourceSourceStateNew",
      tone: "new",
    });
    expect(getSourceStateMeta("MODIFIED", t)).toMatchObject({ tone: "changed" });
    expect(getSourceStateMeta("DELETED", t)).toMatchObject({ tone: "deleted" });
    expect(getSourceStateMeta("UNCHANGED", t)).toMatchObject({ tone: "unchanged" });
  });
});

describe("getSyncStateMeta", () => {
  it("formats a failed state with localized error text when present", () => {
    const meta = getSyncStateMeta("FAILED", { lastError: "2000123" }, t);
    expect(meta.color).toBe("error");
    expect(meta.text).toContain("admin.dataSourceSyncStateFailedWithError");
  });

  it("formats a failed state without an error message", () => {
    const meta = getSyncStateMeta("FAILED", {}, t);
    expect(meta.text).toBe("admin.dataSourceSyncStateFailed");
  });

  it("formats a scheduled state with or without a next sync time", () => {
    expect(getSyncStateMeta("SCHEDULED", { nextSyncAt: "2026-01-01T00:00:00Z" }, t).text).toContain(
      "admin.dataSourceSyncStateScheduledAt",
    );
    expect(getSyncStateMeta("SCHEDULED", {}, t).text).toBe(
      "admin.dataSourceSyncStateScheduled",
    );
  });

  it("formats running/pending/idle states", () => {
    expect(getSyncStateMeta("RUNNING", {}, t).text).toBe("admin.dataSourceSyncStateRunning");
    expect(getSyncStateMeta("PENDING", {}, t).text).toBe("admin.dataSourceSyncStatePending");
    expect(getSyncStateMeta("IDLE", {}, t).text).toBe("admin.dataSourceSyncStateIdle");
  });
});

describe("buildDocumentStatusDetail", () => {
  it("prioritizes deleted state with knowledge base presence", () => {
    expect(
      buildDocumentStatusDetail({ source_state: "DELETED", knowledge_base_present: false }, t),
    ).toBe("admin.dataSourceFileUpdateDeletedDoneDetail");
    expect(
      buildDocumentStatusDetail({ source_state: "DELETED", knowledge_base_present: true }, t),
    ).toBe("admin.dataSourceFileUpdateDeletedPendingDetail");
  });

  it("reports a failed sync with localized error text", () => {
    const detail = buildDocumentStatusDetail(
      { sync_state: "FAILED", last_error: "2000123" },
      t,
    );
    expect(detail).toContain("admin.dataSourceSyncStateFailedWithError");
  });

  it("reports a scheduled sync time", () => {
    const detail = buildDocumentStatusDetail(
      { sync_state: "SCHEDULED", next_sync_at: "2026-01-01T00:00:00Z" },
      t,
    );
    expect(detail).toContain("admin.dataSourceSyncStateScheduledAt");
  });

  it("falls back to source state detail (new/modified/unchanged)", () => {
    expect(buildDocumentStatusDetail({ source_state: "NEW" }, t)).toBe(
      "admin.dataSourceFileUpdateNewDetail",
    );
    expect(buildDocumentStatusDetail({ source_state: "MODIFIED" }, t)).toBe(
      "admin.dataSourceFileUpdateChangedDetail",
    );
    expect(buildDocumentStatusDetail({}, t)).toBe(
      "admin.dataSourceFileUpdateUnchangedDetail",
    );
  });
});
