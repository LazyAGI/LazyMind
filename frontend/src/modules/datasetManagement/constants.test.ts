import { describe, expect, it } from "vitest";
import {
  DATASET_PAGE_SIZE_OPTIONS,
  mockDatasetItems,
  mockDatasets,
  mockImportRecords,
} from "./constants";

describe("dataset management constants", () => {
  it("exposes page size options as strings", () => {
    expect(DATASET_PAGE_SIZE_OPTIONS).toEqual(["10", "20", "50", "100"]);
  });

  it("provides mock datasets with knowledge base associations", () => {
    expect(mockDatasets.length).toBeGreaterThan(0);
    mockDatasets.forEach((dataset) => {
      expect(dataset.id).toBeTruthy();
      expect(dataset.name).toBeTruthy();
    });
  });

  it("keeps mock dataset items keyed by an existing dataset id", () => {
    const datasetIds = new Set(mockDatasets.map((d) => d.id));
    Object.keys(mockDatasetItems).forEach((key) => {
      expect(datasetIds.has(key)).toBe(true);
    });
  });

  it("associates each dataset item with its parent dataset id", () => {
    Object.entries(mockDatasetItems).forEach(([datasetId, items]) => {
      items.forEach((item) => {
        expect(item.dataset_id).toBe(datasetId);
      });
    });
  });

  it("provides import records only for datasets that have them", () => {
    Object.keys(mockImportRecords).forEach((key) => {
      expect(mockDatasets.some((d) => d.id === key)).toBe(true);
    });
  });
});
