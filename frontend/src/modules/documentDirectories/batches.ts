import type { DocumentDirectoryBatch } from './api';

// Routing batches summarize their children; only the latest leaf result per
// directory is actionable, consistently on the page and in the sidebar.
export function latestDirectoryBatches(batches: DocumentDirectoryBatch[], directoryId?: string) {
  const parents = new Set(batches.map(b => b.parent_id).filter(Boolean));
  const latest = new Map<string, DocumentDirectoryBatch>();
  for (const batch of [...batches].sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id))) {
    if (parents.has(batch.id) || directoryId && batch.directory_id !== directoryId) continue;
    const key = batch.directory_id || batch.id;
    if (!latest.has(key)) latest.set(key, batch);
  }
  return [...latest.values()].filter(b => !b.acknowledged && !batches.find(parent => parent.id === b.parent_id)?.acknowledged);
}

export function hasUnreadDirectoryResults(batches: DocumentDirectoryBatch[]) {
  return latestDirectoryBatches(batches).some(b => ['succeeded', 'failed', 'canceled'].includes(b.status));
}
