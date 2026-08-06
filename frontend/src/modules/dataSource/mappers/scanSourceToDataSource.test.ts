import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { mapScanSourceToDataSource } from "./scanSourceToDataSource";
import type { ScanV2Binding, ScanV2Source } from "../utils/scanAccessors";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as unknown as TFunction;

describe("mapScanSourceToDataSource", () => {
  const source: ScanV2Source = {
    source_id: "src-1",
    name: "My Source",
    tenant_id: "tenant-1",
    summary: { total_objects: 3, new_count: 1 },
  } as ScanV2Source;

  it("maps a feishu-connected source with feishu-specific fields", () => {
    const binding: ScanV2Binding = {
      connector_type: "feishu-drive",
      target_ref: "drive:1",
      status: "active",
      enabled: true,
    } as ScanV2Binding;
    const result = mapScanSourceToDataSource(source, t, undefined, binding);
    expect(result.type).toBe("feishu");
    expect(result.scanManaged).toBe(true);
    expect(result.target).toBe("drive:1");
    expect(result.documentCount).toBe(3);
  });

  it("maps a notion source using the database/page target type", () => {
    const binding: ScanV2Binding = {
      target_type: "database",
      target_ref: "db:1",
      sync_mode: "scheduled",
    } as ScanV2Binding;
    const result = mapScanSourceToDataSource(source, t, undefined, binding);
    expect(result.type).toBe("notion");
    expect(result.syncMode).toBe("scheduled");
  });

  it("maps a local source when there is no matching connector/target type", () => {
    const result = mapScanSourceToDataSource(source, t, undefined, null);
    expect(result.type).toBe("local");
    expect(result.conflictPolicy).toBe("overwrite");
  });

  it("preserves fallback oauthConnection only when auth_connection_id still matches", () => {
    const fallback = {
      oauthConnection: { connectionId: "conn-1", provider: "feishu" },
    } as never;
    const matchingBinding: ScanV2Binding = {
      connector_type: "feishu",
      auth_connection_id: "conn-1",
    } as ScanV2Binding;
    const mismatchedBinding: ScanV2Binding = {
      connector_type: "feishu",
      auth_connection_id: "conn-2",
    } as ScanV2Binding;

    expect(
      mapScanSourceToDataSource(source, t, fallback, matchingBinding).oauthConnection,
    ).toEqual(fallback.oauthConnection);
    expect(
      mapScanSourceToDataSource(source, t, fallback, mismatchedBinding).oauthConnection,
    ).toBeNull();
  });
});
