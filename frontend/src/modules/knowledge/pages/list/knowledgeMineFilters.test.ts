import { describe, expect, it } from "vitest";

import {
  getKnowledgeMineOrderBy,
  sortByDatasetOrder,
  type KnowledgeMineSort,
} from "./knowledgeMineFilters";

describe("knowledge mine filter helpers", () => {
  it.each<[KnowledgeMineSort, string | undefined]>([
    ["all", undefined],
    ["recent_used", "recent_used"],
    ["most_used", "most_used"],
    ["latest_updated", "latest_updated"],
  ])("maps %s to the dataset order_by value", (sort, expected) => {
    expect(getKnowledgeMineOrderBy(sort)).toBe(expected);
  });

  it("orders official items by dataset id and keeps unmatched items stable", () => {
    const items = [
      { id: "first", datasetId: "dataset-a" },
      { id: "second", datasetId: "dataset-b" },
      { id: "third", datasetId: "dataset-c" },
      { id: "fourth", datasetId: "" },
    ];

    expect(
      sortByDatasetOrder(items, ["dataset-c", "dataset-a"], (item) => item.datasetId)
        .map((item) => item.id),
    ).toEqual(["third", "first", "second", "fourth"]);
    expect(sortByDatasetOrder(items, null, (item) => item.datasetId)).toBe(items);
  });
});
