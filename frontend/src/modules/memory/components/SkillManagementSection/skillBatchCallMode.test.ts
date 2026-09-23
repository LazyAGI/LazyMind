import { describe, expect, it, vi } from "vitest";
import { updateSelectedSkillCallModes } from "./skillBatchCallMode";
import type { StructuredAsset } from "../../shared";

const asset = (id: string, extra: Partial<StructuredAsset> = {}): StructuredAsset => ({
  id, name: id, description: "old description", category: "internal", tags: [], content: "", ...extra,
});

describe("batch skill call mode updates", () => {
  it("updates only invocation fields, not stale metadata from previous pages", async () => {
    const patch = vi.fn().mockResolvedValue({});
    const result = await updateSelectedSkillCallModes([asset("a"), asset("b")], "manual", patch);
    expect(result.succeededIds).toEqual(["a", "b"]);
    expect(result.failed).toEqual([]);
    expect(patch.mock.calls.map(([id, payload]) => [id, JSON.parse(JSON.stringify(payload))])).toEqual([
      ["a", { call_mode: "manual", is_enabled: true }], ["b", { call_mode: "manual", is_enabled: true }],
    ]);
  });

  it("retains individual failures while continuing independent updates", async () => {
    const patch = vi.fn().mockRejectedValueOnce(new Error("draft locked")).mockResolvedValueOnce({});
    const result = await updateSelectedSkillCallModes([asset("a"), asset("b")], "priority", patch);
    expect(result.succeededIds).toEqual(["b"]);
    expect(result.failed.map(({ skill }) => skill.id)).toEqual(["a"]);
  });

  it("never changes cloud-only or read-only entries and deduplicates IDs", async () => {
    const patch = vi.fn().mockResolvedValue({});
    const result = await updateSelectedSkillCallModes([
      asset("a"), asset("a"), asset("remote", { cloudResourceId: "cloud-id" }), asset("locked", { readonly: true }),
    ], "on_demand", patch);
    expect(result.succeededIds).toEqual(["a"]);
    expect(patch).toHaveBeenCalledTimes(1);
    expect(result.failed.map(({ skill }) => skill.id)).toEqual(["remote", "locked"]);
  });
});
