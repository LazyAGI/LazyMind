export interface RevisionLineageNode {
  revisionId: string;
  parentRevisionId: string;
  isHead: boolean;
}

export type VisibleRevision<T extends RevisionLineageNode> = T & {
  displayRevisionNo: number;
};

export const buildCurrentRevisionLineage = <T extends RevisionLineageNode>(
  revisions: T[],
): VisibleRevision<T>[] => {
  if (!revisions.length) {
    return [];
  }

  const byId = new Map(revisions.map((revision) => [revision.revisionId, revision]));
  const head = revisions.find((revision) => revision.isHead) || revisions[0];
  const lineage: T[] = [];
  const visited = new Set<string>();
  let current: T | undefined = head;

  while (current && !visited.has(current.revisionId)) {
    lineage.push(current);
    visited.add(current.revisionId);
    current = current.parentRevisionId
      ? byId.get(current.parentRevisionId)
      : undefined;
  }

  return lineage.map((revision, index) => ({
    ...revision,
    displayRevisionNo: lineage.length - index,
  }));
};
