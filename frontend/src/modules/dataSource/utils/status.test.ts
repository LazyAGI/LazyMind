import { describe, expect, it } from "vitest";
import {
  getConflictPolicyLabel,
  getConnectionMeta,
  getFileUpdateMeta,
  getPendingUpdateCount,
  getSourceTypeDescription,
  getSourceTypeTitle,
  getStatusMeta,
  getSyncModeLabel,
  isCloudType,
  isDataSourceUpdateState,
  normalizeDataSourceConnectionState,
  normalizeDataSourceFileUpdateState,
  normalizeDataSourceParseStatus,
  normalizeDataSourceStatus,
  shouldSyncFileCandidate,
} from "./status";
import type { FileCandidate } from "../constants/types";

const t = ((key: string) => key) as unknown as (key: string) => string;

describe("isCloudType", () => {
  it("treats feishu and notion as cloud types", () => {
    expect(isCloudType("feishu")).toBe(true);
    expect(isCloudType("notion")).toBe(true);
  });

  it("treats other types as non-cloud", () => {
    expect(isCloudType("local")).toBe(false);
    expect(isCloudType("database")).toBe(false);
    expect(isCloudType(undefined)).toBe(false);
  });
});

describe("normalizeDataSourceStatus", () => {
  it("prioritizes delete over other tokens", () => {
    expect(normalizeDataSourceStatus("deleted")).toBe("deleted");
  });

  it("detects error status", () => {
    expect(normalizeDataSourceStatus("FAILED")).toBe("error");
  });

  it("detects expired status", () => {
    expect(normalizeDataSourceStatus("token_expired")).toBe("expired");
  });

  it("treats watchEnabled=false as paused", () => {
    expect(normalizeDataSourceStatus("active", false)).toBe("paused");
  });

  it("defaults to active", () => {
    expect(normalizeDataSourceStatus("running", true)).toBe("active");
  });
});

describe("normalizeDataSourceConnectionState", () => {
  it("detects expired", () => {
    expect(normalizeDataSourceConnectionState("expired")).toBe("expired");
  });

  it("detects error", () => {
    expect(normalizeDataSourceConnectionState("failed")).toBe("error");
  });

  it("detects pending", () => {
    expect(normalizeDataSourceConnectionState("syncing")).toBe("pending");
  });

  it("defaults to connected", () => {
    expect(normalizeDataSourceConnectionState("ok")).toBe("connected");
  });
});

describe("normalizeDataSourceFileUpdateState", () => {
  it("detects unchanged text variants", () => {
    expect(normalizeDataSourceFileUpdateState("no_change")).toBe("unchanged");
    expect(normalizeDataSourceFileUpdateState("not modified")).toBe("unchanged");
  });

  it("detects deleted variants", () => {
    expect(normalizeDataSourceFileUpdateState("removed")).toBe("deleted");
    expect(normalizeDataSourceFileUpdateState("out_of_scope")).toBe("deleted");
  });

  it("detects new variants", () => {
    expect(normalizeDataSourceFileUpdateState("created")).toBe("new");
  });

  it("detects changed variants", () => {
    expect(normalizeDataSourceFileUpdateState("modified")).toBe("changed");
  });

  it("falls back to hasUpdate flag", () => {
    expect(normalizeDataSourceFileUpdateState(undefined, true)).toBe("changed");
    expect(normalizeDataSourceFileUpdateState(undefined, false)).toBe("unchanged");
  });
});

describe("normalizeDataSourceParseStatus", () => {
  it("detects canceled", () => {
    expect(normalizeDataSourceParseStatus("cancelled")).toBe("canceled");
  });

  it("classifies download failures for cloud sources via lastError phase", () => {
    expect(
      normalizeDataSourceParseStatus(undefined, { phase: "download" }, { sourceType: "feishu" }),
    ).toBe("download_failed");
  });

  it("classifies parse failures via lastError phase", () => {
    expect(
      normalizeDataSourceParseStatus(undefined, { phase: "index" }, { sourceType: "feishu" }),
    ).toBe("parse_failed");
  });

  it("does not surface download failure for local sources", () => {
    expect(
      normalizeDataSourceParseStatus("FAILED", { phase: "download" }, { sourceType: "local" }),
    ).toBe("failed");
  });

  it("detects pending state text", () => {
    expect(normalizeDataSourceParseStatus("not_parsed")).toBe("pending");
  });

  it("detects deleted and duplicate tokens", () => {
    expect(normalizeDataSourceParseStatus("deleted")).toBe("deleted");
    expect(normalizeDataSourceParseStatus("duplicated")).toBe("duplicate");
  });

  it("detects generic failure token", () => {
    expect(normalizeDataSourceParseStatus("failure")).toBe("failed");
  });

  it("detects downloading for cloud sources only", () => {
    expect(
      normalizeDataSourceParseStatus("DOWNLOADING", undefined, { sourceType: "feishu" }),
    ).toBe("downloading");
    expect(
      normalizeDataSourceParseStatus("QUEUED PENDING", undefined, { sourceType: "feishu" }),
    ).toBe("downloading");
    expect(
      normalizeDataSourceParseStatus("QUEUED PENDING", undefined, { sourceType: "local" }),
    ).toBe("reindexing");
  });

  it("detects reindexing tokens", () => {
    expect(normalizeDataSourceParseStatus("reindexing")).toBe("reindexing");
  });

  it("detects parsed/completed tokens", () => {
    expect(normalizeDataSourceParseStatus("completed")).toBe("parsed");
  });

  it("defaults to failed for unrecognized state", () => {
    expect(normalizeDataSourceParseStatus("unknown_state")).toBe("failed");
  });
});

describe("isDataSourceUpdateState", () => {
  it("returns false only for unchanged state", () => {
    expect(isDataSourceUpdateState("modified")).toBe(true);
    expect(isDataSourceUpdateState("no_change")).toBe(false);
  });
});

describe("label and meta helpers", () => {
  it("maps source type to title/description keys", () => {
    expect(getSourceTypeTitle("local", t)).toBe("admin.dataSourceTypeLocal");
    expect(getSourceTypeTitle("feishu", t)).toBe("admin.dataSourceTypeFeishu");
    expect(getSourceTypeTitle("notion", t)).toBe("admin.dataSourceTypeNotion");
    expect(getSourceTypeTitle("database", t)).toBe("admin.dataSourceTypeDatabase");
    expect(getSourceTypeDescription("local", t)).toBe("admin.dataSourceTypeLocalDesc");
  });

  it("maps status to color/text meta", () => {
    expect(getStatusMeta("active", t)).toEqual({
      color: "success",
      text: "admin.dataSourceStatusActive",
    });
    expect(getStatusMeta("deleted", t)).toEqual({ color: "default", text: "common.delete" });
  });

  it("maps connection state to color/text meta", () => {
    expect(getConnectionMeta("connected", t)).toEqual({
      color: "success",
      text: "admin.dataSourceConnectionConnected",
    });
  });

  it("maps conflict policy and sync mode labels", () => {
    expect(getConflictPolicyLabel("overwrite", t)).toBe("admin.dataSourceConflictOverwrite");
    expect(getConflictPolicyLabel("skip", t)).toBe("admin.dataSourceConflictSkip");
    expect(getConflictPolicyLabel("versioned", t)).toBe("admin.dataSourceConflictVersioned");
    expect(getSyncModeLabel("manual", t)).toBe("admin.dataSourceSyncModeManual");
    expect(getSyncModeLabel("scheduled", t)).toBe("admin.dataSourceSyncModeScheduled");
  });

  it("maps file update state to color/text meta", () => {
    expect(getFileUpdateMeta("new", t)).toEqual({
      color: "success",
      text: "admin.dataSourceFileUpdateNew",
    });
  });
});

describe("shouldSyncFileCandidate / getPendingUpdateCount", () => {
  it("flags new/changed/deleted states as syncable", () => {
    expect(shouldSyncFileCandidate("new")).toBe(true);
    expect(shouldSyncFileCandidate("changed")).toBe(true);
    expect(shouldSyncFileCandidate("deleted")).toBe(true);
    expect(shouldSyncFileCandidate("unchanged")).toBe(false);
  });

  it("counts only pending candidates", () => {
    const candidates = [
      { updateState: "new" },
      { updateState: "unchanged" },
      { updateState: "changed" },
    ] as FileCandidate[];
    expect(getPendingUpdateCount(candidates)).toBe(2);
  });
});
