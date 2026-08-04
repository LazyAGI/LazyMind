import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import {
  mapScanDocumentToDetail,
  mapScanSyncDetail,
  stringifyScanError,
} from "./scanDocument";
import type { ScanV2Document } from "../utils/scanAccessors";

const t = ((key: string) => key) as unknown as TFunction;

describe("mapScanSyncDetail", () => {
  it("maps each update state to its localized detail key", () => {
    expect(mapScanSyncDetail("new", t)).toBe("admin.dataSourceFileUpdateNewDetail");
    expect(mapScanSyncDetail("changed", t)).toBe("admin.dataSourceFileUpdateChangedDetail");
    expect(mapScanSyncDetail("deleted", t)).toBe("admin.dataSourceFileUpdateDeletedDetail");
    expect(mapScanSyncDetail("unchanged", t)).toBe("admin.dataSourceFileUpdateUnchangedDetail");
  });
});

describe("stringifyScanError", () => {
  it("returns undefined for falsy input", () => {
    expect(stringifyScanError(undefined)).toBeUndefined();
    expect(stringifyScanError("")).toBeUndefined();
  });

  it("localizes a string error code", () => {
    expect(stringifyScanError("2000123")).toBeTruthy();
  });

  it("localizes an object error via code or error_code", () => {
    expect(stringifyScanError({ code: "2000123" })).toBeTruthy();
    expect(stringifyScanError({ error_code: "2000456" })).toBeTruthy();
  });
});

describe("mapScanDocumentToDetail", () => {
  it("maps a new document with a pending sync state", () => {
    const item: ScanV2Document = {
      document_id: "doc-1",
      display_name: "file.txt",
      source_state: "NEW",
      sync_state: "PENDING",
      size_bytes: 2048,
    } as ScanV2Document;
    const result = mapScanDocumentToDetail(item, t);
    expect(result.id).toBe("doc-1");
    expect(result.name).toBe("file.txt");
    expect(result.updateState).toBe("new");
    expect(result.sourceState).toBe("NEW");
    expect(result.syncState).toBe("PENDING");
    expect(result.size).toBe("2.0 KB");
  });

  it("prefers an explicit update_desc over the derived sync detail", () => {
    const item: ScanV2Document = {
      document_id: "doc-2",
      update_desc: "custom description",
      source_state: "MODIFIED",
    } as ScanV2Document;
    const result = mapScanDocumentToDetail(item, t);
    expect(result.syncDetail).toBe("custom description");
  });

  it("stringifies the last_error when present", () => {
    const item: ScanV2Document = {
      document_id: "doc-3",
      last_error: "2000123",
    } as ScanV2Document;
    const result = mapScanDocumentToDetail(item, t);
    expect(result.lastError).toBeTruthy();
  });

  it("derives syncState IDLE by default and treats a missing source_state as changed", () => {
    const item: ScanV2Document = { document_id: "doc-4" } as ScanV2Document;
    const result = mapScanDocumentToDetail(item, t);
    expect(result.sourceState).toBe("MODIFIED");
    expect(result.syncState).toBe("IDLE");
  });

  it("resolves an explicit UNCHANGED source_state as-is", () => {
    const item: ScanV2Document = {
      document_id: "doc-5",
      source_state: "UNCHANGED",
    } as ScanV2Document;
    const result = mapScanDocumentToDetail(item, t);
    expect(result.sourceState).toBe("UNCHANGED");
  });
});
