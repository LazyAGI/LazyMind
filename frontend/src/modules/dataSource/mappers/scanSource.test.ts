import { describe, expect, it } from "vitest";
import { buildDetailSummaryFromSource } from "./scanSource";
import type { ScanV2Binding, ScanV2Source, ScanV2Summary } from "../utils/scanAccessors";

describe("buildDetailSummaryFromSource", () => {
  const source: ScanV2Source = {
    source_id: "src-1",
    name: "My Source",
    tenant_id: "tenant-1",
  } as ScanV2Source;

  it("builds a local summary using binding target and document counts", () => {
    const binding: ScanV2Binding = {
      target_ref: "/data",
      sync_mode: "manual",
      binding_id: "b1",
    } as ScanV2Binding;
    const summary: ScanV2Summary = { total_objects: 5, new_count: 2 } as ScanV2Summary;
    const result = buildDetailSummaryFromSource(source, summary, [], binding);
    expect(result.id).toBe("src-1");
    expect(result.target).toBe("/data");
    expect(result.sourceType).toBe("local");
    expect(result.documentCount).toBe(5);
    expect(result.addCount).toBe(2);
    expect(result.bindingId).toBe("b1");
  });

  it("marks the source type as feishu when the binding indicates a feishu connector", () => {
    const binding: ScanV2Binding = {
      connector_type: "feishu-drive",
      target_ref: "drive:1",
    } as ScanV2Binding;
    const result = buildDetailSummaryFromSource(source, undefined, [], binding);
    expect(result.sourceType).toBe("feishu");
  });

  it("falls back to '-' for target and document.length for documentCount when summary is missing", () => {
    const result = buildDetailSummaryFromSource(source, undefined, [
      { id: "1" } as never,
    ]);
    expect(result.target).toBe("-");
    expect(result.documentCount).toBe(1);
    expect(result.addCount).toBe(0);
  });
});
