import type { PersistedPaperLibrary } from './store';
// Disposable, bounded navigation cache. The server remains authoritative.
let session = '';
const entries = new Map<string, { saved: PersistedPaperLibrary; at: number }>();
function scope(token: string) { if (session !== token) { entries.clear(); session = token; } }
export function readLibraryViewCache(token: string, dataset: string) {
  scope(token);
  const entry = entries.get(dataset);
  return token && entry && Date.now() - entry.at < 60_000 ? entry.saved : undefined;
}
export function writeLibraryViewCache(token: string, dataset: string, saved: PersistedPaperLibrary | null) {
  scope(token);
  entries.delete(dataset);
  if (!token || !saved) return;
  entries.set(dataset, { saved, at: Date.now() });
  while (entries.size > 2) entries.delete(entries.keys().next().value!);
}
