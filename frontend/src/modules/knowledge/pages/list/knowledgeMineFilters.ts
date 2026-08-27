export type KnowledgeMineSort =
  | "all"
  | "recent_used"
  | "most_used"
  | "latest_updated";

export type KnowledgeMineCloudSource = "all" | "local" | "feishu" | "notion";

export function getKnowledgeMineOrderBy(sort: KnowledgeMineSort) {
  return sort === "all" ? undefined : sort;
}

export function sortByDatasetOrder<T>(
  items: T[],
  datasetIds: string[] | null,
  getDatasetId: (item: T) => string,
) {
  if (!datasetIds) return items;

  const rank = new Map(datasetIds.map((id, index) => [id, index]));
  return items
    .map((item, index) => ({ item, index }))
    .sort((left, right) => {
      const leftRank = rank.get(getDatasetId(left.item));
      const rightRank = rank.get(getDatasetId(right.item));
      if (leftRank === undefined && rightRank === undefined) {
        return left.index - right.index;
      }
      if (leftRank === undefined) return 1;
      if (rightRank === undefined) return -1;
      return leftRank - rightRank;
    })
    .map(({ item }) => item);
}
