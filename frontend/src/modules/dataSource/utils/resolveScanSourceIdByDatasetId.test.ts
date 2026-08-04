import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("../api/clients", () => ({
  dataSourceScanApi: { listSources: vi.fn() },
}));

import { dataSourceScanApi } from "../api/clients";
import { resolveScanSourceIdByDatasetId } from "./resolveScanSourceIdByDatasetId";

describe("resolveScanSourceIdByDatasetId", () => {
  afterEach(() => {
    vi.mocked(dataSourceScanApi.listSources).mockReset();
  });

  it("returns null immediately for a blank dataset id", async () => {
    expect(await resolveScanSourceIdByDatasetId("   ")).toBeNull();
    expect(dataSourceScanApi.listSources).not.toHaveBeenCalled();
  });

  it("returns the matching source id from the first page", async () => {
    vi.mocked(dataSourceScanApi.listSources).mockResolvedValue({
      data: {
        items: [
          { dataset_id: "other", source_id: "s-other" },
          { dataset_id: "ds-1", source_id: "s-1" },
        ],
        total: 2,
      },
    } as never);

    const result = await resolveScanSourceIdByDatasetId("ds-1");
    expect(result).toBe("s-1");
    expect(dataSourceScanApi.listSources).toHaveBeenCalledTimes(1);
  });

  it("paginates until it finds a match on a later page", async () => {
    vi.mocked(dataSourceScanApi.listSources)
      .mockResolvedValueOnce({
        data: { items: Array.from({ length: 200 }, (_, i) => ({ dataset_id: `d${i}`, source_id: `s${i}` })), total: 250 },
      } as never)
      .mockResolvedValueOnce({
        data: { items: [{ dataset_id: "ds-target", source_id: "s-target" }], total: 250 },
      } as never);

    const result = await resolveScanSourceIdByDatasetId("ds-target");
    expect(result).toBe("s-target");
    expect(dataSourceScanApi.listSources).toHaveBeenCalledTimes(2);
  });

  it("returns null when no page contains a match", async () => {
    vi.mocked(dataSourceScanApi.listSources).mockResolvedValue({
      data: { items: [], total: 0 },
    } as never);

    const result = await resolveScanSourceIdByDatasetId("ds-missing");
    expect(result).toBeNull();
  });
});
